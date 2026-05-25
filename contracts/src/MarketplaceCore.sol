// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {ReentrancyGuard} from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import {Pausable}        from "@openzeppelin/contracts/utils/Pausable.sol";
import {AccessControl}   from "@openzeppelin/contracts/access/AccessControl.sol";
import {IERC721}         from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155}        from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";
import {ERC1155Holder}   from "@openzeppelin/contracts/token/ERC1155/utils/ERC1155Holder.sol";

error TransferFailed();
error WithdrawFailed();
error ZeroAddress();

enum TokenStandard { ERC721, ERC1155 }

/// @title MarketplaceCore
/// @notice Shared base: immutable fee config, NFT dispatch, access control, pausability.
/// @dev Single 1.5% platform fee applied to all operations (listing, buy, auction, offer).
///      feeRecipient is immutable post-deploy — protects traders from fee/recipient changes.
abstract contract MarketplaceCore is ReentrancyGuard, Pausable, AccessControl, ERC1155Holder {
    bytes32 public constant PAUSER_ROLE = keccak256("PAUSER_ROLE");

    /// @notice Platform fee: 1.5% on all operations. Hardcoded — cannot be changed post-deploy.
    uint16 public constant PLATFORM_FEE_BPS = 150;

    /// @notice Wallet address that receives the platform fee on every operation. Immutable post-deploy.
    address public immutable feeRecipient;

    constructor(address recipient, address admin) {
        if (recipient == address(0) || admin == address(0)) revert ZeroAddress();
        feeRecipient = recipient;
        _grantRole(DEFAULT_ADMIN_ROLE, admin);
        _grantRole(PAUSER_ROLE, admin);
    }

    // ── Admin ──────────────────────────────────────────────────────────────────

    function pause()   external onlyRole(PAUSER_ROLE) { _pause(); }
    function unpause() external onlyRole(PAUSER_ROLE) { _unpause(); }

    // ── Fee split ──────────────────────────────────────────────────────────────

    /// @dev Deducts 1.5% fee from salePrice, sends fee directly to feeRecipient, remainder to seller.
    ///      Returns the fee amount taken.
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

    // ── ERC-165 ────────────────────────────────────────────────────────────────

    function supportsInterface(bytes4 interfaceId)
        public view override(AccessControl, ERC1155Holder)
        returns (bool)
    {
        return super.supportsInterface(interfaceId);
    }
}
