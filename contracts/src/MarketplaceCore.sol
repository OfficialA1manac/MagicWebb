// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Initializable} from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import {UUPSUpgradeable} from "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import {ERC1155HolderUpgradeable} from "@openzeppelin/contracts-upgradeable/token/ERC1155/utils/ERC1155HolderUpgradeable.sol";
import {IERC721}  from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155} from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";
// v3.4: transient-storage (EIP-1153) reentrancy guard replaces OZ's
// storage-slot ReentrancyGuardUpgradeable (−2k gas per guarded call). To
// fall back (a target chain failing the Cancun probe), swap this import for
// ReentrancyGuardUpgradeable, restore it in the inheritance list, and
// re-add __ReentrancyGuard_init() in __MarketplaceCore_init.
import {TransientReentrancyGuard} from "./TransientReentrancyGuard.sol";

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
/// @dev Single 2% platform fee (1.5% platform + 0.5% keeper), charged ONLY on a successful sale and
///      DEDUCTED from the seller's proceeds — listing, auction creation, bids and offers are all
///      free. The keeper share replenishes the network keeper bot's gas; it goes to whatever
///      address the linked manager reports as `keeper()` at sale time. v3.4: feeRecipient and
///      manager are IMMUTABLES baked into the implementation bytecode (no SLOAD on hot paths);
///      changing either means installing a new implementation via the admin-gated UUPS path — the
///      same trust model as the old storage-with-no-setter, minus the gas. Nothing on the protocol
///      is pausable: no entry or exit path ever consults an off switch.
abstract contract MarketplaceCore is Initializable, TransientReentrancyGuard, ERC1155HolderUpgradeable, UUPSUpgradeable {
    /// @notice Total platform fee: 2% of the sale. Hardcoded — cannot change post-deploy.
    ///         Split PLATFORM_SHARE_BPS → feeRecipient and KEEPER_SHARE_BPS → manager.keeper().
    uint16 public constant PLATFORM_FEE_BPS = 200;
    /// @notice Share of the sale that goes to feeRecipient (the platform wallet): 1.5%.
    uint16 public constant PLATFORM_SHARE_BPS = 150;
    /// @notice Share of the sale that goes to the network keeper (gas replenishment): 0.5%.
    /// @dev Invariant: PLATFORM_SHARE_BPS + KEEPER_SHARE_BPS == PLATFORM_FEE_BPS (asserted in tests).
    uint16 public constant KEEPER_SHARE_BPS = 50;

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

    /// @notice Wallet that receives all platform fees. v3.4: immutable again —
    ///         baked into the implementation at deploy (per-network constructor
    ///         arg). Moving fees to a new recipient = building a new impl with
    ///         the new address and installing it through the admin-gated UUPS
    ///         path. Saves a cold SLOAD on every buy/acceptOffer/settle.
    /// @custom:oz-upgrades-unsafe-allow state-variable-immutable
    address public immutable feeRecipient;

    /// @notice Pull-pattern fallback for any push payment that fails.
    ///         Mirrors AuctionHouse / OfferBook pendingReturns so refund
    ///         bookkeeping is symmetric across cores. Cleared by
    ///         withdrawRefund() once the recipient can accept ETH.
    mapping(address => uint256) public pendingReturns;

    /// @notice Emitted when a push payment fails and the amount is credited to pendingReturns.
    event PushFailed(address indexed to, uint256 amount);

    /// @notice Emitted on every fee payment with the exact split. `platformShare + keeperShare`
    ///         equals the total fee carried in the sale event. `keeper` is address(0) and
    ///         `keeperShare` is 0 when no manager/keeper is resolvable (whole fee → feeRecipient).
    event FeeSplit(address indexed feeRecipient, uint256 platformShare, address indexed keeper, uint256 keeperShare);

    /// @notice Optional MarketplaceManager — the authority anchor (keeper,
    ///         admin) for upgrade gating and keeper consults. address(0) = no
    ///         roles and a permanently frozen implementation. It has no power
    ///         over funds and cannot halt any user action.
    ///         v3.4: immutable, and the manager itself is UNPROXIED — killing
    ///         both the SLOAD and the manager-side proxy hop on every keeper
    ///         authority consult (−6.8k…−9.8k on settle/cleanExpired/refunds).
    ///         Replacing the manager = new core impls baking the new address,
    ///         installed via the (old) admin-gated UUPS path.
    /// @custom:oz-upgrades-unsafe-allow state-variable-immutable
    address public immutable manager;

    /// @notice Implementation queued for upgrade, or address(0) when none is
    ///         pending. Packs into a single slot with upgradeEta.
    address public pendingImplementation;
    /// @notice Earliest timestamp at which `pendingImplementation` may be
    ///         installed. Zero when nothing is queued.
    uint64  public upgradeEta;

    /// @notice v3.4 implementation constructor: bakes feeRecipient + manager
    ///         as immutables and validates them at impl-deploy time. The
    ///         validation that used to live in the initializer moves here —
    ///         a typo'd/EOA manager would silently disable keeper roles and
    ///         freeze upgrades, so the probe runs before anything ships.
    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor(address recipient, address manager_) {
        if (recipient == address(0)) revert ZeroAddress();
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

    /// @notice One-time proxy initializer. v3.4: takes no arguments —
    ///         feeRecipient/manager live in the implementation as immutables.
    ///         Still initializer-gated so nobody can call it on the proxy
    ///         after deploy.
    function __MarketplaceCore_init() internal onlyInitializing {
        __ERC1155Holder_init();
        __UUPSUpgradeable_init();
    }

    // ═══════════════════════════════════════════════════════════════════════
    // Fee math — 2% platform fee (seller-pays, immutable), split 1.5% / 0.5%.
    // ═══════════════════════════════════════════════════════════════════════

    /// @notice Compute the 2% platform fee for a given sale commitment.
    /// @param commitment The gross sale amount (listing price / bid / offer principal).
    /// @return The total platform fee (2% of `commitment`; split by `_payFee`).
    /// @dev Seller-favourable TRUNCATION: `(commitment * 200) / 10_000` floors down.
    ///      For example, a 99-wei sale computes 99*200/10000 = 1 (1.98 truncated to 1).
    ///      The seller receives `commitment - fee`, so truncation always favours the
    ///      seller (less fee deducted). The lost fraction (< 1 wei per sale) is
    ///      economically negligible and cannot be gamed — the divisor (10_000) is
    ///      much larger than any practical price precision.
    function _feeOf(uint256 commitment) internal pure returns (uint256) {
        return (commitment * PLATFORM_FEE_BPS) / 10_000;
    }

    /// @notice Pay the platform fee: PLATFORM_SHARE_BPS/PLATFORM_FEE_BPS of it to the
    ///         on-chain feeRecipient, the rest to the network keeper reported by the manager.
    /// @param fee Total amount to forward (already computed via `_feeOf`).
    /// @dev Best-effort push with a 50,000-gas cap per EIP-150 63/64 forwarding.
    ///      If the feeRecipient is a contract that needs >50k gas for its receive()
    ///      (e.g. Gnosis Safe, Argent, smart wallet), the push falls back to
    ///      `pendingReturns[feeRecipient]` — the credit is visible on-chain and can
    ///      be pulled later via the uncapped `withdrawRefund()` path. This prevents
    ///      a broken or misconfigured feeRecipient from permanently DOSing every
    ///      buy() and acceptOffer() transaction on the protocol. The keeper share
    ///      uses the same push/pull-fallback via `_pay`.
    ///
    ///      Keeper share: `fee * KEEPER_SHARE_BPS / PLATFORM_FEE_BPS`, truncated
    ///      (rounding favours the platform share). The keeper is resolved with a
    ///      guarded staticcall to `manager.keeper()`; if there is no manager, the
    ///      probe fails, or it reports address(0), the keeper share is 0 and the
    ///      entire fee goes to feeRecipient. A fee payment can therefore never
    ///      revert because of the keeper leg.
    function _payFee(uint256 fee) internal {
        if (fee == 0) return;
        uint256 keeperCut;
        address k;
        if (manager != address(0)) {
            (bool okK, bytes memory data) = manager.staticcall(abi.encodeWithSignature("keeper()"));
            if (okK && data.length == 32) {
                k = abi.decode(data, (address));
                if (k != address(0)) {
                    keeperCut = (fee * KEEPER_SHARE_BPS) / PLATFORM_FEE_BPS;
                }
            }
        }
        if (keeperCut == 0) k = address(0);
        uint256 platformCut = fee - keeperCut;
        (bool ok,) = feeRecipient.call{gas: 50_000, value: platformCut}("");
        if (!ok) {
            pendingReturns[feeRecipient] += platformCut;
            emit PushFailed(feeRecipient, platformCut);
        }
        _pay(k, keeperCut);
        emit FeeSplit(feeRecipient, platformCut, k, keeperCut);
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
    ///         v3.4 (owner directive 2026-09-02): ZERO on every network —
    ///         queueUpgrade and upgradeTo run back-to-back, so upgrades are
    ///         instant on Coston2, Songbird and Flare alike. The 2-step queue
    ///         is retained for its event trail, exact-implementation match and
    ///         cancelUpgrade, not as a delay. SECURITY MODEL: with no notice
    ///         window, custody of the per-network admin key IS the entire
    ///         upgrade security until renounceAdmin() seals the network.
    /// @dev Kept as a function (not a constant) so a future upgrade can
    ///      reintroduce a delay without touching call sites.
    function upgradeDelay() public pure returns (uint64) {
        return 0;
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
    /// @dev v3.4: 47 → 49. feeRecipient and manager moved from storage to
    ///      immutables, freeing their two slots (the transient guard uses no
    ///      persistent storage either — ReentrancyGuardUpgradeable's _status
    ///      slot came from its own inherited layout, not this region).
    uint256[49] private __gap;
}
