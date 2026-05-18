// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

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
error TooEarly();
error NotWinner();

/// @title AuctionHouse
/// @notice English auctions with reserve, bid increment, anti-snipe, and pull-refund pattern.
/// @dev IMMUTABLE: no admin, no pause. Once a bid is placed, the auction cannot be cancelled; once
///      `settle` runs, the outcome is FINAL — NFT to highest bidder, then fee → `feeVault` and remainder → seller.
///      NFT is NOT escrowed — seller keeps custody until `settle`. If seller transfers
///      or revokes approval mid-auction, `settle` will revert. Acceptable non-custodial trade-off;
///      the bidder can recover their bid via `withdrawRefund` (outbid) or `reclaimBid` (stuck auction).
contract AuctionHouse is MarketplaceCore {
    /// @notice Cap on `minIncrementBps` (50%). Prevents seller griefing via absurd increments.
    uint16 public constant MAX_MIN_INCREMENT_BPS = 5_000;
    /// @notice Anti-snipe: bids within this window of `endsAt` extend the auction.
    uint64 public constant ANTI_SNIPE_WINDOW = 5 minutes;
    /// @notice Maximum total extension via anti-snipe past `originalEndsAt`.
    uint64 public constant ANTI_SNIPE_MAX_EXTENSION = 2 days;
    /// @notice After `endsAt + SETTLE_DEADLINE` with no settlement, the winner may reclaim their bid.
    /// @dev Safety valve: prevents ETH lockup if seller's receive() permanently reverts.
    uint64 public constant SETTLE_DEADLINE = 7 days;

    /// @notice Auction record.
    /// Slot layout (no wasted space):
    ///   slot 0: seller(20) + startsAt(8) + minIncrementBps(2) + settled(1) + standard(1) = 32 bytes
    ///   slot 1: collection(20) + endsAt(8) + originalEndsAt(4) = 32 bytes
    ///   slot 2: tokenId(32)
    ///   slot 3: reserve(16) + highestBid(16)
    ///   slot 4: highestBidder(20) [12 bytes padding]
    ///   slot 5: amount(16) [16 bytes padding]
    struct Auction {
        address       seller;          // slot 0 lower 20 bytes
        uint64        startsAt;        // slot 0 next 8 bytes
        uint16        minIncrementBps; // slot 0 next 2 bytes
        bool          settled;         // slot 0 next 1 byte
        TokenStandard standard;        // slot 0 next 1 byte  — slot 0 full
        address       collection;      // slot 1 lower 20 bytes
        uint64        endsAt;          // slot 1 next 8 bytes
        uint32        originalEndsAt;  // slot 1 next 4 bytes — slot 1 full (anti-snipe cap baseline)
        uint256       tokenId;         // slot 2
        uint128       reserve;         // slot 3 lower 16 bytes
        uint128       highestBid;      // slot 3 upper 16 bytes
        address       highestBidder;   // slot 4 lower 20 bytes
        uint128       amount;          // slot 5 lower 16 bytes (1 for ERC721)
    }

    /// @notice Auto-incrementing auction id. First valid id is 1.
    uint256 public nextAuctionId;
    /// @notice Auction storage by id.
    mapping(uint256 => Auction) public auctions;
    /// @notice Pull-pattern refund balances. Outbid bidders accumulate here; call `withdrawRefund` to claim.
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
    event BidReclaimed(uint256 indexed id, address indexed winner, uint256 amount);

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
            seller:          msg.sender,
            startsAt:        startsAt,
            minIncrementBps: minIncBps == 0 ? 500 : minIncBps,
            settled:         false,
            standard:        standard,
            collection:      coll,
            endsAt:          endsAt,
            originalEndsAt:  uint32(endsAt),
            tokenId:         tokenId,
            reserve:         reserve,
            highestBid:      0,
            highestBidder:   address(0),
            amount:          amount
        });
        emit AuctionCreated(id, coll, tokenId, msg.sender, standard, amount, reserve, startsAt, endsAt);
    }

    /// @notice Place a bid. Outbid losers are credited 100% of their prior bid in `pendingReturns`.
    /// @dev Current high bidder raising: send only the increment as `msg.value`; compounded onto existing bid.
    function bid(uint256 id) external payable nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp < a.startsAt || block.timestamp >= a.endsAt) revert AuctionEnded();

        address prevBidder = a.highestBidder;
        uint128 prevHigh   = a.highestBid;

        uint128 newBid;
        if (prevBidder != address(0) && msg.sender == prevBidder) {
            uint256 sum = uint256(prevHigh) + msg.value;
            if (sum > type(uint128).max) revert BidOverflow();
            newBid = uint128(sum);
        } else {
            if (msg.value > type(uint128).max) revert BidOverflow();
            newBid = uint128(msg.value);
        }

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

        a.highestBid    = newBid;
        a.highestBidder = msg.sender;

        // Anti-snipe: extend endsAt if winning bid arrives within the snipe window.
        // Extension is capped at originalEndsAt + ANTI_SNIPE_MAX_EXTENSION to prevent infinite auctions.
        uint64 endsAt = a.endsAt;
        if (endsAt - block.timestamp < ANTI_SNIPE_WINDOW) {
            uint64 absoluteCap = uint64(a.originalEndsAt) + ANTI_SNIPE_MAX_EXTENSION;
            uint64 newEnd = uint64(block.timestamp) + ANTI_SNIPE_WINDOW;
            if (newEnd > absoluteCap) newEnd = absoluteCap;
            if (newEnd > endsAt) {
                a.endsAt = newEnd;
                emit AuctionExtended(id, newEnd);
            }
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
    ///         transfers NFT, routes fee + seller payout atomically.
    function settle(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp < a.endsAt) revert AuctionLive();

        address highBidder = a.highestBidder;
        if (highBidder == address(0)) revert NoBids();

        // Effect first, then cache remaining fields to memory before interactions.
        a.settled = true;
        address       sel    = a.seller;
        TokenStandard std    = a.standard;
        address       coll   = a.collection;
        uint256       tid    = a.tokenId;
        uint128       amt    = a.amount;
        uint128       hieBid = a.highestBid;

        _transferToken(std, coll, sel, highBidder, tid, amt);
        (uint256 fee,) = _splitAndPay(sel, hieBid);

        emit AuctionSettled(id, highBidder, sel, hieBid, fee);
    }

    /// @notice Cancel an auction with zero bids. Seller only. Once any bid is placed this path is closed.
    function cancel(uint256 id) external {
        Auction storage a = auctions[id];
        if (a.seller != msg.sender) revert NotSeller();
        if (a.highestBidder != address(0)) revert AuctionLive();
        a.settled = true;
        emit AuctionCancelled(id);
    }

    /// @notice Safety valve: if `settle` has not been called within `SETTLE_DEADLINE` of auction end,
    ///         the highest bidder may reclaim their ETH. Prevents permanent lockup when seller's
    ///         receive() permanently reverts.
    function reclaimBid(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp < uint64(a.endsAt) + SETTLE_DEADLINE) revert TooEarly();
        if (msg.sender != a.highestBidder) revert NotWinner();

        a.settled = true;
        uint128 amt = a.highestBid;
        a.highestBid = 0;

        (bool ok,) = msg.sender.call{value: amt}("");
        if (!ok) revert WithdrawFailed();
        emit BidReclaimed(id, msg.sender, amt);
    }
}
