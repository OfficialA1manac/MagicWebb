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
error InvalidFee();
error ZeroAddress();

enum TokenStandard { ERC721, ERC1155 }

/// @title MarketplaceCore
/// @notice Shared base: immutable fee config, NFT dispatch, access control, pausability.
/// @dev Single platform fee only — no royalties.
///      feeVault and feeBps are both immutable post-deploy (protects traders from rug/fee changes).
abstract contract MarketplaceCore is ReentrancyGuard, Pausable, AccessControl, ERC1155Holder {
    bytes32 public constant PAUSER_ROLE = keccak256("PAUSER_ROLE");

    /// @notice Hard cap on platform fee: 10%.
    uint16 public constant MAX_FEE_BPS = 1_000;

    /// @notice Platform fee in basis points. Immutable post-deploy.
    uint16  public immutable feeBps;
    /// @notice Recipient of the platform fee on every trade. Immutable post-deploy.
    address public immutable feeVault;

    constructor(address vault, uint16 fee, address admin) {
        if (vault == address(0) || admin == address(0)) revert ZeroAddress();
        if (fee > MAX_FEE_BPS) revert InvalidFee();
        feeVault = vault;
        feeBps   = fee;
        _grantRole(DEFAULT_ADMIN_ROLE, admin);
        _grantRole(PAUSER_ROLE, admin);
    }

    // ── Admin ──────────────────────────────────────────────────────────────────

    function pause()   external onlyRole(PAUSER_ROLE) { _pause(); }
    function unpause() external onlyRole(PAUSER_ROLE) { _unpause(); }

    // ── Fee split ──────────────────────────────────────────────────────────────

    /// @dev Takes platform fee from salePrice, sends fee to feeVault, remainder to seller.
    ///      Returns the fee amount taken.
    function _splitAndPay(address seller, uint256 salePrice) internal returns (uint256 fee) {
        fee = (salePrice * feeBps) / 10_000;
        uint256 sellerAmt;
        unchecked { sellerAmt = salePrice - fee; }

        if (fee > 0) {
            (bool ok,) = feeVault.call{value: fee}("");
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
