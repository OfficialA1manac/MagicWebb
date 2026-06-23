// SPDX-License-Identifier: MIT
//   Audit-fuzz harness covering 4 of the v9+/v10-fixes applied in this repo:
//     (a) AuctionHouse.bid() anti-snipe — extension gated on `newLead`
//     (b) AuctionHouse.settle() stalled-state recovery (settleUnstuck / reclaim)
//     (c) OfferBook _pushPullRefund fallback (rejectOffer + refundExpiredOffer)
//     (d) AuctionHouse.refundLosers batch cap (BatchTooLarge) + per-call gas bound
//
//   Each test has a multi-line `// INVARIANT:` comment that explains the
//   behavioural property being exercised; the asserts inside the test body
//   measure exactly that property.
pragma solidity 0.8.26;

import {Test, console2} from "forge-std/Test.sol";

import {
    AuctionHouse,
    BatchTooLarge,
    AuctionLive,
    NotActive,
    NotSettled,
    NotStalled,
    StallNotOver
} from "../src/AuctionHouse.sol";
// Note: `Auction` is an inner struct of AuctionHouse, accessed as
// `AuctionHouse.Auction` from the test contract — Solidity user-defined
// value types only accept elementary types, so keep the inline qualification.
import {
    OfferBook,
    NoOffer,
    OfferActive,
    NoPendingRefund
} from "../src/OfferBook.sol";
import {MockERC721}  from "./MockERC721.sol";
import {MockERC1155} from "./MockERC1155.sol";

// ─── Stubs ──────────────────────────────────────────────────────────────────

/// @dev Bidder stub that can be toggled to revert on every receive(). Used in
///      scenarios (c) and (d-2) to exercise the push-payment fallback path.
contract GreedyReceiver {
    bool public blocked;

    constructor() payable { blocked = true; } // default-blocked; flip to allow push / pull

    receive() external payable {
        if (blocked) revert("blocked");
    }

    function setBlocked(bool b) external { blocked = b; }

    /// @dev Pull-pattern: ask OfferBook / AuctionHouse to credit our pending refund.
    function proxyWithdrawOffer(OfferBook ob) external { ob.withdrawRefund(); }
    function proxyWithdrawAuction(AuctionHouse ah) external { ah.withdrawRefund(); }

    /// @dev Place a 1-wei (or arbitrary) bid on an auction; we fund via vm.deal.
    function bidOn(AuctionHouse ah, uint256 id) external payable { ah.bid{value: msg.value}(id); }
}

// ─── Test contract ──────────────────────────────────────────────────────────

