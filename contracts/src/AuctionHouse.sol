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
error BatchTooLarge();
error NotStalled();
error StallNotOver();
error CannotCancel();

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
    /// @notice Window after which a stalled auction (settle() failed delivery
    ///         because the seller revoked approval) can be reclaimed — refunds
    ///         the winner in full and cancels the auction. Prevents a seller
    ///         from monopolising the winner's escrow indefinitely.
    uint64 public constant STALL_WINDOW        = 7 days;
    /// @notice Minimum absolute increment when both `minIncrementBps` and
    ///         `minIncrementFlat` are zero (or both below this floor). Prevents
    ///         collusive 1-wei bid exchanges that perpetually satisfy the
    ///         `newLead=true` anti-snipe gate and stall the auction forever
    ///         (audit-#5). At 0.001 ETH per flip, two coordinated wallets
    ///         cannot afford to keep the timer extending.
    uint128 public constant MIN_BID_INCREMENT  = 0.001 ether;

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
        uint64        stalledAt;         // timestamp when settle() detected a delivery
                                         // failure and parked the auction. 0 = active.
                                         // Allows settleUnstuck() / reclaim() recovery.
    }

    uint256 public nextAuctionId;
    mapping(uint256 => Auction) public auctions;

    /// @notice Position-stable getter for the Auction struct. Off-chain and
    /// off-test code MUST use this instead of the auto-generated `auctions(id)`
    /// getter, which returns a positional tuple — silent misreads on struct
    /// reflow if a new field is ever added before `stalledAt`. Test harnesses
    /// also gain audit-fix stability: invariants can assert on a named field
    /// (`.endsAt`, `.settled`, `.stalledAt`, `.leader`, `.leaderTotal`)
    /// rather than counting commas in a destructuring tuple.
    function getAuction(uint256 id) external view returns (Auction memory) {
        return auctions[id];
    }

    /// @notice cumulative[auctionId][bidder] → total wei this bidder has escrowed.
    mapping(uint256 => mapping(address => uint128)) public cumulative;
    /// @notice Distinct bidders per auction, for off-chain enumeration + refund batching.
    ///         First-bid detection rides on `cumulative == 0`: escrow never decreases
    ///         while an auction is active (refunds/consumption only happen after
    ///         `settled`, which blocks further bids), so no separate flag is needed.
    mapping(uint256 => address[]) private _bidders;

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
event AuctionStalled(uint256 indexed id, address indexed winner, address indexed seller);
event AuctionReclaimed(uint256 indexed id, address indexed winner, uint256 refundAmount);

    constructor(address recipient, address manager_) MarketplaceCore(recipient, manager_) {}

    // ── Create (free) ───────────────────────────────────────────────────────────

    /// @notice Create an ERC-721 auction. Starts immediately.
    function create(address coll, uint256 tokenId, uint128 reserve, uint64 endsAt, uint16 minIncBps, uint128 minIncFlat)
        external entryGate returns (uint256 id)
    {
        return _create(TokenStandard.ERC721, coll, tokenId, 1, reserve, endsAt, minIncBps, minIncFlat);
    }

    /// @notice Create an ERC-1155 auction. Starts immediately.
    function create1155(address coll, uint256 tokenId, uint128 amount, uint128 reserve, uint64 endsAt, uint16 minIncBps, uint128 minIncFlat)
        external entryGate returns (uint256 id)
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
        // v21 — INC floor: when both minIncrementBps and minIncrementFlat are
        // 0, the bid() path now falls through to MIN_BID_INCREMENT (0.001
        // ether) instead of the legacy +1 wei rule. The previous 1-wei
        // fall-through let two colluding wallets perpetually trade 1-wei
        // leads and stall an auction indefinitely via repeated anti-snipe
        // extensions (audit-#5). 0.001 ETH per flip is economically unviable
        // for griefing while remaining trivial for any legitimate bidder.
        // Existing auctions are unaffected — this only governs auctions
        // created after the redeploy.
        a.minIncrementBps = minIncBps;
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
    function bid(uint256 id) external payable nonReentrant entryGate {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp >= a.endsAt) revert AuctionEnded();
        if (msg.value == 0) revert InvalidAmount();

        uint128 prevCum = cumulative[id][msg.sender];
        uint256 nt;
        unchecked { nt = uint256(prevCum) + msg.value; } // uint128 + msg.value cannot overflow uint256
        if (nt > type(uint128).max) revert BidOverflow();
        uint128 newTotal = uint128(nt);

        if (prevCum == 0) {
            _bidders[id].push(msg.sender); // first bid on this auction
        }
        cumulative[id][msg.sender] = newTotal;

        // Leadership update. Invariant: the leader always holds the max cumulative.
        // `newLead` flips true ONLY when leadership actually changes; the anti-snipe
        // extension below reads that flag so escrow-accumulating sub-threshold bids
        // can no longer keep pushing endsAt forward (audit-#1: griefer repeated
        // 1-wei bids inside the closing window and permanently stalled the
        // auction, stranding winner + losers' funds).
        bool newLead = false;
        if (a.leader == msg.sender) {
            a.leaderTotal = newTotal; // leader tops up; no leadership change.
        } else if (a.leaderTotal == 0) {
            // No leader yet: clearing the reserve takes the lead; otherwise the
            // bid simply accumulates (no revert) toward a future qualifying total.
            newLead = newTotal >= a.reserve;
            if (newLead) {
                a.leader      = msg.sender;
                a.leaderTotal = newTotal;
            }
        } else if (newTotal > a.leaderTotal) {
            // Overtaking the leader requires clearing the min increment — a bidder
            // may not sit above the leader without taking the lead.
            uint256 incPct  = uint256(a.leaderTotal) * a.minIncrementBps / 10_000;
            uint256 inc     = incPct > a.minIncrementFlat ? incPct : a.minIncrementFlat;
            // Floor at MIN_BID_INCREMENT so a 0/0 increment config cannot be
            // reduced to a 1-wei griefing loop (audit-#5). Per-cycle
            // gas cost vs. 0.001 ETH flip cost makes the attack uneconomic.
            if (inc < MIN_BID_INCREMENT) inc = MIN_BID_INCREMENT;
            uint128 minNext = uint128(uint256(a.leaderTotal) + inc);
            if (newTotal < minNext) revert BidTooLow();
            newLead = true;
            address prev  = a.leader;
            a.leader      = msg.sender;
            a.leaderTotal = newTotal;
            emit OutbidNotification(id, prev, newTotal);
        }
        // else (newTotal <= leaderTotal): escrow accumulates, no lead change.

        // Anti-snipe — gated on `newLead` so sub-threshold accumulation cannot
        // extend the timer. Underflow-safe: the AuctionEnded check above
        // guarantees block.timestamp < a.endsAt here.
        unchecked {
            if (newLead && a.endsAt - block.timestamp < EXTENSION_WINDOW) {
                uint64 newEnd = uint64(block.timestamp) + EXTENSION_WINDOW;
                a.endsAt = newEnd;
                emit AuctionExtended(id, newEnd);
            }
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
        if (a.stalledAt != 0) revert NotStalled(); // parked after delivery failure — use settleUnstuck() or reclaim()

        address winner = a.leader;
        if (winner == address(0)) {
            a.settled = true;
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

        // Consume the winner's escrow up front so refundLosers never repays them
        // (whichever path the settle takes).
        cumulative[id][winner] = 0;

        bool moved = false;
        if (std == TokenStandard.ERC721) {
            try IERC721(coll).safeTransferFrom(sel, winner, tid) { moved = true; } catch {}
        } else {
            try IERC1155(coll).safeTransferFrom(sel, winner, tid, amt, "") { moved = true; } catch {}
        }

        if (!moved) {
            // Seller revoked approval between endsAt and settle() (or any other
            // delivery failure). Park the auction in STALLED state instead of
            // auto-cancelling — the previous behavior let the seller unilaterally
            // cancel a winning auction by revoking approval (audit-#2). Now:
            //   - the seller can re-approve + call settleUnstuck(id) to re-attempt,
            //   - after STALL_WINDOW, anyone can call reclaim(id) to refund the winner
            //     in full and cancel the auction.
            a.stalledAt = uint64(block.timestamp);
            emit AuctionStalled(id, winner, sel);
            return;
        }

        a.settled = true;

        // Payouts never revert: non-receiving recipient falls back to pull-withdrawal.
        if (fee > 0) {
            (bool okFee,) = feeRecipient.call{value: fee}("");
            if (!okFee) pendingReturns[feeRecipient] += fee;
        }
        uint128 proceeds;
        unchecked { proceeds = winBid - fee; } // fee = 1.5% of winBid, always < winBid
        (bool okSel,) = sel.call{value: proceeds}("");
        if (!okSel) pendingReturns[sel] += proceeds;

        emit AuctionSettled(id, winner, sel, winBid, fee);
    }

    /// @notice Re-attempt delivery for a stalled auction (settle() bailed into
    ///         STALLED because the seller revoked approval between endsAt and
    ///         the original settle call). On success, completes the settlement
    ///         normally. On another delivery failure, refreshes `stalledAt`
    ///         (frontend re-poll) and the caller can wait out STALL_WINDOW for
    ///         reclaim().
    // slither-disable-next-line reentrancy-eth
    function settleUnstuck(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (a.stalledAt == 0) revert NotStalled();

        address       winner = a.leader;
        TokenStandard std    = a.standard;
        address       coll   = a.collection;
        address       sel    = a.seller;
        uint128       winBid = a.leaderTotal;
        uint128       fee    = uint128(_feeOf(winBid));

        bool moved = false;
        if (std == TokenStandard.ERC721) {
            try IERC721(coll).safeTransferFrom(sel, winner, a.tokenId) { moved = true; } catch {}
        } else {
            try IERC1155(coll).safeTransferFrom(sel, winner, a.tokenId, a.amount, "") { moved = true; } catch {}
        }
        if (!moved) {
            a.stalledAt = uint64(block.timestamp);
            emit AuctionStalled(id, winner, sel);
            return;
        }

        a.settled   = true;
        a.stalledAt = 0;

        if (fee > 0) {
            (bool okFee,) = feeRecipient.call{value: fee}("");
            if (!okFee) pendingReturns[feeRecipient] += fee;
        }
        uint128 proceeds;
        unchecked { proceeds = winBid - fee; }
        (bool okSel,) = sel.call{value: proceeds}("");
        if (!okSel) pendingReturns[sel] += proceeds;

        emit AuctionSettled(id, winner, sel, winBid, fee);
    }

    /// @notice Anyone can call this after STALL_WINDOW has elapsed to refund
    ///         the stalled auction's winner in full and cancel the auction.
    ///         Prevents a seller from holding the winner's escrow hostage
    ///         indefinitely after revoking approval.
    // slither-disable-next-line reentrancy-eth
    function reclaim(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (a.stalledAt == 0) revert NotStalled();
        if (block.timestamp < a.stalledAt + STALL_WINDOW) revert StallNotOver();

        address winner = a.leader;
        uint128 winBid = a.leaderTotal;

        a.settled   = true;
        a.stalledAt = 0;

        if (winBid > 0) {
            (bool ok,) = winner.call{value: winBid}("");
            if (!ok) pendingReturns[winner] += winBid;
            emit RefundPushed(winner, winBid);
        }
        emit AuctionReclaimed(id, winner, winBid);
        emit AuctionCancelled(id);
    }

    /// @notice Refund a batch of non-winning bidders their full escrow. Callable
    ///         by anyone once the auction is settled. Idempotent (zeroed escrow
    ///         is skipped); pull-fallback per address. Bounded `batch.length` (200)
    ///         keeps a single call inside a block's gas budget, and per-call
    ///         `gas: 50_000` caps the EIP-150 63/64 forwarding budget so a griefing
    ///         receiver can't cascade OOG the keeper mid-loop and roll back prior
    ///         pendingReturns credits (audit-#4).
    // slither-disable-next-line reentrancy-eth
    function refundLosers(uint256 id, address[] calldata batch) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0)) revert NotActive();
        if (!a.settled) revert NotSettled();
        if (batch.length == 0 || batch.length > 200) revert BatchTooLarge();

        for (uint256 i; i < batch.length; ++i) {
            address b = batch[i];
            uint128 amt = cumulative[id][b];
            if (amt == 0) continue; // winner (consumed) or already refunded
            cumulative[id][b] = 0;
            // Safe: amt is b's OWN escrowed balance (zeroed above, CEI); a non-bidder
            // address has 0 and was skipped, so funds can only return to their owner.
            // `gas: 50_000` caps the EIP-150 forward-budget — a hostile receive()
            // can burn at most 50k of in-loop gas per iteration; the outer tx
            // can never OOG with a surviving prior-iteration pendingReturns credit.
            // slither-disable-next-line arbitrary-send-eth
            (bool ok,) = b.call{gas: 50_000, value: amt}("");
            if (!ok) pendingReturns[b] += amt;
            emit LoserRefunded(id, b, amt);
        }
    }

    // ── Seller early cancel (before endsAt) ───────────────────────────────────────

    /// @notice Seller cancels before `endsAt`. No sale; every bidder's escrow becomes
    ///         refundable via refundLosers.
    /// @dev A seller cannot cancel once a qualifying leader (cleared the reserve)
    ///      has taken the lead — bidders have committed capital in good faith
    ///      and auction integrity forbids the seller from walking away with the
    ///      bidding time paid for. Bidders can withdraw via `withdrawRefund()`
    ///      only after settlement (v8: `cancelEarly` leaves escrow in place
    ///      until `refundLosers(id, batch)` is called — the keeper sweeper
    ///      drives this automatically for cancelled auctions).
    function cancelEarly(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller != msg.sender) revert NotSeller();
        if (a.settled) revert NotActive();
        if (block.timestamp >= a.endsAt) revert AuctionEnded();
        // Reserve-met lock: the auction has met its reserve and a leader
        // exists. Walking back at this point would let the seller snipe
        // their own auction (audit-#6).
        if (a.leader != address(0) && a.leaderTotal >= a.reserve) revert CannotCancel();
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
