// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {AccessControl} from "@openzeppelin/contracts/access/AccessControl.sol";
import {IMarketplaceManager} from "./MarketplaceCore.sol";

error ZeroAddr();
error NotContract();
error SameValue();

/// @title MarketplaceManager
/// @notice Role registry, atomic circuit breaker, and future-module anchor for the
///         marketplace contract set.
///
/// Design contract ("pausable entries, unstoppable exits"):
///   - The core escrow contracts (Marketplace, AuctionHouse, OfferBook) stay
///     IMMUTABLE and consult this manager only on ENTRY paths — creating listings
///     and auctions, bidding, making and accepting offers, buying.
///   - EXIT paths (settle, refundLosers, withdrawRefund, cancel, cancelEarly,
///     rejectOffer, refundExpiredOffer) never reference the manager: no role,
///     pause, or upgrade can ever trap escrowed funds.
///   - The manager holds no funds and cannot move funds. Compromise of every role
///     here can halt NEW activity but cannot redirect a single wei.
///
/// Roles:
///   - DEFAULT_ADMIN_ROLE  — grants/revokes roles, re-points module registry.
///   - OPERATOR_ROLE       — pause/unpause entries (circuit breaker).
///   - KEEPER_ROLE         — registry of authorized keeper addresses. Settlement
///                           stays permissionless on-chain by design; the role is
///                           the discoverable identity set for off-chain keepers
///                           and for future modules that may gate keeper-only ops.
///   - FEE_MANAGER_ROLE    — reserved for the future FeeDistributor module (the
///                           core 1.5% fee itself is immutable and untouchable).
///
/// Token integration points (slots only — no token logic yet, see docs/TOKEN_HOOKS.md):
///   - setTokenAddress      — future native marketplace token.
///   - setFeeDistributor    — future token-based fee rebate module.
///   - setStakingModule     — future token utility.
///   - setGovernanceModule  — future on-chain governance.
contract MarketplaceManager is AccessControl, IMarketplaceManager {
    bytes32 public constant OPERATOR_ROLE    = keccak256("OPERATOR_ROLE");
    bytes32 public constant KEEPER_ROLE      = keccak256("KEEPER_ROLE");
    bytes32 public constant FEE_MANAGER_ROLE = keccak256("FEE_MANAGER_ROLE");

    /// @notice Circuit breaker: true halts every entry path across all cores
    ///         atomically (they all read this one flag). Exits are unaffected.
    bool public entriesPaused;

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
    event EntriesPaused(address indexed by);
    event EntriesUnpaused(address indexed by);
    event ModuleSet(bytes32 indexed slot, address indexed addr);

    constructor(address admin) {
        if (admin == address(0)) revert ZeroAddr();
        _grantRole(DEFAULT_ADMIN_ROLE, admin);
        _grantRole(OPERATOR_ROLE, admin);
    }

    // ── Circuit breaker ───────────────────────────────────────────────────────

    /// @notice Halt all entry paths (new listings/auctions/bids/offers/buys)
    ///         across every core contract atomically. Exits stay live.
    function pauseEntries() external onlyRole(OPERATOR_ROLE) {
        if (entriesPaused) revert SameValue();
        entriesPaused = true;
        emit EntriesPaused(msg.sender);
        emit AuditLog("PAUSE", msg.sender, address(0), 0);
    }

    function unpauseEntries() external onlyRole(OPERATOR_ROLE) {
        if (!entriesPaused) revert SameValue();
        entriesPaused = false;
        emit EntriesUnpaused(msg.sender);
        emit AuditLog("UNPAUSE", msg.sender, address(0), 0);
    }

    /// @notice Read by the cores' entry guard. Inverted accessor so the cores
    ///         fail closed on a true flag, open on the zero-state default.
    function entriesAllowed() external view returns (bool) {
        return !entriesPaused;
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

    // ── Role audit shim ───────────────────────────────────────────────────────

    /// @dev Mirror every role change into the uniform audit stream.
    function _grantRole(bytes32 role, address account) internal override returns (bool granted) {
        granted = super._grantRole(role, account);
        if (granted) emit AuditLog("GRANT_ROLE", msg.sender, account, role);
    }

    function _revokeRole(bytes32 role, address account) internal override returns (bool revoked) {
        revoked = super._revokeRole(role, account);
        if (revoked) emit AuditLog("REVOKE_ROLE", msg.sender, account, role);
    }
}
