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
    /// @dev L-10 fix (v28 — Round 3): the prior design relied on `cumulative == 0`
    ///      as the first-bid signal, but `refundLosers` zeros `cumulative[id][b]`
    ///      for every bidder it processes, and a previously-refunded bidder can
    ///      re-bid exactly as a fresh entry would. Without the seen-mapping below,
    ///      `_bidders[id]` accumulates a duplicate entry on every rebind cycle —
    ///      not exploitable (each duplicate address still maps to its own
    ///      cumulative slot and refundLosers is idempotent) but unbounded
    ///      off-chain enumeration gas and a permanent on-chain storage tax.
    ///      Iteration is bounded by uint256 so a worst-case attacker could in
    ///      principle grow `_bidders[id]` without limit and force every
    ///      `bidderCount(id)` / `getBidder(id, i)` off-chain loop to scan an
    ///      ever-larger array — capping the bids-per-auction count by gas
    ///      alone. Cheaper fix: a presence flag keyed `(id, bidder)` that the
    ///      push path consults before appending and the consume path never
    ///      clears (since refunding + rebidding counts as the SAME logical
    ///      enrollee from the off-chain indexer's point of view).
    mapping(uint256 => address[]) private _bidders;
    /// @notice Presence flag for `_bidders[id]` uniqueness. Set true on first
    ///         push; never cleared (a refunded-then-rebidded bidder is the same
    ///         logical enrollee — the off-chain indexer dedupes on `address`,
    ///         not on cumulative epoch). Adds ~20k gas per unique-bidder
    ///         enrollment (one SSTORE) but caps `_bidders[id].length` to the
    ///         number of distinct participating addresses — bounded by total
    ///         accounts on the chain, and naturally bounded by per-auction
    ///         gas-budget economics (a single bidder pays gas for each rebind).
    mapping(uint256 => mapping(address => bool)) private _seenBidder;

    // pendingReturns inherited from MarketplaceCore — no shadowing needed.
    // AuctionHouse.writeRefund() overrides MarketplaceCore.withdrawRefund()
    // to use the inherited pendingReturns for pull-fallback bookkeeping.


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
            // L-10 fix (Round 3): the prior `prevCum == 0` check is racy
            // against refundLosers zeroing-then-rebid sequences; gate the
            // push on a dedicated presence flag instead so _bidders[id]
            // is one entry per distinct participating address across
            // the auction's full lifetime, regardless of how many times
            // they were outbid and re-bid.
            if (!_seenBidder[id][msg.sender]) {
                _bidders[id].push(msg.sender);
                _seenBidder[id][msg.sender] = true;
            }
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
        // C-01 fix: use transferFrom for ERC721 to bypass onERC721Received
        // hook — the winning bidder VOLUNTARILY bid and must accept delivery.
        // safeTransferFrom lets a malicious contract bidder revert the receiver
        // hook, force a stall, wait 7 days, and reclaim a full refund (free
        // cancellation exploit). transferFrom moves the token without calling
        // the receiver hook, eliminating this attack vector entirely.
        // For ERC1155 there is no plain transferFrom in the standard, so we
        // must keep safeTransferFrom; the stall path below handles the case
        // where the buyer's onERC1155Received reverts.
        if (std == TokenStandard.ERC721) {
            try IERC721(coll).transferFrom(sel, winner, tid) { moved = true; } catch {}
        } else {
            try IERC1155(coll).safeTransferFrom(sel, winner, tid, amt, "") { moved = true; } catch {}
        }

        if (!moved) {
            // Delivery failed. Determine who is at fault.
            bool sellerStillOwns = false;
            if (std == TokenStandard.ERC721) {
                try IERC721(coll).ownerOf(tid) returns (address o) {
                    sellerStillOwns = (o == sel);
                } catch { sellerStillOwns = false; }
            } else {
                try IERC1155(coll).balanceOf(sel, tid) returns (uint256 bal) {
                    sellerStillOwns = (bal >= amt);
                } catch { sellerStillOwns = false; }
            }
            if (!sellerStillOwns) {
                // NFT is gone — will never be deliverable. Refund winner, cancel.
                _refundWinnerAndCancel(a, id, winner, winBid);
                return;
            }
            // C-02 fix: check if the seller still has approval. If the seller
            // revoked approval, the delivery failure is the SELLER's fault —
            // refund the winner immediately instead of stalling. The previous
            // behavior gave the seller a free 7-day option: stall, watch the
            // market, and decide whether to re-approve (forcing the sale) or
            // do nothing (cancelling after STALL_WINDOW). By refunding
            // immediately on seller-fault, the seller loses the auction
            // outcome and the winner's escrow is freed.
            bool sellerApproved = _checkSellerApproval(std, coll, sel, tid);
            if (!sellerApproved) {
                // Seller's fault: revoked approval. Refund winner, cancel.
                _refundWinnerAndCancel(a, id, winner, winBid);
                return;
            }
            // Seller approved but transfer STILL failed. For ERC721 with
            // transferFrom, this should never happen (only if the collection
            // itself is broken). For ERC1155, this means the buyer's
            // onERC1155Received reverted — the BUYER is at fault.
            // Park the auction for retry via settleUnstuck(). After
            // STALL_WINDOW, reclaim() becomes available as a safety valve
            // so the winner's funds are never permanently locked — the
            // 7-day delay is the economic cost of the buyer's refusal.
            a.stalledAt = uint64(block.timestamp);
            emit AuctionStalled(id, winner, sel);
            return;
        }

        a.settled = true;

        // Payouts never revert: non-receiving recipient falls back to pull-withdrawal.
        // gas: 50_000 caps EIP-150 63/64 forwarding — a hostile seller contract
        // cannot OOG-grief settlement and trap the winner's escrow forever.
        // `emit PushFailed` on fallback so off-chain indexers can detect a
        // stuck feeRecipient / non-receiving seller and surface the credit;
        // without this signal, pendingReturns would silently accumulate with
        // no on-chain observability, leaving operators to poll storage.
        if (fee > 0) {
            (bool okFee,) = feeRecipient.call{gas: 50_000, value: fee}("");
            if (!okFee) {
                pendingReturns[feeRecipient] += fee;
                emit PushFailed(feeRecipient, fee);
            }
        }
        uint128 proceeds;
        unchecked { proceeds = winBid - fee; } // fee = 1.5% of winBid, always < winBid
        (bool okSel,) = sel.call{gas: 50_000, value: proceeds}("");
        if (!okSel) {
            pendingReturns[sel] += proceeds;
            emit PushFailed(sel, proceeds);
        }

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

        // C-01 fix: use transferFrom for ERC721 (same rationale as settle).
        bool moved = false;
        if (std == TokenStandard.ERC721) {
            try IERC721(coll).transferFrom(sel, winner, a.tokenId) { moved = true; } catch {}
        } else {
            try IERC1155(coll).safeTransferFrom(sel, winner, a.tokenId, a.amount, "") { moved = true; } catch {}
        }
        if (!moved) {
            bool sellerStillOwns = false;
            if (std == TokenStandard.ERC721) {
                try IERC721(coll).ownerOf(a.tokenId) returns (address o) {
                    sellerStillOwns = (o == sel);
                } catch { sellerStillOwns = false; }
            } else {
                try IERC1155(coll).balanceOf(sel, a.tokenId) returns (uint256 bal) {
                    sellerStillOwns = (bal >= a.amount);
                } catch { sellerStillOwns = false; }
            }
            if (!sellerStillOwns) {
                _refundWinnerAndCancel(a, id, winner, winBid);
                return;
            }
            // C-02 fix: seller-fault detection (same as settle).
            bool sellerApproved = _checkSellerApproval(std, coll, sel, a.tokenId);
            if (!sellerApproved) {
                _refundWinnerAndCancel(a, id, winner, winBid);
                return;
            }
            // R-04 (Round 5): Buyer's fault. Emit AuctionStalled for
            // observability, but DO NOT refresh `a.stalledAt`.
            //
            // The previous implementation set `stalledAt = block.timestamp`
            // on every failed delivery attempt, which let any third party
            // reset the 7-day reclaim window by calling settleUnstuck()
            // repeatedly close to STALL_WINDOW. The winner's reclaim()
            // safety valve (which refunds the escrow once 7d have elapsed)
            // would then NEVER open, and the bidder's funds were trapped
            // indefinitely — denial-of-service via perpetual timer reset.
            //
            // The fix: the first-stall timestamp is immutable. Every
            // subsequent settleUnstuck call that hits the same buyer-fault
            // branch emits AuctionStalled (so off-chain observers see the
            // retry attempt and can alert on a sustained buyer-fault signal)
            // but does NOT modify stalledAt. reclaim() opens at
            // `firstStalledAt + STALL_WINDOW` regardless of how many
            // failed-retry events the auction accumulates.
            emit AuctionStalled(id, winner, sel);
            return;
        }

        a.settled   = true;
        a.stalledAt = 0;

        // Same PushFailed pattern as in settle() — see comment there.
        if (fee > 0) {
            (bool okFee,) = feeRecipient.call{gas: 50_000, value: fee}("");
            if (!okFee) {
                pendingReturns[feeRecipient] += fee;
                emit PushFailed(feeRecipient, fee);
            }
        }
        uint128 proceeds;
        unchecked { proceeds = winBid - fee; }
        (bool okSel,) = sel.call{gas: 50_000, value: proceeds}("");
        if (!okSel) {
            pendingReturns[sel] += proceeds;
            emit PushFailed(sel, proceeds);
        }

        emit AuctionSettled(id, winner, sel, winBid, fee);
    }

    /// @notice Safety valve: refund the winner and cancel a stalled auction
    ///         after STALL_WINDOW (7 days) has elapsed. Callable by anyone.
    ///
    ///         After the C-01/C-02 fixes, stalls only occur for buyer-fault
    ///         (ERC1155 receiver hook reverted while seller was ready).
    ///         The primary resolution path is settleUnstuck() (buyer fixes
    ///         their contract, anyone retries). reclaim() exists as a
    ///         backstop so the winner's funds are never permanently locked
    ///         if the buyer never cooperates — the 7-day delay is the
    ///         economic cost of the buyer's refusal to accept delivery.
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
            // gas: 50_000 caps EIP-150 63/64 forwarding to protect reclaim.
            (bool ok,) = winner.call{gas: 50_000, value: winBid}("");
            if (!ok) {
                pendingReturns[winner] += winBid;
                emit PushFailed(winner, winBid);
            }
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
        // C-03 fix: allow refundLosers on stalled auctions too, not just
        // settled ones. Without this, all losers' funds are trapped for up
        // to 7+ days while the auction is stalled — only the winner's escrow
        // was consumed at stall time, but losers cannot pull their escrow
        // because refundLosers required settled == true.
        if (!a.settled && a.stalledAt == 0) revert NotSettled();
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
            if (!ok) {
                pendingReturns[b] += amt;
                emit PushFailed(b, amt);
            }
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

    // ── Internal helpers ────────────────────────────────────────────────────────

    /// @dev Shared logic: refund the winner in full and cancel the auction.
    ///      Used by settle() and settleUnstuck() when delivery can never succeed
    ///      (seller no longer owns, or seller revoked approval — seller-fault).
    ///      `PushFailed` event is emitted on push-failure so off-chain
    ///      indexers observe the credit and the operator can surface it;
    ///      `RefundPushed` fires on every attempt to record intent.
    ///      NOTE: `sel` (seller) is deliberately NOT a parameter — the helper
    ///      only needs the winner (escrow owner) and amount; the seller is
    ///      already encoded in `a.seller` and the auction event stream.
    /// @param a     Mutable auction storage; flipped to settled=true, stalledAt=0.
    /// @param id    Auction id (for event indexing).
    /// @param winner Escrow owner receiving the refund. Must equal `a.leader`.
    /// @param winBid Escrow amount to refund to the winner.
    function _refundWinnerAndCancel(
        Auction storage a,
        uint256 id,
        address winner,
        uint128 winBid
    ) internal {
        a.settled   = true;
        a.stalledAt = 0;
        if (winBid > 0) {
            (bool ok,) = winner.call{gas: 50_000, value: winBid}("");
            if (!ok) {
                pendingReturns[winner] += winBid;
                emit PushFailed(winner, winBid);
            }
            emit RefundPushed(winner, winBid);
        }
        emit AuctionReclaimed(id, winner, winBid);
        emit AuctionCancelled(id);
    }

    /// @dev Check whether the seller has approved this contract to transfer
    ///      the NFT. Used to distinguish seller-fault (revoked approval) from
    ///      buyer-fault (receiver hook reverted) when safeTransferFrom fails.
    ///      Each external call is wrapped in try/catch so a malicious or
    ///      non-conforming collection cannot break settlement resolution.
    /// @param std  Token standard (ERC721 vs ERC1155) — picks the right probe.
    /// @param coll Collection address being queried.
    /// @param sel  Seller address whose approvals are being checked.
    /// @param tid  Token id (ERC721 only — irrelevant for ERC1155 which uses
    ///             isApprovedForAll only).
    /// @return True iff this contract is approved to move the seller's NFT.
    function _checkSellerApproval(
        TokenStandard std,
        address coll,
        address sel,
        uint256 tid
    ) internal view returns (bool) {
        if (std == TokenStandard.ERC721) {
            try IERC721(coll).isApprovedForAll(sel, address(this)) returns (bool ok) {
                if (ok) return true;
            } catch {}
            try IERC721(coll).getApproved(tid) returns (address approved) {
                return approved == address(this);
            } catch { return false; }
        } else {
            try IERC1155(coll).isApprovedForAll(sel, address(this)) returns (bool ok) {
                return ok;
            } catch { return false; }
        }
    }

    // ── Views (keeper enumeration) ────────────────────────────────────────────────

    function bidderCount(uint256 id) external view returns (uint256) { return _bidders[id].length; }
    function getBidder(uint256 id, uint256 i) external view returns (address) { return _bidders[id][i]; }

    // ── Emergency pull refund ─────────────────────────────────────────────────────

    /// @notice Withdraw a pending refund (only needed when an automatic push failed).
    ///
    ///         The gas:50_000 cap from M-02 is REMOVED here (v27). While the
    ///         cap protected against OOG-griefing in settlement paths, it
    ///         permanently trapped funds for legitimate contract wallets
    ///         (Gnosis Safe, Argent, smart accounts) that require >50k gas
    ///         for receive(). Since this function is nonReentrant and follows
    ///         CEI (zero-then-call), uncapped gas poses no reentrancy risk.
    ///         Restore-on-failure: if the push fails, the credit is restored
    ///         so the caller can retry once their contract is fixed — no
    ///         funds are permanently lost.
    function withdrawRefund() external override nonReentrant {
        uint256 amt = pendingReturns[msg.sender];
        if (amt == 0) revert NothingToWithdraw();
        pendingReturns[msg.sender] = 0;
        (bool ok,) = msg.sender.call{value: amt}("");
        if (!ok) {
            pendingReturns[msg.sender] = amt; // restore — no funds lost
            revert WithdrawFailed();
        }
    }
}
