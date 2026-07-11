// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {MarketplaceCore, TokenStandard, TransferFailed, WithdrawFailed, BelowMinPrice, InvalidDuration, DURATION_3MIN, DURATION_15MIN, DURATION_30MIN, DURATION_1HR, DURATION_4HR, DURATION_24HR} from "./MarketplaceCore.sol";
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
error NotKeeper();

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
/// Settlement (three-tier gate, so funds are never trapped):
///   1. KEEPER_ROLE — settles immediately after `endsAt` (1s ticker).
///   2. Seller OR auction winner — settles after `endsAt + 5 minutes`.
///   3. Permissionless — anyone settles after `endsAt + DURATION_24HR + 1hr`.
///   NFT → winner; 1.5% fee → feeRecipient; winningBid−fee → seller.
///   The winner's escrow is consumed.
///   - `refundLosers(id, batch)` is callable by ANYONE after settlement and returns
///     each non-winner's full escrow. Batched + pull-fallback so one non-receiving
///     bidder can never brick the refunds. Bounded gas per call.
///
/// Non-custodial; immutable; no admin, no pause, no upgrade.
///
/// @dev Timestamp usage: This contract uses `block.timestamp` for auction timing
///      (startsAt, endsAt, extension window). Miners can manipulate
///      block.timestamp by up to ~15 seconds on Ethereum mainnet (less on Flare),
///      but all time windows are far larger than the manipulation threshold:
///      - Auctions last from 3 minutes to 24 hours (one of 6 fixed durations)
///      - Anti-snipe extension window is 3 minutes (EXTENSION_WINDOW)
///      - No stall window: settle() reverts entirely on transfer failure; keeper retries
///      A 15-second skew is negligible against these magnitudes and cannot be
///      exploited to force premature settlement or indefinitely extend an auction.
contract AuctionHouse is MarketplaceCore {
    /// @notice Cap on minIncrementBps (50%). Prevents seller griefing via absurd increments.
    uint16 public constant MAX_MIN_INCREMENT_BPS = 5_000;
    /// @notice Anti-snipe: bids inside this closing window extend the auction by it.
    uint64 public constant EXTENSION_WINDOW     = 3 minutes;


    /// @notice Absolute cap on total anti-snipe extensions. Set to 30 minutes
    ///         per the product requirement: the auction can only be extended
    ///         up to 30 minutes past its original end time via anti-snipe bids.
    ///         startsAt + DURATION_24HR + MAX_TOTAL_EXTENSION = the
    ///         absolute latest possible endsAt, regardless of extension count.
    uint64 public constant MAX_TOTAL_EXTENSION = 30 minutes;
    /// @notice Flat minimum increment of 1 FLR/C2FLR/SGB (1 ether) for overtaking
    ///         the current leader. The user's new cumulative bid must exceed the
    ///         leader's total by at least this amount. The percentage-based
    ///         `minIncrementBps` and `minIncrementFlat` parameters are still
    ///         accepted at creation for backward-compatibility but the bid() logic
    ///         uses only this flat value — guaranteeing a +1 C2FLR/FLR/SGB floor
    ///         across all chains.
    uint128 public constant MIN_BID_INCREMENT  = 1 ether;

    struct Auction {
        address       seller;
        uint64        startsAt;
        uint16        minIncrementBps;   // kept for backwards-compat; bid() ignores it — see MIN_BID_INCREMENT
        bool          settled;
        bool          active;            // seller activates after creation; bids only when true
        TokenStandard standard;
        address       collection;
        uint64        endsAt;
        uint256       tokenId;
        uint128       reserve;
        uint128       amount;            // token amount (1 for ERC-721)
        address       leader;            // current highest-cumulative bidder
        uint128       leaderTotal;       // leader's cumulative escrow
        uint128       minIncrementFlat;  // kept for backwards-compat; bid() ignores it — see MIN_BID_INCREMENT
    }

    uint256 public nextAuctionId;
    mapping(uint256 => Auction) public auctions;

    /// @notice Position-stable getter for the Auction struct. Off-chain and
    /// off-test code MUST use this instead of the auto-generated `auctions(id)`
    /// getter, which returns a positional tuple — silent misreads on struct
    /// reflow if a new field is ever added before `leader`. Test harnesses
    /// also gain audit-fix stability: invariants can assert on a named field
    /// (`.endsAt`, `.settled`, `.active`, `.leader`, `.leaderTotal`)
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
    event AuctionActivated(uint256 indexed id);

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() { _disableInitializers(); }

    /// @notice One-time initializer. Calls __MarketplaceCore_init to store
    ///         feeRecipient + manager in upgradeable storage.
    function initialize(address recipient, address manager_) public initializer {
        __MarketplaceCore_init(recipient, manager_);
    }

    // ── Activate Auction ──────────────────────────────────────────────────────

    /// @notice Activate an auction, allowing bids. Only the seller can call this.
    ///         Once activated, the auction cannot be deactivated.
    /// @dev Auctions are now auto-activated on creation; calling this on an
    ///      already-active auction reverts.
    /// @param id The auction to activate.
    function activateAuction(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller != msg.sender) revert NotSeller();
        if (a.settled) revert NotActive();
        if (a.active) revert AuctionLive();
        // Unreachable under current create()-auto-activate behaviour;
        // retained for ABI backwards-compatibility.
        a.active = true;
        emit AuctionActivated(id);
    }

    // ── Create (free) ───────────────────────────────────────────────────────────

    /// @notice Create an ERC-721 auction. Starts immediately.
    /// @param minIncBps  DEPRECATED — accepted for ABI backwards-compatibility but IGNORED.
    ///                   bid() always uses MIN_BID_INCREMENT (1 ether flat floor).
    /// @param minIncFlat DEPRECATED — accepted for ABI backwards-compatibility but IGNORED.
    ///                   bid() always uses MIN_BID_INCREMENT (1 ether flat floor).
    function create(address coll, uint256 tokenId, uint128 reserve, uint64 endsAt, uint16 minIncBps, uint128 minIncFlat)
        external nonReentrant entryGate returns (uint256 id)
    {
        return _create(TokenStandard.ERC721, coll, tokenId, 1, reserve, endsAt, minIncBps, minIncFlat);
    }

    /// @notice Create an ERC-1155 auction. Starts immediately.
    /// @param minIncBps  DEPRECATED — accepted for ABI backwards-compatibility but IGNORED.
    /// @param minIncFlat DEPRECATED — accepted for ABI backwards-compatibility but IGNORED.
    function create1155(address coll, uint256 tokenId, uint128 amount, uint128 reserve, uint64 endsAt, uint16 minIncBps, uint128 minIncFlat)
        external nonReentrant entryGate returns (uint256 id)
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
        // Validate auction duration is one of the 6 fixed durations shared across all cores.
        uint256 duration = uint256(endsAt) - block.timestamp;
        if (duration != DURATION_3MIN && duration != DURATION_15MIN
            && duration != DURATION_30MIN && duration != DURATION_1HR
            && duration != DURATION_4HR && duration != DURATION_24HR) {
            revert InvalidDuration();
        }
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

        // Auction starts ACTIVE — seller creating the auction is activating it.
        // Bids are accepted immediately after creation. There is no separate
        // activate step; creation = activation.
        a.active = true;

        emit AuctionCreated(id, coll, tokenId, msg.sender, standard, amount, reserve, startsAt, endsAt);
    }

    // ── Bid (free, cumulative, escrow-until-settle) ───────────────────────────────

    /// @notice Add `msg.value` to your cumulative bid on auction `id`. No refund on
    ///         being outbid — top up again to reclaim the lead. Your effective bid is
    ///         the sum of all your bids. Auctions are auto-activated on creation —
    ///         bids are accepted immediately. Bids that do not place the caller in
    ///         first place (or clear the reserve when there is no leader) revert.
    ///         Losers can withdraw early via withdrawLoserFunds().
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
            // No leader yet: bidder MUST clear the reserve to take the lead.
            // Sub-reserve bids revert — accumulators that can never lead
            // just burn gas for no purpose and complicate refund logic.
            if (newTotal < a.reserve) revert BidTooLow();
            newLead         = true;
            a.leader        = msg.sender;
            a.leaderTotal   = newTotal;
        } else if (newTotal > a.leaderTotal) {
            // Overtaking the leader requires clearing the min increment — a bidder
            // may not sit above the leader without taking the lead.
            uint256 incPct  = uint256(a.leaderTotal) * a.minIncrementBps / 10_000;
            uint256 inc     = incPct > a.minIncrementFlat ? incPct : a.minIncrementFlat;
            // Floor at MIN_BID_INCREMENT so a 0/0 increment config cannot be
            // reduced to a 1-wei griefing loop (audit-#5). Per-cycle
            // gas cost vs. 0.001 ETH flip cost makes the attack uneconomic.
            if (inc < MIN_BID_INCREMENT) inc = MIN_BID_INCREMENT;
            // L-11 fix: keep the min-next comparison in uint256 to avoid
            // silent truncation when leaderTotal + inc exceeds uint128 max.
            // The downcast to uint128 is only done after the comparison
            // passes; if the threshold overflows uint128, revert BidOverflow
            // so the bidder knows the ceiling and can (in principle) request
            // an on-chain increment parameter update from the seller.
            uint256 minNext256 = uint256(a.leaderTotal) + inc;
            if (minNext256 > type(uint128).max) revert BidOverflow();
            uint128 minNext = uint128(minNext256);
            if (newTotal < minNext) revert BidTooLow();
            newLead = true;
            address prev  = a.leader;
            a.leader      = msg.sender;
            a.leaderTotal = newTotal;
            emit OutbidNotification(id, prev, newTotal);
        }
        // else (newTotal <= leaderTotal): bid does not take the lead — revert.
        // Users must bid enough to claim first place; sub-leader accumulation
        // is not allowed. If a bidder cannot afford to take the lead, they
        // should withdraw their funds via withdrawLoserFunds().
        else {
            revert BidTooLow();
        }

        // Anti-snipe — gated on `newLead` so sub-threshold accumulation cannot
        // extend the timer. Underflow-safe: the AuctionEnded check above
        // guarantees block.timestamp < a.endsAt here.
        // M-01 fix: cap total cumulative extensions at 24h beyond the
        // original auction window so griefers on low-gas/high-throughput
        // networks (Flare mainnet at sub-cent FLR) cannot keep the auction
        // alive indefinitely by alternating the lead by min increment.
        unchecked {
            if (newLead && a.endsAt - block.timestamp < EXTENSION_WINDOW) {
                uint64 hardCap = a.startsAt + DURATION_24HR + MAX_TOTAL_EXTENSION;
                uint64 newEnd  = uint64(block.timestamp) + EXTENSION_WINDOW;
                if (newEnd > hardCap) newEnd = hardCap;
                if (newEnd > a.endsAt) {
                    a.endsAt = newEnd;
                    emit AuctionExtended(id, newEnd);
                }
            }
        }

        emit BidPlaced(id, msg.sender, msg.value, newTotal);
    }

    // ── Settle (3-tier: keeper, seller/winner after 5min, permissionless after 25hr)

    /// @notice Finalize a finished auction. Three-tier settlement gate:
    ///         1. KEEPER_ROLE — settles immediately after `endsAt` (1s ticker).
    ///         2. Seller OR auction winner — settles after `endsAt + 5 minutes`.
    ///         3. Permissionless — anyone settles after `endsAt + DURATION_24HR + 1hr`.
    ///         NFT → winner, 1.5% fee → feeRecipient, winningBid−fee → seller.
    ///         If there is no qualifying leader, cancels (all escrow refundable via
    ///         refundLosers). If the NFT can't be delivered, the entire tx reverts —
    ///         no stall state. The keeper bot retries on the next block.
    ///         Losers are refunded separately via refundLosers.
    ///
    ///         If no MarketplaceManager is deployed (manager == address(0)),
    ///         settlement is permissionless immediately as a fallback — funds are
    ///         never trapped.
    // slither-disable-next-line reentrancy-eth
    function settle(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp < a.endsAt) revert AuctionLive();

        // Three-tier settlement gate:
        // 1. KEEPER_ROLE — settles immediately after endsAt (1s ticker).
        // 2. Seller or auction winner — settles after endsAt + 5 minutes.
        // 3. Permissionless — anyone settles after endsAt + DURATION_24HR + 1hr.
        // When no manager is deployed (address(0)), settlement is permissionless
        // immediately — funds are never trapped.
        if (manager != address(0)) {
            (bool ok, bytes memory data) = manager.staticcall(
                abi.encodeWithSignature("hasRole(bytes32,address)", keccak256("KEEPER_ROLE"), msg.sender)
            );
            bool isKeeper = ok && data.length == 32 && abi.decode(data, (bool));
            // Seller or auction winner can settle after a 5-minute cooldown
            // post-auction. This gives the primary parties control over
            // settlement timing without waiting for the keeper or the full
            // 25-hour permissionless fallback.
            bool isSellerOrWinner = (msg.sender == a.seller || msg.sender == a.leader);
            bool canSettle = isKeeper || (isSellerOrWinner && block.timestamp >= a.endsAt + 5 minutes);
            // Permissionless fallback: after DURATION_24HR + 1 hour past endsAt,
            // anyone can settle.
            if (!canSettle && block.timestamp < a.endsAt + DURATION_24HR + 1 hours) {
                revert NotKeeper();
            }
        }

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

        // Consume the winner's escrow up front so refundLosers never repays them.
        cumulative[id][winner] = 0;

        // Transfer the NFT. Both standards use try/catch with state restoration
        // so a seller who moved the NFT or revoked approval cannot permanently
        // trap the winner's escrow. On failure the winner's escrow is restored
        // and the tx reverts; the keeper retries on the next tick.
        // slither-disable-next-line arbitrary-send-erc20
        a.settled = true;
        if (std == TokenStandard.ERC721) {
            try IERC721(coll).transferFrom(sel, winner, tid) {}
            catch {
                a.settled = false;
                cumulative[id][winner] = winBid;
                revert TransferFailed();
            }
        } else {
            try IERC1155(coll).safeTransferFrom(sel, winner, tid, amt, "") {}
            catch {
                a.settled = false;
                cumulative[id][winner] = winBid;
                revert TransferFailed();
            }
        }

        a.settled = true;

        // Payouts never revert: non-receiving recipient falls back to pull-withdrawal.
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



    /// @notice Refund a batch of non-winning bidders their full escrow. Callable
    ///         by anyone after the auction is settled — the keeper handles it
    ///         automatically (1s ticker), but the gate is permissionless so funds
    ///         can never be trapped. Idempotent (zeroed escrow is skipped);
    ///         pull-fallback per address. Bounded `batch.length` (200) keeps a single
    ///         call inside a block's gas budget, and per-call `gas: 50_000` caps the
    ///         EIP-150 63/64 forwarding budget so a griefing receiver can't cascade
    ///         OOG the keeper mid-loop and roll back prior pendingReturns credits.
    // slither-disable-next-line reentrancy-eth
    function refundLosers(uint256 id, address[] calldata batch) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0)) revert NotActive();

        // refundLosers is permissionless — anyone can call it after the auction
        // is settled. The keeper handles it automatically (1s ticker), but the
        // gate is open so losing bidders can always recover their escrow. Idempotent:
        // calling it after all escrow is zeroed simply costs the caller gas.
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



    // ── Early loser withdrawal ─────────────────────────────────────────────────

    /// @notice Withdraw your full escrow from an auction before settlement, if you
    ///         are currently not the leader. Losers can pull their funds early
    ///         instead of waiting for refundLosers after settlement.
    /// @param id The auction to withdraw from.
    function withdrawLoserFunds(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (a.leader == msg.sender) revert AuctionLive(); // leader cannot withdraw early

        uint128 amt = cumulative[id][msg.sender];
        if (amt == 0) revert NothingToWithdraw();

        cumulative[id][msg.sender] = 0;
        (bool ok,) = msg.sender.call{gas: 50_000, value: amt}("");
        if (!ok) {
            pendingReturns[msg.sender] += amt;
            emit PushFailed(msg.sender, amt);
        }
        emit LoserRefunded(id, msg.sender, amt);
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
