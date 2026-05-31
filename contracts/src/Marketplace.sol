// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {MarketplaceCore, TokenStandard} from "./MarketplaceCore.sol";
import {IERC721}  from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155} from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";

error NotOwner();
error NotListed();
error WrongPrice();
error Expired();
error NotApproved();
error InvalidExpiry();
error InvalidAmount();
error BatchTooLarge();

/// @dev Max listing duration: 90 days (per spec).
uint64 constant MAX_LISTING_DURATION = 90 days;

/// @title Marketplace
/// @notice Fixed-price, time-bound listings for ERC-721 and ERC-1155.
///         Non-custodial: NFT stays in the seller's wallet until settle.
///         Listing is FREE (no value sent). Buyers pay price + 1.5% fee on top.
///         Listings key on (collection, tokenId, seller) — ERC-1155 stacks per holder.
contract Marketplace is MarketplaceCore {
    /// @notice Listing record. Two storage slots.
    struct Listing {
        address       seller;    // slot 0
        uint64        expiresAt; // slot 0
        TokenStandard standard;  // slot 0
        uint128       price;     // slot 1
        uint128       amount;    // slot 1
    }

    /// @notice listings[collection][tokenId][seller] → Listing.
    ///         Triple key supports per-holder ERC-1155 listings.
    mapping(address => mapping(uint256 => mapping(address => Listing))) public listings;

    event Listed(
        address indexed coll,
        uint256 indexed id,
        address indexed seller,
        TokenStandard standard,
        uint128 amount,
        uint128 price,
        uint64  expiresAt
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

    constructor(address recipient)
        MarketplaceCore(recipient)
    {}

    // ── List (free) ────────────────────────────────────────────────────────

    function list(address coll, uint256 id, uint128 price, uint64 expiresAt) external {
        _list(TokenStandard.ERC721, coll, id, 1, price, expiresAt);
    }

    function list1155(address coll, uint256 id, uint128 amount, uint128 price, uint64 expiresAt) external {
        if (amount == 0) revert InvalidAmount();
        _list(TokenStandard.ERC1155, coll, id, amount, price, expiresAt);
    }

    // ── Batch List (free) ──────────────────────────────────────────────────

    struct BatchItem {
        address coll;
        uint256 id;
        uint128 price;
        uint64  expiresAt;
    }

    /// @notice List up to 50 ERC-721 tokens in one transaction. Free.
    function batchList(BatchItem[] calldata items) external {
        if (items.length == 0 || items.length > 50) revert BatchTooLarge();
        for (uint256 i; i < items.length; ++i) {
            _list(TokenStandard.ERC721, items[i].coll, items[i].id, 1, items[i].price, items[i].expiresAt);
        }
    }

    function _list(
        TokenStandard standard,
        address coll,
        uint256 id,
        uint128 amount,
        uint128 price,
        uint64  expiresAt
    ) internal {
        _checkMin(price);
        if (expiresAt <= block.timestamp) revert InvalidExpiry();
        if (expiresAt > block.timestamp + MAX_LISTING_DURATION) revert InvalidExpiry();

        if (standard == TokenStandard.ERC721) {
            if (IERC721(coll).ownerOf(id) != msg.sender) revert NotOwner();
            if (!IERC721(coll).isApprovedForAll(msg.sender, address(this))
                && IERC721(coll).getApproved(id) != address(this)) revert NotApproved();
        } else {
            if (IERC1155(coll).balanceOf(msg.sender, id) < amount) revert NotOwner();
            if (!IERC1155(coll).isApprovedForAll(msg.sender, address(this))) revert NotApproved();
        }

        listings[coll][id][msg.sender] = Listing({
            seller:    msg.sender,
            expiresAt: expiresAt,
            standard:  standard,
            price:     price,
            amount:    amount
        });
        emit Listed(coll, id, msg.sender, standard, amount, price, expiresAt);
    }

    // ── Cancel ────────────────────────────────────────────────────────────

    /// @notice Cancel one of your active listings.
    function cancel(address coll, uint256 id) external {
        Listing memory l = listings[coll][id][msg.sender];
        if (l.seller != msg.sender) revert NotListed();
        delete listings[coll][id][msg.sender];
        emit Cancelled(coll, id, msg.sender);
    }

    // ── Buy ───────────────────────────────────────────────────────────────

    /// @notice Buy a specific seller's listing. msg.value MUST equal price + 1.5% fee.
    ///         seller param disambiguates per-holder ERC-1155 stacks.
    /// @dev FINAL on success. Reverts if the NFT no longer lives at `seller` (stale listing).
    ///      No exclusivity check across other markets — first settle wins.
    function buy(address coll, uint256 id, address seller) external payable nonReentrant {
        Listing memory l = listings[coll][id][seller];
        if (l.seller == address(0)) revert NotListed();
        if (block.timestamp > l.expiresAt) revert Expired();

        uint256 fee = _feeOnTop(l.price);
        if (msg.value != uint256(l.price) + fee) revert WrongPrice();

        delete listings[coll][id][seller];

        // Token transfer first — if NFT moved, this reverts and the buyer keeps their funds.
        _transferToken(l.standard, coll, l.seller, msg.sender, id, l.amount);
        _payOut(l.seller, l.price, fee);

        emit Bought(coll, id, msg.sender, l.seller, l.standard, l.amount, l.price, fee);
    }
}
