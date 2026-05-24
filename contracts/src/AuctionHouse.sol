// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {MarketplaceCore, TokenStandard, WithdrawFailed} from "./MarketplaceCore.sol";
import {IERC721}  from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
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
error CommitMismatch();
error CommitTooFresh();
error NoCommit();

/// @title AuctionHouse
/// @notice English auctions with reserve, bid increment, anti-snipe, commit-reveal MEV protection,
///         and pull-refund pattern.
///
/// MEV protection rationale — commit-reveal chosen over batched settlement:
///   Batched settlement adds per-bid block-window latency, ruining the live auction feel.
///   Commit-reveal has 2-tx UX cost but keeps continuous bidding. On Flare (~1.8s blocks),
///   commit+reveal completes in ≈4 s. Front-runners can't act on a revealed bid amount until
///   it's already in the same block, eliminating profitable sandwich attacks on bid transactions.
///
/// Commit-reveal flow:
///   1. Bidder calls `commitBid(id, keccak256(abi.encode(id, bidder, fullBidAmount, salt)))`.
///   2. After COMMIT_DELAY_BLOCKS, bidder calls `bid(id, fullBidAmount, salt)` with
///      msg.value = increment (fullBidAmount - existingBid, or fullBidAmount for first bid).
///   3. Contract verifies hash, enforces delay, applies bid logic.
///
/// Non-custodial: seller keeps NFT until `settle`. If approval revoked mid-auction,
/// `settle` reverts but bidder can `withdrawRefund` (outbid) or `reclaimBid` (stuck auction).
contract AuctionHouse is MarketplaceCore {
    /// @notice Cap on `minIncrementBps` (50%). Prevents seller griefing via absurd increments.
    uint16  public constant MAX_MIN_INCREMENT_BPS  = 5_000;
    /// @notice Anti-snipe: bids within this window extend the auction.
    uint64  public constant ANTI_SNIPE_WINDOW      = 5 minutes;
    /// @notice Maximum total anti-snipe extension past `originalEndsAt`.
    uint64  public constant ANTI_SNIPE_MAX_EXTENSION = 2 days;
    /// @notice After `endsAt + SETTLE_DEADLINE` with no settlement the winner may reclaim.
    uint64  public constant SETTLE_DEADLINE        = 7 days;
    /// @notice Minimum blocks between commit and reveal. Prevents same-block front-running.
    ///         At Flare's ~1.8s block time: 2 blocks ≈ 3.6 s delay.
    uint8   public constant COMMIT_DELAY_BLOCKS    = 2;

    /// @notice Auction record.
    /// Slot layout (packed, 6 storage slots):
    ///   slot 0: seller(20) + startsAt(8) + minIncrementBps(2) + settled(1) + standard(1)
    ///   slot 1: collection(20) + endsAt(8) + originalEndsAt(4)
    ///   slot 2: tokenId(32)
    ///   slot 3: reserve(16) + highestBid(16)
    ///   slot 4: highestBidder(20)
    ///   slot 5: amount(16)
    struct Auction {
        address       seller;
        uint64        startsAt;
        uint16        minIncrementBps;
        bool          settled;
        TokenStandard standard;
        address       collection;
        uint64        endsAt;
        uint32        originalEndsAt;
        uint256       tokenId;
        uint128       reserve;
        uint128       highestBid;
        address       highestBidder;
        uint128       amount;
    }

    uint256 public nextAuctionId;
    mapping(uint256 => Auction) public auctions;

    /// @notice Pull-pattern refund balances from outbid events.
    mapping(address => uint256) public pendingReturns;

    /// @notice Commit-reveal: commitment hash per (auction, bidder).
    mapping(uint256 => mapping(address => bytes32)) public commitments;
    /// @notice Block number at which the commitment was made.
    mapping(uint256 => mapping(address => uint256)) public commitBlock;

    // ── Events ────────────────────────────────────────────────────────────

    event AuctionCreated(
        uint256 indexed id,
        address indexed coll,
        uint256 indexed tokenId,
        address seller,
        TokenStandard standard,
        uint128 amount,
        uint128 reserve,
        uint64  startsAt,
        uint64  endsAt
    );
    event BidCommitted(uint256 indexed id, address indexed bidder, bytes32 commitment);
    event BidPlaced(uint256 indexed id, address indexed bidder, uint128 amount);
    event AuctionSettled(uint256 indexed id, address indexed winner, address indexed seller, uint128 amount, uint256 fee);
    event AuctionCancelled(uint256 indexed id);
    event RefundWithdrawn(address indexed bidder, uint256 amount);
    event AuctionExtended(uint256 indexed id, uint64 newEndsAt);
    event BidReclaimed(uint256 indexed id, address indexed winner, uint256 amount);

    constructor(address vault, uint16 fee, address admin)
        MarketplaceCore(vault, fee, admin)
    {}

    // ── Create ────────────────────────────────────────────────────────────

    function create(
        address coll,
        uint256 tokenId,
        uint128 reserve,
        uint64  startsAt,
        uint64  endsAt,
        uint16  minIncBps
    ) external whenNotPaused returns (uint256 id) {
        return _create(TokenStandard.ERC721, coll, tokenId, 1, reserve, startsAt, endsAt, minIncBps);
    }

    function create1155(
        address coll,
        uint256 tokenId,
        uint128 amount,
        uint128 reserve,
        uint64  startsAt,
        uint64  endsAt,
        uint16  minIncBps
    ) external whenNotPaused returns (uint256 id) {
        if (amount == 0) revert InvalidAmount();
        return _create(TokenStandard.ERC1155, coll, tokenId, amount, reserve, startsAt, endsAt, minIncBps);
    }

    function _create(
        TokenStandard standard,
        address coll,
        uint256 tokenId,
        uint128 amount,
        uint128 reserve,
        uint64  startsAt,
        uint64  endsAt,
        uint16  minIncBps
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

    // ── Commit-Reveal Bid ─────────────────────────────────────────────────

    /// @notice Phase 1: submit a commitment. No ETH required.
    ///         `commitment = keccak256(abi.encode(id, msg.sender, fullBidAmount, salt))`
    ///         where `fullBidAmount` is your total bid (not the increment).
    function commitBid(uint256 id, bytes32 commitment) external whenNotPaused {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp < a.startsAt || block.timestamp >= a.endsAt) revert AuctionEnded();
        commitments[id][msg.sender] = commitment;
        commitBlock[id][msg.sender] = block.number;
        emit BidCommitted(id, msg.sender, commitment);
    }

    /// @notice Phase 2: reveal and apply bid.
    ///         `fullBidAmount`: your total intended bid.
    ///         `msg.value`: for a new bidder = fullBidAmount; for the existing high bidder = fullBidAmount - existingBid.
    ///         Must be called at least COMMIT_DELAY_BLOCKS after `commitBid`.
    function bid(uint256 id, uint128 fullBidAmount, bytes32 salt)
        external payable nonReentrant whenNotPaused
    {
        // Verify commitment
        bytes32 expected = keccak256(abi.encode(id, msg.sender, fullBidAmount, salt));
        if (commitments[id][msg.sender] == bytes32(0)) revert NoCommit();
        if (commitments[id][msg.sender] != expected) revert CommitMismatch();
        if (block.number <= commitBlock[id][msg.sender] + uint256(COMMIT_DELAY_BLOCKS) - 1) revert CommitTooFresh();

        delete commitments[id][msg.sender];
        delete commitBlock[id][msg.sender];

        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp < a.startsAt || block.timestamp >= a.endsAt) revert AuctionEnded();

        address prevBidder = a.highestBidder;
        uint128 prevHigh   = a.highestBid;

        // Compound bid: existing high bidder raises; msg.value is the increment only.
        if (prevBidder != address(0) && msg.sender == prevBidder) {
            uint256 sum = uint256(prevHigh) + msg.value;
            if (sum > type(uint128).max) revert BidOverflow();
            if (uint128(sum) != fullBidAmount) revert CommitMismatch();
        } else {
            if (msg.value > type(uint128).max) revert BidOverflow();
            if (uint128(msg.value) != fullBidAmount) revert CommitMismatch();
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
        if (fullBidAmount < minNext) revert BidTooLow();

        a.highestBid    = fullBidAmount;
        a.highestBidder = msg.sender;

        // Anti-snipe extension
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
        emit BidPlaced(id, msg.sender, fullBidAmount);
    }

    // ── Withdraw refund ───────────────────────────────────────────────────

    /// @notice Withdraw accumulated refunds from outbid events. Works while paused.
    function withdrawRefund() external nonReentrant {
        uint256 amt = pendingReturns[msg.sender];
        if (amt == 0) revert NothingToWithdraw();
        pendingReturns[msg.sender] = 0;
        (bool ok,) = msg.sender.call{value: amt}("");
        if (!ok) revert WithdrawFailed();
        emit RefundWithdrawn(msg.sender, amt);
    }

    // ── Settle ────────────────────────────────────────────────────────────

    /// @notice Settle a finished auction. Anyone can call after `endsAt`. FINAL.
    ///         NFT → winner, fee → feeVault, royalty → creator, remainder → seller.
    function settle(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp < a.endsAt) revert AuctionLive();

        address highBidder = a.highestBidder;
        if (highBidder == address(0)) revert NoBids();

        a.settled = true;
        address       sel   = a.seller;
        TokenStandard std   = a.standard;
        address       coll  = a.collection;
        uint256       tid   = a.tokenId;
        uint128       amt   = a.amount;
        uint128       hieBid = a.highestBid;

        _transferToken(std, coll, sel, highBidder, tid, amt);
        uint256 fee = _splitAndPay(sel, hieBid);

        emit AuctionSettled(id, highBidder, sel, hieBid, fee);
    }

    // ── Cancel ────────────────────────────────────────────────────────────

    /// @notice Cancel an auction with zero bids. Seller only. Works while paused.
    function cancel(uint256 id) external {
        Auction storage a = auctions[id];
        if (a.seller != msg.sender) revert NotSeller();
        if (a.highestBidder != address(0)) revert AuctionLive();
        a.settled = true;
        emit AuctionCancelled(id);
    }

    // ── Safety valve ──────────────────────────────────────────────────────

    /// @notice If `settle` has not run within `SETTLE_DEADLINE` of auction end,
    ///         the highest bidder may reclaim their ETH. Works while paused.
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
