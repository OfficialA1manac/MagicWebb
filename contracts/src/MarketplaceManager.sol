// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Initializable} from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import {UUPSUpgradeable} from "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import {AccessControlUpgradeable} from "@openzeppelin/contracts-upgradeable/access/AccessControlUpgradeable.sol";
import {BadImplementation, UpgradeNotQueued, UpgradeNotReady, UpgradeExpired} from "./MarketplaceCore.sol";

error ZeroAddr();
error NotContract();
error NotKeeperOrAdmin();

/// @title MarketplaceManager
/// @notice Role registry and future-module anchor for the marketplace contract set.
///
/// Design contract:
///   - The core escrow contracts (Marketplace, AuctionHouse, OfferBook) consult
///     this manager only for roles (keeper authorization on auction settlement,
///     admin authorization on timelocked upgrades). Nothing on the protocol is
///     pausable — no entry or exit path can ever be halted.
///   - The manager holds no funds and cannot move funds. Compromise of every role
///     here cannot redirect a single wei or block any user action.
///
/// Roles:
///   - DEFAULT_ADMIN_ROLE  — grants/revokes roles, re-points module registry,
///                           queues timelocked upgrades.
///   - KEEPER_ROLE         — authorized keeper addresses: settle auctions the
///                           instant they end and sweep expired-offer refunds.
///   - FEE_MANAGER_ROLE    — reserved for the future FeeDistributor module (the
///                           core 1.5% fee itself is immutable and untouchable).
///
/// Token integration points (slots only — no token logic yet, see docs/TOKEN_HOOKS.md):
///   - setTokenAddress      — future native marketplace token.
///   - setFeeDistributor    — future token-based fee rebate module.
///   - setStakingModule     — future token utility.
///   - setGovernanceModule  — future on-chain governance.
contract MarketplaceManager is Initializable, AccessControlUpgradeable, UUPSUpgradeable {
    bytes32 public constant KEEPER_ROLE      = keccak256("KEEPER_ROLE");
    bytes32 public constant FEE_MANAGER_ROLE = keccak256("FEE_MANAGER_ROLE");

    // ── Module registry (single source of truth for the deployed set) ────────
    address public marketplace;
    address public auctionHouse;
    address public offerBook;

    // ── Future-module slots (token architecture; unset until those ship) ─────
    address public token;
    address public feeDistributor;
    address public stakingModule;
    address public governanceModule;

    // ── Audit log ─────────────────────────────────────────────────────────────
    /// @notice Emitted on every state-changing operation, uniformly indexable.
    event AuditLog(bytes32 indexed action, address indexed actor, address indexed subject, bytes32 extra);
    event ModuleSet(bytes32 indexed slot, address indexed addr);

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() { _disableInitializers(); }

    /// @notice One-time initializer. Grants DEFAULT_ADMIN_ROLE to the supplied
    ///         admin address.
    function initialize(address admin) public initializer {
        __AccessControl_init();
        __UUPSUpgradeable_init();
        if (admin == address(0)) revert ZeroAddr();
        _grantRole(DEFAULT_ADMIN_ROLE, admin);
    }

    // ── Module registry ───────────────────────────────────────────────────────

    /// @dev Strict address validation: non-zero and actually a deployed contract.
    function _validContract(address a) private view {
        if (a == address(0)) revert ZeroAddr();
        if (a.code.length == 0) revert NotContract();
    }

    /// @notice Register (or re-point, e.g. after a versioned redeploy) the core set.
    function setCoreContracts(address marketplace_, address auctionHouse_, address offerBook_)
        external onlyRole(DEFAULT_ADMIN_ROLE)
    {
        _validContract(marketplace_);
        _validContract(auctionHouse_);
        _validContract(offerBook_);
        marketplace  = marketplace_;
        auctionHouse = auctionHouse_;
        offerBook    = offerBook_;
        emit ModuleSet("MARKETPLACE",  marketplace_);
        emit ModuleSet("AUCTION_HOUSE", auctionHouse_);
        emit ModuleSet("OFFER_BOOK",   offerBook_);
        emit AuditLog("SET_CORES", msg.sender, marketplace_, 0);
    }

    // ── Token architecture hooks (slots only; see docs/TOKEN_HOOKS.md) ───────

    function setTokenAddress(address token_) external onlyRole(DEFAULT_ADMIN_ROLE) {
        _validContract(token_);
        token = token_;
        emit ModuleSet("TOKEN", token_);
        emit AuditLog("SET_TOKEN", msg.sender, token_, 0);
    }

    function setFeeDistributor(address fd) external onlyRole(DEFAULT_ADMIN_ROLE) {
        _validContract(fd);
        feeDistributor = fd;
        emit ModuleSet("FEE_DISTRIBUTOR", fd);
        emit AuditLog("SET_FEE_DISTRIBUTOR", msg.sender, fd, 0);
    }

    function setStakingModule(address sm) external onlyRole(DEFAULT_ADMIN_ROLE) {
        _validContract(sm);
        stakingModule = sm;
        emit ModuleSet("STAKING", sm);
        emit AuditLog("SET_STAKING", msg.sender, sm, 0);
    }

    function setGovernanceModule(address gm) external onlyRole(DEFAULT_ADMIN_ROLE) {
        _validContract(gm);
        governanceModule = gm;
        emit ModuleSet("GOVERNANCE", gm);
        emit AuditLog("SET_GOVERNANCE", msg.sender, gm, 0);
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
    function queueUpgrade(address impl) external onlyRole(DEFAULT_ADMIN_ROLE) {
        if (impl == address(0) || impl.code.length == 0) revert BadImplementation();
        pendingImplementation = impl;
        upgradeEta = uint64(block.timestamp) + upgradeDelay();
        emit UpgradeQueued(impl, upgradeEta);
        emit AuditLog("QUEUE_UPGRADE", msg.sender, impl, 0);
    }

    /// @notice Abandon the queued upgrade.
    function cancelUpgrade() external onlyRole(DEFAULT_ADMIN_ROLE) {
        emit UpgradeCancelled(pendingImplementation);
        emit AuditLog("CANCEL_UPGRADE", msg.sender, pendingImplementation, 0);
        pendingImplementation = address(0);
        upgradeEta = 0;
    }

    /// @notice DEFAULT_ADMIN_ROLE plus the queue/delay/window discipline the
    ///         cores use. Previously a bare role check — the one instant-upgrade
    ///         path in the system, and the trust anchor the cores' timelocks
    ///         depend on. Now the manager is held to the same standard.
    function _authorizeUpgrade(address newImplementation) internal override onlyRole(DEFAULT_ADMIN_ROLE) {
        if (newImplementation != pendingImplementation) revert UpgradeNotQueued();
        if (block.timestamp < upgradeEta) revert UpgradeNotReady();
        if (block.timestamp > uint256(upgradeEta) + MAX_UPGRADE_WINDOW) revert UpgradeExpired();
        pendingImplementation = address(0);
        upgradeEta = 0;
    }

    // ── Keeper fleet (self-replenishing — survives full immutability) ──────

    /// @notice Enroll a keeper. Callable by the admin OR any current keeper,
    ///         so the fleet can rotate/replace its own keys forever — even
    ///         after DEFAULT_ADMIN_ROLE is renounced and the system is fully
    ///         immutable, instant settlement never dies with a lost key.
    /// @dev A rogue keeper key can only enroll more keepers. Keeper power is
    ///      strictly benign: settle auctions to the CORRECT parties and sweep
    ///      refunds to their OWNERS. It cannot move, redirect, or block funds,
    ///      so self-extension is safe by construction.
    function addKeeper(address k) external {
        if (!hasRole(DEFAULT_ADMIN_ROLE, msg.sender) && !hasRole(KEEPER_ROLE, msg.sender)) {
            revert NotKeeperOrAdmin();
        }
        if (k == address(0)) revert ZeroAddr();
        _grantRole(KEEPER_ROLE, k);
        emit AuditLog("ADD_KEEPER", msg.sender, k, 0);
    }

    /// @notice Retire a keeper. The admin (while one exists) may prune any
    ///         key; a keeper may retire ONLY ITSELF — one compromised key can
    ///         therefore never evict the honest fleet.
    function removeKeeper(address k) external {
        if (!hasRole(DEFAULT_ADMIN_ROLE, msg.sender) && msg.sender != k) {
            revert NotKeeperOrAdmin();
        }
        _revokeRole(KEEPER_ROLE, k);
        emit AuditLog("REMOVE_KEEPER", msg.sender, k, 0);
    }

    // ── Role audit shim ───────────────────────────────────────────────────────

    /// @dev Mirror every role change into the uniform audit stream.
    function _grantRole(bytes32 role, address account) internal override {
        super._grantRole(role, account);
        emit AuditLog("GRANT_ROLE", msg.sender, account, role);
    }

    function _revokeRole(bytes32 role, address account) internal override {
        super._revokeRole(role, account);
        emit AuditLog("REVOKE_ROLE", msg.sender, account, role);
    }

    /// @dev Storage gap for future role extensions and module slots.
    ///      50 → 49: pendingImplementation (address) and upgradeEta (uint64)
    ///      pack into one freshly-claimed slot ahead of this gap.
    uint256[49] private __gap;
}
