// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Initializable}    from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import {UUPSUpgradeable}  from "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";

error ZeroAddr();
error NotContract();
error NotAdmin();
/// @dev transferAdmin() was handed the current admin.
error SameAdmin();
/// @dev acceptAdmin() called by anyone other than the pending admin.
error NotPendingAdmin();
/// @dev upgradeTo()/upgradeToAndCall() handed address(0) or an address with no code.
error BadManagerImplementation();

/// @title MarketplaceManager (v3.7)
/// @notice Authority anchor for the marketplace contract set: exactly ONE
///         keeper and (until renounced) exactly ONE admin per network.
///
/// v3.7 — the manager is a UUPS (ERC-1967) PROXY again (owner directive
/// 2026-09-09: EVERY contract on EVERY network stays upgradeable until the
/// owner orders immutability):
///   - v3.4 had deployed the manager as plain bytecode to save the second
///     proxy hop on keeper consults (~7–12k gas per settle/clean/refund). At
///     500–650 gwei that is under 0.01 native per keeper transaction; the
///     owner chose continuous upgradeability over that saving.
///   - The manager's ADDRESS is now stable for the life of a network. Its
///     logic is replaced in place by the admin (`upgradeTo`/`upgradeToAndCall`,
///     gated by `_authorizeUpgrade` → `onlyAdmin`), instantly, with no queue:
///     the manager holds no funds and no user state, so there is nothing an
///     upgrade could steal — only the two authority addresses, which the
///     admin already controls directly.
///   - The cores still bake the manager address as an immutable and consult
///     it through the same `hasRole(bytes32,address)` staticcall. That call
///     now crosses the ERC-1967 fallback (delegatecall) — byte-identical
///     protocol, stable address, upgradeable answer.
///   - `renounceAdmin()` seals the manager too: with `admin == address(0)`
///     `_authorizeUpgrade` reverts NotAdmin forever, so the seal covers all
///     four contracts of a network at once.
///
/// Admin-key lifecycle (the weak link, and how it is bounded):
///   - While a network is unsealed, custody of ONE admin key is the entire
///     upgrade security model (upgradeDelay()==0 on the cores, no queue on the
///     manager — see docs/UPGRADE_RUNBOOK.md). That risk is TEMPORARY and
///     ROTATABLE: `transferAdmin(new)` + `acceptAdmin()` is a two-step
///     hand-off (the new key must prove it can sign before the old one loses
///     power, so a typo cannot brick the network into an unintended seal),
///     `cancelAdminTransfer()` withdraws an unaccepted offer, and
///     `renounceAdmin()` ENDS the admin role permanently — clearing any
///     pending transfer with it. There is no path back from renunciation and
///     no path that grants a second admin.
///
/// v3.2 role redesign retained (owner decision 2026-08-31):
///   - No role machinery, no grant paths. The keeper is a single address the
///     admin may REPLACE via `setKeeper`; after `renounceAdmin()` that path
///     is dead and the keeper is fixed for the life of the deployment.
///   - Deployment modes, same bytecode on every network (owner decision
///     2026-09-02): every network — Coston2, Songbird, Flare — deploys
///     admin-held and instantly upgradeable. Immutability is a LATER,
///     explicit, per-network `renounceAdmin()` on the owner's order, not a
///     deploy-time default.
///
/// Design contract (unchanged from v3.1):
///   - The core escrow contracts (Marketplace, AuctionHouse, OfferBook)
///     consult this manager only for authority checks, via the
///     `hasRole(bytes32,address)` shim below. Nothing on the protocol is
///     pausable — no entry or exit path can ever be halted.
///   - The manager holds no funds and cannot move funds. Compromise of every
///     authority here cannot redirect a single wei or block any user action.
///     Keeper power is strictly benign: settle auctions to the RECORDED
///     parties, sweep refunds to their OWNERS, clean expired listings.
contract MarketplaceManager is Initializable, UUPSUpgradeable {
    /// @notice Role ids retained ONLY as the wire protocol for the cores'
    ///         `hasRole` staticcall (MarketplaceCore._requireAdmin,
    ///         AuctionHouse.settle, Marketplace.cleanExpired,
    ///         OfferBook.setOfferEligible / refundExpiredOffer). There is no
    ///         grantable role machinery behind them.
    bytes32 public constant KEEPER_ROLE        = keccak256("KEEPER_ROLE");
    bytes32 public constant DEFAULT_ADMIN_ROLE = 0x00;

    /// @notice The single settlement keeper for this network.
    address public keeper;
    /// @notice The single admin (core + manager upgrades, keeper rotation), or
    ///         address(0) forever after `renounceAdmin()`.
    address public admin;
    /// @notice Address offered the admin role by `transferAdmin`; holds no
    ///         power until it calls `acceptAdmin()`. address(0) when no
    ///         transfer is in flight.
    address public pendingAdmin;

    // ── Audit log ─────────────────────────────────────────────────────────────
    /// @notice Emitted on every state-changing operation, uniformly indexable.
    event AuditLog(bytes32 indexed action, address indexed actor, address indexed subject, bytes32 extra);
    event KeeperSet(address indexed previousKeeper, address indexed newKeeper);
    event AdminRenounced(address indexed lastAdmin);
    event AdminTransferStarted(address indexed current, address indexed pending);
    event AdminTransferCancelled(address indexed pending);
    event AdminTransferred(address indexed previous, address indexed current);

    /// @dev The implementation is never used directly: lock its initializer
    ///      so nobody can claim admin on the bare impl (OZ pattern).
    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() { _disableInitializers(); }

    /// @notice One-shot initializer, run by the ERC-1967 proxy constructor in
    ///         the deploy broadcast. Sets the two authority addresses; only
    ///         `setKeeper`, the `transferAdmin`/`acceptAdmin` hand-off,
    ///         `renounceAdmin` and an admin-signed upgrade can change state
    ///         afterwards.
    function initialize(address admin_, address keeper_) external initializer {
        __UUPSUpgradeable_init();
        if (admin_ == address(0) || keeper_ == address(0)) revert ZeroAddr();
        admin  = admin_;
        keeper = keeper_;
        emit KeeperSet(address(0), keeper_);
        emit AuditLog("INIT", msg.sender, admin_, 0);
    }

    modifier onlyAdmin() {
        if (msg.sender != admin || admin == address(0)) revert NotAdmin();
        _;
    }

    // ── Authority shim (the cores' entire view of this contract) ─────────────

    /// @notice AccessControl-compatible probe answered from the two fixed
    ///         addresses. Keeps every core consult site byte-identical to
    ///         v3.1 while removing the role registry those sites used to hit.
    function hasRole(bytes32 role, address account) public view returns (bool) {
        if (account == address(0)) return false;
        if (role == KEEPER_ROLE)        return account == keeper;
        if (role == DEFAULT_ADMIN_ROLE) return account == admin;
        return false;
    }

    // ── Keeper replacement (admin-only; dies with renunciation) ──────────────

    /// @notice Replace the network's keeper. ONLY the admin — the keeper has
    ///         no ability to alter the keeper set, and after `renounceAdmin()`
    ///         nobody does. There is deliberately no "add": one keeper exists
    ///         at any time.
    function setKeeper(address k) external onlyAdmin {
        if (k == address(0)) revert ZeroAddr();
        emit KeeperSet(keeper, k);
        emit AuditLog("SET_KEEPER", msg.sender, k, 0);
        keeper = k;
    }

    // ── Admin rotation (two-step; dies with renunciation) ────────────────────

    /// @notice Offer the admin role to `newAdmin`. Nothing changes until the
    ///         offeree calls `acceptAdmin()` — the current admin keeps every
    ///         power (and can `cancelAdminTransfer`) in the meantime. Calling
    ///         again simply replaces the outstanding offer.
    function transferAdmin(address newAdmin) external onlyAdmin {
        if (newAdmin == address(0)) revert ZeroAddr();
        if (newAdmin == admin) revert SameAdmin();
        pendingAdmin = newAdmin;
        emit AdminTransferStarted(msg.sender, newAdmin);
        emit AuditLog("TRANSFER_ADMIN", msg.sender, newAdmin, 0);
    }

    /// @notice Withdraw an outstanding admin offer. Idempotent on an empty
    ///         pending slot (emits with address(0)).
    function cancelAdminTransfer() external onlyAdmin {
        address p = pendingAdmin;
        pendingAdmin = address(0);
        emit AdminTransferCancelled(p);
        emit AuditLog("CANCEL_ADMIN_TRANSFER", msg.sender, p, 0);
    }

    /// @notice Second step: the offeree proves control of its key by
    ///         accepting. From this block the previous admin has NO power on
    ///         this manager or on any core (their `_requireAdmin` probes
    ///         `hasRole(DEFAULT_ADMIN_ROLE, caller)` here).
    function acceptAdmin() external {
        address p = pendingAdmin;
        if (p == address(0) || msg.sender != p) revert NotPendingAdmin();
        address previous = admin;
        admin = p;
        pendingAdmin = address(0);
        emit AdminTransferred(previous, p);
        emit AuditLog("ACCEPT_ADMIN", msg.sender, previous, 0);
    }

    /// @notice One-way seal. After this: no core upgrades (every core's
    ///         _requireAdmin probes this contract), no manager upgrade
    ///         (`_authorizeUpgrade` is onlyAdmin), no keeper rotation, no
    ///         admin ever again — any in-flight `transferAdmin` offer is
    ///         wiped too, so no pending key can resurrect the role. Called
    ///         per network ONLY on the owner's explicit go-immutable order;
    ///         a lost keeper key thereafter costs automation only — every
    ///         user exit remains self-service by design.
    function renounceAdmin() external onlyAdmin {
        emit AdminRenounced(msg.sender);
        emit AuditLog("RENOUNCE_ADMIN", msg.sender, address(0), 0);
        admin = address(0);
        pendingAdmin = address(0);
    }

    // ── UUPS upgrade authorization ───────────────────────────────────────────

    /// @notice Only the admin may replace the manager's logic, and only with
    ///         a deployed contract. Instant — no queue: the manager holds no
    ///         escrow, so the cores' notice-window machinery would protect
    ///         nothing here. Dead after `renounceAdmin()` (admin == 0 →
    ///         NotAdmin), which is what makes the seal cover all four
    ///         contracts. The ERC-1967 `Upgraded(address)` event is emitted
    ///         by OpenZeppelin on every install; `AuditLog("UPGRADE")` keeps
    ///         the uniform trail the indexer already follows.
    function _authorizeUpgrade(address newImplementation) internal override onlyAdmin {
        if (newImplementation == address(0) || newImplementation.code.length == 0) {
            revert BadManagerImplementation();
        }
        emit AuditLog("UPGRADE", msg.sender, newImplementation, 0);
    }

    /// @dev Storage gap for future upgrades (OpenZeppelin UUPS convention):
    ///      keeper / admin / pendingAdmin occupy three slots after the
    ///      Initializable + UUPS bases; a future manager may append below
    ///      them by consuming this gap.
    uint256[47] private __gap;
}
