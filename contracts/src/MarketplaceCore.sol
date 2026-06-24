// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {ReentrancyGuard} from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import {IERC721}         from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155}        from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";
import {ERC1155Holder}   from "@openzeppelin/contracts/token/ERC1155/utils/ERC1155Holder.sol";

error TransferFailed();
error WithdrawFailed();
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

    /// @notice Optional MarketplaceManager consulted on ENTRY paths only
    ///         (list/buy/create/bid/makeOffer/acceptOffer). address(0) = ungated.
    ///         EXIT paths (settle, refunds, withdrawals, cancels, reject) never
    ///         consult it — escrowed funds can always leave regardless of any
    ///         role, pause, or manager compromise.
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

    // ── Seller-pays fee math ─────────────────────────────────────────────────

    /// @dev 1.5% fee for a given sale price. Deducted from the seller's proceeds at settlement.
    function _feeOf(uint256 commitment) internal pure returns (uint256) {
        return (commitment * PLATFORM_FEE_BPS) / 10_000;
    }

    /// @dev Forward the platform fee to the immutable feeRecipient. Reverts on failure.
    ///      gas: 50_000 caps EIP-150 63/64 forwarding so a hostile recipient
    ///      contract cannot OOG-grief the settlement tx and trap buyer/seller funds.
    function _payFee(uint256 fee) internal {
        if (fee == 0) return;
        (bool ok,) = feeRecipient.call{gas: 50_000, value: fee}("");
        if (!ok) revert TransferFailed();
    }

    /// @dev Pay `amount` to `to`. Reverts on failure.
    ///      gas: 50_000 caps EIP-150 63/64 forwarding — a malicious seller
    ///      contract cannot burn all forwarded gas and permanently trap the
    ///      buyer's ETH in a failed buy() tx.
    function _pay(address to, uint256 amount) internal {
        if (amount == 0) return;
        (bool ok,) = to.call{gas: 50_000, value: amount}("");
        if (!ok) revert TransferFailed();
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
