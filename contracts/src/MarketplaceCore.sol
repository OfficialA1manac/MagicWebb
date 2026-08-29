// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Initializable} from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import {UUPSUpgradeable} from "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import {ReentrancyGuardUpgradeable} from "@openzeppelin/contracts-upgradeable/security/ReentrancyGuardUpgradeable.sol";
import {ERC1155HolderUpgradeable} from "@openzeppelin/contracts-upgradeable/token/ERC1155/utils/ERC1155HolderUpgradeable.sol";
import {IERC721}  from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155} from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";

error TransferFailed();
error WithdrawFailed();
error NothingToWithdraw();
error ZeroAddress();
error BelowMinPrice();
error BadManager();
error InvalidDuration();
/// @dev Caller does not hold DEFAULT_ADMIN_ROLE on the linked manager.
error NotAdmin();
/// @dev No manager is configured, so upgrade authorization has no trust anchor.
error NoManager();
/// @dev queueUpgrade was given a zero address or an EOA.
error BadImplementation();
/// @dev The implementation being installed is not the queued one.
error UpgradeNotQueued();
/// @dev The upgrade timelock has not elapsed yet.
error UpgradeNotReady();
/// @dev The queued upgrade sat past MAX_UPGRADE_WINDOW and must be re-queued.
error UpgradeExpired();

enum TokenStandard { ERC721, ERC1155 }

/// @dev Shared durations for listings, auctions, and offers across all cores.
///      Every time-bound action must pick one of these exact fifteen values.
///      Callers pass the DURATION; the contract computes the expiry from
///      block.timestamp (MarketplaceCore._expiryFor). Passing an absolute
///      expiresAt was unusable from a wallet: the caller cannot know the
///      timestamp of the block that will mine the transaction.
uint64 constant DURATION_1MIN  = 1 minutes;
uint64 constant DURATION_3MIN  = 3 minutes;
uint64 constant DURATION_5MIN  = 5 minutes;
uint64 constant DURATION_10MIN = 10 minutes;
uint64 constant DURATION_15MIN = 15 minutes;
uint64 constant DURATION_30MIN = 30 minutes;
uint64 constant DURATION_45MIN = 45 minutes;
uint64 constant DURATION_1HR   = 1 hours;
uint64 constant DURATION_2HR   = 2 hours;
uint64 constant DURATION_4HR   = 4 hours;
uint64 constant DURATION_8HR   = 8 hours;
uint64 constant DURATION_12HR  = 12 hours;
uint64 constant DURATION_16HR  = 16 hours;
uint64 constant DURATION_20HR  = 20 hours;
uint64 constant DURATION_24HR  = 24 hours;

