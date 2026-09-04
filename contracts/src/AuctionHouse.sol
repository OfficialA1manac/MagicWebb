// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {MarketplaceCore, TokenStandard, TransferFailed, WithdrawFailed, BelowMinPrice, InvalidDuration} from "./MarketplaceCore.sol";
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

error NotSettled();
error NothingToWithdraw();
error BatchTooLarge();
error NotAuthorized();

error CannotCancel();

/// @title AuctionHouse
/// @notice English auctions with a CUMULATIVE bid model, escrow-until-settle, and
///         keeper-driven instant auto-settlement.
///
/// Cumulative model:
///   - A bidder may place many bids on one auction. Their EFFECTIVE bid is the SUM
///     of all their bids (`cumulative[id][bidder]`). Highest effective total wins.
///   - Bidding is FREE. Each bid escrows its msg.value; **outbid bidders are NOT
///     auto-refunded** — funds stay escrowed so they can top up and reclaim the lead.
///     An `OutbidNotification` is emitted when the lead changes.
///   - To take the lead, a bidder's new cumulative must clear the reserve and beat
///     the current leader by the min increment. Sub-threshold bids REVERT
///     (`BidTooLow`) — they do not escrow. Accumulating below the lead was
///     removed: it burned gas for a position that could never win and let a
///     griefer extend the anti-snipe timer with 1-wei bids. A bidder who cannot
///     afford the lead reclaims their escrow with `withdrawLoserFunds()`.
///
/// Settlement (keeper + parties only — funds still never trapped):
///   1. KEEPER_ROLE — settles the instant `endsAt` passes (1s ticker).
///   2. Seller OR auction winner — settle any time after `endsAt` if the
///      keeper has not already done so.
///   No third party can ever settle someone else's auction. Escrow can still
///   never be stuck for a NON-leader: all non-leading escrow is withdrawable at
///   any time via withdrawLoserFunds(). `forceCancel()` (endsAt + 3d, same
///   keeper+seller+winner authority as settle — v3.2 removed the permissionless
///   tier) finalises an auction nobody settled, unlocking refundLosers() for
///   the leader too. Worst case — seller vanished AND keeper dead — the LEADER
///   force-cancels their own auction after 3 days; no path leaves funds locked
///   forever, but no outsider can trigger it either.
///   NFT → winner; 2% fee (1.5% platform + 0.5% keeper); winningBid−fee → seller.
///   The winner's escrow is consumed.
///   - `refundLosers(id, batch)` is callable by ANYONE after settlement and returns
///     each non-winner's full escrow. Batched + pull-fallback so one non-receiving
///     bidder can never brick the refunds. Bounded gas per call.
///
/// Non-custodial; no admin over funds; NOTHING is pausable — no entry or exit
/// path can ever be halted. Upgrades only via the timelocked UUPS path.
///
/// @dev Timestamp usage: This contract uses `block.timestamp` for auction timing
///      (startsAt, endsAt, extension window). Miners can manipulate
///      block.timestamp by up to ~15 seconds on Ethereum mainnet (less on Flare),
///      but all time windows are far larger than the manipulation threshold:
///      - Auctions last from 1 minute to 24 hours (one of 14 fixed durations)
///      - Anti-snipe extension window is 3 minutes (EXTENSION_WINDOW)
///      - No stall window: a failed NFT transfer finalises the auction and refunds
///        the winner on the spot (AuctionSettlementFailed) — nothing waits on a retry
///      A 15-second skew is negligible against these magnitudes and cannot be
///      exploited to force premature settlement or indefinitely extend an auction.
contract AuctionHouse is MarketplaceCore {
    /// @notice Anti-snipe: bids inside this closing window extend the auction by it.
    uint64 public constant EXTENSION_WINDOW     = 3 minutes;


    /// @notice Absolute cap on total anti-snipe extensions: the auction can be
    ///         extended at most 30 minutes past its ORIGINAL end time
    ///         (`originalEndsAt[id] + MAX_TOTAL_EXTENSION` = absolute latest
    ///         possible endsAt, regardless of extension count or duration).
    uint64 public constant MAX_TOTAL_EXTENSION = 30 minutes;
    /// @notice Grace period after endsAt before an auction that was never
    ///         settled at all can be force-cancelled via forceCancel().
    ///
    ///         Undeliverable NFTs no longer need this window: settle() finalises
    ///         the auction on a failed transfer and returns the winner's escrow
    ///         on the spot (see AuctionSettlementFailed). forceCancel() remains
    ///         only as a backstop for an auction nobody ever called settle() on
    ///         (lost seller + dead keeper) — callable by keeper/seller/winner
    ///         (v3.2), after which anyone may drive refundLosers().
    uint64 public constant SELLER_DEFAULT_WINDOW = 3 days;
    /// @notice THE overtaking increment: exactly 1 native token (C2FLR / SGB /
    ///         FLR) marketplace-wide, on every network. v3.3 (owner decision
    ///         2026-08-31) removed seller-chosen percentage increments
    ///         entirely: to take the lead, a bidder's new CUMULATIVE must be
    ///         at least leaderTotal + 1 token. The struct's minIncrementBps /
    ///         minIncrementFlat fields are vestigial storage — written 0,
    ///         never read.
    uint128 public constant MIN_BID_INCREMENT  = 1 ether;

    /// @dev v3.4 repack: 6 slots → 4 (create writes 4 cold SSTOREs, −66k).
    ///      Dropped fields and where their duties went:
    ///        - minIncrementBps/minIncrementFlat: vestigial since v3.3
    ///          (marketplace-wide MIN_BID_INCREMENT), deleted outright.
    ///        - active: vestigial (creation = activation since v3.3);
    ///          activateAuction() removed with it.
    ///        - startsAt: only fed the legacy anti-snipe fallback and the
    ///          AuctionCreated event; the fallback died when originalEndsAt
    ///          moved into the struct (always set on a fresh deployment) and
    ///          the event emits block.timestamp directly.
    ///        - leaderTotal: DERIVED, not stored — the invariant
    ///          `leaderTotal == cumulative[id][leader]` held by construction,
    ///          so bid()/settle() read cumulative and the leaderTotal(id)
    ///          compat view serves off-chain readers (note: it reads 0 after
    ///          settlement, when the winner's escrow has been consumed).
    ///      Width bounds (external ABI keeps uint128; _create guards):
    ///        - reserve/amount: uint96 caps at ~7.9e10 ether — above any
    ///          single wallet's holdings on Flare-family chains; BidOverflow
    ///          on anything larger.
    ///        - endsAt/originalEndsAt: uint40 seconds is fine until y36812.
    struct Auction {
        // slot 0: 20 + 5 + 5 + 1 + 1 = 32
        address       seller;
        uint40        endsAt;
        uint40        originalEndsAt;   // pre-extension end; anti-snipe hard cap anchor
        bool          settled;
        TokenStandard standard;
        // slot 1: 20 + 12 = 32
        address       collection;
        uint96        reserve;
        // slot 2
        uint256       tokenId;
        // slot 3: 20 + 12 = 32
        address       leader;            // current highest-cumulative bidder
        uint96        amount;            // token amount (1 for ERC-721)
    }

    uint256 public nextAuctionId;
    mapping(uint256 => Auction) public auctions;

    /// @notice Position-stable getter for the Auction struct. Off-chain and
    /// off-test code MUST use this instead of the auto-generated `auctions(id)`
    /// getter, which returns a positional tuple — silent misreads on struct
    /// reflow if a new field is ever added before `leader`. Test harnesses
    /// also gain audit-fix stability: invariants can assert on a named field
    /// (`.endsAt`, `.settled`, `.leader`) rather than counting commas in a
    /// destructuring tuple.
    function getAuction(uint256 id) external view returns (Auction memory) {
        return auctions[id];
    }

    /// @notice The leader's cumulative escrow — v3.4 compat view for the
    ///         dropped stored field. DERIVED: equals cumulative[id][leader]
    ///         by construction. Reads 0 when there is no leader, and 0 after
    ///         settlement (the winner's escrow is consumed by settle()); code
    ///         that needs the winning amount post-settle should read the
    ///         AuctionSettled event.
    function leaderTotal(uint256 id) external view returns (uint128) {
        address l = auctions[id].leader;
        return l == address(0) ? 0 : cumulative[id][l];
    }

    /// @notice v3.4 compat view: originalEndsAt moved from a standalone
    ///         mapping into the Auction struct.
    function originalEndsAt(uint256 id) external view returns (uint64) {
        return auctions[id].originalEndsAt;
    }

    /// @notice cumulative[auctionId][bidder] → total wei this bidder has escrowed.
    /// @dev v3.4: the write-only bidder registry (_bidders array + _seenBidder
    ///      presence flag) is GONE — the contract never read it, refundLosers
    ///      takes its batch as calldata, and the off-chain keeper/indexer
    ///      reconstructs the bidder set from BidPlaced events. First-bid cost
    ///      drops ~44k (array length + element + seen-flag cold SSTOREs).
    ///      originalEndsAt likewise moved from a standalone mapping into the
    ///      Auction struct (fresh deployment — no layout compatibility owed).
    mapping(uint256 => mapping(address => uint128)) public cumulative;

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
    /// @notice Emitted by forceCancel() — auction was permanently undeliverable.
    event AuctionForceCancelled(uint256 indexed id);
    /// @notice Emitted by settle() when the seller failed to deliver the NFT.
    ///         The auction is final, no fee is charged, the seller is paid
    ///         nothing, and `amount` has been returned to (or credited to) the
    ///         winner. Losers withdraw as usual via refundLosers().
    event AuctionSettlementFailed(uint256 indexed id, address indexed winner, uint128 amount);
    event RefundPushed(address indexed bidder, uint256 amount);

    /// @notice v3.4: feeRecipient + manager are baked into the implementation
    ///         as immutables (validated in MarketplaceCore's constructor).
    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor(address recipient, address manager_)
        MarketplaceCore(recipient, manager_)
    { _disableInitializers(); }

    /// @notice One-time proxy initializer — no arguments in v3.4 (immutables
    ///         live in the implementation). Still initializer-gated.
    function initialize() public initializer {
        __MarketplaceCore_init();
    }

    // ── Create (free) ───────────────────────────────────────────────────────────
    // v3.4: activateAuction() removed — creation IS activation since v3.3 and
    // the vestigial `active` field is gone from the struct.

    /// @notice Create an ERC-721 auction. Starts immediately.
    /// @param duration One of the fourteen shared durations; endsAt is computed on-chain.
    /// @dev v3.3: no increment parameters. The overtake step is the
    ///      marketplace-wide MIN_BID_INCREMENT (1 native token) — sellers
    ///      cannot raise or lower it.
    function create(address coll, uint256 tokenId, uint128 reserve, uint64 duration)
        external nonReentrant returns (uint256 id)
    {
        return _create(TokenStandard.ERC721, coll, tokenId, 1, reserve, _expiryFor(duration));
    }

    /// @notice Create an ERC-1155 auction. Starts immediately. See create().
    function create1155(address coll, uint256 tokenId, uint128 amount, uint128 reserve, uint64 duration)
        external nonReentrant returns (uint256 id)
    {
        if (amount == 0) revert InvalidAmount();
        return _create(TokenStandard.ERC1155, coll, tokenId, amount, reserve, _expiryFor(duration));
    }

    function _create(
        TokenStandard standard,
        address coll,
        uint256 tokenId,
        uint128 amount,
        uint128 reserve,
        uint64  endsAt
    ) internal returns (uint256 id) {
        // endsAt was produced by _expiryFor(): in the future, one of the fourteen durations.
        if (endsAt <= block.timestamp) revert InvalidWindow();
        if (reserve < MIN_PRICE) revert BelowMinPrice();

        if (standard == TokenStandard.ERC721) {
            if (IERC721(coll).ownerOf(tokenId) != msg.sender) revert NotSeller();
            if (!IERC721(coll).isApprovedForAll(msg.sender, address(this))
                && IERC721(coll).getApproved(tokenId) != address(this)) revert NotApproved();
        } else {
            if (IERC1155(coll).balanceOf(msg.sender, tokenId) < amount) revert NotSeller();
            if (!IERC1155(coll).isApprovedForAll(msg.sender, address(this))) revert NotApproved();
        }

        // v3.4 width bounds: external ABI keeps uint128, storage is uint96.
        // ~7.9e10 ether is beyond any single wallet on Flare-family chains.
        if (reserve > type(uint96).max || amount > type(uint96).max) revert BidOverflow();

        id = ++nextAuctionId;
        // v3.4: exactly 4 cold slot writes (seller/endsAt/originalEndsAt/
        // standard | collection/reserve | tokenId | amount). The auction is
        // live from creation (no `active` flag, no separate activate step);
        // originalEndsAt lives in the struct and anchors the anti-snipe cap.
        Auction storage a = auctions[id];
        a.seller         = msg.sender;
        a.endsAt         = uint40(endsAt);
        a.originalEndsAt = uint40(endsAt);
        a.standard       = standard;
        a.collection     = coll;
        a.reserve        = uint96(reserve);
        a.tokenId        = tokenId;
        a.amount         = uint96(amount);

        emit AuctionCreated(id, coll, tokenId, msg.sender, standard, amount, reserve, uint64(block.timestamp), endsAt);
    }

    // ── Bid (free, cumulative, escrow-until-settle) ───────────────────────────────

    /// @notice Add `msg.value` to your cumulative bid on auction `id`. No refund on
    ///         being outbid — top up again to reclaim the lead. Your effective bid is
    ///         the sum of all your bids. Auctions are auto-activated on creation —
    ///         bids are accepted immediately. Bids that do not place the caller in
    ///         first place (or clear the reserve when there is no leader) revert.
    ///         Losers can withdraw early via withdrawLoserFunds().
    function bid(uint256 id) external payable nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp >= a.endsAt) revert AuctionEnded();
        if (msg.value == 0) revert InvalidAmount();

        uint128 prevCum = cumulative[id][msg.sender];
        uint256 nt;
        unchecked { nt = uint256(prevCum) + msg.value; } // uint128 + msg.value cannot overflow uint256
        if (nt > type(uint128).max) revert BidOverflow();
        uint128 newTotal = uint128(nt);

        // v3.4: no bidder registry — the contract never read it; the keeper/
        // indexer reconstructs the bidder set from BidPlaced events and passes
        // refundLosers its batch as calldata. First bid saves ~44k.
        cumulative[id][msg.sender] = newTotal;

        // Leadership update. Invariant: the leader always holds the max cumulative.
        // `newLead` flips true ONLY when leadership actually changes; the anti-snipe
        // extension below reads that flag so escrow-accumulating sub-threshold bids
        // can no longer keep pushing endsAt forward (audit-#1: griefer repeated
        // 1-wei bids inside the closing window and permanently stalled the
        // auction, stranding winner + losers' funds).
        bool newLead = false;
        if (a.leader == msg.sender) {
            // Leader tops up: cumulative (written above) IS the leader total
            // in v3.4 — no stored duplicate to maintain, no SSTORE at all.
        } else if (a.leader == address(0)) {
            // No leader yet: bidder MUST clear the reserve to take the lead.
            // Sub-reserve bids revert — accumulators that can never lead
            // just burn gas for no purpose and complicate refund logic.
            if (newTotal < a.reserve) revert BidTooLow();
            newLead  = true;
            a.leader = msg.sender;
        } else {
            // Overtake path. v3.4: the leader's total is DERIVED —
            // cumulative[id][leader] (one cold SLOAD) replaces the stored
            // leaderTotal (which cost an SSTORE on every lead change).
            // v3.3 rule unchanged: ONE increment for the whole marketplace —
            // overtaking costs exactly leaderTotal + 1 native token
            // (MIN_BID_INCREMENT). No seller percentage, no per-auction knobs.
            uint128 lead = cumulative[id][a.leader];
            // L-11 fix retained: compare in uint256 to avoid silent
            // truncation when lead + increment exceeds uint128 max.
            uint256 minNext256 = uint256(lead) + MIN_BID_INCREMENT;
            if (minNext256 > type(uint128).max) revert BidOverflow();
            if (uint256(newTotal) < minNext256) revert BidTooLow();
            // Users must bid enough to claim first place; sub-leader
            // accumulation is not allowed. If a bidder cannot afford the
            // lead, they withdraw via withdrawLoserFunds().
            newLead = true;
            address prev = a.leader;
            a.leader     = msg.sender;
            emit OutbidNotification(id, prev, newTotal);
        }

        // Anti-snipe — gated on `newLead` so sub-threshold accumulation cannot
        // extend the timer. Underflow-safe: the AuctionEnded check above
        // guarantees block.timestamp < a.endsAt here.
        // Extensions are hard-capped at 30 minutes past the auction's ORIGINAL
        // end (originalEndsAt + MAX_TOTAL_EXTENSION) for every duration, so a
        // 3-minute auction can run at most 33 minutes total and griefers on
        // low-gas networks (Flare at sub-cent FLR) cannot keep any auction
        // alive indefinitely by alternating the lead. v3.4: originalEndsAt is
        // a struct field, ALWAYS set by _create on this fresh deployment —
        // the legacy startsAt-based fallback is gone with the startsAt field.
        unchecked {
            if (newLead && uint64(a.endsAt) - block.timestamp < EXTENSION_WINDOW) {
                uint64 hardCap = uint64(a.originalEndsAt) + MAX_TOTAL_EXTENSION;
                uint64 newEnd  = uint64(block.timestamp) + EXTENSION_WINDOW;
                if (newEnd > hardCap) newEnd = hardCap;
                if (newEnd > a.endsAt) {
                    a.endsAt = uint40(newEnd);
                    emit AuctionExtended(id, newEnd);
                }
            }
        }

        emit BidPlaced(id, msg.sender, msg.value, newTotal);
    }

    // ── Settle (keeper instant, or seller/winner — never a third party) ──────

    /// @notice Finalize a finished auction. Settlement gate:
    ///         1. KEEPER_ROLE — settles the instant `endsAt` passes (1s ticker).
    ///         2. Seller OR auction winner — settle any time after `endsAt` when
    ///            the keeper has not already done so.
    ///         No one else can ever settle: there is no permissionless tier.
    ///         NFT → winner, 2% fee (1.5% platform + 0.5% keeper), winningBid−fee → seller.
    ///         If there is no qualifying leader, cancels (all escrow refundable
    ///         via refundLosers; with no leader the escrow is provably zero for
    ///         losing bids that never led — non-leading bids revert on entry).
    ///         If the NFT can't be delivered the auction still finalises: no fee,
    ///         seller gets nothing, and the winner's escrow is pushed back (with
    ///         pull-fallback) on the spot — see AuctionSettlementFailed.
    ///         Losers are refunded separately via refundLosers.
    ///
    ///         Escrow backstop: if the seller is lost AND the keeper is dead,
    ///         the WINNER can forceCancel() (endsAt + 3 days) to finalise the
    ///         auction so refundLosers releases every bidder's escrow — an
    ///         auction can NEVER be stuck for its parties.
    // slither-disable-next-line reentrancy-eth
    function settle(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp < a.endsAt) revert AuctionLive();

        // Parties first (skips the staticcall on the common seller/winner path);
        // keeper checked via the manager role registry. No time-based fallback:
        // settlement authority never widens beyond keeper + seller + winner.
        bool authorized = (msg.sender == a.seller || msg.sender == a.leader);
        if (!authorized) {
            (bool ok, bytes memory data) = manager.staticcall(
                abi.encodeWithSignature("hasRole(bytes32,address)", keccak256("KEEPER_ROLE"), msg.sender)
            );
            authorized = ok && data.length == 32 && abi.decode(data, (bool));
        }
        if (!authorized) revert NotAuthorized();

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
        // v3.4: the winning total is DERIVED — read the winner's escrow
        // BEFORE consuming it below (the stored leaderTotal field is gone).
        uint128       winBid  = cumulative[id][winner];
        uint128       fee     = uint128(_feeOf(winBid));

        // Consume the winner's escrow up front so refundLosers never repays them.
        cumulative[id][winner] = 0;

        // Transfer the NFT. A seller who moved the NFT away or revoked approval
        // has defaulted on delivery: settlement finalises anyway and the
        // winner's escrow is released immediately. Reverting here (the previous
        // behaviour) stranded the leader's funds until forceCancel() became
        // callable at endsAt + SELLER_DEFAULT_WINDOW — a 3-day lockup the
        // seller could trigger for free, and which the leader could not escape
        // via withdrawLoserFunds() because that path excludes the leader.
        a.settled = true;
        bool delivered;
        if (std == TokenStandard.ERC721) {
            // `sel` is the auction's recorded seller, not caller-supplied: it was
            // written by create() and is never mutated. Pulling from an arbitrary
            // `from` is the whole point of an escrowless marketplace — the seller
            // approved this contract at listing time.
            //
            // DELIBERATE ASYMMETRY: this is `transferFrom`, while the direct-sale
            // path (MarketplaceCore._deliver) uses `safeTransferFrom`. The
            // difference is who can veto the transfer.
            //   - Direct sale: the buyer is msg.sender. A reverting
            //     onERC721Received only reverts their own purchase. Safe transfer
            //     costs them nothing and protects them from buying into a
            //     contract that cannot hold the NFT.
            //   - Auction settle: the winner is NOT the caller, and the auction
            //     has already closed at a price they committed to. With
            //     safeTransferFrom, a winner whose contract reverts in
            //     onERC721Received would land in the `catch` below, take a FULL
            //     refund, and leave the seller with no sale — a free post-hoc
            //     veto on an auction they already won. transferFrom removes that
            //     veto: delivery succeeds and the seller gets paid.
            // The trade-off is that a winning contract with no ERC-721 handling
            // receives a token it may be unable to move. That is the winner's own
            // contract and their own bid; it is strictly preferable to letting
            // them cancel a concluded auction at the seller's expense.
            // ERC-1155 has no unsafe transfer variant, so that branch cannot make
            // the same choice — see the `catch` path for how it degrades.
            // slither-disable-next-line arbitrary-send-erc20
            try IERC721(coll).transferFrom(sel, winner, tid) { delivered = true; }
            catch {}
        } else {
            try IERC1155(coll).safeTransferFrom(sel, winner, tid, amt, "") { delivered = true; }
            catch {}
        }
        if (!delivered) {
            // No sale happened: no fee is taken and the seller is paid nothing.
            // The winner is made whole with the same best-effort push and
            // pull-fallback used everywhere else, so a contract bidder without
            // a payable receive() can still withdrawRefund(). Losers are
            // refunded through refundLosers(), which is unlocked by a.settled.
            (bool okWin,) = winner.call{gas: 50_000, value: winBid}("");
            if (!okWin) {
                pendingReturns[winner] += winBid;
                emit PushFailed(winner, winBid);
            }
            emit AuctionSettlementFailed(id, winner, winBid);
            return;
        }

        // Payouts never revert: non-receiving recipient falls back to pull-withdrawal.
        _payFee(fee); // 2% split 1.5% feeRecipient / 0.5% keeper; event `fee` stays the total
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

        // v3.4: length hoisted + unchecked increment (−7k on a full batch).
        // Safe: the bounds check above ran, calldata length is constant, and
        // i < len < 2^256 cannot overflow.
        uint256 len = batch.length;
        for (uint256 i; i < len; ) {
            address b = batch[i];
            uint128 amt = cumulative[id][b];
            if (amt == 0) { unchecked { ++i; } continue; } // winner (consumed) or already refunded
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
            unchecked { ++i; }
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
        // Reserve-met lock: a leader exists, so the reserve has been met by
        // construction — bid() only ever installs a leader whose cumulative
        // clears the reserve. Walking back at this point would let the seller
        // snipe their own auction (audit-#6).
        if (a.leader != address(0)) revert CannotCancel();
        a.settled = true;
        emit AuctionCancelled(id);
    }

    // ── Force cancel (party/keeper safety valve for permanently-failed delivery) ──

    /// @notice Safety valve: after `endsAt + SELLER_DEFAULT_WINDOW`, the
    ///         auction's PARTIES (seller, leader) or the keeper can
    ///         force-cancel an auction whose settle() has permanently failed
    ///         (seller moved the NFT away or revoked approval, making delivery
    ///         impossible). This sets `a.settled = true`, unlocking
    ///         refundLosers() so ALL bidders — including the trapped leader —
    ///         recover their escrow. Before the window, settle() must be
    ///         retried normally (the keeper does this every tick). The NFT
    ///         stays with whoever holds it; forceCancel is purely an escrow
    ///         recovery path, not a trade reversal.
    ///
    ///         v3.2 (owner decision 2026-08-31): this was permissionless;
    ///         it is now restricted to the same authority set as settle() —
    ///         keeper + seller + winner. Nobody else may trigger the 3-day
    ///         rescue. The leader's own recovery interest is preserved (the
    ///         leader IS an authorized caller); non-leading bidders were never
    ///         dependent on this path — withdrawLoserFunds() lets them pull
    ///         their own escrow at any time before settlement.
    ///
    ///         Design rationale: the contract docstring promises "funds are
    ///         never trapped" — without this path, a seller who permanently
    ///         blocks NFT delivery (by transferring it elsewhere or revoking
    ///         approval forever) would trap the leader's cumulative escrow
    ///         with zero on-chain recovery. The 3-day window is far longer
    ///         than the longest auction (24h) and gives the keeper ample time
    ///         to retry; after that, bidder protection dominates.
    function forceCancel(uint256 id) external nonReentrant {
        Auction storage a = auctions[id];
        if (a.seller == address(0) || a.settled) revert NotActive();
        if (block.timestamp < a.endsAt + SELLER_DEFAULT_WINDOW) revert AuctionLive();
        // Same authority shape as settle(): parties first, then the keeper
        // probe via the manager. No permissionless tier.
        bool authorized = (msg.sender == a.seller || msg.sender == a.leader);
        if (!authorized) {
            (bool ok, bytes memory data) = manager.staticcall(
                abi.encodeWithSignature("hasRole(bytes32,address)", keccak256("KEEPER_ROLE"), msg.sender)
            );
            authorized = ok && data.length == 32 && abi.decode(data, (bool));
        }
        if (!authorized) revert NotAuthorized();
        a.settled = true;
        emit AuctionForceCancelled(id);
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

    // v3.4: bidderCount/getBidder views removed with the bidder registry —
    // the keeper/indexer enumerates bidders from BidPlaced events.

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
    // Same shape as MarketplaceCore.withdrawRefund: zero-then-call, and the
    // only post-call write is the restore inside the reverting failure branch.
    // slither-disable-next-line reentrancy-eth
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
