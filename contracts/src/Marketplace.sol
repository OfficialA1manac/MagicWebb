// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {MarketplaceCore, TokenStandard, BelowMinPrice} from "./MarketplaceCore.sol";
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

/// @dev Max listing duration. Prevents listings expiring decades in the future.
uint64 constant MAX_LISTING_DURATION = 90 days;

/// @title Marketplace
/// @notice Fixed-price, time-bound listings for ERC-721 and ERC-1155 tokens.
/// @dev Non-custodial: tokens stay with seller until buyer settles. Approval required.
///      Listing is FREE. The buyer pays exactly the asking price; a 1.5% platform fee is
///      deducted from the seller's proceeds (the seller receives 98.5%).
///      Listings are keyed by (collection, tokenId, seller): ERC-1155 holders each keep
///      their own stacked listing; for ERC-721 only the true owner's listing is
///      settle-able (a stale listing from a prior owner simply reverts on `buy`).
///      No exclusivity: the same NFT may also sit in the AuctionHouse / OfferBook —
///      first settle wins, the rest revert when the token has moved.
///      Once `buy` settles the trade is FINAL — no reverse, refund, or admin override.
///      Unstoppable: no pause, no admin. Runs forever.
contract Marketplace is MarketplaceCore {
    /// @notice Listing record. Two storage slots:
    ///   slot 0: seller(20) + expiresAt(8) + standard(1) [3 bytes padding]
    ///   slot 1: price(16)  + amount(16)
    struct Listing {
        address       seller;    // slot 0
        uint64        expiresAt; // slot 0
        TokenStandard standard;  // slot 0
        uint128       price;     // slot 1
        uint128       amount;    // slot 1 (always 1 for ERC-721)
    }

    /// @notice listings[collection][tokenId][seller] → Listing.
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

    constructor(address recipient, address manager_)
        MarketplaceCore(recipient, manager_)
    {}

    // ── List (free) ───────────────────────────────────────────────────────────

    /// @notice List an ERC-721 token at a fixed price. FREE — no listing fee.
    function list(address coll, uint256 id, uint128 price, uint64 expiresAt) external entryGate {
        _list(TokenStandard.ERC721, coll, id, 1, price, expiresAt);
    }

    /// @notice List ERC-1155 units at a fixed price. FREE — no listing fee.
    function list1155(address coll, uint256 id, uint128 amount, uint128 price, uint64 expiresAt) external entryGate {
        if (amount == 0) revert InvalidAmount();
        _list(TokenStandard.ERC1155, coll, id, amount, price, expiresAt);
    }

    // ── Batch List ────────────────────────────────────────────────────────────

    struct BatchItem {
        address coll;
        uint256 id;
        uint128 price;
        uint64  expiresAt;
    }

    /// @notice List up to 50 ERC-721 tokens in one transaction. FREE.
    ///         Caller must have approved this contract on each collection.
    function batchList(BatchItem[] calldata items) external entryGate {
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
        if (price < MIN_PRICE) revert BelowMinPrice();
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

    // ── Cancel ────────────────────────────────────────────────────────────────

    /// @notice Cancel an unsold listing. Seller only.
    function cancel(address coll, uint256 id) external {
        Listing memory l = listings[coll][id][msg.sender];
        if (l.seller != msg.sender) revert NotOwner(); // seller == address(0) → not listed
        delete listings[coll][id][msg.sender];
        emit Cancelled(coll, id, msg.sender);
    }

    // ── Buy (seller pays 1.5% on the sale) ────────────────────────────────────

    /// @notice Buy a listed token. Send exactly `price` as msg.value.
    /// @dev FINAL on success. NFT → buyer, 1.5% fee → feeRecipient, price − fee → seller.
    ///      The `seller` arg selects which listing to buy (listings are seller-keyed).
    ///      Entire tx reverts if the NFT transfer fails (seller no longer owns/approves) —
    ///      no fee is taken, the listing remains. This is how first-settle-wins works.
    function buy(address coll, uint256 id, address seller) external payable nonReentrant entryGate {
        Listing memory l = listings[coll][id][seller];
        if (l.seller == address(0)) revert NotListed();
        if (block.timestamp > l.expiresAt) revert Expired();

        if (msg.value != uint256(l.price)) revert WrongPrice();
        uint256 fee = _feeOf(l.price);

        delete listings[coll][id][seller];

        _transferToken(l.standard, coll, l.seller, msg.sender, id, l.amount);
        _payFee(fee);
        _pay(l.seller, uint256(l.price) - fee);

        emit Bought(coll, id, msg.sender, l.seller, l.standard, l.amount, l.price, fee);
    }
}
