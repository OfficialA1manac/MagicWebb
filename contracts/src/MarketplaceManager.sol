// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Initializable} from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import {UUPSUpgradeable} from "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import {BadImplementation, UpgradeNotQueued, UpgradeNotReady, UpgradeExpired} from "./MarketplaceCore.sol";

error ZeroAddr();
error NotContract();
error NotAdmin();

/// @title MarketplaceManager (v3.2)
/// @notice Authority anchor for the marketplace contract set: exactly ONE
///         keeper and (until renounced) exactly ONE admin per network.
///
/// v3.2 redesign — role machinery deleted by owner decision (2026-08-31):
///   - v3.1's AccessControl fleet is gone. `addKeeper` let any keeper enroll
///     unlimited further keepers ("self-replenishing fleet"); the inherited
///     `grantRole` let the admin mint arbitrary role holders. Both grant paths
///     are removed at the type level: there is no function anywhere in this
///     contract that adds a settlement-authorized address. The keeper cannot
///     extend itself, ever.
///   - The keeper is a single address. The admin may REPLACE it via
///     `setKeeper` (testnet iteration); once `renounceAdmin()` is called that
///     path is dead and the keeper is fixed for the life of the deployment.
///   - Deployment modes, same bytecode on every network:
///       * Coston2 (dev): admin retained — contracts stay upgradeable and the
///         keeper is rotatable, both by the admin only.
///       * Mainnets (Songbird/Flare): the deploy script calls
///         `renounceAdmin()` as its final action — from block one there is no
///         admin, no upgrade path, no keeper rotation, no grants. Sealed.
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
    /// @notice The single admin (upgrades + keeper rotation), or address(0)
    ///         forever after `renounceAdmin()`.
    address public admin;

    // ── Audit log ─────────────────────────────────────────────────────────────
    /// @notice Emitted on every state-changing operation, uniformly indexable.
    event AuditLog(bytes32 indexed action, address indexed actor, address indexed subject, bytes32 extra);
    event KeeperSet(address indexed previousKeeper, address indexed newKeeper);
    event AdminRenounced(address indexed lastAdmin);

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() { _disableInitializers(); }

    /// @notice One-time initializer: the single admin and the single keeper.
    function initialize(address admin_, address keeper_) public initializer {
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

    /// @notice One-way seal. After this: no upgrades (here and in every core,
    ///         whose _requireAdmin probes this contract), no keeper rotation,
    ///         no admin ever again. The mainnet deploy script calls this as
    ///         its final action; a lost keeper key thereafter costs automation
    ///         only — every user exit remains self-service by design.
    function renounceAdmin() external onlyAdmin {
        emit AdminRenounced(msg.sender);
        emit AuditLog("RENOUNCE_ADMIN", msg.sender, address(0), 0);
        admin = address(0);
    }

    // ── UUPS upgrade authorization (timelocked — mirrors MarketplaceCore) ────

    /// @notice Implementation queued for upgrade, or address(0) when none is
    ///         pending. Packs into a single slot with upgradeEta.
    address public pendingImplementation;
    /// @notice Earliest timestamp at which `pendingImplementation` may be
    ///         installed. Zero when nothing is queued.
    uint64  public upgradeEta;

    /// @notice Grace period after `upgradeEta` during which the queued
    ///         implementation may still be installed.
    uint64 public constant MAX_UPGRADE_WINDOW = 7 days;

    /// @notice Emitted when an upgrade is queued and its timer starts.
    event UpgradeQueued(address indexed implementation, uint64 eta);
    /// @notice Emitted when a queued upgrade is abandoned before installation.
    event UpgradeCancelled(address indexed implementation);

    /// @notice 0 on test chains (instant while the marketplace is in testing),
    ///         48h elsewhere. Mirrors MarketplaceCore.upgradeDelay().
    function upgradeDelay() public view returns (uint64) {
        uint256 id = block.chainid;
        if (id == 114 || id == 16 || id == 31337) return 0;
        return 48 hours;
    }

    /// @notice Start the timer on a manager upgrade. Queuing is public and
    ///         emits, so holders can see a pending implementation.
    function queueUpgrade(address impl) external onlyAdmin {
        if (impl == address(0) || impl.code.length == 0) revert BadImplementation();
        pendingImplementation = impl;
        upgradeEta = uint64(block.timestamp) + upgradeDelay();
        emit UpgradeQueued(impl, upgradeEta);
        emit AuditLog("QUEUE_UPGRADE", msg.sender, impl, 0);
    }

    /// @notice Abandon the queued upgrade.
    function cancelUpgrade() external onlyAdmin {
        emit UpgradeCancelled(pendingImplementation);
        emit AuditLog("CANCEL_UPGRADE", msg.sender, pendingImplementation, 0);
        pendingImplementation = address(0);
        upgradeEta = 0;
    }

    /// @notice Admin plus the queue/delay/window discipline the cores use.
    ///         After `renounceAdmin()` this reverts unconditionally — the
    ///         implementation is frozen for the life of the deployment.
    function _authorizeUpgrade(address newImplementation) internal override onlyAdmin {
        if (newImplementation != pendingImplementation) revert UpgradeNotQueued();
        if (block.timestamp < upgradeEta) revert UpgradeNotReady();
        if (block.timestamp > uint256(upgradeEta) + MAX_UPGRADE_WINDOW) revert UpgradeExpired();
        pendingImplementation = address(0);
        upgradeEta = 0;
    }

    /// @dev Storage gap. v3.2 note: `keeper`/`admin` occupy the slots the
    ///      v3.1 module registry used; this is a FRESH deployment target, not
    ///      a storage-compatible upgrade of a live v3.1 proxy.
    uint256[49] private __gap;
}
