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
error WrongBidValue();
error NothingToWithdraw();

/// @title AuctionHouse
/// @notice English auctions with anti-snipe extension and bid-only refunds.
///
/// Fee semantics:
///   - Bidder sends bid + 1.5% fee. The 1.5% is non-refundable on outbid /
///     reserve-unmet / seller cancel. Only the BID (principal) is refunded.
///   - On a winning settle, fee → feeRecipient, bid → seller.
///
/// Anti-snipe: a bid placed within EXTENSION_WINDOW of endsAt pushes endsAt
/// forward to (now + EXTENSION_WINDOW).
///
/// Min next bid = max(currentHigh * 5%, sellerFlatMinFLR, reserve, MIN_PRICE).
///
/// Settle behaviour:
///   - winner exists AND bid ≥ reserve → transfer + payout.
///   - no bids OR bid < reserve at endsAt → auto-cancel; refund current high bid only.
///
/// Non-custodial: seller keeps NFT until settle. Approval must persist through endsAt.
contract AuctionHouse is MarketplaceCore {
    uint64  public constant EXTENSION_WINDOW  = 3 minutes;
    uint64  public constant DEFAULT_DURATION  = 3 days;
    uint64  public constant MAX_DURATION      = 7 days;
    /// @notice Hardcoded 5% minimum bid increment.
    uint16  public constant MIN_INCREMENT_BPS = 500;

    /// @notice Auction record.
    struct Auction {
        address       seller;            // slot 0
        uint64        startsAt;          // slot 0
        bool          settled;           // slot 0
        TokenStandard standard;          // slot 0
        address       collection;        // slot 1
        uint64        endsAt;            // slot 1
        uint256       tokenId;           // slot 2
        uint128       reserve;           // slot 3
        uint128       highestBid;        // slot 3 (bid principal)
        address       highestBidder;     // slot 4
        uint128       amount;            // slot 5 (1 for ERC-721)
        uint128       sellerFlatMinFLR;  // slot 5 (per-seller floor for min increment)
    }

    uint256 public nextAuctionId;
    mapping(uint256 => Auction) public auctions;

    /// @notice Fallback for push-refund failures (bid principal only).
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
        uint128 sellerFlatMinFLR,
        uint64  startsAt,
        uint64  endsAt
    );
    event BidPlaced(uint256 indexed id, address indexed bidder, uint128 bidAmount, uint128 fee);
    event AuctionExtended(uint256 indexed id, uint64 newEnd);
    event AuctionSettled(uint256 indexed id, address indexed winner, address indexed seller, uint128 bidAmount, uint256 fee);
    event AuctionCancelled(uint256 indexed id);
    event RefundPushed(address indexed bidder, uint256 amount);

    constructor(address recipient)
        MarketplaceCore(recipient)
    {}

    // ── Create ────────────────────────────────────────────────────────────

    /// @notice Create an ERC-721 English auction.
    function create(
        address coll,
        uint256 tokenId,
        uint128 reserve,
        uint64  endsAt,
        uint128 sellerFlatMinFLR
    ) external returns (uint256 id) {
        return _create(TokenStandard.ERC721, coll, tokenId, 1, reserve, endsAt, sellerFlatMinFLR);
    }

    /// @notice Create an ERC-1155 English auction.
    function create1155(
        address coll,
        uint256 tokenId,
        uint128 amount,
        uint128 reserve,
        uint64  endsAt,
        uint128 sellerFlatMinFLR
    ) external returns (uint256 id) {
        if (amount == 0) revert InvalidAmount();
        return _create(TokenStandard.ERC1155, coll, tokenId, amount, reserve, endsAt, sellerFlatMinFLR);
    }

    function _create(
        TokenStandard standard,
        address coll,
        uint256 tokenId,
        uint128 amount,
        uint128 reserve,
        uint64  endsAt,
        uint128 sellerFlatMinFLR
    ) internal returns (uint256 id) {
        if (endsAt <= block.timestamp) revert InvalidWindow();
        if (endsAt > block.timestamp + MAX_DURATION) revert InvalidWindow();
        if (reserve > 0) _checkMin(reserve);

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
            settled:          false,
            standard:         standard,
            collection:       coll,
            endsAt:           endsAt,
            tokenId:          tokenId,
            reserve:          reserve,
            highestBid:       0,
            highestBidder:    address(0),
            amount:           amount,
            sellerFlatMinFLR: sellerFlatMinFLR
        });
        emit AuctionCreated(id, coll, tokenId, msg.sender, standard, amount, reserve, sellerFlatMinFLR, startsAt, endsAt);
    }

    // ── Bid ───────────────────────────────────────────────────────────────

    /// @notice Place a bid. msg.value MUST equal bidAmount + 1.5% fee.
    ///         The 1.5% is non-refundable: forwarded to feeRecipient on outbid
    ///         and on a winning settle. Outbid bidders are refunded bid only.
    function bid(uint256 id, uint128 bidAmount) external payable nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp >= a.endsAt) revert AuctionEnded();

        // Validate exact payment: bid + fee
        uint128 fee = uint128(uint256(bidAmount) * PLATFORM_FEE_BPS / 10_000);
        if (uint256(bidAmount) + uint256(fee) > type(uint128).max) revert BidOverflow();
        if (msg.value != uint256(bidAmount) + uint256(fee)) revert WrongBidValue();

        // Compute minimum acceptable next bid
        uint128 prevHigh = a.highestBid;
        uint256 minNext;
        if (prevHigh == 0) {
            uint256 floor = a.reserve;
            if (a.sellerFlatMinFLR > floor) floor = a.sellerFlatMinFLR;
            if (MIN_PRICE > floor) floor = MIN_PRICE;
            minNext = floor;
        } else {
            uint256 inc = uint256(prevHigh) * MIN_INCREMENT_BPS / 10_000;
            uint256 next = uint256(prevHigh) + (inc == 0 ? 1 : inc);
            if (next < uint256(a.sellerFlatMinFLR)) next = uint256(a.sellerFlatMinFLR);
            if (next > type(uint128).max) revert BidOverflow();
            minNext = next;
        }
        if (bidAmount < minNext) revert BidTooLow();

        // Snapshot previous leader
        address prevBidder = a.highestBidder;
        uint128 prevBid    = prevHigh;

        // Record new leader (we hold only their bid principal — fee forwarded below)
        a.highestBid    = bidAmount;
        a.highestBidder = msg.sender;

        // Forward this bidder's fee to feeRecipient immediately (non-refundable)
        if (fee > 0) {
            (bool feeOk,) = feeRecipient.call{value: fee}("");
            if (!feeOk) revert WithdrawFailed();
        }

        // Refund prev bidder their principal only (their fee was already forwarded)
        if (prevBidder != address(0) && prevBid > 0) {
            (bool ok,) = prevBidder.call{value: prevBid}("");
            if (ok) {
                emit RefundPushed(prevBidder, prevBid);
            } else {
                pendingReturns[prevBidder] += prevBid;
            }
        }

        // Anti-snipe: extend if bid lands in final EXTENSION_WINDOW
        if (a.endsAt - block.timestamp <= EXTENSION_WINDOW) {
            uint64 newEnd = uint64(block.timestamp) + EXTENSION_WINDOW;
            a.endsAt = newEnd;
            emit AuctionExtended(id, newEnd);
        }

        emit BidPlaced(id, msg.sender, bidAmount, fee);
    }

    // ── Settle ────────────────────────────────────────────────────────────

    /// @notice Settle a finished auction. Anyone may call after endsAt.
    ///         Winner exists and bid ≥ reserve → transfer + payout.
    ///         Otherwise (no bids or reserve unmet) → auto-cancel, refund principal.
    function settle(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp < a.endsAt) revert AuctionLive();

        a.settled = true;

        address winner = a.highestBidder;
        uint128 winBid = a.highestBid;

        // Cancel path: no bids OR reserve unmet
        if (winner == address(0) || winBid < a.reserve) {
            // Refund the leading bidder's principal (their fee was already forwarded)
            if (winner != address(0) && winBid > 0) {
                (bool ok,) = winner.call{value: winBid}("");
                if (ok) {
                    emit RefundPushed(winner, winBid);
                } else {
                    pendingReturns[winner] += winBid;
                }
            }
            emit AuctionCancelled(id);
            return;
        }

        // Win path: fee was already forwarded on each bid. Only the bid principal remains.
        address       sel  = a.seller;
        TokenStandard std  = a.standard;
        address       coll = a.collection;
        uint256       tid  = a.tokenId;
        uint128       amt  = a.amount;

        _transferToken(std, coll, sel, winner, tid, amt);

        (bool ok2,) = sel.call{value: winBid}("");
        if (!ok2) revert WithdrawFailed();

        // Fee already paid up-front; emit the cumulative fee for indexer convenience.
        uint256 feeEmitted = _feeOnTop(winBid);
        emit AuctionSettled(id, winner, sel, winBid, feeEmitted);
    }

    // ── Cancel: owner early ───────────────────────────────────────────────

    /// @notice Seller cancels at any time before settle (even after endsAt before settle).
    ///         Current high bidder refunded their bid principal only (fee retained).
    function cancelEarly(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (a.seller != msg.sender) revert NotSeller();

        a.settled = true;

        address hiBidder = a.highestBidder;
        uint128 hiBid    = a.highestBid;

        if (hiBidder != address(0) && hiBid > 0) {
            (bool ok,) = hiBidder.call{value: hiBid}("");
            if (ok) {
                emit RefundPushed(hiBidder, hiBid);
            } else {
                pendingReturns[hiBidder] += hiBid;
            }
        }

        emit AuctionCancelled(id);
    }

    // ── Emergency refund ──────────────────────────────────────────────────

    /// @notice Withdraw a pending refund (bid principal only) if push failed.
    function withdrawRefund() external nonReentrant {
        uint256 amt = pendingReturns[msg.sender];
        if (amt == 0) revert NothingToWithdraw();
        pendingReturns[msg.sender] = 0;
        (bool ok,) = msg.sender.call{value: amt}("");
        if (!ok) revert WithdrawFailed();
    }
}
