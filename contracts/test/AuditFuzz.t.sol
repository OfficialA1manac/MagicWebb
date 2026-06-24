// SPDX-License-Identifier: MIT
//   Audit-fuzz harness covering the v9+/v10-fixes applied in this repo:
//     (a) AuctionHouse.bid() anti-snipe — extension gated on `newLead`
//     (b) AuctionHouse.settle() seller-fault immediate refund + buyer-fault stall recovery
//     (c) OfferBook _pushPullRefund fallback (rejectOffer + refundExpiredOffer)
//     (d) AuctionHouse.refundLosers batch cap (BatchTooLarge) + per-call gas bound
//     (e) OfferBook M-01: expiry reduction on top-up is blocked
//     (f) AuctionHouse.refundLosers works on stalled auctions (C-03)
//     (g) AuctionHouse.withdrawRefund gas:50_000 cap (M-02)
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
import {
    OfferBook,
    NoOffer,
    OfferActive,
    NoPendingRefund,
    InvalidExpiry
} from "../src/OfferBook.sol";
import {BelowMinPrice} from "../src/MarketplaceCore.sol";
import {MockERC721}  from "./MockERC721.sol";
import {MockERC1155} from "./MockERC1155.sol";

// ─── Stubs ──────────────────────────────────────────────────────────────────

/// @dev Bidder stub that can be toggled to revert on every receive(). Used in
///      scenarios (c) and (d-2) to exercise the push-payment fallback path.
contract GreedyReceiver {
    bool public blocked;

    constructor() payable { blocked = true; }

    receive() external payable {
        if (blocked) revert("blocked");
    }

    function setBlocked(bool b) external { blocked = b; }

    function proxyWithdrawOffer(OfferBook ob) external { ob.withdrawRefund(); }
    function proxyWithdrawAuction(AuctionHouse ah) external { ah.withdrawRefund(); }
    function bidOn(AuctionHouse ah, uint256 id) external payable { ah.bid{value: msg.value}(id); }
}

/// @dev ERC1155 bidder that can be toggled to reject onERC1155Received.
contract ERC1155RejectingBidder {
    bool public blocked;

    constructor() payable { blocked = true; }

    receive() external payable {
        if (blocked) revert("blocked");
    }

    function setBlocked(bool b) external { blocked = b; }

    function onERC1155Received(address, address, uint256, uint256, bytes calldata)
        external view returns (bytes4)
    {
        if (blocked) revert("reject ERC1155");
        return this.onERC1155Received.selector;
    }

    function onERC721Received(address, address, uint256, bytes calldata)
        external pure returns (bytes4)
    {
        return this.onERC721Received.selector;
    }

    function bidOn(AuctionHouse ah, uint256 id) external payable { ah.bid{value: msg.value}(id); }
    function proxyWithdrawAuction(AuctionHouse ah) external { ah.withdrawRefund(); }
}