/// @title MarketplaceCore
/// @notice Shared base: immutable fee config, price floor, seller-pays fee math, NFT dispatch.
/// @dev Single 1.5% platform fee, charged ONLY on a successful sale and DEDUCTED from the seller's
///      proceeds — listing, auction creation, bids and offers are all free. feeRecipient lives in
///      upgradeable storage but has no setter; only a UUPS upgrade can move it. Nothing on the
///      protocol is pausable: no entry or exit path ever consults an off switch. Upgrades go
///      through the timelocked UUPS path gated by _requireAdmin.
abstract contract MarketplaceCore is Initializable, ReentrancyGuardUpgradeable, ERC1155HolderUpgradeable, UUPSUpgradeable {
    /// @notice Platform fee: 1.5%. Hardcoded — cannot change post-deploy.
    uint16 public constant PLATFORM_FEE_BPS = 150;

    /// @notice Minimum accepted commitment everywhere (list price, auction reserve, offer amount).
    uint256 public constant MIN_PRICE = 1 ether;

    /// @dev Validate a caller-supplied duration and turn it into an absolute expiry.
    ///      Reverts InvalidDuration unless duration is one of the fifteen shared values.
    function _expiryFor(uint64 duration) internal view returns (uint64) {
        bool ok = duration == DURATION_1MIN  || duration == DURATION_3MIN
               || duration == DURATION_5MIN  || duration == DURATION_10MIN
               || duration == DURATION_15MIN || duration == DURATION_30MIN
               || duration == DURATION_45MIN || duration == DURATION_1HR
               || duration == DURATION_2HR   || duration == DURATION_4HR
               || duration == DURATION_8HR   || duration == DURATION_12HR
               || duration == DURATION_16HR  || duration == DURATION_20HR
               || duration == DURATION_24HR;
        if (!ok) revert InvalidDuration();
        return uint64(block.timestamp) + duration;
    }

    /// @notice Wallet that receives all platform fees. Set once during initialization.
    ///         Was immutable in v1; now upgradeable storage so future upgrades can
    ///         point fees to a new recipient.
    address public feeRecipient;

    /// @notice Pull-pattern fallback for any push payment that fails.
    ///         Mirrors AuctionHouse / OfferBook pendingReturns so refund
    ///         bookkeeping is symmetric across cores. Cleared by
    ///         withdrawRefund() once the recipient can accept ETH.
    mapping(address => uint256) public pendingReturns;

    /// @notice Optional MarketplaceManager — the roles registry (keeper, admin)
    ///         and the trust anchor for timelocked upgrades. address(0) = no
    ///         roles and a permanently frozen implementation. It has no power
    ///         over funds and cannot halt any user action.
    ///         Was immutable in v1; now upgradeable storage.
    /// @notice Emitted when a push payment fails and the amount is credited to pendingReturns.
    event PushFailed(address indexed to, uint256 amount);

    address public manager;

    /// @notice Implementation queued for upgrade, or address(0) when none is
    ///         pending. Packs into a single slot with upgradeEta.
    address public pendingImplementation;
    /// @notice Earliest timestamp at which `pendingImplementation` may be
    ///         installed. Zero when nothing is queued.
    uint64  public upgradeEta;

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {}

    /// @notice One-time initializer replacing the legacy constructor.
    ///         Validates and stores feeRecipient + manager in upgradeable storage.
    function __MarketplaceCore_init(address recipient, address manager_) internal onlyInitializing {
        __ReentrancyGuard_init();
        __ERC1155Holder_init();
        __UUPSUpgradeable_init();
        if (recipient == address(0)) revert ZeroAddress();
        // manager is stored in upgradeable storage (was immutable). A typo'd/EOA
        // address would silently disable keeper roles and freeze upgrades, so
        // validate it answers the role probe the cores actually consult.
        if (manager_ != address(0)) {
            if (manager_.code.length == 0) revert BadManager();
            (bool ok, bytes memory d) = manager_.staticcall(
                abi.encodeWithSignature("hasRole(bytes32,address)", bytes32(0), address(0))
            );
            if (!ok || d.length != 32) revert BadManager();
        }
        feeRecipient = recipient;
        manager      = manager_;
    }

    // ═══════════════════════════════════════════════════════════════════════
    // Fee math — 1.5% platform fee (seller-pays, immutable).
    // ═══════════════════════════════════════════════════════════════════════

    /// @notice Compute 1.5% platform fee for a given sale commitment.
    /// @param commitment The gross sale amount (listing price / bid / offer principal).
    /// @return The platform fee (1.5% of `commitment`).
    /// @dev Seller-favourable TRUNCATION: `(commitment * 150) / 10_000` floors down.
    ///      For example, a 99-wei sale computes 99*150/10000 = 1 (1.485 truncated to 1).
    ///      The seller receives `commitment - fee`, so truncation always favours the
    ///      seller (less fee deducted). The lost fraction (< 1 wei per sale) is
    ///      economically negligible and cannot be gamed — the divisor (10_000) is
    ///      much larger than any practical price precision.
    function _feeOf(uint256 commitment) internal pure returns (uint256) {
        return (commitment * PLATFORM_FEE_BPS) / 10_000;
    }

    /// @notice Pay the platform fee to the on-chain feeRecipient.
    /// @param fee Amount to forward (already computed via `_feeOf`).
    /// @dev Best-effort push with a 50,000-gas cap per EIP-150 63/64 forwarding.
    ///      If the feeRecipient is a contract that needs >50k gas for its receive()
    ///      (e.g. Gnosis Safe, Argent, smart wallet), the push falls back to
    ///      `pendingReturns[feeRecipient]` — the credit is visible on-chain and can
    ///      be pulled later via the uncapped `withdrawRefund()` path. This prevents
    ///      a broken or misconfigured feeRecipient from permanently DOSing every
    ///      buy() and acceptOffer() transaction on the protocol.
    function _payFee(uint256 fee) internal {
        if (fee == 0) return;
        (bool ok,) = feeRecipient.call{gas: 50_000, value: fee}("");
        if (!ok) {
            pendingReturns[feeRecipient] += fee;
            emit PushFailed(feeRecipient, fee);
        }
    }

    /// @notice Send `amount` ETH to `to`. Best-effort push with pull-fallback.
    /// @param to     Recipient address.
    /// @param amount ETH amount in wei.
    /// @dev gas: 50_000 cap respects EIP-150 63/64 forwarding budget.
    ///      If the recipient's receive() or fallback() needs more than 50k gas
    ///      (common for smart wallets and multisigs), the push is capped and the
    ///      amount is credited to `pendingReturns[to]` instead. The recipient can
    ///      then pull the full amount at their convenience via the uncapped
    ///      `withdrawRefund()` function — no funds are permanently lost.
    ///      Emits `PushFailed(to, amount)` on fallback so off-chain indexers can
    ///      surface the credit without polling storage.
    function _pay(address to, uint256 amount) internal {
        if (amount == 0) return;
        (bool ok,) = to.call{gas: 50_000, value: amount}("");
        if (!ok) {
            pendingReturns[to] += amount;
            emit PushFailed(to, amount);
        }
    }

    /// @notice Withdraw a pending refund from failed push payments.
    ///         Callable by any address that has a pendingReturns credit.
    ///         virtual so child contracts (AuctionHouse, OfferBook) can
    ///         override with their own pendingReturns mapping.
    // The post-call write is a restore-on-failure inside `if (!ok)`, and that
    // branch reverts immediately, so the write is discarded with the rest of
    // the transaction. The credit is zeroed BEFORE the call (CEI) and the
    // function is nonReentrant, so there is no reentrant path to drain.
    // slither-disable-next-line reentrancy-eth
    function withdrawRefund() external virtual nonReentrant {
        uint256 amt = pendingReturns[msg.sender];
        if (amt == 0) revert NothingToWithdraw();
        pendingReturns[msg.sender] = 0;
        (bool ok,) = msg.sender.call{value: amt}("");
        if (!ok) {
            pendingReturns[msg.sender] = amt;
            revert WithdrawFailed();
        }
    }

    // ── Token dispatch ─────────────────────────────────────────────────────────

    function _transferToken(
        TokenStandard standard,
        address coll,
        address from,
        address to,
        uint256 id,
        uint256 amount
    ) internal {
        if (standard == TokenStandard.ERC721) {
            IERC721(coll).safeTransferFrom(from, to, id);
        } else {
            IERC1155(coll).safeTransferFrom(from, to, id, amount, "");
        }
    }

    // ── UUPS upgrade authorization ───────────────────────────────────────────

    /// @notice How long a queued upgrade must wait before it can be installed.
    ///         0 on testnets while the marketplace is in active testing so fixes
    ///         ship instantly; 48 hours on Songbird and Flare, where the escrow
    ///         is real money and users need a window to exit if an upgrade looks
    ///         hostile.
    /// @dev Chain IDs: 114 Coston2, 16 Coston, 31337 anvil/local, 19 Songbird,
    ///      14 Flare. Anything unrecognised gets the conservative 48h.
    function upgradeDelay() public view returns (uint64) {
        uint256 id = block.chainid;
        if (id == 114 || id == 16 || id == 31337) return 0;
        return 48 hours;
    }

    /// @notice Grace period after `upgradeEta` during which the queued
    ///         implementation may still be installed. Past it the queue entry is
    ///         stale and must be re-queued, so an approval granted months ago
    ///         cannot be exercised on a whim.
    uint64 public constant MAX_UPGRADE_WINDOW = 7 days;

    /// @notice Emitted when an upgrade is queued and its timer starts.
    event UpgradeQueued(address indexed implementation, uint64 eta);
    /// @notice Emitted when a queued upgrade is abandoned before installation.
    event UpgradeCancelled(address indexed implementation);

    /// @dev Reverts unless the caller holds DEFAULT_ADMIN_ROLE on the manager.
    function _requireAdmin() internal view {
        (bool ok, bytes memory data) = manager.staticcall(
            abi.encodeWithSignature("hasRole(bytes32,address)", bytes32(0), msg.sender)
        );
        if (!ok || data.length != 32 || !abi.decode(data, (bool))) revert NotAdmin();
    }

    /// @notice Start the timer on an upgrade. Queuing is public and emits, so
    ///         holders can see a pending implementation and exit before it lands.
    /// @param impl The implementation contract to install after the delay.
    function queueUpgrade(address impl) external {
        if (manager == address(0)) revert NoManager();
        _requireAdmin();
        if (impl == address(0) || impl.code.length == 0) revert BadImplementation();
        pendingImplementation = impl;
        upgradeEta = uint64(block.timestamp) + upgradeDelay();
        emit UpgradeQueued(impl, upgradeEta);
    }

    /// @notice Abandon the queued upgrade. Used to withdraw a proposal, and as
    ///         the immediate response if an admin key is suspected compromised.
    function cancelUpgrade() external {
        if (manager == address(0)) revert NoManager();
        _requireAdmin();
        emit UpgradeCancelled(pendingImplementation);
        pendingImplementation = address(0);
        upgradeEta = 0;
    }

    /// @notice UUPS upgrade authorization: DEFAULT_ADMIN_ROLE on the linked
    ///         MarketplaceManager, AND the exact implementation must have been
    ///         queued at least `upgradeDelay()` ago and not left to go stale.
    /// @dev Previously this was a bare role check, so one compromised admin key
    ///      could swap in a malicious implementation and sweep every escrowed
    ///      wei in a single transaction with no warning. It was also a complete
    ///      no-op when manager == address(0), which made such a proxy upgradable
    ///      by anyone; that case now reverts, permanently freezing the
    ///      implementation of any ungated (test-only) deployment.
    function _authorizeUpgrade(address newImplementation) internal override {
        if (manager == address(0)) revert NoManager();
        _requireAdmin();
        if (newImplementation != pendingImplementation) revert UpgradeNotQueued();
        if (block.timestamp < upgradeEta) revert UpgradeNotReady();
        if (block.timestamp > uint256(upgradeEta) + MAX_UPGRADE_WINDOW) revert UpgradeExpired();
        pendingImplementation = address(0);
        upgradeEta = 0;
    }

    /// @dev Storage gap for future upgrades — preserves child storage layout
    ///      across implementation upgrades. Follows OpenZeppelin UUPS convention.
    /// @dev 48 → 47: pendingImplementation (address) and upgradeEta (uint64)
    ///      pack into one freshly-claimed slot ahead of this gap.
    uint256[47] private __gap;
}
