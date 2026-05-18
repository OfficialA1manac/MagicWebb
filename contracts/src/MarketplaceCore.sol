// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {ReentrancyGuard} from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import {IERC721} from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155} from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";
import {ERC1155Holder} from "@openzeppelin/contracts/token/ERC1155/utils/ERC1155Holder.sol";

error TransferFailed();
error WithdrawFailed();
error InvalidFee();
error ZeroAddress();

enum TokenStandard { ERC721, ERC1155 }

/// @title MarketplaceCore
/// @notice Shared base for trade contracts: immutable fee config, fee routing, NFT transfer, reentrancy guard.
/// @dev IMMUTABLE BY DESIGN. `feeVault` and `feeBps` are set in the constructor and CANNOT be changed.
///      There is no owner, no admin, no upgradability, no pause switch. Every settled trade pays the platform
///      fee directly to the constructor-supplied `feeVault` address atomically with the seller payout.
abstract contract MarketplaceCore is ReentrancyGuard, ERC1155Holder {
    /// @notice Hard cap on platform fee (10%). Enforced in constructor.
    uint16 public constant MAX_FEE_BPS = 1_000;

    /// @notice Platform fee in basis points (1 bp = 0.01%). Set once at deploy. Cap = `MAX_FEE_BPS`.
    uint16  public immutable feeBps;
    /// @notice Recipient of fee on every trade. Set once at deploy. CANNOT be changed.
    address public immutable feeVault;

    constructor(address vault, uint16 fee) {
        if (vault == address(0)) revert ZeroAddress();
        if (fee > MAX_FEE_BPS) revert InvalidFee();
        feeVault = vault;
        feeBps   = fee;
    }

    /// @dev Splits `amount` into fee + seller cut and pushes both. Fee ≤ amount by construction.
    function _splitAndPay(address seller, uint256 amount) internal returns (uint256 fee, uint256 sellerAmt) {
        fee = (amount * feeBps) / 10_000;
        unchecked { sellerAmt = amount - fee; } // fee ≤ amount via /10_000
        if (fee > 0) {
            (bool ok,) = feeVault.call{value: fee}("");
            if (!ok) revert TransferFailed();
        }
        (bool ok2,) = seller.call{value: sellerAmt}("");
        if (!ok2) revert TransferFailed();
    }

    /// @dev Standard-aware safeTransfer dispatch. ERC721 ignores `amount` (must be 1).
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