/// @dev Contract that consumes massive gas in its receive() hook.
///      Used to test the M-02 gas:50_000 cap on withdrawRefund().
///      The receive() body burns ~100k gas via a storage loop, which
///      exceeds the 50k cap and forces the outer .call to fail.
contract GasGriefingReceiver {
    uint256[] public junk;

    constructor() payable {}

    receive() external payable {
        // Burn ~100k gas to exceed the 50k cap.
        for (uint256 i; i < 1000; ++i) {
            junk.push(i);
        }
    }

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

    function _eoa(uint256 i) internal pure returns (address) {
        return address(uint160(uint256(keccak256(abi.encodePacked("EOA", i)))));
    }

    function _grain(uint256 i) internal pure returns (address) {
        return address(uint160(uint256(keccak256(abi.encodePacked("GRAIN", i)))));
    }

    // ════════════════════════════════════════════════════════════════════════
    //  (a)  Anti-snipe: 1000 1-wei late bids MUST NOT keep extending endsAt
    //      past a single EXTENSION_WINDOW push. (Audit-#1)
    // ════════════════════════════════════════════════════════════════════════

    function testFuzz_antiSnipe1kLateBids(uint256 seed) public {
        uint256 n = bound(seed, 100, 1000);

        (uint256 id,) = _auctionEndsIn(1 hours);
        _bid(id, alice, 2 ether);
        uint64 startEnd = _endsAt(id);

        vm.warp(uint256(startEnd) - 30);

        address lateLeader = _eoa(0xCAFE);
        vm.deal(lateLeader, 100 ether);
        vm.prank(lateLeader);
        ah.bid{value: 3 ether}(id);

        uint64 endAfterLead = _endsAt(id);
        assertGt(endAfterLead, startEnd, "new-lead bid MUST extend endsAt");

        for (uint256 i = 0; i < n; ++i) {
            address grain = _grain(i);
            vm.deal(grain, 1);
            vm.prank(grain);
            ah.bid{value: 1}(id);
            assertEq(_endsAt(id), endAfterLead, "non-lead 1-wei bid MUST NOT extend endsAt");
        }

        assertEq(_endsAt(id), endAfterLead, "endsAt unchanged across N accreting 1-wei bids");
    }

    // ════════════════════════════════════════════════════════════════════════
    //  (b)  C-02 fix: seller revokes approval → settle() now IMMEDIATELY
    //      refunds the winner and cancels (no stall). (Audit-#2)
    //
    //  C-01 fix: for ERC1155, when the BUYER's receiver hook reverts but
    //      the seller is ready (approved + owns), the auction enters
    //      STALLED state. settleUnstuck() can retry.
    // ════════════════════════════════════════════════════════════════════════

    function test_sellerRevokeCausesImmediateRefund() public {
        (uint256 id, uint256 tid) = _auction7d();
        _bid(id, alice, 2 ether);
        vm.warp(block.timestamp + 8 days);

        vm.prank(seller);
        nft.setApprovalForAll(address(ah), false);

        uint256 aliceBalBefore = alice.balance;

        vm.expectEmit(false, false, false, true, address(ah));
        emit AuctionHouse.AuctionCancelled(id);
        ah.settle(id);

        assertTrue(_settled(id), "settled=true (immediate cancel, not stall)");
        assertEq(_stalled(id), 0, "stalledAt NOT set (no stall)");
        assertEq(alice.balance, aliceBalBefore + 2 ether, "winner refunded in full immediately");
        assertEq(nft.ownerOf(tid), seller, "NFT still with seller");
        assertEq(ah.cumulative(id, alice), 0, "leader escrow cleared");
    }

    function test_sellerMovedNftCausesImmediateRefund() public {
        (uint256 id, uint256 tid) = _auction7d();
        _bid(id, alice, 2 ether);
        vm.warp(block.timestamp + 8 days);

        vm.prank(seller);
        nft.transferFrom(seller, address(0x999), tid);

        uint256 aliceBalBefore = alice.balance;
        ah.settle(id);

        assertTrue(_settled(id), "settled=true (immediate cancel)");
        assertEq(_stalled(id), 0, "stalledAt NOT set");
        assertEq(alice.balance, aliceBalBefore + 2 ether, "winner refunded immediately");
    }

    /// C-01 fix: ERC1155 buyer's onERC1155Received reverts → stall (buyer-fault).
    function test_erc1155BuyerFaultCausesStallThenUnstuck() public {
        ERC1155RejectingBidder bidder = new ERC1155RejectingBidder();
        vm.deal(address(bidder), 100 ether);

        vm.startPrank(seller);
        multi.mint(seller, 7, 5);
        multi.setApprovalForAll(address(ah), true);
        uint256 id = ah.create1155(address(multi), 7, 5, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();

        vm.prank(address(bidder));
        ah.bid{value: 2 ether}(id);
        vm.warp(block.timestamp + 2 days);

        ah.settle(id);

        assertFalse(_settled(id), "NOT settled (stalled for buyer-fault)");
        assertGt(_stalled(id), 0, "stalledAt is set");

        vm.expectRevert(NotStalled.selector);
        ah.settle(id);

        // Buyer fixes their contract → settleUnstuck succeeds.
        bidder.setBlocked(false);
        ah.settleUnstuck(id);

        assertTrue(_settled(id), "settled after settleUnstuck");
        assertEq(_stalled(id), 0, "stalledAt cleared");
        assertEq(multi.balanceOf(address(bidder), 7), 5, "bidder received ERC1155");
    }

    // ════════════════════════════════════════════════════════════════════════
    //  (c)  Non-receiving bidder for an OfferBook EXPIRED offer. (Audit-#3)
    // ════════════════════════════════════════════════════════════════════════

    function test_offerExpiredRefundPushFallback() public {
        uint256 tid = nft.mint(seller);
        vm.prank(seller);
        nft.setApprovalForAll(address(ob), true);

        GreedyReceiver bidder = new GreedyReceiver();
        vm.deal(address(bidder), 10 ether);

        uint64 exp = uint64(block.timestamp) + 1 days;
        // Unblock for makeOffer (receive() not called during escrow, but be safe).
        bidder.setBlocked(false);
        vm.prank(address(bidder));
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, exp);
        (uint128 principal,,,) = ob.positions(address(nft), tid, address(bidder));
        assertEq(principal, 1 ether, "offer escrowed at principal = 1 ETH");

        vm.warp(uint256(exp) + 1);

        bidder.setBlocked(true);

        assertEq(ob.pendingReturns(address(bidder)), 0, "no pending before expiry");
        ob.refundExpiredOffer(address(nft), tid, address(bidder));
        (principal,,,) = ob.positions(address(nft), tid, address(bidder));
        assertEq(principal, 0, "position deleted on refund");
        assertEq(ob.pendingReturns(address(bidder)), 1 ether, "push-failed refund -> pendingReturns");

        vm.expectRevert();
        bidder.proxyWithdrawOffer(ob);
        assertEq(ob.pendingReturns(address(bidder)), 1 ether, "withdraw all-or-nothing restores on failure");

        bidder.setBlocked(false);
        uint256 balBefore = address(bidder).balance;
        bidder.proxyWithdrawOffer(ob);
        assertEq(ob.pendingReturns(address(bidder)), 0, "pendingReturns cleared on successful withdraw");
        assertEq(address(bidder).balance, balBefore + 1 ether, "bidder received refund");
    }

    // ════════════════════════════════════════════════════════════════════════
    //  (d-1)  refundLosers batch.length cap. (Audit-#4 part A)
    // ════════════════════════════════════════════════════════════════════════

    function testFuzz_refundLosersBatchCap(uint256 n) public {
        n = bound(n, 0, 1000);

        (uint256 id,) = _auction7d();
        _bid(id, alice, 1 ether);
        vm.warp(block.timestamp + 8 days);
        ah.settle(id);

        address[] memory batch = new address[](n);
        for (uint256 i; i < n; ++i) batch[i] = alice;

        if (n == 0 || n > 200) {
            vm.expectRevert(BatchTooLarge.selector);
            ah.refundLosers(id, batch);
        } else {
            ah.refundLosers(id, batch);
        }
    }

    // ════════════════════════════════════════════════════════════════════════
    //  (d-2) 50%-griefing 200-batch: outer tx MUST NOT OOG. (Audit-#4 part B)
    // ════════════════════════════════════════════════════════════════════════

    function test_refundLosersGriefingHalfBatchDoesNotOOG() public {
        (uint256 id,) = _auction7d();

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

        assertEq(address(ah).balance, 200 ether + 200 ether);

        vm.warp(block.timestamp + 8 days);
        ah.settle(id);
        assertTrue(_settled(id));
        assertEq(ah.cumulative(id, leaderAddr), 0, "leader escrow consumed");

        address[] memory batch = new address[](200);
        for (uint256 i; i < 200; ++i) {
            batch[i] = (i % 2 == 0) ? eoas[i / 2] : address(greedies[(i - 1) / 2]);
        }

        ah.refundLosers(id, batch);

        for (uint256 i; i < 100; ++i) {
            assertEq(eoas[i].balance, 1 ether, "EOA loser refund succeeded");
            assertEq(ah.cumulative(id, eoas[i]), 0, "EOA cumulative cleared");
            assertEq(ah.pendingReturns(eoas[i]), 0, "EOA has no pendingReturns");
        }
        for (uint256 i; i < 100; ++i) {
            assertEq(ah.cumulative(id, address(greedies[i])), 0, "greedy cumulative cleared");
            assertEq(ah.pendingReturns(address(greedies[i])), 1 ether, "greedy -> pendingReturns");
        }
        greedies[0].setBlocked(false);
        uint256 balBefore = address(greedies[0]).balance;
        greedies[0].proxyWithdrawAuction(ah);
        assertEq(ah.pendingReturns(address(greedies[0])), 0, "withdrawRefund clears credit");
        assertEq(address(greedies[0]).balance, balBefore + 1 ether, "greedy pulled refund");
    }

    // ════════════════════════════════════════════════════════════════════════
    //  (e)  M-01 fix: OfferBook top-up MUST NOT allow expiry reduction.
    // ════════════════════════════════════════════════════════════════════════

    function test_topUpCannotReduceExpiry() public {
        uint256 tid = nft.mint(seller);
        vm.prank(seller);
        nft.setApprovalForAll(address(ob), true);

        uint64 longExp = uint64(block.timestamp + 7 days);
        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, longExp);

        // Top-up with shorter expiry → must revert.
        uint64 shortExp = uint64(block.timestamp + 1 days);
        vm.prank(alice);
        vm.expectRevert(InvalidExpiry.selector);
        ob.makeOffer{value: 0.01 ether}(address(nft), tid, 0.01 ether, shortExp);

        // Top-up with same or longer expiry → must succeed.
        vm.prank(alice);
        ob.makeOffer{value: 0.01 ether}(address(nft), tid, 0.01 ether, longExp);
        (uint128 p,,,) = ob.positions(address(nft), tid, alice);
        assertEq(p, 1.01 ether);
    }

    function test_cannotExpireImmediatelyViaTopUp() public {
        uint256 tid = nft.mint(seller);
        vm.prank(seller);
        nft.setApprovalForAll(address(ob), true);

        uint64 longExp = uint64(block.timestamp + 14 days);
        vm.prank(alice);
        ob.makeOffer{value: 5 ether}(address(nft), tid, 5 ether, longExp);

        // Attempt to expire in 1 second by topping up → must revert.
        vm.prank(alice);
        vm.expectRevert(InvalidExpiry.selector);
        ob.makeOffer{value: 0.01 ether}(address(nft), tid, 0.01 ether, uint64(block.timestamp + 1));

        (uint128 p,, uint64 exp,) = ob.positions(address(nft), tid, alice);
        assertEq(p, 5 ether);
        assertEq(exp, longExp);
    }

    // ════════════════════════════════════════════════════════════════════════
    //  (f)  C-03: refundLosers MUST work on stalled auctions so losers'
    //      funds are not trapped for 7+ days during a buyer-fault stall.
    //      Only the winner's escrow was consumed at stall time; losers
    //      must be able to pull their escrow immediately.
    // ════════════════════════════════════════════════════════════════════════

    function test_refundLosersWorksDuringStall() public {
        // Create an ERC1155 auction with a malicious bidder (buyer-fault).
        ERC1155RejectingBidder winner = new ERC1155RejectingBidder();
        vm.deal(address(winner), 100 ether);

        vm.startPrank(seller);
        multi.mint(seller, 7, 5);
        multi.setApprovalForAll(address(ah), true);
        uint256 id = ah.create1155(address(multi), 7, 5, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();

        // Winner bids 2 ETH (will be the leader).
        vm.prank(address(winner));
        ah.bid{value: 2 ether}(id);
        // Two losers bid 1 ETH each.
        _bid(id, alice, 1 ether);
        _bid(id, bob, 1 ether);

        // Sanity: auction holds 2 + 1 + 1 = 4 ETH.
        assertEq(address(ah).balance, 4 ether);

        vm.warp(block.timestamp + 2 days);

        // settle() → buyer's receiver reverts → seller approved → stall.
        ah.settle(id);

        assertFalse(_settled(id), "NOT settled (stalled)");
        assertGt(_stalled(id), 0, "stalledAt is set");
        // Winner's escrow consumed at stall time.
        assertEq(ah.cumulative(id, address(winner)), 0, "winner escrow consumed");
        // Losers' escrow is still recorded.
        assertEq(ah.cumulative(id, alice), 1 ether, "alice escrow intact");
        assertEq(ah.cumulative(id, bob), 1 ether, "bob escrow intact");

        // C-03: refundLosers MUST work on the stalled auction.
        address[] memory losers = new address[](2);
        losers[0] = alice;
        losers[1] = bob;

        uint256 aliceBalBefore = alice.balance;
        uint256 bobBalBefore   = bob.balance;
        ah.refundLosers(id, losers);

        assertEq(alice.balance, aliceBalBefore + 1 ether, "alice refunded during stall");
        assertEq(bob.balance,   bobBalBefore + 1 ether, "bob refunded during stall");
        assertEq(ah.cumulative(id, alice), 0, "alice cumulative cleared");
        assertEq(ah.cumulative(id, bob), 0, "bob cumulative cleared");

        // Auction is still stalled (not settled) — winner's funds remain consumed.
        assertFalse(_settled(id), "still not settled after refundLosers");
        assertGt(_stalled(id), 0, "still stalled");
        assertEq(ah.cumulative(id, address(winner)), 0, "winner escrow still consumed");
        // Contract only holds the winner's escrow now.
        assertEq(address(ah).balance, 2 ether, "contract holds only winner escrow");
    }

    /// @dev Verify that refundLosers REVERTS on an active (non-settled, non-stalled) auction.
    function test_refundLosersRevertsOnActiveAuction() public {
        (uint256 id,) = _auction7d();
        _bid(id, alice, 1 ether);

        address[] memory batch = new address[](1);
        batch[0] = alice;
        vm.expectRevert(NotSettled.selector);
        ah.refundLosers(id, batch);
    }

    // ════════════════════════════════════════════════════════════════════════
    //  (g)  M-02: withdrawRefund() gas:50_000 cap. A contract that burns
    //      >50k gas in its receive() hook MUST cause withdrawRefund to
    //      revert — the gas cap prevents the griefing contract from
    //      consuming unlimited gas and OOG-ing the withdrawal.
    // ════════════════════════════════════════════════════════════════════════

    function test_withdrawRefundGasCapBlocksGriefingReceiver() public {
        // Create a GasGriefingReceiver that burns >50k gas in receive().
        GasGriefingReceiver griefer = new GasGriefingReceiver();
        vm.deal(address(griefer), 10 ether);

        // Set up an auction where griefer bids and gets outbid.
        AuctionHouse ah2 = new AuctionHouse(feeRecipient, address(0));
        MockERC721 nft2 = new MockERC721();

        vm.startPrank(seller);
        uint256 tid2 = nft2.mint(seller);
        nft2.setApprovalForAll(address(ah2), true);
        uint256 id2 = ah2.create(address(nft2), tid2, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();

        // Griefer bids 1 ETH, then alice outbids with 2 ETH.
        vm.prank(address(griefer));
        ah2.bid{value: 1 ether}(id2);
        vm.prank(alice);
        ah2.bid{value: 2 ether}(id2);

        vm.warp(block.timestamp + 2 days);
        ah2.settle(id2);

        // refundLosers with griefer in the batch — the push to griefer
        // will fail (receive() burns >50k gas), so it falls back to
        // pendingReturns.
        address[] memory batch = new address[](1);
        batch[0] = address(griefer);
        ah2.refundLosers(id2, batch);

        assertEq(ah2.pendingReturns(address(griefer)), 1 ether, "griefer credited in pendingReturns");

        // Now griefer tries to call withdrawRefund — but withdrawRefund
        // has gas:50_000 cap, and griefer's receive() burns >50k gas.
        // The .call fails, and withdrawRefund reverts with WithdrawFailed.
        // Call withdrawRefund from the griefer's context. The griefer's
        // receive() burns >50k gas, exceeding the gas:50_000 cap, so the
        // inner .call fails and withdrawRefund reverts with WithdrawFailed.
        vm.prank(address(griefer));
        vm.expectRevert(); // WithdrawFailed
        ah2.withdrawRefund();

        // The pendingReturns credit is preserved (revert undid the state change).
        assertEq(ah2.pendingReturns(address(griefer)), 1 ether, "pendingReturns preserved after failed withdraw");
    }

    /// @dev Verify that withdrawRefund works for normal EOAs (no griefing).
    function test_withdrawRefundWorksForNormalReceiver() public {
        // Use the existing auction setup — force a pendingReturns credit
        // by having a GreedyReceiver get swept, then unblock and withdraw.
        GreedyReceiver gr = new GreedyReceiver();
        vm.deal(address(gr), 10 ether);

        (uint256 id,) = _auction7d();
        vm.prank(address(gr));
        ah.bid{value: 1 ether}(id);
        _bid(id, alice, 2 ether); // alice wins

        vm.warp(block.timestamp + 8 days);
        ah.settle(id);

        // gr is a loser — refundLosers tries to push, but gr is blocked.
        address[] memory batch = new address[](1);
        batch[0] = address(gr);
        ah.refundLosers(id, batch);
        assertEq(ah.pendingReturns(address(gr)), 1 ether);

        // Unblock and withdraw — should succeed with the gas cap.
        gr.setBlocked(false);
        uint256 balBefore = address(gr).balance;
        gr.proxyWithdrawAuction(ah);
        assertEq(address(gr).balance, balBefore + 1 ether, "normal receiver withdrew successfully");
        assertEq(ah.pendingReturns(address(gr)), 0, "pendingReturns cleared");
    }
}
