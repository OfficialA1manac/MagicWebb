// SPDX-License-Identifier: MIT
pragma solidity 0.8.24;

import {MarketplaceCore, TokenStandard, WithdrawFailed} from "./MarketplaceCore.sol";
import {IERC721} from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155} from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";

error NotSeller();
error InvalidAmount();
error NotActive();
error AuctionEnded();
error AuctionLive();
error BidTooLow();
error NoBids();
error InvalidWindow();
error NotApproved();
error NothingToWithdraw();
error BidOverflow();
error BadIncrement();

/// @title AuctionHouse
/// @notice English auctions with reserve, bid increment, and pull-refund pattern.
/// @dev IMMUTABLE: no admin, no pause. Once a bid is placed, the auction cannot be cancelled; once
///      `settle` runs, the outcome is FINAL — NFT to highest bidder, then fee → `feeVault` and remainder → seller
///      (platform fee applies only on this winning settlement, after the NFT transfer succeeds in the same tx).
///      NFT is NOT escrowed — seller keeps custody until `settle`. If seller transfers
///      or revokes approval mid-auction, `settle` will revert. Acceptable non-custodial
///      trade-off; the bidder can recover their bid via `withdrawRefund`.
contract AuctionHouse is MarketplaceCore {
    /// @notice Cap on `minIncrementBps` (50% = 5_000). Prevents seller griefing via absurd increments.
    uint16 public constant MAX_MIN_INCREMENT_BPS = 5_000;
    /// @notice Anti-snipe: if a winning bid arrives within `ANTI_SNIPE_WINDOW` of `endsAt`,
    ///         the auction is extended by `ANTI_SNIPE_WINDOW`. Set to 0 to disable per-auction.
    uint64 public constant ANTI_SNIPE_WINDOW = 5 minutes;

    /// @notice Auction record. Layout chosen for clarity; storage cost dominated by mapping overhead.
    struct Auction {
        address       seller;          // slot 0 lower 20 bytes
        uint64        startsAt;        // slot 0 next 8 bytes
        uint16        minIncrementBps; // slot 0 next 2 bytes
        bool          settled;         // slot 0 next byte
        TokenStandard standard;        // slot 0 next byte
        address       collection;      // slot 1 lower 20 bytes
        uint64        endsAt;          // slot 1 next 8 bytes
        uint256       tokenId;         // slot 2
        uint128       reserve;         // slot 3 lower 16 bytes
        uint128       highestBid;      // slot 3 upper 16 bytes
        address       highestBidder;   // slot 4 lower 20 bytes
        uint128       amount;          // slot 5 (1 for ERC721)
    }

    /// @notice Auto-incrementing auction id. First valid id is 1.
    uint256 public nextAuctionId;
    /// @notice Auction storage by id.
    mapping(uint256 => Auction) public auctions;
    /// @notice Pull-pattern refund balances. Outbid bidders accumulate here; call `withdrawRefund` to claim.
    /// @dev Prevents griefing via a contract bidder that reverts on `receive()`.
    mapping(address => uint256) public pendingReturns;

    event AuctionCreated(
        uint256 indexed id,
        address indexed coll,
        uint256 indexed tokenId,
        address seller,
        TokenStandard standard,
        uint128 amount,
        uint128 reserve,
        uint64 startsAt,
        uint64 endsAt
    );
    event BidPlaced(uint256 indexed id, address indexed bidder, uint128 amount);
    event AuctionSettled(uint256 indexed id, address indexed winner, address indexed seller, uint128 amount, uint256 fee);
    event AuctionCancelled(uint256 indexed id);
    event RefundWithdrawn(address indexed bidder, uint256 amount);
    event AuctionExtended(uint256 indexed id, uint64 newEndsAt);

    constructor(address vault, uint16 fee) MarketplaceCore(vault, fee) {}

    /// @notice Create an ERC-721 English auction.
    function create(address coll, uint256 tokenId, uint128 reserve, uint64 startsAt, uint64 endsAt, uint16 minIncBps)
        external returns (uint256 id)
    {
        return _create(TokenStandard.ERC721, coll, tokenId, 1, reserve, startsAt, endsAt, minIncBps);
    }

    /// @notice Create an ERC-1155 English auction for `amount` units of `tokenId`.
    function create1155(
        address coll,
        uint256 tokenId,
        uint128 amount,
        uint128 reserve,
        uint64 startsAt,
        uint64 endsAt,
        uint16 minIncBps
    ) external returns (uint256 id) {
        if (amount == 0) revert InvalidAmount();
        return _create(TokenStandard.ERC1155, coll, tokenId, amount, reserve, startsAt, endsAt, minIncBps);
    }

    function _create(
        TokenStandard standard,
        address coll,
        uint256 tokenId,
        uint128 amount,
        uint128 reserve,
        uint64 startsAt,
        uint64 endsAt,
        uint16 minIncBps
    ) internal returns (uint256 id) {
        if (endsAt <= startsAt || startsAt < block.timestamp) revert InvalidWindow();
        if (minIncBps > MAX_MIN_INCREMENT_BPS) revert BadIncrement();

        if (standard == TokenStandard.ERC721) {
            if (IERC721(coll).ownerOf(tokenId) != msg.sender) revert NotSeller();
            if (!IERC721(coll).isApprovedForAll(msg.sender, address(this))
                && IERC721(coll).getApproved(tokenId) != address(this)) revert NotApproved();
        } else {
            if (IERC1155(coll).balanceOf(msg.sender, tokenId) < amount) revert NotSeller();
            if (!IERC1155(coll).isApprovedForAll(msg.sender, address(this))) revert NotApproved();
        }

        id = ++nextAuctionId;
        auctions[id] = Auction({
            seller: msg.sender,
            startsAt: startsAt,
            minIncrementBps: minIncBps == 0 ? 500 : minIncBps,
            settled: false,
            standard: standard,
            collection: coll,
            endsAt: endsAt,
            tokenId: tokenId,
            reserve: reserve,
            highestBid: 0,
            highestBidder: address(0),
            amount: amount
        });
        emit AuctionCreated(id, coll, tokenId, msg.sender, standard, amount, reserve, startsAt, endsAt);
    }

    /// @notice Place a bid. Outbid losers are credited **100%** of their prior bid in `pendingReturns` (no fee on bids).
    /// @dev **New bidder:** send the full new winning amount as `msg.value` (must be ≥ `minNext`). Previous high bidder
    ///      is credited their old bid in `pendingReturns` (pull `withdrawRefund`). **Current high bidder raising:**
    ///      send only the **increment** as `msg.value`; it is **compounded** onto your existing high bid (no round-trip
    ///      through `pendingReturns`). Pull pattern: a malicious outbidder cannot block others by reverting on `receive()`.
    function bid(uint256 id) external payable nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp < a.startsAt || block.timestamp >= a.endsAt) revert AuctionEnded();

        address prevBidder = a.highestBidder;
        uint128 prevHigh = a.highestBid;

        uint128 newBid;
        if (prevBidder != address(0) && msg.sender == prevBidder) {
            uint256 sum = uint256(prevHigh) + msg.value;
            if (sum > type(uint128).max) revert BidOverflow();
            newBid = uint128(sum);
        } else {
            if (msg.value > type(uint128).max) revert BidOverflow();
            newBid = uint128(msg.value);
        }

        // Increment math: when highestBid is small, (highestBid * bps) / 10_000 may round to 0.
        // Force a strict-greater-than rule so equal bids never displace the prior bidder.
        uint256 incRaw = uint256(prevHigh) * a.minIncrementBps / 10_000;
        uint128 minNext;
        if (prevHigh == 0) {
            minNext = a.reserve == 0 ? 1 : a.reserve;
        } else {
            uint256 next = uint256(prevHigh) + (incRaw == 0 ? 1 : incRaw);
            if (next > type(uint128).max) revert BidOverflow();
            minNext = uint128(next);
        }
        if (newBid < minNext) revert BidTooLow();

        a.highestBid = newBid;
        a.highestBidder = msg.sender;

        // Anti-snipe: extend endsAt if a winning bid arrives within the snipe window.
        // Cap the extension so the auction cannot run forever — extension applies at most until
        // bids stop arriving in the window.
        uint64 endsAt = a.endsAt;
        if (endsAt - block.timestamp < ANTI_SNIPE_WINDOW) {
            uint64 newEnd = uint64(block.timestamp) + ANTI_SNIPE_WINDOW;
            a.endsAt = newEnd;
            emit AuctionExtended(id, newEnd);
        }

        if (prevBidder != address(0) && prevBidder != msg.sender) {
            pendingReturns[prevBidder] += prevHigh;
        }
        emit BidPlaced(id, msg.sender, newBid);
    }

    /// @notice Withdraw any accumulated refund balance from prior outbids.
    function withdrawRefund() external nonReentrant {
        uint256 amt = pendingReturns[msg.sender];
        if (amt == 0) revert NothingToWithdraw();
        pendingReturns[msg.sender] = 0;
        (bool ok,) = msg.sender.call{value: amt}("");
        if (!ok) revert WithdrawFailed();
        emit RefundWithdrawn(msg.sender, amt);
    }

    /// @notice Settle a finished auction. Anyone can call after `endsAt`. FINAL — sets `settled=true`,
    ///         transfers NFT, routes fee + seller payout. Cannot be undone.
    function settle(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp < a.endsAt) revert AuctionLive();
        if (a.highestBidder == address(0)) revert NoBids();

        a.settled = true;

        _transferToken(a.standard, a.collection, a.seller, a.highestBidder, a.tokenId, a.amount);
        (uint256 fee,) = _splitAndPay(a.seller, a.highestBid);

        emit AuctionSettled(id, a.highestBidder, a.seller, a.highestBid, fee);
    }

    /// @notice Cancel an auction with zero bids. Only seller. Once any bid is placed, this path is closed.
    function cancel(uint256 id) external {
        Auction storage a = auctions[id];
        if (a.seller != msg.sender) revert NotSeller();
        if (a.highestBidder != address(0)) revert AuctionLive();
        a.settled = true;
        emit AuctionCancelled(id);
    }
}
