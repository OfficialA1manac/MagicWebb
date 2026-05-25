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
error AlreadyListed();
error BatchTooLarge();
error TransferFailed();

/// @dev Max listing duration. Prevents listings expiring decades in the future.
uint64 constant MAX_LISTING_DURATION = 365 days;

/// @title Marketplace
/// @notice Fixed-price, time-bound listings for ERC-721 and ERC-1155 tokens.
/// @dev Non-custodial: tokens stay with seller until a buyer settles. Approval required.
///      Once `buy` settles the trade is FINAL — no reverse, refund, or admin override.
///      Funds flow atomically: 1.5% platform fee → feeRecipient wallet, remainder → seller.
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

    /// @notice listings[collection][tokenId] → Listing.
    mapping(address => mapping(uint256 => Listing)) public listings;

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

    constructor(address recipient, address admin)
        MarketplaceCore(recipient, admin)
    {}

    // ── List ──────────────────────────────────────────────────────────────

    function list(address coll, uint256 id, uint128 price, uint64 expiresAt)
        external payable whenNotPaused
    {
        uint256 fee = (uint256(price) * PLATFORM_FEE_BPS) / 10_000;
        if (msg.value != fee) revert WrongPrice();
        if (fee > 0) {
            (bool ok,) = feeRecipient.call{value: fee}("");
            if (!ok) revert TransferFailed();
        }
        _list(TokenStandard.ERC721, coll, id, 1, price, expiresAt);
    }

    function list1155(address coll, uint256 id, uint128 amount, uint128 price, uint64 expiresAt)
        external payable whenNotPaused
    {
        if (amount == 0) revert InvalidAmount();
        uint256 fee = (uint256(price) * PLATFORM_FEE_BPS) / 10_000;
        if (msg.value != fee) revert WrongPrice();
        if (fee > 0) {
            (bool ok,) = feeRecipient.call{value: fee}("");
            if (!ok) revert TransferFailed();
        }
        _list(TokenStandard.ERC1155, coll, id, amount, price, expiresAt);
    }

    // ── Batch List ────────────────────────────────────────────────────────

    struct BatchItem {
        address coll;
        uint256 id;
        uint128 price;
        uint64  expiresAt;
    }

    /// @notice List up to 50 ERC-721 tokens in one transaction.
    ///         Caller must have approved this contract on each collection.
    ///         msg.value must equal sum of 1.5% listing fees across all items.
    function batchList(BatchItem[] calldata items) external payable whenNotPaused {
        if (items.length == 0 || items.length > 50) revert BatchTooLarge();
        uint256 totalFee;
        for (uint256 i; i < items.length; ++i) {
            totalFee += (uint256(items[i].price) * PLATFORM_FEE_BPS) / 10_000;
        }
        if (msg.value != totalFee) revert WrongPrice();
        if (totalFee > 0) {
            (bool ok,) = feeRecipient.call{value: totalFee}("");
            if (!ok) revert TransferFailed();
        }
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
        if (price == 0) revert WrongPrice();
        if (expiresAt <= block.timestamp) revert InvalidExpiry();
        if (expiresAt > block.timestamp + MAX_LISTING_DURATION) revert InvalidExpiry();

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
            seller:    msg.sender,
            expiresAt: expiresAt,
            standard:  standard,
            price:     price,
            amount:    amount
        });
        emit Listed(coll, id, msg.sender, standard, amount, price, expiresAt);
    }

    // ── Cancel ────────────────────────────────────────────────────────────

    /// @notice Cancel an unsold listing. Seller only. Works even while paused so sellers can unwind.
    function cancel(address coll, uint256 id) external {
        Listing memory l = listings[coll][id];
        if (l.seller != msg.sender) revert NotOwner();
        delete listings[coll][id];
        emit Cancelled(coll, id, msg.sender);
    }

    // ── Buy ───────────────────────────────────────────────────────────────

    /// @notice Buy a listed token at exactly the listing price.
    /// @dev FINAL on success. NFT → buyer, then 1.5% fee → feeRecipient wallet, remainder → seller.
    ///      Entire tx reverts if NFT transfer fails — no fee is taken, listing remains valid.
    function buy(address coll, uint256 id) external payable nonReentrant whenNotPaused {
        Listing memory l = listings[coll][id];
        if (l.seller == address(0)) revert NotListed();
        if (block.timestamp > l.expiresAt) revert Expired();
        if (msg.value != l.price) revert WrongPrice();

        delete listings[coll][id];

        _transferToken(l.standard, coll, l.seller, msg.sender, id, l.amount);
        uint256 fee = _splitAndPay(l.seller, msg.value);

        emit Bought(coll, id, msg.sender, l.seller, l.standard, l.amount, l.price, fee);
    }
}
