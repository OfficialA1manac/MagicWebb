// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {MarketplaceCore} from "./MarketplaceCore.sol";
import {IERC721} from "@openzeppelin/contracts/token/ERC721/IERC721.sol";

error NotSeller();
error NotActive();
error AuctionEnded();
error AuctionLive();
error BidTooLow();
error NoBids();
error InvalidWindow();
error NotApproved();
error NothingToWithdraw();

/// @title AuctionHouse
/// @notice English auctions with reserve, bid increment, and pull-refund pattern.
/// @dev NFT is NOT escrowed — seller keeps custody until `settle`. If seller transfers
///      or revokes approval mid-auction, `settle` will revert. Acceptable non-custodial
///      trade-off; the bidder can recover their bid via `withdrawRefund`.
contract AuctionHouse is MarketplaceCore {
    /// @notice Auction record. Layout chosen for clarity; storage cost dominated by mapping overhead.
    struct Auction {
        address seller;          // slot 0 lower 20 bytes
        uint64  startsAt;        // slot 0 next 8 bytes
        uint16  minIncrementBps; // slot 0 next 2 bytes
        bool    settled;         // slot 0 final byte
        address collection;      // slot 1 lower 20 bytes
        uint64  endsAt;          // slot 1 next 8 bytes
        uint256 tokenId;         // slot 2
        uint128 reserve;         // slot 3 lower 16 bytes
        uint128 highestBid;      // slot 3 upper 16 bytes
        address highestBidder;   // slot 4
    }

    /// @notice Auto-incrementing auction id. First valid id is 1.
    uint256 public nextAuctionId;
    /// @notice Auction storage by id.
    mapping(uint256 => Auction) public auctions;
    /// @notice Pull-pattern refund balances. Outbid bidders accumulate here; call `withdrawRefund` to claim.
    /// @dev Prevents griefing via a contract bidder that reverts on `receive()`.
    mapping(address => uint256) public pendingReturns;

    event AuctionCreated(uint256 indexed id, address indexed coll, uint256 indexed tokenId, address seller, uint128 reserve, uint64 startsAt, uint64 endsAt);
    event BidPlaced(uint256 indexed id, address indexed bidder, uint128 amount);
    event AuctionSettled(uint256 indexed id, address indexed winner, address indexed seller, uint128 amount, uint256 fee);
    event AuctionCancelled(uint256 indexed id);
    event RefundWithdrawn(address indexed bidder, uint256 amount);

    constructor(address admin, address vault, uint16 fee) MarketplaceCore(admin, vault, fee) {}

    /// @notice Create an English auction. Caller must own and have approved this contract for the token.
    function create(address coll, uint256 tokenId, uint128 reserve, uint64 startsAt, uint64 endsAt, uint16 minIncBps)
        external whenNotPaused returns (uint256 id)
    {
        if (endsAt <= startsAt || startsAt < block.timestamp) revert InvalidWindow();
        if (IERC721(coll).ownerOf(tokenId) != msg.sender) revert NotSeller();
        if (!IERC721(coll).isApprovedForAll(msg.sender, address(this))
            && IERC721(coll).getApproved(tokenId) != address(this)) revert NotApproved();

        id = ++nextAuctionId;
        auctions[id] = Auction({
            seller: msg.sender,
            startsAt: startsAt,
            minIncrementBps: minIncBps == 0 ? 500 : minIncBps,
            settled: false,
            collection: coll,
            endsAt: endsAt,
            tokenId: tokenId,
            reserve: reserve,
            highestBid: 0,
            highestBidder: address(0)
        });
        emit AuctionCreated(id, coll, tokenId, msg.sender, reserve, startsAt, endsAt);
    }

    /// @notice Place a bid. Outbid bid is credited to the prior bidder via `pendingReturns`.
    /// @dev Pull pattern: a malicious bidder cannot block outbids by reverting on `receive()`.
    function bid(uint256 id) external payable nonReentrant whenNotPaused {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp < a.startsAt || block.timestamp >= a.endsAt) revert AuctionEnded();

        uint128 amount = uint128(msg.value);
        uint128 minNext = a.highestBid == 0
            ? (a.reserve == 0 ? 1 : a.reserve)
            : a.highestBid + uint128((uint256(a.highestBid) * a.minIncrementBps) / 10_000);
        if (amount < minNext) revert BidTooLow();

        address prev    = a.highestBidder;
        uint128 prevAmt = a.highestBid;

        a.highestBid    = amount;
        a.highestBidder = msg.sender;

        if (prev != address(0)) {
            pendingReturns[prev] += prevAmt;
        }
        emit BidPlaced(id, msg.sender, amount);
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

    /// @notice Settle a finished auction. Anyone can call after `endsAt`.
    function settle(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp < a.endsAt) revert AuctionLive();
        if (a.highestBidder == address(0)) revert NoBids();

        a.settled = true;

        _transferNFT(a.collection, a.seller, a.highestBidder, a.tokenId);
        (uint256 fee,) = _splitAndPay(a.seller, a.highestBid);

        emit AuctionSettled(id, a.highestBidder, a.seller, a.highestBid, fee);
    }

    /// @notice Cancel an auction with zero bids. Only seller.
    function cancel(uint256 id) external {
        Auction storage a = auctions[id];
        if (a.seller != msg.sender) revert NotSeller();
        if (a.highestBidder != address(0)) revert AuctionLive();
        a.settled = true;
        emit AuctionCancelled(id);
    }
}
