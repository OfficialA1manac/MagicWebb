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
error NotSettled();
error NothingToWithdraw();

/// @title AuctionHouse
/// @notice English auctions with a CUMULATIVE bid model, escrow-until-settle, and
///         keeper-friendly (permissionless) auto-settlement.
///
/// Cumulative model:
///   - A bidder may place many bids on one auction. Their EFFECTIVE bid is the SUM
///     of all their bids (`cumulative[id][bidder]`). Highest effective total wins.
///   - Bidding is FREE. Each bid escrows its msg.value; **outbid bidders are NOT
///     auto-refunded** — funds stay escrowed so they can top up and reclaim the lead.
///     An `OutbidNotification` is emitted when the lead changes.
///   - To take the lead, a bidder's new cumulative must clear the reserve and beat
///     the current leader by the min increment. Sub-threshold bids still escrow
///     (accumulate) but do not lead.
///
/// Settlement (no on-chain roles — fully permissionless, so funds are never trapped):
///   - `settle(id)` is callable by ANYONE after `endsAt` (a keeper is just the
///     default caller). NFT → winner; 1.5% fee → feeRecipient; winningBid−fee →
///     seller. The winner's escrow is consumed.
///   - `refundLosers(id, batch)` is callable by ANYONE after settlement and returns
///     each non-winner's full escrow. Batched + pull-fallback so one non-receiving
///     bidder can never brick the refunds. Bounded gas per call.
///
/// Non-custodial; immutable; no admin, no pause, no upgrade.
contract AuctionHouse is MarketplaceCore {
    /// @notice Cap on minIncrementBps (50%). Prevents seller griefing via absurd increments.
    uint16 public constant MAX_MIN_INCREMENT_BPS = 5_000;
    /// @notice Anti-snipe: bids inside this closing window extend the auction by it.
    uint64 public constant EXTENSION_WINDOW     = 3 minutes;
    /// @notice Maximum auction duration from creation.
    uint64 public constant MAX_AUCTION_DURATION = 7 days;

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
        uint128       amount;            // token amount (1 for ERC-721)
        address       leader;            // current highest-cumulative bidder
        uint128       leaderTotal;       // leader's cumulative escrow
        uint128       minIncrementFlat;  // absolute min increment in wei (may be 0)
    }

    uint256 public nextAuctionId;
    mapping(uint256 => Auction) public auctions;

    /// @notice cumulative[auctionId][bidder] → total wei this bidder has escrowed.
    mapping(uint256 => mapping(address => uint128)) public cumulative;
    /// @notice Distinct bidders per auction, for off-chain enumeration + refund batching.
    mapping(uint256 => address[]) private _bidders;
    mapping(uint256 => mapping(address => bool)) private _hasBid;

    /// @notice Pull-pattern fallback for any push payment that fails (non-receiving
    ///         contract): loser refunds, the winner's full refund when settlement
    ///         can't deliver the NFT, and seller/fee payouts that bounce at settle.
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
    /// @param amount   this bid's wei. @param newTotal bidder's cumulative after it.
    event BidPlaced(uint256 indexed id, address indexed bidder, uint256 amount, uint256 newTotal);
    /// @notice Lead changed: `outbid` is no longer the leader (funds stay escrowed).
    event OutbidNotification(uint256 indexed id, address indexed outbid, uint256 newLeaderTotal);
    event AuctionExtended(uint256 indexed id, uint64 newEndsAt);
    event AuctionSettled(uint256 indexed id, address indexed winner, address indexed seller, uint128 winningBid, uint256 fee);
    event LoserRefunded(uint256 indexed id, address indexed bidder, uint256 amount);
    event AuctionCancelled(uint256 indexed id);
    event RefundPushed(address indexed bidder, uint256 amount);

    constructor(address recipient) MarketplaceCore(recipient) {}

    // ── Create (free) ───────────────────────────────────────────────────────────

    /// @notice Create an ERC-721 auction. Starts immediately.
    function create(address coll, uint256 tokenId, uint128 reserve, uint64 endsAt, uint16 minIncBps, uint128 minIncFlat)
        external returns (uint256 id)
    {
        return _create(TokenStandard.ERC721, coll, tokenId, 1, reserve, endsAt, minIncBps, minIncFlat);
    }

    /// @notice Create an ERC-1155 auction. Starts immediately.
    function create1155(address coll, uint256 tokenId, uint128 amount, uint128 reserve, uint64 endsAt, uint16 minIncBps, uint128 minIncFlat)
        external returns (uint256 id)
    {
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
        Auction storage a = auctions[id];
        a.seller          = msg.sender;
        a.startsAt        = startsAt;
        a.minIncrementBps = minIncBps == 0 ? 500 : minIncBps;
        a.standard        = standard;
        a.collection      = coll;
        a.endsAt          = endsAt;
        a.tokenId         = tokenId;
        a.reserve         = reserve;
        a.amount          = amount;
        a.minIncrementFlat = minIncFlat;

        emit AuctionCreated(id, coll, tokenId, msg.sender, standard, amount, reserve, startsAt, endsAt);
    }

    // ── Bid (free, cumulative, escrow-until-settle) ───────────────────────────────

    /// @notice Add `msg.value` to your cumulative bid on auction `id`. No refund on
    ///         being outbid — top up again to reclaim the lead. Your effective bid is
    ///         the sum of all your bids.
    function bid(uint256 id) external payable nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp >= a.endsAt) revert AuctionEnded();
        if (msg.value == 0) revert InvalidAmount();

        uint256 nt = uint256(cumulative[id][msg.sender]) + msg.value;
        if (nt > type(uint128).max) revert BidOverflow();
        uint128 newTotal = uint128(nt);

        if (!_hasBid[id][msg.sender]) {
            _hasBid[id][msg.sender] = true;
            _bidders[id].push(msg.sender);
        }
        cumulative[id][msg.sender] = newTotal;

        // Leadership update. Invariant: the leader always holds the max cumulative.
        if (a.leader == msg.sender) {
            a.leaderTotal = newTotal; // leader tops up
        } else if (a.leaderTotal == 0) {
            // No leader yet: clearing the reserve takes the lead; otherwise the
            // bid simply accumulates (no revert) toward a future qualifying total.
            if (newTotal >= a.reserve) {
                a.leader      = msg.sender;
                a.leaderTotal = newTotal;
            }
        } else if (newTotal > a.leaderTotal) {
            // Overtaking the leader requires clearing the min increment — a bidder
            // may not sit above the leader without taking the lead.
            uint256 incPct  = uint256(a.leaderTotal) * a.minIncrementBps / 10_000;
            uint256 inc     = incPct > a.minIncrementFlat ? incPct : a.minIncrementFlat;
            if (inc == 0) inc = 1;
            uint128 minNext = uint128(uint256(a.leaderTotal) + inc);
            if (newTotal < minNext) revert BidTooLow();
            address prev  = a.leader;
            a.leader      = msg.sender;
            a.leaderTotal = newTotal;
            emit OutbidNotification(id, prev, newTotal);
        }
        // else (newTotal <= leaderTotal): escrow accumulates, no lead change — allowed.

        // Anti-snipe.
        if (a.endsAt - block.timestamp < EXTENSION_WINDOW) {
            uint64 newEnd = uint64(block.timestamp) + EXTENSION_WINDOW;
            a.endsAt = newEnd;
            emit AuctionExtended(id, newEnd);
        }

        emit BidPlaced(id, msg.sender, msg.value, newTotal);
    }

    // ── Settle (permissionless, after endsAt) ─────────────────────────────────────

    /// @notice Finalize a finished auction. Callable by anyone after `endsAt`.
    ///         NFT → winner, 1.5% fee → feeRecipient, winningBid−fee → seller.
    ///         If there is no qualifying leader, cancels (all escrow refundable via
    ///         refundLosers). If the NFT can't be delivered, refunds the winner and
    ///         cancels. Losers are refunded separately via refundLosers.
    // slither-disable-next-line reentrancy-eth
    function settle(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp < a.endsAt) revert AuctionLive();

        a.settled = true;

        address winner = a.leader;
        if (winner == address(0)) {
            emit AuctionCancelled(id); // no qualifying bid; all escrow refundable
            return;
        }

        address       sel    = a.seller;
        TokenStandard std     = a.standard;
        address       coll    = a.collection;
        uint256       tid     = a.tokenId;
        uint128       amt     = a.amount;
        uint128       winBid  = a.leaderTotal;
        uint128       fee     = uint128(_feeOf(winBid));

        // Consume the winner's escrow up front so refundLosers never repays them.
        cumulative[id][winner] = 0;

        bool moved = false;
        if (std == TokenStandard.ERC721) {
            try IERC721(coll).safeTransferFrom(sel, winner, tid) { moved = true; } catch {}
        } else {
            try IERC1155(coll).safeTransferFrom(sel, winner, tid, amt, "") { moved = true; } catch {}
        }
        if (!moved) {
            // Can't deliver NFT → refund the winner in full, cancel.
            (bool refunded,) = winner.call{value: winBid}("");
            if (!refunded) pendingReturns[winner] += winBid;
            emit RefundPushed(winner, winBid);
            emit AuctionCancelled(id);
            return;
        }

        // Payouts never revert: non-receiving recipient falls back to pull-withdrawal.
        if (fee > 0) {
            (bool okFee,) = feeRecipient.call{value: fee}("");
            if (!okFee) pendingReturns[feeRecipient] += fee;
        }
        uint128 proceeds = winBid - fee;
        (bool okSel,) = sel.call{value: proceeds}("");
        if (!okSel) pendingReturns[sel] += proceeds;

        emit AuctionSettled(id, winner, sel, winBid, fee);
    }

    /// @notice Refund a batch of non-winning bidders their full escrow. Callable by
    ///         anyone once the auction is settled. Idempotent (zeroed escrow is
    ///         skipped); pull-fallback per address so one bad recipient can't brick
    ///         the batch. The keeper enumerates bidders via bidderCount/getBidder.
    // slither-disable-next-line reentrancy-eth
    function refundLosers(uint256 id, address[] calldata batch) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0)) revert NotActive();
        if (!a.settled) revert NotSettled();

        for (uint256 i; i < batch.length; ++i) {
            address b = batch[i];
            uint128 amt = cumulative[id][b];
            if (amt == 0) continue; // winner (consumed) or already refunded
            cumulative[id][b] = 0;
            // Safe: amt is b's OWN escrowed balance (zeroed above, CEI); a non-bidder
            // address has 0 and was skipped, so funds can only return to their owner.
            // slither-disable-next-line arbitrary-send-eth
            (bool ok,) = b.call{value: amt}("");
            if (!ok) pendingReturns[b] += amt;
            emit LoserRefunded(id, b, amt);
        }
    }

    // ── Seller early cancel (before endsAt) ───────────────────────────────────────

    /// @notice Seller cancels before `endsAt`. No sale; every bidder's escrow becomes
    ///         refundable via refundLosers.
    function cancelEarly(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller != msg.sender) revert NotSeller();
        if (a.settled) revert NotActive();
        if (block.timestamp >= a.endsAt) revert AuctionEnded();
        a.settled = true;
        emit AuctionCancelled(id);
    }

    // ── Views (keeper enumeration) ────────────────────────────────────────────────

    function bidderCount(uint256 id) external view returns (uint256) { return _bidders[id].length; }
    function getBidder(uint256 id, uint256 i) external view returns (address) { return _bidders[id][i]; }

    // ── Emergency pull refund ─────────────────────────────────────────────────────

    /// @notice Withdraw a pending refund (only needed when an automatic push failed).
    function withdrawRefund() external nonReentrant {
        uint256 amt = pendingReturns[msg.sender];
        if (amt == 0) revert NothingToWithdraw();
        pendingReturns[msg.sender] = 0;
        (bool ok,) = msg.sender.call{value: amt}("");
        if (!ok) revert WithdrawFailed();
    }
}
