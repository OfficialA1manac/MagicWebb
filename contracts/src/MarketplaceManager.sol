// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

error ZeroAddr();
error NotContract();
error NotAdmin();
/// @dev transferAdmin() was handed the current admin.
error SameAdmin();
/// @dev acceptAdmin() called by anyone other than the pending admin.
error NotPendingAdmin();

/// @title MarketplaceManager (v3.4)
/// @notice Authority anchor for the marketplace contract set: exactly ONE
///         keeper and (until renounced) exactly ONE admin per network.
///
/// v3.4 redesign — the manager is a PLAIN, UNPROXIED contract:
///   - v3.2 deployed the manager behind its own UUPS proxy, which taxed every
///     keeper-path authority consult with a second proxy hop (delegatecall +
///     impl-slot SLOAD) — measured ~11.9k per consult, worst on settle.
///     The manager holds two addresses and a shim; there is nothing in it
///     worth an upgrade surface. v3.4 deploys it as immutable bytecode and
///     the cores bake its address as an `immutable`, cutting keeper-path
///     consults by 6.8k–9.8k gas.
///   - Replacing the manager remains possible while the cores are unsealed:
///     deploy a new manager, build new core implementations whose constructor
///     bakes the new address, then queueUpgrade + upgradeTo on each core
///     (authorized by the OLD manager's admin). The upgrade surface lives on
///     the cores, where it always was.
///
/// Admin-key lifecycle (the weak link, and how it is bounded):
///   - While a network is unsealed, custody of ONE admin key is the entire
///     upgrade security model (upgradeDelay()==0 — see docs/UPGRADE_RUNBOOK.md).
///     That risk is TEMPORARY and ROTATABLE: `transferAdmin(new)` +
///     `acceptAdmin()` is a two-step hand-off (the new key must prove it can
///     sign before the old one loses power, so a typo cannot brick the
///     network into an unintended seal), `cancelAdminTransfer()` withdraws
///     an unaccepted offer, and `renounceAdmin()` ENDS the admin role
///     permanently — clearing any pending transfer with it. There is no
///     path back from renunciation and no path that grants a second admin.
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
contract MarketplaceManager {
    /// @notice Role ids retained ONLY as the wire protocol for the cores'
    ///         `hasRole` staticcall (MarketplaceCore._requireAdmin,
    ///         AuctionHouse.settle, Marketplace.cleanExpired,
    ///         OfferBook.setOfferEligible / refundExpiredOffer). There is no
    ///         grantable role machinery behind them.
    bytes32 public constant KEEPER_ROLE        = keccak256("KEEPER_ROLE");
    bytes32 public constant DEFAULT_ADMIN_ROLE = 0x00;

    /// @notice The single settlement keeper for this network.
    address public keeper;
    /// @notice The single admin (core upgrades + keeper rotation), or
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

    /// @notice Plain constructor — no proxy, no initializer. The manager's
    ///         bytecode and these two addresses are fixed at deploy; only
    ///         `setKeeper`, the `transferAdmin`/`acceptAdmin` hand-off and
    ///         `renounceAdmin` can change state afterwards.
    constructor(address admin_, address keeper_) {
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
    ///         _requireAdmin probes this contract), no keeper rotation, no
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
}
