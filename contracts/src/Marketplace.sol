// SPDX-License-Identifier: MIT
pragma solidity 0.8.24;

import {MarketplaceCore, TokenStandard} from "./MarketplaceCore.sol";
import {IERC721} from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155} from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";

error NotOwner();
error NotListed();
error WrongPrice();
error Expired();
error NotApproved();
error InvalidExpiry();
error InvalidAmount();
error AlreadyListed();

/// @title Marketplace
/// @notice Fixed-price, time-bound listings for ERC-721 and ERC-1155 tokens.
/// @dev Non-custodial: tokens stay with seller until a buyer settles. Approval to this contract is required.
///      Once `buy` settles, the trade is FINAL — there is no reverse, refund, or admin override. Funds flow
///      atomically: platform fee → immutable `feeVault`, remainder → seller.
contract Marketplace is MarketplaceCore {
    /// @notice Listing record. Two slots: (seller + expiresAt + standard) | (price + amount).
    struct Listing {
        address       seller;     // slot 0 lower 20 bytes
        uint64        expiresAt;  // slot 0 next 8 bytes
        TokenStandard standard;   // slot 0 next 1 byte
        uint128       price;      // slot 1 lower 16 bytes
        uint128       amount;     // slot 1 upper 16 bytes (1 for ERC721)
    }

    /// @notice listings[collection][tokenId] => Listing. For ERC1155 the same (coll,id) slot is reused per-seller via cancel/relist.
    mapping(address => mapping(uint256 => Listing)) public listings;

    event Listed(
        address indexed coll,
        uint256 indexed id,
        address indexed seller,
        TokenStandard standard,
        uint128 amount,
        uint128 price,
        uint64 expiresAt
    );
    event Cancelled(address indexed coll, uint256 indexed id, address indexed seller);
    event Bought(
        address indexed coll,
        uint256 indexed id,
        address indexed buyer,
        address seller,
        TokenStandard standard,
        uint128 amount,
        uint128 price,
        uint256 fee
    );

    constructor(address vault, uint16 fee) MarketplaceCore(vault, fee) {}

    /// @notice List an ERC-721 token at a fixed price.
    function list(address coll, uint256 id, uint128 price, uint64 expiresAt) external {
        _list(TokenStandard.ERC721, coll, id, 1, price, expiresAt);
    }

    /// @notice List an ERC-1155 amount at a fixed total price.
    function list1155(address coll, uint256 id, uint128 amount, uint128 price, uint64 expiresAt) external {
        if (amount == 0) revert InvalidAmount();
        _list(TokenStandard.ERC1155, coll, id, amount, price, expiresAt);
    }

    function _list(
        TokenStandard standard,
        address coll,
        uint256 id,
        uint128 amount,
        uint128 price,
        uint64 expiresAt
    ) internal {
        if (price == 0) revert WrongPrice();
        if (expiresAt <= block.timestamp) revert InvalidExpiry();

        // Prevent a second seller from overwriting another active listing for the same (coll,id).
        // Original seller can always overwrite (re-list with new params); anyone else is blocked.
        address curSeller = listings[coll][id].seller;
        if (curSeller != address(0) && curSeller != msg.sender) revert AlreadyListed();

        if (standard == TokenStandard.ERC721) {
            if (IERC721(coll).ownerOf(id) != msg.sender) revert NotOwner();
            if (!IERC721(coll).isApprovedForAll(msg.sender, address(this))
                && IERC721(coll).getApproved(id) != address(this)) revert NotApproved();
        } else {
            if (IERC1155(coll).balanceOf(msg.sender, id) < amount) revert NotOwner();
            if (!IERC1155(coll).isApprovedForAll(msg.sender, address(this))) revert NotApproved();
        }

        listings[coll][id] = Listing({
            seller: msg.sender,
            expiresAt: expiresAt,
            standard: standard,
            price: price,
            amount: amount
        });
        emit Listed(coll, id, msg.sender, standard, amount, price, expiresAt);
    }

    /// @notice Cancel an active (unsold) listing. Seller only. Pre-trade only — cannot reverse a completed sale.
    function cancel(address coll, uint256 id) external {
        Listing memory l = listings[coll][id];
        if (l.seller != msg.sender) revert NotOwner();
        delete listings[coll][id];
        emit Cancelled(coll, id, msg.sender);
    }

/// @notice Buy a listed token at the listing price. Reverts if expired, missing, or wrong msg.value.
/// @dev Final on success: NFT moves to buyer, then fee → `feeVault` and remainder → seller (same atomic tx).
///      If the transfer reverts, the whole transaction reverts — no fee is taken and the listing remains valid until expiry.
    function buy(address coll, uint256 id) external payable nonReentrant {
        Listing memory l = listings[coll][id];
        if (l.seller == address(0)) revert NotListed();
        if (block.timestamp > l.expiresAt) revert Expired();
        if (msg.value != l.price) revert WrongPrice();

        delete listings[coll][id];

        _transferToken(l.standard, coll, l.seller, msg.sender, id, l.amount);
        (uint256 fee,) = _splitAndPay(l.seller, msg.value);

        emit Bought(coll, id, msg.sender, l.seller, l.standard, l.amount, l.price, fee);
    }
}
