// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {MarketplaceCore, TokenStandard, WithdrawFailed, BelowMinPrice} from "./MarketplaceCore.sol";
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
/// @notice English auctions with auto-settlement and anti-snipe extension.
///
/// Auction flow:
///   1. Seller calls `create` → AuctionCreated event. Auction starts immediately.
///   2. Bidder calls `bid(id, bidAmount)` with msg.value = bidAmount + 1.5% platform fee.
///      The first bid must be ≥ reserve. Each later bid must clear
///      max(currentHigh * minIncrementBps, minIncrementFlat). BidPlaced event.
///   3. Anti-snipe: a bid landing within EXTENSION_WINDOW of `endsAt` pushes `endsAt`
///      out to now + EXTENSION_WINDOW. AuctionExtended event.
///   4. After `endsAt`, keeper bot calls `settle(id)` → NFT → winner,
///      fee → feeRecipient, full bid → seller. AuctionSettled event.
///   5. Auto-cancel: if no bid within NO_BID_CANCEL_WINDOW, keeper calls `settle(id)` which
///      triggers `_cancelIfInactive` internally. Not directly callable externally.
///   6. Owner early cancel: seller calls `cancelEarly` any time before expiry.
///
/// Fee semantics: the bidder pays 1.5% ON TOP of their bid, and the fee is NON-REFUNDABLE.
/// When outbid, the previous leader is refunded their BID amount only — the platform keeps
/// the 1.5%. The winner's fee → feeRecipient, the full winning bid → seller (seller gets 100%).
///
/// Non-custodial: seller keeps NFT until settle. Approval must remain valid through auction end.
/// Unstoppable: no pause, no admin.
contract AuctionHouse is MarketplaceCore {
    /// @notice Cap on minIncrementBps (50%). Prevents seller griefing via absurd increments.
    uint16  public constant MAX_MIN_INCREMENT_BPS = 5_000;
    /// @notice Auction auto-cancels if no bid arrives within this window of creation.
    uint64  public constant NO_BID_CANCEL_WINDOW  = 30 minutes;
    /// @notice Anti-snipe: bids inside this closing window extend the auction by it.
    uint64  public constant EXTENSION_WINDOW      = 3 minutes;
    /// @notice Maximum auction duration from creation.
    uint64  public constant MAX_AUCTION_DURATION  = 7 days;

    /// @notice Auction record.
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
        uint128       highestBid;       // bid amount proper (used for reserve/increment checks)
        address       highestBidder;
        uint128       amount;           // token amount (always 1 for ERC-721)
        uint128       highestTotal;     // exact ETH held for highest bidder: bid + fee
        uint128       minIncrementFlat; // absolute minimum increment in wei (seller-set, may be 0)
    }

    uint256 public nextAuctionId;
    mapping(uint256 => Auction) public auctions;

    /// @notice Pull-pattern fallback for any push payment that fails (a non-receiving
    ///         contract): outbid/early-cancel refunds (bid only), a winner's full refund
    ///         when settlement cannot deliver the NFT, and seller proceeds / fees that
    ///         bounce at settle. Recoverable via withdrawRefund().
    mapping(address => uint256) public pendingReturns;

    // ── Events ──────────────────────────────────────────────────────────────────

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
    event AuctionExtended(uint256 indexed id, uint64 newEndsAt);
    event AuctionSettled(uint256 indexed id, address indexed winner, address indexed seller, uint128 bidAmount, uint256 fee);
    event AuctionCancelled(uint256 indexed id);
    event RefundPushed(address indexed bidder, uint256 amount);

    constructor(address recipient)
        MarketplaceCore(recipient)
    {}

    // ── Create ────────────────────────────────────────────────────────────────

    /// @notice Create an ERC-721 auction. Starts immediately at block.timestamp.
    /// @param coll       NFT collection address.
    /// @param tokenId    Token ID to auction.
    /// @param reserve    Minimum first bid (must be ≥ MIN_PRICE).
    /// @param endsAt     Unix timestamp when bidding closes (≤ now + 7 days).
    /// @param minIncBps  Minimum bid increment in bps (0 → defaults to 500 = 5%).
    /// @param minIncFlat Absolute minimum increment in wei (0 = none). Effective increment
    ///                   is max(currentHigh * minIncBps, minIncFlat).
    function create(
        address coll,
        uint256 tokenId,
        uint128 reserve,
        uint64  endsAt,
        uint16  minIncBps,
        uint128 minIncFlat
    ) external returns (uint256 id) {
        return _create(TokenStandard.ERC721, coll, tokenId, 1, reserve, endsAt, minIncBps, minIncFlat);
    }

    /// @notice Create an ERC-1155 auction. Starts immediately at block.timestamp.
    function create1155(
        address coll,
        uint256 tokenId,
        uint128 amount,
        uint128 reserve,
        uint64  endsAt,
        uint16  minIncBps,
        uint128 minIncFlat
    ) external returns (uint256 id) {
        if (amount == 0) revert InvalidAmount();
        return _create(TokenStandard.ERC1155, coll, tokenId, amount, reserve, endsAt, minIncBps, minIncFlat);
    }

    function _create(
        TokenStandard standard,
        address coll,
        uint256 tokenId,
        uint128 amount,
        uint128 reserve,
        uint64  endsAt,
        uint16  minIncBps,
        uint128 minIncFlat
    ) internal returns (uint256 id) {
        if (endsAt <= block.timestamp) revert InvalidWindow();
        if (endsAt > block.timestamp + MAX_AUCTION_DURATION) revert InvalidWindow();
        if (minIncBps > MAX_MIN_INCREMENT_BPS) revert BadIncrement();
        if (reserve < MIN_PRICE) revert BelowMinPrice();

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
            seller:           msg.sender,
            startsAt:         startsAt,
            minIncrementBps:  minIncBps == 0 ? 500 : minIncBps,
            settled:          false,
            standard:         standard,
            collection:       coll,
            endsAt:           endsAt,
            tokenId:          tokenId,
            reserve:          reserve,
            highestBid:       0,
            highestBidder:    address(0),
            amount:           amount,
            highestTotal:     0,
            minIncrementFlat: minIncFlat
        });
        emit AuctionCreated(id, coll, tokenId, msg.sender, standard, amount, reserve, startsAt, endsAt);
    }

    // ── Bid ───────────────────────────────────────────────────────────────────

    /// @notice Place a bid. Send bidAmount + 1.5% platform fee as msg.value.
    ///         The fee is NON-REFUNDABLE. If you are later outbid you are refunded your
    ///         BID amount only — the platform keeps the 1.5%.
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

        // Enforce minimum bid: first bid ≥ reserve, later bids clear the effective increment.
        uint128 prevHigh = a.highestBid;
        uint128 minNext;
        if (prevHigh == 0) {
            minNext = a.reserve;
        } else {
            uint256 incPct = uint256(prevHigh) * a.minIncrementBps / 10_000;
            uint256 inc    = incPct > a.minIncrementFlat ? incPct : a.minIncrementFlat;
            if (inc == 0) inc = 1;
            uint256 next = uint256(prevHigh) + inc;
            if (next > type(uint128).max) revert BidOverflow();
            minNext = uint128(next);
        }
        if (bidAmount < minNext) revert BidTooLow();

        // Snapshot previous leader before overwriting
        address prevBidder = a.highestBidder;
        uint128 prevBid    = a.highestBid;
        uint128 prevTotal  = a.highestTotal;

        // Record new leader
        a.highestBid    = bidAmount;
        a.highestBidder = msg.sender;
        a.highestTotal  = totalRequired;

        // Refund the outbid bidder their BID only; the platform keeps their fee.
        if (prevBidder != address(0)) {
            _payFee(prevTotal - prevBid); // retain the outbid bidder's 1.5%
            (bool ok,) = prevBidder.call{value: prevBid}("");
            if (ok) {
                emit RefundPushed(prevBidder, prevBid);
            } else {
                // Fallback: store bid (fee already retained) for manual withdrawal
                pendingReturns[prevBidder] += prevBid;
            }
        }

        // Anti-snipe: extend the close if this bid landed inside the window.
        if (a.endsAt - block.timestamp < EXTENSION_WINDOW) {
            uint64 newEnd = uint64(block.timestamp) + EXTENSION_WINDOW;
            a.endsAt = newEnd;
            emit AuctionExtended(id, newEnd);
        }

        emit BidPlaced(id, msg.sender, bidAmount, totalRequired);
    }

    // ── Settle ──────────────────────────────────────────────────────────────────

    /// @notice Settle a finished auction. Keeper bot calls this after endsAt.
    ///         NFT → winner. Fee (exact premium paid by winner) → feeRecipient.
    ///         Full winning bid → seller (seller gets 100%).
    ///         If no bids and NO_BID_CANCEL_WINDOW has elapsed, cancels internally.
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

        // Attempt the NFT transfer. If it fails (seller moved the token or revoked
        // approval after the auction closed, or the winner cannot receive it), refund the
        // winner their full payment and cancel — the winner's escrow is never left locked.
        bool moved;
        if (std == TokenStandard.ERC721) {
            try IERC721(coll).safeTransferFrom(sel, winner, tid) { moved = true; } catch {}
        } else {
            try IERC1155(coll).safeTransferFrom(sel, winner, tid, amt, "") { moved = true; } catch {}
        }
        if (!moved) {
            (bool refunded,) = winner.call{value: winTotal}("");
            if (!refunded) pendingReturns[winner] += winTotal;
            emit RefundPushed(winner, winTotal);
            emit AuctionCancelled(id);
            return;
        }

        // Payouts never revert: a non-receiving recipient falls back to pull-withdrawal,
        // so a finished, transferred auction can never be bricked at the payout step.
        if (fee > 0) {
            (bool okFee,) = feeRecipient.call{value: fee}("");
            if (!okFee) pendingReturns[feeRecipient] += fee;
        }
        (bool okSel,) = sel.call{value: winBid}("");
        if (!okSel) pendingReturns[sel] += winBid;

        emit AuctionSettled(id, winner, sel, winBid, fee);
    }

    // ── Cancel: inactive (internal) ────────────────────────────────────────────

    /// @dev Called by settle() when a zero-bid auction has passed NO_BID_CANCEL_WINDOW.
    ///      Not externally callable.
    function _cancelIfInactive(Auction storage a, uint256 id) private {
        a.settled = true;
        emit AuctionCancelled(id);
    }

    // ── Cancel: owner early ────────────────────────────────────────────────────

    /// @notice Seller cancels the auction early, before endsAt.
    ///         If a highest bidder exists, their BID is refunded — the platform keeps
    ///         the 1.5% fee (fees are non-refundable).
    function cancelEarly(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller != msg.sender) revert NotSeller();
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp >= a.endsAt) revert AuctionEnded();

        a.settled = true;

        address hiBidder = a.highestBidder;
        uint128 hiBid    = a.highestBid;
        uint128 hiTotal  = a.highestTotal;

        if (hiBidder != address(0)) {
            _payFee(hiTotal - hiBid); // retain the high bidder's 1.5%
            (bool ok,) = hiBidder.call{value: hiBid}("");
            if (ok) {
                emit RefundPushed(hiBidder, hiBid);
            } else {
                pendingReturns[hiBidder] += hiBid;
            }
        }

        emit AuctionCancelled(id);
    }

    // ── Emergency refund ──────────────────────────────────────────────────────

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
