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

enum TokenStandard { ERC721, ERC1155 }

/// @title MarketplaceCore
/// @notice Shared base: immutable fee config, price floor, taker-pays fee math, NFT dispatch.
/// @dev Single 1.5% platform fee, paid ON TOP by the taker (buyer/bidder/offerer) — sellers never pay.
///      feeRecipient immutable post-deploy. No pause, no admin — contracts are unstoppable once deployed.
abstract contract MarketplaceCore is ReentrancyGuard, ERC1155Holder {
    /// @notice Platform fee: 1.5%. Hardcoded — cannot change post-deploy.
    uint16 public constant PLATFORM_FEE_BPS = 150;

    /// @notice Minimum accepted commitment everywhere (list price, auction reserve, offer amount).
    uint256 public constant MIN_PRICE = 0.01 ether;

    /// @notice Wallet that receives all platform fees. Immutable post-deploy.
    address public immutable feeRecipient;

    constructor(address recipient) {
        if (recipient == address(0)) revert ZeroAddress();
        feeRecipient = recipient;
    }

    // ── Taker-pays fee math ──────────────────────────────────────────────────

    /// @dev 1.5% fee for a given commitment. The taker always pays this ON TOP — sellers keep 100%.
    function _feeOf(uint256 commitment) internal pure returns (uint256) {
        return (commitment * PLATFORM_FEE_BPS) / 10_000;
    }

    /// @dev Forward the platform fee to the immutable feeRecipient. Reverts on failure.
    function _payFee(uint256 fee) internal {
        if (fee == 0) return;
        (bool ok,) = feeRecipient.call{value: fee}("");
        if (!ok) revert TransferFailed();
    }

    /// @dev Pay `amount` to `to`. Reverts on failure.
    function _pay(address to, uint256 amount) internal {
        if (amount == 0) return;
        (bool ok,) = to.call{value: amount}("");
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