contract AuditFuzzTest is Test {
    AuctionHouse ah;
    OfferBook    ob;
    MockERC721   nft;
    MockERC1155  multi;

    address feeRecipient = address(0x1111000000000000000000000000000000111100);
    address seller       = address(0xBEEF);
    address alice        = address(0xA11CE);
    address bob          = address(0xB0B);

    function setUp() public {
        ah    = new AuctionHouse(feeRecipient, address(0));
        ob    = new OfferBook(feeRecipient, address(0));
        nft   = new MockERC721();
        multi = new MockERC1155();

        vm.deal(alice, 100 ether);
        vm.deal(bob,   100 ether);
        vm.deal(seller, 100 ether);
    }

    // ─── helpers ──────────────────────────────────────────────────────────────

    function _auctionEndsIn(uint64 dt) internal returns (uint256 id, uint256 tid) {
        vm.startPrank(seller);
        tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        id = ah.create(address(nft), tid, 1 ether, uint64(block.timestamp) + dt, 500, 0);
        vm.stopPrank();
    }

    function _auction7d() internal returns (uint256 id, uint256 tid) { return _auctionEndsIn(7 days); }

    function _bid(uint256 id, address who, uint128 amt) internal {
        vm.prank(who);
        ah.bid{value: amt}(id);
    }

    // Position-stable reads via AuctionHouse.getAuction(id) — if the Auction
    // struct field order ever changes, these helpers self-update by name rather
    // than relying on a brittle positional comma count. The previous
    // destructuring silently misread on struct reflow: `audit-#2`'s
    // stalledAt-at-position-14 assumption could shift to a different slot if
    // anyone inserted a field before it, and the compile would still pass
    // while every C-02 invariant asserted wrong numbers.
    function _a(uint256 id) internal view returns (AuctionHouse.Auction memory) {
        return ah.getAuction(id);
    }
    function _endsAt(uint256 id)   internal view returns (uint64)  { return _a(id).endsAt; }
    function _settled(uint256 id)  internal view returns (bool)    { return _a(id).settled; }
    function _stalled(uint256 id)  internal view returns (uint64)  { return _a(id).stalledAt; }
    function _leader(uint256 id)   internal view returns (address, uint128) {
        AuctionHouse.Auction memory a = _a(id);
        return (a.leader, a.leaderTotal);
    }

    /// @dev Deterministic EOA factory: seed ("EOA" || i) -> address. Avoids
    ///      any hex-literal math pitfalls (uint160 width, trailing letters
    ///      inside number literals, etc.).
    function _eoa(uint256 i) internal pure returns (address) {
        return address(uint160(uint256(keccak256(abi.encodePacked("EOA", i)))));
    }

    /// @dev Deterministic 1-wei "grain" bidder factory for the anti-snipe loop.
    function _grain(uint256 i) internal pure returns (address) {
        return address(uint160(uint256(keccak256(abi.encodePacked("GRAIN", i)))));
    }

    // ════════════════════════════════════════════════════════════════════════
    //  (a)  Anti-snipe: 1000 1-wei late bids MUST NOT keep extending endsAt
    //      past a single EXTENSION_WINDOW push.
    //
    //  INVARIANT: endsAt may be extended exactly ONCE — the moment the FIRST
    //  qualified new-lead bid lands inside the closing window. After that,
    //  every sub-threshold accumulation bid (1-wei from non-lead addresses)
    //  MUST keep `endsAt` strictly equal to the once-extended value. (Audit-#1)
    // ════════════════════════════════════════════════════════════════════════

    function testFuzz_antiSnipe1kLateBids(uint256 seed) public {
        uint256 n = bound(seed, 100, 1000);

        // 1. Auction that ends in 60 minutes; alice takes the lead with 2 ETH.
        (uint256 id,) = _auctionEndsIn(1 hours);
        _bid(id, alice, 2 ether);
        uint64 startEnd = _endsAt(id);

        // 2. Warp INSIDE the closing window — 30 s before endsAt, well inside
        //    the 180 s EXTENSION_WINDOW. We need to be past endsAt - EXTENSION_WINDOW
        //    so the anti-snipe branch's guard `a.endsAt - block.timestamp < EXTENSION_WINDOW` is true.
        vm.warp(uint256(startEnd) - 30);

        // 3. Drive a NEW-LEAD bid from a fresh address — this is the single
        //    extension event the audit fix preserves (newLead=true).
        //    Bid 3 ETH so it visibly beats alice's 2 ETH + 5 % min increment (= 2.1 ETH).
        address lateLeader = _eoa(0xCAFE);
        vm.deal(lateLeader, 100 ether);
        vm.prank(lateLeader);
        ah.bid{value: 3 ether}(id); // 3 ether > 2.1 ether -> new leader; endsAt extended by EXTENSION_WINDOW

        uint64 endAfterLead = _endsAt(id);
        // The anti-snipe rule is: ONE new-lead bid inside the closing window
        // extends endsAt. We don't care about the exact arithmetic; just that
        // endsAt strictly advanced past its pre-extension value.
        assertGt(
            endAfterLead,
            startEnd,
            "new-lead bid MUST extend endsAt (anti-snipe fires ONCE on new-lead only)"
        );

        // 4. Now hammer the closing window with N 1-wei bids from random
        //    addresses. None of them accumulate enough to lead → newLead=false
        //    → the fix MUST skip the extension branch.
        for (uint256 i = 0; i < n; ++i) {
            address grain = _grain(i);
            vm.deal(grain, 1);
            vm.prank(grain);
            ah.bid{value: 1}(id);
            assertEq(_endsAt(id), endAfterLead, "non-lead 1-wei bid MUST NOT extend endsAt");
        }

        // Final: endsAt is still the SINGLE-EXTENSION value (post-leader-change).
        assertEq(_endsAt(id), endAfterLead, "endsAt unchanged across N accreting 1-wei bids");
    }

    // ════════════════════════════════════════════════════════════════════════
    //  (b)  Stalled-state: seller revokes between endsAt and settle →
    //      settle() parks the auction (stalledAt != 0), does NOT latch
    //      `settled`. After seller re-approves, settleUnstuck() completes.
    //      If seller never re-approves and STALL_WINDOW elapses,
    //      reclaim() refunds the winner and cancels.  (Audit-#2)
    // ════════════════════════════════════════════════════════════════════════

    function test_sellerRevokeThenSettleParksThenUnstuckCompletes() public {
        (uint256 id, uint256 tid) = _auction7d();
        _bid(id, alice, 2 ether); // leader
        vm.warp(block.timestamp + 8 days);

        // Seller revokes approval before settle.
        vm.prank(seller);
        nft.setApprovalForAll(address(ah), false);

        // settle() parks: emits AuctionStalled, sets stalledAt, does NOT
        // latch settled. NFT stays with the seller.
        vm.expectEmit(false, false, false, true, address(ah));
        emit AuctionHouse.AuctionStalled(id, alice, seller);
        ah.settle(id);

        assertFalse(_settled(id), "settled must be FALSE (parked, not latched)");
        assertGt(_stalled(id), 0, "stalledAt must be SET");
        assertEq(nft.ownerOf(tid), seller, "NFT still with seller (no delivery)");

        // A repeat settle() must revert with NotStalled (the new guard).
        vm.expectRevert(NotStalled.selector);
        ah.settle(id);

        // Seller re-approves → settleUnstuck by anyone completes.
        vm.prank(seller);
        nft.setApprovalForAll(address(ah), true);

        uint256 sellerBalBefore = seller.balance;
        uint256 vaultBalBefore  = feeRecipient.balance;
        ah.settleUnstuck(id);

        assertTrue(_settled(id), "settled after settleUnstuck");
        assertEq(_stalled(id), 0, "stalledAt cleared after delivery");
        assertEq(nft.ownerOf(tid), alice, "NFT delivered to winner on settleUnstuck");
        uint256 fee = (uint256(2 ether) * 150) / 10_000;
        assertEq(feeRecipient.balance - vaultBalBefore, fee, "fee transferred");
        assertEq(seller.balance - sellerBalBefore, 2 ether - fee, "proceeds transferred");
        assertEq(ah.cumulative(id, alice), 0, "winner escrow consumed on settleUnstuck");
    }

    function test_sellerNeverReapprovesReclaimAfterStallWindow() public {
        (uint256 id,) = _auction7d();
        _bid(id, alice, 1.5 ether);
        vm.warp(block.timestamp + 8 days);

        vm.prank(seller);
        nft.setApprovalForAll(address(ah), false);
        ah.settle(id);
        assertGt(_stalled(id), 0);

        // Just before STALL_WINDOW — reclaim MUST revert with StallNotOver.
        vm.warp(block.timestamp + ah.STALL_WINDOW() - 1);
        vm.expectRevert(StallNotOver.selector);
        ah.reclaim(id);

        // After STALL_WINDOW — reclaim refunds winner + cancels.
        vm.warp(block.timestamp + 1);
        uint256 aliceBalBefore = alice.balance;
        vm.expectEmit(false, false, false, true, address(ah));
        emit AuctionHouse.AuctionReclaimed(id, alice, 1.5 ether);
        ah.reclaim(id);

        assertTrue(_settled(id),  "settled=true post-reclaim");
        assertEq(_stalled(id),  0, "stalledAt cleared post-reclaim");
        assertEq(alice.balance, aliceBalBefore + 1.5 ether, "winner refunded in full");
        assertEq(ah.cumulative(id, alice), 0, "leader escrow cleared on reclaim");
    }

    // ════════════════════════════════════════════════════════════════════════
    //  (c)  Non-receiving bidder for an OfferBook EXPIRED offer:
    //      makeOffer succeeds (receive() works at first); expires; once
    //      receive() reverts, refundExpiredOffer's push branch must FAIL,
    //      but _pushPullRefund MUST divert the refund into pendingReturns
    //      and avoid a stuck offer. Bidder later un-blocks and pulls
    //      via withdrawRefund().  (Audit-#3)
    // ════════════════════════════════════════════════════════════════════════

    function test_offerExpiredRefundPushFallback() public {
        // NFT minted + approved by seller for OfferBook consumption.
        uint256 tid = nft.mint(seller);
        vm.prank(seller);
        nft.setApprovalForAll(address(ob), true);

        // Bidder is a GreedyReceiver — receive() works at first; we can flip it.
        GreedyReceiver bidder = new GreedyReceiver();
        vm.deal(address(bidder), 10 ether);

        uint64 exp = uint64(block.timestamp) + 1 days;
        vm.prank(address(bidder));
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, exp);
        (uint128 principal,,,) = ob.positions(address(nft), tid, address(bidder));
        assertEq(principal, 1 ether, "offer escrowed at principal = 1 ETH");

        // Expire the offer.
        vm.warp(uint256(exp) + 1);

        // Block receive() — the push-payment branch in refundExpiredOffer will
        // now fail. Verify the contract does NOT revert: the pull fallback in
        // _pushPullRefund stores the refund into pendingReturns instead.
        bidder.setBlocked(true);

        assertEq(ob.pendingReturns(address(bidder)), 0, "no pending before expiry");
        ob.refundExpiredOffer(address(nft), tid, address(bidder));
        (principal,,,) = ob.positions(address(nft), tid, address(bidder));
        assertEq(principal, 0, "position deleted on refund");
        assertEq(ob.pendingReturns(address(bidder)), 1 ether, "push-failed refund -> pendingReturns");

        // withdrawRefund while receive() blocked → inner call{value}() fails,
        // and the withdrawRefund body RESTORES the bookkeeping via
        // pendingReturns[msg.sender] = amt before reverting → it MUST revert.
        vm.expectRevert(); // generic revert (WithdrawFailed re-throw)
        bidder.proxyWithdrawOffer(ob);
        assertEq(ob.pendingReturns(address(bidder)), 1 ether, "withdraw all-or-nothing restores on failure");

        // Unblock receive() → withdrawRefund succeeds and clears the credit.
        bidder.setBlocked(false);
        uint256 balBefore = address(bidder).balance;
        bidder.proxyWithdrawOffer(ob);
        assertEq(ob.pendingReturns(address(bidder)), 0, "pendingReturns cleared on successful withdraw");
        assertEq(address(bidder).balance, balBefore + 1 ether, "bidder received refund");
    }

    // ════════════════════════════════════════════════════════════════════════
    //  (d-1)  refundLosers batch.length cap. (Audit-#4 part A)
    //
    //  INVARIANT: batch.length==0 or batch.length>200 → revert BatchTooLarge.
    //  Batch.length in [1,200] → success (no revert).
    // ════════════════════════════════════════════════════════════════════════

    function testFuzz_refundLosersBatchCap(uint256 n) public {
        n = bound(n, 0, 1000);

        (uint256 id,) = _auction7d();
        _bid(id, alice, 1 ether); // leader so something exists to settle.
        vm.warp(block.timestamp + 8 days);
        ah.settle(id);

        address[] memory batch = new address[](n);
        for (uint256 i; i < n; ++i) batch[i] = alice;

        if (n == 0 || n > 200) {
            vm.expectRevert(BatchTooLarge.selector);
            ah.refundLosers(id, batch);
        } else {
            ah.refundLosers(id, batch); // success
        }
    }

    // ════════════════════════════════════════════════════════════════════════
    //  (d-2) 50%-griefing 200-batch: outer tx MUST NOT OOG.
    //
    //  INVARIANT: with 100 EOA losers + 100 GreedyReceiver losers in a single
    //  200-batch, the per-call gas:50000 cap confines hostile receive()'
    //  failures. Outer call succeeds; EOA losers receive funds; greedy losers
    //  bounce to pendingReturns. (Audit-#4 part B)
    // ════════════════════════════════════════════════════════════════════════

    function test_refundLosersGriefingHalfBatchDoesNotOOG() public {
        (uint256 id,) = _auction7d();

        // Single leader + 100 EOA losers + 100 GreedyReceiver losers — every
        // non-leader escrows 1 ETH.
        address leaderAddr = address(uint160(uint256(keccak256(abi.encodePacked("AUCTION_LEADER")))));
        vm.deal(leaderAddr, 200 ether);
        _bid(id, leaderAddr, 200 ether);

        address[] memory eoas = new address[](100);
        for (uint256 i; i < 100; ++i) {
            eoas[i] = _eoa(i);
            vm.deal(eoas[i], 1 ether);
            _bid(id, eoas[i], 1 ether);
        }

        GreedyReceiver[] memory greedies = new GreedyReceiver[](100);
        for (uint256 i; i < 100; ++i) {
            greedies[i] = new GreedyReceiver();
            vm.deal(address(greedies[i]), 1 ether);
            vm.prank(address(greedies[i]));
            ah.bid{value: 1 ether}(id);
        }

        // Sanity: 201 distinct bidiing parties; balance equals sum of escrows.
        assertEq(address(ah).balance, 200 ether + 200 ether); // leader + 200 losers

        // Settle (leader wins)..
        vm.warp(block.timestamp + 8 days);
        uint256 gasBefore = gasleft();
        ah.settle(id);
        uint256 gasSettle = gasBefore - gasleft();
        assertTrue(_settled(id));
        assertEq(ah.cumulative(id, leaderAddr), 0, "leader escrow consumed");

        // Build a 200-batch interleaving EOA + Greedy losers.
        address[] memory batch = new address[](200);
        for (uint256 i; i < 200; ++i) {
            batch[i] = (i % 2 == 0) ? eoas[i / 2] : address(greedies[(i - 1) / 2]);
        }

        // Run refundLosers; EIP-150 63/64-rule means a hostile per-call gas
        // forward CANNOT OOG the outer call thanks to the 50_000 cap — the
        // outer frame is left with enough gas to keep iterating.
        gasBefore = gasleft();
        ah.refundLosers(id, batch);
        uint256 gasRefund = gasBefore - gasleft();
        assertGt(gasRefund, 100_000, "refund loop actually ran");

        // EOA losers received refs directly.
        for (uint256 i; i < 100; ++i) {
            assertEq(eoas[i].balance, 1 ether, "EOA loser refund succeeded");
            assertEq(ah.cumulative(id, eoas[i]), 0, "EOA cumulative cleared");
            assertEq(ah.pendingReturns(eoas[i]), 0, "EOA has no pendingReturns");
        }
        // Greedy losers bounced into pendingReturns.
        for (uint256 i; i < 100; ++i) {
            assertEq(ah.cumulative(id, address(greedies[i])), 0, "greedy cumulative cleared");
            assertEq(ah.pendingReturns(address(greedies[i])), 1 ether, "greedy -> pendingReturns");
        }
        // Greedy bidder can later pull via withdrawRefund once un-blocked.
        greedies[0].setBlocked(false);
        uint256 balBefore = address(greedies[0]).balance;
        greedies[0].proxyWithdrawAuction(ah);
        assertEq(ah.pendingReturns(address(greedies[0])), 0, "withdrawRefund clears credit");
        assertEq(address(greedies[0]).balance, balBefore + 1 ether, "greedy pulled refund");
    }
}
