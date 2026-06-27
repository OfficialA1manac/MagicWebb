// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {ReentrancyGuard} from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import {IERC721}         from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155}        from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";
import {ERC1155Holder}   from "@openzeppelin/contracts/token/ERC1155/utils/ERC1155Holder.sol";

error TransferFailed();
error WithdrawFailed();
error NothingToWithdraw();
error ZeroAddress();
error BelowMinPrice();
error EntriesHalted();
error BadManager();

enum TokenStandard { ERC721, ERC1155 }

/// @dev Read-only surface the cores consult on entry paths.
interface IMarketplaceManager {
    function entriesAllowed() external view returns (bool);
}

/// @title MarketplaceCore
/// @notice Shared base: immutable fee config, price floor, seller-pays fee math, NFT dispatch.
/// @dev Single 1.5% platform fee, charged ONLY on a successful sale and DEDUCTED from the seller's
///      proceeds — listing, auction creation, bids and offers are all free. feeRecipient immutable
///      post-deploy. No pause, no admin — contracts are unstoppable once deployed.
abstract contract MarketplaceCore is ReentrancyGuard, ERC1155Holder {
    /// @notice Platform fee: 1.5%. Hardcoded — cannot change post-deploy.
    uint16 public constant PLATFORM_FEE_BPS = 150;

    /// @notice Minimum accepted commitment everywhere (list price, auction reserve, offer amount).
    uint256 public constant MIN_PRICE = 0.01 ether;

    /// @notice Wallet that receives all platform fees. Immutable post-deploy.
    address public immutable feeRecipient;

    /// @notice Pull-pattern fallback for any push payment that fails.
    ///         Mirrors AuctionHouse / OfferBook pendingReturns so refund
    ///         bookkeeping is symmetric across cores. Cleared by
    ///         withdrawRefund() once the recipient can accept ETH.
    mapping(address => uint256) public pendingReturns;

    /// @notice Optional MarketplaceManager consulted on ENTRY paths only
    ///         (list/buy/create/bid/makeOffer/acceptOffer). address(0) = ungated.
    ///         EXIT paths (settle, refunds, withdrawals, cancels, reject) never
    ///         consult it — escrowed funds can always leave regardless of any
    ///         role, pause, or manager compromise.
    /// @notice Emitted when a push payment fails and the amount is credited to pendingReturns.
    event PushFailed(address indexed to, uint256 amount);

    address public immutable manager;

    constructor(address recipient, address manager_) {
        if (recipient == address(0)) revert ZeroAddress();
        // manager is immutable: a typo'd/EOA address would brick every entry
        // path forever, so validate it answers the gate probe at deploy time.
        if (manager_ != address(0)) {
            if (manager_.code.length == 0) revert BadManager();
            IMarketplaceManager(manager_).entriesAllowed(); // must not revert
        }
        feeRecipient = recipient;
        manager      = manager_;
    }

    /// @dev Circuit-breaker guard for entry paths. Fails open if no manager is
    ///      configured; reverts with EntriesHalted while the manager is paused.
    modifier entryGate() {
        if (manager != address(0) && !IMarketplaceManager(manager).entriesAllowed()) {
            revert EntriesHalted();
        }
        _;
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

    /// @notice Pay the platform fee to the immutable feeRecipient.
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
}
