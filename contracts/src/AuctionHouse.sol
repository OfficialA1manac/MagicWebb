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
error InvalidWindow();
error NotApproved();
error BidOverflow();
error BadIncrement();
error WrongBidValue();
error NothingToWithdraw();

/// @title AuctionHouse
/// @notice English auctions with auto-settlement. Single-step bidding.
///
/// Auction flow:
///   1. Seller calls `create` → AuctionCreated event. Auction starts immediately.
///   2. Bidder calls `bid(id, bidAmount)` with msg.value = bidAmount + 1.5% platform fee.
///      Outbid bidder is refunded immediately (full bid + fee). BidPlaced event.
///   3. After `endsAt`, keeper bot calls `settle(id)` → NFT → winner,
///      fee → feeRecipient, full bid → seller. AuctionSettled event.
///   4. Auto-cancel: if no bid within first 30 minutes, keeper calls `settle(id)` which
///      triggers `_cancelIfInactive` internally. Not directly callable externally.
///   5. Owner early cancel: seller calls `cancelEarly` any time before expiry (manual approval).
///      Highest bidder (if any) refunded in full automatically.
///
/// Fee semantics: bidder pays 1.5% on top of their bid. Fee is only kept by platform
/// when the bidder wins. Losing bidders receive their full payment (bid + fee) back.
///
/// Non-custodial: seller keeps NFT until settle. Approval must remain valid through auction end.
/// Unstoppable: no pause, no admin.
contract AuctionHouse is MarketplaceCore {
    /// @notice Cap on minIncrementBps (50%). Prevents seller griefing via absurd increments.
    uint16  public constant MAX_MIN_INCREMENT_BPS = 5_000;
    /// @notice Auction auto-cancels if no bid arrives within this window of creation.
    uint64  public constant NO_BID_CANCEL_WINDOW  = 30 minutes;

    /// @notice Auction record.
    /// slot 0: seller(20) + startsAt(8) + minIncrementBps(2) + settled(1) + standard(1)
    /// slot 1: collection(20) + endsAt(8)
    /// slot 2: tokenId(32)
    /// slot 3: reserve(16) + highestBid(16)
    /// slot 4: highestBidder(20)
    /// slot 5: amount(16) + highestTotal(16)
    struct Auction {
        address       seller;
        uint64        startsAt;
        uint16        minIncrementBps;
        bool          settled;
        TokenStandard standard;
        address       collection;
        uint64        endsAt;
        uint256       tokenId;
        uint128       reserve;
        uint128       highestBid;    // bid amount proper (used for reserve/increment checks)
        address       highestBidder;
        uint128       amount;        // token amount (always 1 for ERC-721)
        uint128       highestTotal;  // exact ETH held for highest bidder: bid + fee
    }

    uint256 public nextAuctionId;
    mapping(uint256 => Auction) public auctions;

    /// @notice Emergency fallback for push-refund failures (e.g. bidder is a non-receiving contract).
    mapping(address => uint256) public pendingReturns;

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
    event BidPlaced(uint256 indexed id, address indexed bidder, uint128 bidAmount, uint128 totalPaid);
    event AuctionSettled(uint256 indexed id, address indexed winner, address indexed seller, uint128 bidAmount, uint256 fee);
    event AuctionCancelled(uint256 indexed id);
    event RefundPushed(address indexed bidder, uint256 amount);

    constructor(address recipient)
        MarketplaceCore(recipient)
    {}

    // ── Create ────────────────────────────────────────────────────────────

    /// @notice Create an ERC-721 auction. Starts immediately at block.timestamp.
    /// @param coll       NFT collection address.
    /// @param tokenId    Token ID to auction.
    /// @param reserve    Minimum first bid (0 = accept any bid).
    /// @param endsAt     Unix timestamp when bidding closes.
    /// @param minIncBps  Minimum bid increment in bps (0 → defaults to 500 = 5%).
    function create(
        address coll,
        uint256 tokenId,
        uint128 reserve,
        uint64  endsAt,
        uint16  minIncBps
    ) external returns (uint256 id) {
        return _create(TokenStandard.ERC721, coll, tokenId, 1, reserve, endsAt, minIncBps);
    }

    /// @notice Create an ERC-1155 auction. Starts immediately at block.timestamp.
    function create1155(
        address coll,
        uint256 tokenId,
        uint128 amount,
        uint128 reserve,
        uint64  endsAt,
        uint16  minIncBps
    ) external returns (uint256 id) {
        if (amount == 0) revert InvalidAmount();
        return _create(TokenStandard.ERC1155, coll, tokenId, amount, reserve, endsAt, minIncBps);
    }

    function _create(
        TokenStandard standard,
        address coll,
        uint256 tokenId,
        uint128 amount,
        uint128 reserve,
        uint64  endsAt,
        uint16  minIncBps
    ) internal returns (uint256 id) {
        if (endsAt <= block.timestamp) revert InvalidWindow();
        if (minIncBps > MAX_MIN_INCREMENT_BPS) revert BadIncrement();

        if (standard == TokenStandard.ERC721) {
            if (IERC721(coll).ownerOf(tokenId) != msg.sender) revert NotSeller();
            if (!IERC721(coll).isApprovedForAll(msg.sender, address(this))
                && IERC721(coll).getApproved(tokenId) != address(this)) revert NotApproved();
        } else {
            if (IERC1155(coll).balanceOf(msg.sender, tokenId) < amount) revert NotSeller();
            if (!IERC1155(coll).isApprovedForAll(msg.sender, address(this))) revert NotApproved();
        }

        uint64 startsAt = uint64(block.timestamp);
        id = ++nextAuctionId;
        auctions[id] = Auction({
            seller:          msg.sender,
            startsAt:        startsAt,
            minIncrementBps: minIncBps == 0 ? 500 : minIncBps,
            settled:         false,
            standard:        standard,
            collection:      coll,
            endsAt:          endsAt,
            tokenId:         tokenId,
            reserve:         reserve,
            highestBid:      0,
            highestBidder:   address(0),
            amount:          amount,
            highestTotal:    0
        });
        emit AuctionCreated(id, coll, tokenId, msg.sender, standard, amount, reserve, startsAt, endsAt);
    }

    // ── Bid ───────────────────────────────────────────────────────────────

    /// @notice Place a bid. Send bidAmount + 1.5% platform fee as msg.value.
    ///         The fee is only kept by the platform if this bid wins. If outbid,
    ///         the previous highest bidder receives their full payment back immediately.
    /// @param id         Auction ID.
    /// @param bidAmount  The bid value (not including the fee).
    ///                   msg.value must equal bidAmount + floor(bidAmount * 150 / 10000).
    function bid(uint256 id, uint128 bidAmount) external payable nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp >= a.endsAt) revert AuctionEnded();

        // Compute required fee and validate exact payment
        uint128 fee = uint128(uint256(bidAmount) * PLATFORM_FEE_BPS / 10_000);
        if (uint256(bidAmount) + uint256(fee) > type(uint128).max) revert BidOverflow();
        uint128 totalRequired = bidAmount + fee;
        if (msg.value != totalRequired) revert WrongBidValue();

        // Enforce minimum bid (reserve or increment above current high)
        uint128 prevHigh = a.highestBid;
        uint128 minNext;
        if (prevHigh == 0) {
            minNext = a.reserve == 0 ? 1 : a.reserve;
        } else {
            uint256 inc  = uint256(prevHigh) * a.minIncrementBps / 10_000;
            uint256 next = uint256(prevHigh) + (inc == 0 ? 1 : inc);
            if (next > type(uint128).max) revert BidOverflow();
            minNext = uint128(next);
        }
        if (bidAmount < minNext) revert BidTooLow();

        // Snapshot previous leader before overwriting
        address prevBidder = a.highestBidder;
        uint128 prevTotal  = a.highestTotal;

        // Record new leader
        a.highestBid    = bidAmount;
        a.highestBidder = msg.sender;
        a.highestTotal  = totalRequired;

        // Push full refund to outbid bidder immediately
        if (prevBidder != address(0)) {
            (bool ok,) = prevBidder.call{value: prevTotal}("");
            if (ok) {
                emit RefundPushed(prevBidder, prevTotal);
            } else {
                // Fallback: store for manual withdrawal (edge case: non-receiving contract)
                pendingReturns[prevBidder] += prevTotal;
            }
        }

        emit BidPlaced(id, msg.sender, bidAmount, totalRequired);
    }

    // ── Settle ────────────────────────────────────────────────────────────

    /// @notice Settle a finished auction. Keeper bot calls this after endsAt.
    ///         NFT → winner. Fee (exact amount paid by bidder on top of bid) → feeRecipient.
    ///         Full bid amount → seller.
    ///         If no bids and the 30-minute inactivity window has elapsed, cancels internally.
    function settle(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();

        address winner = a.highestBidder;

        if (winner == address(0)) {
            if (block.timestamp <= a.startsAt + NO_BID_CANCEL_WINDOW) revert AuctionLive();
            _cancelIfInactive(a, id);
            return;
        }

        if (block.timestamp < a.endsAt) revert AuctionLive();

        a.settled = true;

        address       sel      = a.seller;
        TokenStandard std      = a.standard;
        address       coll     = a.collection;
        uint256       tid      = a.tokenId;
        uint128       amt      = a.amount;
        uint128       winBid   = a.highestBid;
        uint128       winTotal = a.highestTotal;
        // Fee = exact premium paid by winner (no rounding loss)
        uint128       fee      = winTotal - winBid;

        _transferToken(std, coll, sel, winner, tid, amt);

        if (fee > 0) {
            (bool ok,) = feeRecipient.call{value: fee}("");
            if (!ok) revert WithdrawFailed();
        }
        (bool ok2,) = sel.call{value: winBid}("");
        if (!ok2) revert WithdrawFailed();

        emit AuctionSettled(id, winner, sel, winBid, fee);
    }

    // ── Cancel: inactive (internal) ───────────────────────────────────────

    /// @dev Called by settle() when a zero-bid auction has passed NO_BID_CANCEL_WINDOW.
    ///      Not externally callable.
    function _cancelIfInactive(Auction storage a, uint256 id) private {
        a.settled = true;
        emit AuctionCancelled(id);
    }

    // ── Cancel: owner early ───────────────────────────────────────────────

    /// @notice Seller cancels the auction early, before endsAt. Requires manual approval.
    ///         If a highest bidder exists, their full payment (bid + fee) is refunded immediately.
    function cancelEarly(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller != msg.sender) revert NotSeller();
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp >= a.endsAt) revert AuctionEnded();

        a.settled = true;

        address hiBidder = a.highestBidder;
        uint128 hiTotal  = a.highestTotal;

        if (hiBidder != address(0)) {
            (bool ok,) = hiBidder.call{value: hiTotal}("");
            if (ok) {
                emit RefundPushed(hiBidder, hiTotal);
            } else {
                pendingReturns[hiBidder] += hiTotal;
            }
        }

        emit AuctionCancelled(id);
    }

    // ── Emergency refund ──────────────────────────────────────────────────

    /// @notice Withdraw a pending refund. Only needed when automatic push failed
    ///         (edge case: bidder is a contract that cannot receive ETH).
    function withdrawRefund() external nonReentrant {
        uint256 amt = pendingReturns[msg.sender];
        if (amt == 0) revert NothingToWithdraw();
        pendingReturns[msg.sender] = 0;
        (bool ok,) = msg.sender.call{value: amt}("");
        if (!ok) revert WithdrawFailed();
    }
}
