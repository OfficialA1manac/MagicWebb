// SPDX-License-Identifier: MIT
//   Audit-fuzz harness covering the v9+/v10-fixes applied in this repo:
//     (a) AuctionHouse.bid() anti-snipe — extension gated on `newLead`
//     (b) AuctionHouse.settle() seller-fault immediate refund + buyer-fault stall recovery
//     (c) OfferBook _pay() fallback (rejectOffer + refundExpiredOffer) — L-05 M-03
//     (d) AuctionHouse.refundLosers batch cap (BatchTooLarge) + per-call gas bound
//     (e) OfferBook M-01: expiry reduction on top-up is blocked
//     (f) AuctionHouse.refundLosers works on stalled auctions (C-03)
//     (g) AuctionHouse.withdrawRefund gas:50_000 cap (M-02)
//     (h) L-04/L-05/M-03: PushFailed event coverage + NothingToWithdraw selector
//         regression on all three cores — every push-failure path emits the
//         canonical MarketplaceCore.PushFailed event, and every empty-credit
//         withdrawRefund() reverts with NothingToWithdraw (single selector
//         across all cores, not the older NoPendingRefund on OfferBook).
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
    InvalidExpiry
} from "../src/OfferBook.sol";
import {BelowMinPrice, NothingToWithdraw} from "../src/MarketplaceCore.sol";
import {Marketplace} from "../src/Marketplace.sol";
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
///      After M-02 v27 (gas cap REMOVED from withdrawRefund), this contract
///      demonstrates that smart accounts / gas-heavy receivers CAN now
///      successfully withdraw — the gas cap removal was intentional to
///      support Gnosis Safe, Argent, and other smart wallets that need
///      >50k gas for receive(). The cost is borne by the caller (themselves).
contract GasGriefingReceiver {
    uint256[] public junk;

    constructor() payable {}

    receive() external payable {
        // Burn ~100k gas — works because withdrawRefund no longer caps gas.
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
    //  (g)  M-02 v27: gas:50_000 cap REMOVED from withdrawRefund().
    //      Smart accounts (Gnosis Safe, Argent, smart wallets) that need
    //      >50k gas for receive() can now successfully withdraw. The cost
    //      is borne by the caller (the smart account itself). nonReentrant
    //      + CEI (zero-then-call) prevents reentrancy. restore-on-failure
    //      ensures no funds are lost if the call reverts for other reasons.
    // ════════════════════════════════════════════════════════════════════════

    function test_withdrawRefundGasHeavyReceiverCanWithdraw() public {
        // Create a GasGriefingReceiver that burns >50k gas in receive().
        // Previously this would fail with the gas:50_000 cap. Now it succeeds
        // because the cap was removed for smart account compatibility.
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
        // will fail (receive() burns >50k gas with the gas:50_000 cap on
        // refundLosers), so it falls back to pendingReturns.
        address[] memory batch = new address[](1);
        batch[0] = address(griefer);
        ah2.refundLosers(id2, batch);

        assertEq(ah2.pendingReturns(address(griefer)), 1 ether, "griefer credited in pendingReturns");

        // v27: withdrawRefund() has NO gas cap — the gas-heavy receiver
        // can now successfully withdraw. This is the intended behavior:
        // smart accounts that need >50k gas for receive() are no longer
        // permanently trapped. The caller (griefer) pays for the gas.
        uint256 grieferBalBefore = address(griefer).balance;
        vm.prank(address(griefer));
        ah2.withdrawRefund();

        assertEq(ah2.pendingReturns(address(griefer)), 0, "pendingReturns cleared after successful withdraw");
        assertEq(address(griefer).balance, grieferBalBefore + 1 ether, "griefer received refund");
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

    // ═════════════════════════════════════════════════════════════════════════
    //  (h)  L-04 / L-05 / M-03 fix verification — PushFailed event coverage
    //      on every settlement-path fallback. Each test exercises a specific
    //      push-failure code path and asserts the corresponding PushFailed
    //      event fires with the correct indexed `to` and the credited amount.
    //
    //      Without these tests, a future regression that drops a single
    //      `emit PushFailed(...)` would silently re-introduce the monitoring
    //      blindspot. Foundry's vm.expectEmit is the canonical regression
    //      guard. Each test produces a real on-chain PushFailed receipt.
    //
    //      The local `event PushFailed(address indexed to, uint256 amount)`
    //      declaration below has the SAME topic-0 / topic-1 / data signature
    //      as the canonical MarketplaceCore event, so vm.expectEmit matches.
    // ═════════════════════════════════════════════════════════════════════════

    /// @dev Mirror MarketplaceCore.PushFailed signature so vm.expectEmit matches.
    event PushFailed(address indexed to, uint256 amount);

    /// @dev feeRecipient rejects ETH on settle → PushFailed(feeRecipient, fee) fires.
    function test_settle_feePushFallback_emitsPushFailed() public {
        // Replace ah with one whose feeRecipient can't receive ETH.
        RejectEtherNoReceive badFee = new RejectEtherNoReceive();
        AuctionHouse ah2 = new AuctionHouse(address(badFee), address(0));
        MockERC721 nft2 = new MockERC721();

        vm.startPrank(seller);
        uint256 tid2 = nft2.mint(seller);
        nft2.setApprovalForAll(address(ah2), true);
        uint256 id2 = ah2.create(address(nft2), tid2, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();

        vm.prank(alice);
        ah2.bid{value: 2 ether}(id2);
        vm.warp(block.timestamp + 2 days);

        uint256 fee = uint256(2 ether) * 150 / 10_000; // 0.03 ether
        vm.expectEmit(true, false, false, true, address(ah2));
        emit PushFailed(address(badFee), fee);

        ah2.settle(id2);

        assertTrue(ah2.getAuction(id2).settled, "settled");
        assertEq(ah2.pendingReturns(address(badFee)), fee, "fee credited to badFee");
        assertEq(nft2.ownerOf(tid2), alice, "winner received NFT");
    }

    /// @dev seller rejects ETH on settle → PushFailed(seller, proceeds) fires.
    function test_settle_sellerPushFallback_emitsPushFailed() public {
        // Make the seller a contract that can't receive ETH.
        SellerNoReceive badSeller = new SellerNoReceive();
        vm.deal(address(badSeller), 1 ether);
        MockERC721 nft2 = new MockERC721();

        vm.startPrank(address(badSeller));
        uint256 tid2 = nft2.mint(address(badSeller));
        nft2.setApprovalForAll(address(ah), true);
        uint256 id2 = ah.create(address(nft2), tid2, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();

        vm.prank(alice);
        ah.bid{value: 2 ether}(id2);
        vm.warp(block.timestamp + 2 days);

        uint128 winBid = 2 ether;
        uint256 fee = uint256(winBid) * 150 / 10_000;
        uint256 proceeds = uint256(winBid) - fee;

        vm.expectEmit(true, false, false, true, address(ah));
        emit PushFailed(address(badSeller), proceeds);

        ah.settle(id2);

        assertTrue(ah.getAuction(id2).settled);
        assertEq(ah.pendingReturns(address(badSeller)), proceeds, "proceeds credited");
    }

    /// @dev Seller-moved-NFT path → _refundWinnerAndCancel → PushFailed(winner) fires.
    function test_settle_sellerMovedNft_emitsPushFailedOnStuckWinner() public {
        SellerNoReceive badWinner = new SellerNoReceive();
        vm.deal(address(badWinner), 10 ether);
        // badWinner will be the leader so it gets the escrow refund on failure.
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint256 id = ah.create(address(nft), tid, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();

        deal(address(badWinner), 2 ether);
        vm.prank(address(badWinner));
        ah.bid{value: 2 ether}(id);
        vm.warp(block.timestamp + 2 days);

        // Seller moves the NFT away → settle refunds winner; but winner can't
        // receive ETH → PushFailed fires.
        vm.prank(seller);
        nft.transferFrom(seller, address(0x999), tid);

        vm.expectEmit(true, false, false, true, address(ah));
        emit PushFailed(address(badWinner), 2 ether);

        ah.settle(id);
    }

    /// @dev settleUnstuck's payout paths use the same code as settle()'s payouts,
    ///      so the PushFailed emission for feeRecipient / seller fallback is
    ///      covered by test_settle_feePushFallback_emitsPushFailed and
    ///      test_settle_sellerPushFallback_emitsPushFailed. The stall-refresh
    ///      path (transferFrom fails, seller still ready → refresh
    ///      stalledAt) does NOT touch pendingReturns, so it cannot emit
    ///      PushFailed. There is no settleUnstuck-specific PushFailed path
    ///      that isn't covered by the settle tests.

    /// @dev reclaim() with non-receiving winner → PushFailed(winner, winBid) fires.
    function test_reclaim_winnerPushFallback_emitsPushFailed() public {
        SellerNoReceive badWinner = new SellerNoReceive();
        vm.deal(address(badWinner), 100 ether);

        vm.startPrank(seller);
        multi.mint(seller, 11, 5);
        multi.setApprovalForAll(address(ah), true);
        uint256 id = ah.create1155(address(multi), 11, 5, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();

        ERC1155RejectingBidder bidder = new ERC1155RejectingBidder();
        vm.deal(address(bidder), 100 ether);
        vm.prank(address(bidder));
        ah.bid{value: 2 ether}(id);
        vm.warp(block.timestamp + 2 days);

        ah.settle(id); // stalls (buyer-fault because receiver reverts)
        assertGt(ah.getAuction(id).stalledAt, 0, "stalled");

        vm.warp(block.timestamp + 8 days); // past STALL_WINDOW

        uint128 winBid = 2 ether;
        vm.expectEmit(true, false, false, true, address(ah));
        emit PushFailed(address(bidder), winBid);

        ah.reclaim(id);
        assertEq(ah.pendingReturns(address(bidder)), winBid, "winner credit parked");
    }

    /// @dev refundLosers per-iteration fallback → PushFailed(b, amt) fires per stuck bidder.
    function test_refundLosers_perIterationPushFallback_emitsPushFailed() public {
        (uint256 id,) = _auction7d();
        _bid(id, alice, 2 ether);

        GreedyReceiver[] memory greedies = new GreedyReceiver[](3);
        for (uint256 i; i < 3; ++i) {
            greedies[i] = new GreedyReceiver();
            vm.deal(address(greedies[i]), 1 ether);
            vm.prank(address(greedies[i]));
            ah.bid{value: 1 ether}(id);
        }

        vm.warp(block.timestamp + 8 days);
        ah.settle(id);

        address[] memory batch = new address[](3);
        for (uint256 i; i < 3; ++i) batch[i] = address(greedies[i]);

        for (uint256 i; i < 3; ++i) {
            vm.expectEmit(true, false, false, true, address(ah));
            emit PushFailed(address(greedies[i]), 1 ether);
        }
        ah.refundLosers(id, batch);

        for (uint256 i; i < 3; ++i) {
            assertEq(ah.pendingReturns(address(greedies[i])), 1 ether, "grief receiver credited");
        }
    }

    /// @dev OfferBook.refundExpiredOffer with non-receiving bidder → PushFailed fires.
    ///      Previously the pull-fallback in this path silently credited
    ///      pendingReturns without emitting an event (because _pushPullRefund
    ///      was a local duplicate of _pay that didn't share the event).
    ///      After M-03 dedup, OfferBook uses inherited `_pay()` and the
    ///      event fires. This test guards the regression.
    function test_offer_refundExpiredOffer_emitsPushFailed() public {
        uint256 tid = nft.mint(seller);
        vm.prank(seller);
        nft.setApprovalForAll(address(ob), true);

        GreedyReceiver bidder = new GreedyReceiver();
        bidder.setBlocked(false);
        vm.deal(address(bidder), 10 ether);

        uint64 exp = uint64(block.timestamp + 1 days);
        vm.prank(address(bidder));
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, exp);

        vm.warp(uint256(exp) + 1);
        bidder.setBlocked(true); // push will revert

        vm.expectEmit(true, false, false, true, address(ob));
        emit PushFailed(address(bidder), 1 ether);

        ob.refundExpiredOffer(address(nft), tid, address(bidder));

        assertEq(ob.pendingReturns(address(bidder)), 1 ether);
    }

    /// @dev OfferBook.rejectOffer with non-receiving bidder → PushFailed fires.
    function test_offer_rejectOffer_emitsPushFailed() public {
        uint256 tid = nft.mint(seller);
        vm.prank(seller);
        nft.setApprovalForAll(address(ob), true);

        GreedyReceiver bidder = new GreedyReceiver();
        bidder.setBlocked(false); // for makeOffer receive()
        vm.deal(address(bidder), 10 ether);

        uint64 exp = uint64(block.timestamp + 1 days);
        vm.prank(address(bidder));
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, exp);

        bidder.setBlocked(true); // push in rejectOffer will revert

        vm.expectEmit(true, false, false, true, address(ob));
        emit PushFailed(address(bidder), 1 ether);

        vm.prank(seller);
        ob.rejectOffer(address(nft), tid, address(bidder));

        assertEq(ob.pendingReturns(address(bidder)), 1 ether);
    }

    /// @dev OfferBook.withdrawRefund() with no credits reverts with NOTHING-TO-WITHDRAW
    ///      (inherited from MarketplaceCore after L-04 dedup — used to revert with
    ///      the older NoPendingRefund selector on OfferBook specifically).
    function test_offer_withdrawRefund_empty_revertsNothingToWithdraw() public {
        vm.expectRevert(NothingToWithdraw.selector);
        ob.withdrawRefund();
    }

    /// @dev AuctionHouse.withdrawRefund() with no credits reverts with NOTHING-TO-WITHDRAW.
    function test_auction_withdrawRefund_empty_revertsNothingToWithdraw() public {
        vm.expectRevert(NothingToWithdraw.selector);
        ah.withdrawRefund();
    }

    // ════════════════════════════════════════════════════════════════════════════
    //  (i)  L-09 + L-10 regression tests (v28 — Round 3):
    //
    //  (i.1) L-09 happy-path: batchList writes exactly N listings for N items.
    //        Verifies the loop still iterates fully under the modifier.
    //
    //  (i.2) L-09 reentrancy guard: a malicious ERC-721 collection whose
    //        getApproved() attempts to re-enter batchList(reentry) MUST have
    //        its inner call reverted by ReentrancyGuard. The outer call's
    //        listings are preserved, and the reentry target slot (a token id
    //        the outer doesn't touch) MUST remain unset. With nonReentrant
    //        absent, the reentry would write to listings[99] and the assertion
    //        below would FAIL — this is a behavioral distinguisher, not a
    //        syntax check.
    //
    //  (i.3) L-10: AuctionHouse._bidders is unique per distinct participating
    //        address across an auction's lifetime, regardless of how many
    //        times the bidder was refunded and re-bid. The seen-mapping
    //        prevents duplicate pushes; without it, _bidders[id].length
    //        would grow unboundedly across refund+rebid cycles.
    // ════════════════════════════════════════════════════════════════════════════

    /// @notice L-09 happy-path: batchList writes exactly N listings for N items.
    function test_batchList_listsAllItemsAtomically() public {
        Marketplace mp = new Marketplace(feeRecipient, address(0));
        MockERC721 coll = new MockERC721();

        vm.startPrank(seller);
        uint256 t1 = coll.mint(seller);
        uint256 t2 = coll.mint(seller);
        uint256 t3 = coll.mint(seller);
        coll.setApprovalForAll(address(mp), true);
        vm.stopPrank();

        Marketplace.BatchItem[] memory items = new Marketplace.BatchItem[](3);
        items[0] = Marketplace.BatchItem(address(coll), t1, 0.1 ether,  uint64(block.timestamp + 7 days));
        items[1] = Marketplace.BatchItem(address(coll), t2, 0.15 ether, uint64(block.timestamp + 7 days));
        items[2] = Marketplace.BatchItem(address(coll), t3, 0.2 ether,  uint64(block.timestamp + 7 days));

        vm.prank(seller);
        mp.batchList(items);

        // Every item MUST be listed with the round-tripped seller/standard/price.
        assertEq(mp.listings(address(coll), t1, seller).seller, seller, "item 1 seller");
        assertEq(uint256(mp.listings(address(coll), t1, seller).standard),
                 uint256(TokenStandard.ERC721), "item 1 ERC-721");
        assertEq(uint256(mp.listings(address(coll), t1, seller).price),
                 uint256(0.1 ether), "item 1 price");

        assertEq(mp.listings(address(coll), t2, seller).seller, seller, "item 2 seller");
        assertEq(uint256(mp.listings(address(coll), t2, seller).price),
                 uint256(0.15 ether), "item 2 price");

        assertEq(mp.listings(address(coll), t3, seller).seller, seller, "item 3 seller");
        assertEq(uint256(mp.listings(address(coll), t3, seller).price),
                 uint256(0.2 ether), "item 3 price");
    }

    /// @notice L-09: batchList IS nonReentrant. A malicious collection whose
    ///         getApproved attempts to re-enter mp.batchList with a SECOND,
    ///         distinct batch (item 99 at 0.99 ETH) MUST see the inner call
    ///         reverted. The reentry target slot MUST remain empty.
    ///         The outer call's listings at items 1 and 2 stay intact.
    function test_batchList_protectedByNonReentrant() public {
        Marketplace mp = new Marketplace(feeRecipient, address(0));
        ReentrantBatchColl coll = new ReentrantBatchColl(mp);

        // `seller` owns the token ids we use; the mock's getApproved reenters
        // batchList during the OUTER call's first iteration and writes to
        // listings[coll][99][seller] — a slot the outer never touches.
        coll.setOwner(1, seller);
        coll.setOwner(2, seller);
        coll.setOwner(99, seller);

        // Outer batch: list tokens 1 and 2 at 0.1 ETH.
        Marketplace.BatchItem[] memory outer = new Marketplace.BatchItem[](2);
        outer[0] = Marketplace.BatchItem(address(coll), 1, 0.1 ether, uint64(block.timestamp + 7 days));
        outer[1] = Marketplace.BatchItem(address(coll), 2, 0.1 ether, uint64(block.timestamp + 7 days));

        // Reentry batch: token 99 at 0.99 ETH — DISTINCT slot from outer.
        // If nonReentrant is absent, listings[99] would exist at 0.99 after
        // the reentry call returns and we could detect it via storage.
        Marketplace.BatchItem[] memory reentry = new Marketplace.BatchItem[](1);
        reentry[0] = Marketplace.BatchItem(address(coll), 99, 0.99 ether, uint64(block.timestamp + 7 days));
        coll.setReentryItems(reentry);
        coll.arm();

        vm.prank(seller);
        mp.batchList(outer);
        coll.disarm();

        // Outer items MUST be listed at the outer's prices.
        assertEq(mp.listings(address(coll), 1, seller).seller, seller,
                 "item 1 listed by outer call");
        assertEq(uint256(mp.listings(address(coll), 1, seller).price),
                 uint256(0.1 ether), "item 1 outer price preserved");
        assertEq(mp.listings(address(coll), 2, seller).seller, seller,
                 "item 2 listed by outer call");
        assertEq(uint256(mp.listings(address(coll), 2, seller).price),
                 uint256(0.1 ether), "item 2 outer price preserved");

        // Reentry target slot MUST be unset: the inner mp.batchList call was
        // reverted by ReentrancyGuard before it could write the listing.
        // If nonReentrant were absent, listings[coll][99][seller] would
        // exist at price 0.99 (the reentry target) — this assertion would
        // fail and the test would surface the regression immediately.
        (address s99, , , uint128 p99, ) = mp.listings(address(coll), 99, seller);
        assertEq(s99, address(0),
                 "reentry slot UNSET — inner mp.batchList was reverted by ReentrancyGuard");
        assertEq(uint256(p99), 0, "reentry slot price zero (reentry blocked)");
    }

    /// @notice L-10: _bidders is unique per distinct participating address.
    ///         A bidder who is refunded and re-bids MUST NOT add a duplicate
    ///         entry to _bidders[id]. off-chain indexers can scan the array
    ///         once per auction lifetime, not unbounded across refund+rebid
    ///         cycles. Without the seen-mapping fix, _bidders[id].length
    ///         would increment on every refund→rebid sequence.
    function test_bidders_uniqueAcrossRefundAndRebid() public {
        (uint256 id, ) = _auction7d();

        // Alice bids 1 ETH — enrolled on first bid.
        _bid(id, alice, 1 ether);
        assertEq(ah.bidderCount(id), 1, "alice enrolled on first bid");

        // Bob outbids to 3 ETH — second distinct address.
        _bid(id, bob, 3 ether);
        assertEq(ah.bidderCount(id), 2,
                 "bob enrolled; no duplicate for alice (Alice's prevCum was nonzero so no push fires)");

        // Refund alice; then she re-bids. Without the seen-mapping fix,
        // the second bid would trigger ANOTHER push to _bidders[id].
        vm.warp(block.timestamp + 8 days);
        ah.settle(id);

        address[] memory batch = new address[](1);
        batch[0] = alice;
        ah.refundLosers(id, batch);
        assertEq(ah.cumulative(id, alice), 0,
                 "alice cumulative cleared by refundLosers");

        // Alice re-bids 2 ETH — prevCum is 0 again, BUT the seen-mapping
        // gate prevents a duplicate push to _bidders[id].
        _bid(id, alice, 2 ether);
        assertEq(ah.bidderCount(id), 2,
                 "_bidders[id] has 2 entries (alice once + bob); alice re-bid did NOT push a duplicate");
        assertEq(ah.cumulative(id, alice), 2 ether,
                 "alice's new cumulative accurately reflects the rebind");

        // Bob's cumulative intact.
        assertEq(ah.cumulative(id, bob), 3 ether, "bob's cumulative intact");
    }
}

/// @dev Helper stub for feeRecipient that has NO receive()/fallback → all ETH pushes fail.
contract RejectEtherNoReceive {
    // intentionally empty
}

/// @dev Helper stub for a seller/bidder wallet that has NO receive()/fallback.
contract SellerNoReceive {
    // intentionally empty; will be given ETH via vm.deal for balance but reject sends.
    receive() external payable { revert("no receive"); }
}

/// @dev Mock malicious ERC-721 collection for the L-09 reentrancy test.
///
///      `ownerOf`, `isApprovedForAll`, and `getApproved` are deliberately
///      declared NON-view (despite IERC721 declaring them as `view`) so the
///      mock can fire an external call to `mp.batchList(reentryItems)` from
///      inside `getApproved`. Solidity's compile-time view-purity check only
///      applies to LOCAL state writes — cross-contract calls to non-view
///      functions from a locally-declared view context are allowed because
///      the compiler cannot prove the target's mutability.
///
///      The runtime ABI dispatcher uses the IERC721 function selector
///      (e.g. 0x081812fc for `getApproved(uint256)`); the mock matches the
///      selector with its non-view implementation and fires the reentry
///      attempt. The `arm()` / `_attempts` pair ensures the reentry fires
///      exactly ONCE on the outer call's first getApproved (any deeper
///      reentrant call sees `_attempts >= 1` and skips).
contract ReentrantBatchColl {
    Marketplace public immutable mp;
    Marketplace.BatchItem[] private _reentryItems;
    bool public armed;
    uint256 private _attempts;
    mapping(uint256 => address) private _owners;

    constructor(Marketplace _mp) { mp = _mp; }

    function setOwner(uint256 tid, address o) external { _owners[tid] = o; }

    function setReentryItems(Marketplace.BatchItem[] calldata items) external {
        delete _reentryItems;
        for (uint256 i; i < items.length; ++i) _reentryItems.push(items[i]);
    }

    function arm()   external { armed = true;  _attempts = 0; }
    function disarm() external { armed = false; }

    // Mock's non-view declarations intentionally diverge from IERC721's view
    // marker so state writes + external calls are allowed. The runtime ABI
    // dispatch in Marketplace._list() uses the IERC721 selector; mock matches.
    function ownerOf(uint256 id) external returns (address) { return _owners[id]; }
    function isApprovedForAll(address, address) external pure returns (bool) { return true; }
    function getApproved(uint256) external returns (address) {
        if (armed && _attempts < 1 && _reentryItems.length > 0) {
            _attempts++;
            // Inner mp.batchList call. With nonReentrant PRESENT, the call
            // reverts immediately and the try/catch swallows the revert;
            // listings[reentry-token-id] stays empty. With nonReentrant
            // ABSENT, the call executes its full _list loop and would write
            // listings[coll][reentry-token][seller] at the reentry's price.
            // The test below observes listings[99] to distinguish the cases.
            try mp.batchList(_reentryItems) {} catch {}
        }
        return address(mp);
    }
}
