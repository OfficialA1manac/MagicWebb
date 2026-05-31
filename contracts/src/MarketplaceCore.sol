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
/// @notice Shared base: immutable fee config, NFT dispatch, taker-fee helpers.
/// @dev Unified 1.5% taker fee. Sellers never pay. Buyers/bidders pay price + 1.5% on top.
///      feeRecipient immutable post-deploy. No pause, no admin, no upgrade.
///      MIN_PRICE = 0.01 FLR floor on all priced inputs.
abstract contract MarketplaceCore is ReentrancyGuard, ERC1155Holder {
    /// @notice Platform fee in basis points: 150 = 1.5%.
    uint16 public constant PLATFORM_FEE_BPS = 150;

    /// @notice Minimum value for list price, auction reserve, or offer amount.
    uint128 public constant MIN_PRICE = 0.01 ether;

    /// @notice Wallet that receives all platform fees. Immutable post-deploy.
    address public immutable feeRecipient;

    constructor(address recipient) {
        if (recipient == address(0)) revert ZeroAddress();
        feeRecipient = recipient;
    }

    // ── Fee helpers ────────────────────────────────────────────────────────────

    /// @dev 1.5% surcharge on a commitment value (price / bid / offer).
    function _feeOnTop(uint256 commit) internal pure returns (uint256) {
        return (commit * PLATFORM_FEE_BPS) / 10_000;
    }

    /// @dev Reverts if `v` is below the global floor.
    function _checkMin(uint128 v) internal pure {
        if (v < MIN_PRICE) revert BelowMinPrice();
    }

    /// @dev Forwards `fee` to feeRecipient and `bid` to seller. Used when caller
    ///      already separated commitment and surcharge (auction settle, offer accept,
    ///      taker-pay buy).
    function _payOut(address seller, uint256 bid, uint256 fee) internal {
        if (fee > 0) {
            (bool ok,) = feeRecipient.call{value: fee}("");
            if (!ok) revert TransferFailed();
        }
        if (bid > 0) {
            (bool ok2,) = seller.call{value: bid}("");
            if (!ok2) revert TransferFailed();
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
