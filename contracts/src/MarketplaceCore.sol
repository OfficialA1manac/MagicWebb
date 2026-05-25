// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {ReentrancyGuard} from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import {IERC721}         from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155}        from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";
import {ERC1155Holder}   from "@openzeppelin/contracts/token/ERC1155/utils/ERC1155Holder.sol";

error TransferFailed();
error WithdrawFailed();
error ZeroAddress();

enum TokenStandard { ERC721, ERC1155 }

/// @title MarketplaceCore
/// @notice Shared base: immutable fee config, NFT dispatch.
/// @dev Single 1.5% platform fee on all operations. feeRecipient immutable post-deploy.
///      No pause, no admin — contracts are unstoppable once deployed.
abstract contract MarketplaceCore is ReentrancyGuard, ERC1155Holder {
    /// @notice Platform fee: 1.5%. Hardcoded — cannot change post-deploy.
    uint16 public constant PLATFORM_FEE_BPS = 150;

    /// @notice Wallet that receives all platform fees. Immutable post-deploy.
    address public immutable feeRecipient;

    constructor(address recipient) {
        if (recipient == address(0)) revert ZeroAddress();
        feeRecipient = recipient;
    }

    // ── Fee split ──────────────────────────────────────────────────────────────

    /// @dev Sends fee to feeRecipient and remainder to seller. Returns fee taken.
    function _splitAndPay(address seller, uint256 salePrice) internal returns (uint256 fee) {
        fee = (salePrice * PLATFORM_FEE_BPS) / 10_000;
        uint256 sellerAmt;
        unchecked { sellerAmt = salePrice - fee; }

        if (fee > 0) {
            (bool ok,) = feeRecipient.call{value: fee}("");
            if (!ok) revert TransferFailed();
        }
        (bool ok2,) = seller.call{value: sellerAmt}("");
        if (!ok2) revert TransferFailed();
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
