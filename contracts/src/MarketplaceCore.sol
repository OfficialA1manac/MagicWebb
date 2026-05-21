// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {ReentrancyGuard} from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import {Pausable}        from "@openzeppelin/contracts/utils/Pausable.sol";
import {AccessControl}   from "@openzeppelin/contracts/access/AccessControl.sol";
import {IERC721}         from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155}        from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";
import {ERC1155Holder}   from "@openzeppelin/contracts/token/ERC1155/utils/ERC1155Holder.sol";
import {IERC2981}        from "@openzeppelin/contracts/interfaces/IERC2981.sol";
import {IERC165}         from "@openzeppelin/contracts/utils/introspection/IERC165.sol";
import {RoyaltyRegistry} from "./RoyaltyRegistry.sol";

error TransferFailed();
error WithdrawFailed();
error InvalidFee();
error ZeroAddress();

enum TokenStandard { ERC721, ERC1155 }

/// @title MarketplaceCore
/// @notice Shared base: immutable fee config, royalty routing, NFT dispatch, access control, pausability.
/// @dev Fee routing: `feeVault` is immutable (redirect requires redeployment — protects users from rug).
///      `feeBps` is also immutable; only emergency pause can stop trading, not fee manipulation.
///      `royaltyRegistry` is mutable by DEFAULT_ADMIN_ROLE so the registry can be upgraded without
///      redeploying the market contracts. Royalty lookup: ERC-2981 on the NFT takes priority;
///      registry is the fallback for non-ERC-2981 collections.
abstract contract MarketplaceCore is ReentrancyGuard, Pausable, AccessControl, ERC1155Holder {
    /// @notice Grants emergency pause/unpause. Intended for Safe multi-sig.
    bytes32 public constant PAUSER_ROLE = keccak256("PAUSER_ROLE");

    /// @notice Hard cap on platform fee: 10%.
    uint16 public constant MAX_FEE_BPS = 1_000;
    /// @notice Hard cap on royalty enforced during payment split: 25%.
    uint16 public constant MAX_ROYALTY_CAP_BPS = 2_500;

    /// @notice Platform fee in basis points. Immutable post-deploy.
    uint16  public immutable feeBps;
    /// @notice Recipient of the platform fee on every trade. Immutable post-deploy.
    address public immutable feeVault;

    /// @notice Optional royalty registry. Set to address(0) to disable registry fallback.
    address public royaltyRegistry;

    event RoyaltyRegistryUpdated(address indexed prev, address indexed next);

    constructor(address vault, uint16 fee, address admin) {
        if (vault == address(0) || admin == address(0)) revert ZeroAddress();
        if (fee > MAX_FEE_BPS) revert InvalidFee();
        feeVault = vault;
        feeBps   = fee;
        _grantRole(DEFAULT_ADMIN_ROLE, admin);
        _grantRole(PAUSER_ROLE, admin);
    }

    // ── Admin ─────────────────────────────────────────────────────────────

    function pause()   external onlyRole(PAUSER_ROLE) { _pause(); }
    function unpause() external onlyRole(PAUSER_ROLE) { _unpause(); }

    function setRoyaltyRegistry(address registry) external onlyRole(DEFAULT_ADMIN_ROLE) {
        if (registry == address(0)) revert ZeroAddress();
        emit RoyaltyRegistryUpdated(royaltyRegistry, registry);
        royaltyRegistry = registry;
    }

    // ── Fee + royalty split ───────────────────────────────────────────────

    /// @dev Splits `salePrice` into platform fee + royalty + seller payment.
    ///      Royalty is capped at MAX_ROYALTY_CAP_BPS and cannot cause fee + royalty > salePrice.
    ///      Returns (fee, royalty, sellerAmt).
    function _splitAndPay(
        address seller,
        uint256 salePrice,
        address collection,
        uint256 tokenId
    ) internal returns (uint256 fee, uint256 royalty, uint256 sellerAmt) {
        fee = (salePrice * feeBps) / 10_000;

        (address royaltyReceiver, uint256 royaltyAmt) = _resolveRoyalty(collection, tokenId, salePrice);
        // Cap royalty so fee + royalty never exceeds salePrice.
        if (royaltyAmt + fee > salePrice) {
            royaltyAmt = salePrice > fee ? salePrice - fee : 0;
        }
        royalty = royaltyAmt;

        unchecked { sellerAmt = salePrice - fee - royalty; }

        if (fee > 0) {
            (bool ok,) = feeVault.call{value: fee}("");
            if (!ok) revert TransferFailed();
        }
        if (royalty > 0 && royaltyReceiver != address(0)) {
            (bool ok,) = royaltyReceiver.call{value: royalty}("");
            if (!ok) revert TransferFailed();
        }
        (bool ok2,) = seller.call{value: sellerAmt}("");
        if (!ok2) revert TransferFailed();
    }

    /// @dev Two-tier royalty lookup: NFT's own ERC-2981 first, registry fallback.
    function _resolveRoyalty(address collection, uint256 tokenId, uint256 salePrice)
        internal view returns (address receiver, uint256 amount)
    {
        // 1. Try ERC-2981 on the NFT contract.
        try IERC165(collection).supportsInterface(type(IERC2981).interfaceId) returns (bool supported) {
            if (supported) {
                try IERC2981(collection).royaltyInfo(tokenId, salePrice) returns (address r, uint256 a) {
                    if (r != address(0) && a > 0) {
                        // Enforce cap even on native ERC-2981.
                        uint256 cap = (salePrice * MAX_ROYALTY_CAP_BPS) / 10_000;
                        return (r, a > cap ? cap : a);
                    }
                } catch {}
            }
        } catch {}

        // 2. Registry fallback.
        if (royaltyRegistry != address(0)) {
            (receiver, amount) = RoyaltyRegistry(royaltyRegistry).getRoyalty(collection, tokenId, salePrice);
            uint256 cap = (salePrice * MAX_ROYALTY_CAP_BPS) / 10_000;
            if (amount > cap) amount = cap;
        }
    }

    // ── Token dispatch ────────────────────────────────────────────────────

    /// @dev Standard-aware safeTransfer. ERC-721 ignores `amount`.
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

    // ── ERC-165 ───────────────────────────────────────────────────────────

    function supportsInterface(bytes4 interfaceId)
        public view override(AccessControl, ERC1155Holder)
        returns (bool)
    {
        return super.supportsInterface(interfaceId);
    }
}
