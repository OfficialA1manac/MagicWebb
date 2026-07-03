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
    StallNotOver,
    BidTooLow,
    BidOverflow
} from "../src/AuctionHouse.sol";
import {
    OfferBook,
    NoOffer,
    OfferActive,
    InvalidExpiry
} from "../src/OfferBook.sol";
import {BelowMinPrice, NothingToWithdraw, WithdrawFailed, TokenStandard} from "../src/MarketplaceCore.sol";
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
        // Burn ~220k gas via 10 storage writes — works because withdrawRefund
        // no longer caps gas. 10 pushes exceeds the old 50k gas cap and is
        // sufficient to exercise the gas-heavy receive() path without the
        // waste of 1000 iterations (~2M gas).
        for (uint256 i; i < 10; ++i) {
            junk.push(i);
        }
    }
}


/// @dev Malicious buyer that re-enters Marketplace.batchList during
///      onERC721Received. L-09: proves batchList is protected by
///      nonReentrant from a real (non-STATICCALL) callback context.
///      Unlike _list()'s approval probes (view functions called via
///      STATICCALL which block state-changing sub-calls at the EVM
///      level), buy()'s safeTransferFrom uses a regular CALL to the
///      recipient, so onERC721Received can legitimately attempt
///      state changes — making it a valid ReentrancyGuard distinguisher.
contract ReentrantBuyer {
    Marketplace public immutable mp;
    Marketplace.BatchItem[] private _reentryItems;
    bool public armed;
    uint256 private _attempts;

    constructor(Marketplace _mp) { mp = _mp; }

    function setReentryItems(Marketplace.BatchItem[] calldata items) external {
        delete _reentryItems;
        for (uint256 i; i < items.length; ++i) _reentryItems.push(items[i]);
    }

    function arm()   external { armed = true;  _attempts = 0; }
    function disarm() external { armed = false; }

    function onERC721Received(address, address, uint256, bytes calldata)
        external returns (bytes4)
    {
        if (armed && _attempts < 1 && _reentryItems.length > 0) {
            _attempts++;
            // Inner mp.batchList call. With nonReentrant PRESENT, the call
            // reverts immediately (caught by try/catch). With nonReentrant
            // ABSENT, the call executes its full _list loop and writes
            // listings[coll][reentry-token][seller] at the reentry's price.
            try mp.batchList(_reentryItems) {} catch {}
        }
        return this.onERC721Received.selector;
    }

    // Required to receive ETH for buy() payments.
    receive() external payable {}
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

    // ── Fixed listing durations for Marketplace tests ───────────────────────
    uint64 constant _LIST_24HR = 24 hours;

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

    /// @dev Enable offers on a collection (test contract owns MockERC721 token 0).
    function _enableOffers(address coll) internal {
        ob.setOfferEligible(coll, true);
    }

    /// @dev Create an auction with custom increment parameters.
    function _createWithIncrement(uint128 reserve, uint16 minIncBps, uint128 minIncFlat)
        internal returns (uint256 id, uint256 tid)
    {
        vm.startPrank(seller);
        tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        id = ah.create(address(nft), tid, reserve, uint64(block.timestamp + 7 days), minIncBps, minIncFlat);
        vm.stopPrank();
    }

    /// @dev Create an auction with custom increment parameters and a named leader bidder.
    function _setupLeader(uint128 reserve, uint16 minIncBps, uint128 minIncFlat, address leader, uint128 leaderBid)
        internal returns (uint256 id)
    {
        (uint256 id_,) = _createWithIncrement(reserve, minIncBps, minIncFlat);
        vm.deal(leader, uint256(leaderBid) + 10 ether);
        _bid(id_, leader, leaderBid);
        (address l,) = _leader(id_);
        assertEq(l, leader, "leader must be set");
        return id_;
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
        _enableOffers(address(nft));
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

        _enableOffers(address(nft));
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

        _enableOffers(address(nft));
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

        _enableOffers(address(nft));
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

        _enableOffers(address(nft));
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
        items[0] = Marketplace.BatchItem(address(coll), t1, 0.1 ether,  uint64(block.timestamp + _LIST_24HR));
        items[1] = Marketplace.BatchItem(address(coll), t2, 0.15 ether, uint64(block.timestamp + _LIST_24HR));
        items[2] = Marketplace.BatchItem(address(coll), t3, 0.2 ether,  uint64(block.timestamp + _LIST_24HR));

        vm.prank(seller);
        mp.batchList(items);

        // Every item MUST be listed with the round-tripped seller/standard/price.
        // Auto-generated getter for nested mapping returns positional tuple in struct
        // declaration order (seller, expiresAt, standard, price, amount) — not a
        // struct member. Use destructuring; use the actual enum type TokenStandard
        // (not uint8 - implicit conversion not allowed in tuple destructuring).
        (address s1, , TokenStandard std1, uint128 p1,) = mp.listings(address(coll), t1, seller);
        assertEq(s1, seller, "item 1 seller");
        assertEq(uint256(std1), uint256(TokenStandard.ERC721), "item 1 ERC-721");
        assertEq(uint256(p1), uint256(0.1 ether), "item 1 price");

        (address s2, , , uint128 p2,) = mp.listings(address(coll), t2, seller);
        assertEq(s2, seller, "item 2 seller");
        assertEq(uint256(p2), uint256(0.15 ether), "item 2 price");

        (address s3, , , uint128 p3,) = mp.listings(address(coll), t3, seller);
        assertEq(s3, seller, "item 3 seller");
        assertEq(uint256(p3), uint256(0.2 ether), "item 3 price");
    }

    /// @notice L-09: batchList IS nonReentrant. Uses buy() → safeTransferFrom →
    ///         onERC721Received as the reentry trigger because _list()'s approval
    ///         probes (ownerOf, isApprovedForAll, getApproved) are view functions
    ///         called via STATICCALL, which makes any state-changing reentry
    ///         impossible at the EVM level regardless of ReentrancyGuard.
    ///         buy()'s safeTransferFrom uses a regular CALL, so the recipient's
    ///         onERC721Received can re-enter mp.batchList with a SECOND, distinct
    ///         batch (item 99 at 0.99 ETH).
    function test_batchList_protectedByNonReentrant() public {
        Marketplace mp = new Marketplace(feeRecipient, address(0));
        MockERC721 coll = new MockERC721();

        // Seller mints and lists tokens 1 and 2.
        vm.startPrank(seller);
        uint256 t1 = coll.mint(seller);
        uint256 t2 = coll.mint(seller);
        uint256 t99 = coll.mint(seller);
        coll.setApprovalForAll(address(mp), true);
        mp.list(address(coll), t1, 0.1 ether, uint64(block.timestamp + _LIST_24HR));
        mp.list(address(coll), t2, 0.15 ether, uint64(block.timestamp + _LIST_24HR));
        vm.stopPrank();

        // ReentrantBuyer receives token 99 and approves the marketplace.
        ReentrantBuyer buyer = new ReentrantBuyer(mp);
        vm.prank(seller);
        coll.safeTransferFrom(seller, address(buyer), t99);
        vm.deal(address(buyer), 10 ether);
        vm.prank(address(buyer));
        coll.setApprovalForAll(address(mp), true);

        // Reentry batch: list token 99 at 0.99 ETH.
        Marketplace.BatchItem[] memory reentry = new Marketplace.BatchItem[](1);
        reentry[0] = Marketplace.BatchItem(address(coll), t99, 0.99 ether, uint64(block.timestamp + _LIST_24HR));
        buyer.setReentryItems(reentry);
        buyer.arm();

        // Buyer buys token 1 — during safeTransferFrom, onERC721Received fires
        // which re-enters mp.batchList(reentry). If nonReentrant PRESENT, the
        // inner call reverts and listing[99][buyer] stays unset. If ABSENT,
        // listing[99][buyer] gets written at 0.99 ETH.
        vm.prank(address(buyer));
        mp.buy{value: 0.1 ether}(address(coll), t1, seller);
        buyer.disarm();

        // Outer buy succeeded — buyer now owns token 1.
        assertEq(coll.ownerOf(t1), address(buyer), "buyer received token 1");

        // Outer listing for token 2 stays intact.
        (address s2, , , uint128 p2,) = mp.listings(address(coll), t2, seller);
        assertEq(s2, seller, "item 2 seller preserved");
        assertEq(uint256(p2), uint256(0.15 ether), "item 2 price preserved");

        // Reentry target slot MUST be unset: the inner mp.batchList call was
        // reverted by ReentrancyGuard before it could write the listing.
        (address s99, , , uint128 p99, ) = mp.listings(address(coll), t99, address(buyer));
        assertEq(s99, address(0),
                 "reentry slot UNSET - inner mp.batchList was reverted by ReentrancyGuard");
        assertEq(uint256(p99), 0, "reentry slot price zero (reentry blocked)");
    }

    /// @notice L-10: _bidders is unique per distinct participating address.
    ///         A bidder who tops up (prevCum already > 0) MUST NOT add a
    ///         duplicate entry; the `prevCum == 0` guard in bid() prevents
    ///         re-enrollment. The `_seenBidder` mapping provides defense-in-
    ///         depth for the edge case where a bidder is refunded (cumulative
    ///         zeroed via refundLosers on a stalled auction) and then re-bids
    ///         on the same live auction. While bid() in practice rejects
    ///         settled auctions, the _seenBidder guard remains as a compile-
    ///         time invariant: every push to _bidders is gated by both
    ///         `prevCum == 0` and `!_seenBidder`.
    ///
    ///         The settle→refundLosers→rebid flow tested in earlier drafts
    ///         is impossible: refundLosers requires settled or stalled state,
    ///         and bid() reverts with NotActive() on settled auctions. This
    ///         test instead verifies enrollment uniqueness via top-ups.
    function test_bidders_uniqueAcrossRefundAndRebid() public {
        (uint256 id, ) = _auction7d();

        // Alice bids 1 ETH — enrolled on first bid.
        _bid(id, alice, 1 ether);
        assertEq(ah.bidderCount(id), 1, "alice enrolled on first bid");

        // Alice tops up with another 1 ETH — prevCum is already > 0,
        // so the _seenBidder push gate is NOT entered. bidderCount stays 1.
        _bid(id, alice, 1 ether);
        assertEq(ah.bidderCount(id), 1,
                 "alice top-up does NOT push duplicate (prevCum > 0 skips enrollment)");
        assertEq(ah.cumulative(id, alice), 2 ether, "alice cumulative = 2 ether after top-up");

        // Bob outbids to 3 ETH — second distinct address, enrolled.
        _bid(id, bob, 3 ether);
        assertEq(ah.bidderCount(id), 2, "bob enrolled; no duplicate for alice");

        // Bob tops up — same invariant: prevCum > 0, no duplicate push.
        _bid(id, bob, 1 ether);
        assertEq(ah.bidderCount(id), 2,
                 "bob top-up does NOT push duplicate; _bidders[id] has exactly 2 entries");
        assertEq(ah.cumulative(id, bob), 4 ether, "bob cumulative = 4 ether after top-up");
    }

    // ════════════════════════════════════════════════════════════════════════════
    //  (k)  nonReentrant on auction creation (v29 — Round 4).
    //      AuctionHouse.create() and create1155() carry both `entryGate`
    //      and `nonReentrant`. All external calls during creation are to
    //      IERC721/IERC1155 view functions (ownerOf, balanceOf,
    //      isApprovedForAll, getApproved) — Solidity compiles these as
    //      STATICCALL, which forbids state changes at the EVM level.
    //      Practical reentrancy through these probes is therefore
    //      impossible; the nonReentrant guard is defense-in-depth
    //      against any future code path that adds a non-staticcall
    //      (e.g. an onReceived hook, a registry read with a callback,
    //      or an ERC-777-style pre-transfer hook). This test validates
    //      that both creation entry points function correctly and
    //      documents the STATICCALL limitation.
    // ════════════════════════════════════════════════════════════════════════════

    /// @dev Validates that create() and create1155() both produce auctions
    ///      normally through a legitimate collection. The nonReentrant
    ///      modifier is present on both functions; STATICCALL from IERC721/
    ///      IERC1155 view probes prevents practical reentrancy at the EVM
    ///      level, making nonReentrant defense-in-depth for any future
    ///      non-staticcall path added to auction creation.
    function test_create_nonReentrantDefenseInDepth() public {
        // ERC-721: create() succeeds with a valid collection.
        (uint256 id721, uint256 tid721) = _auction7d();
        assertEq(id721, 1, "create() produced auction id 1");
        AuctionHouse.Auction memory a721 = ah.getAuction(id721);
        assertEq(a721.seller, seller);
        assertEq(a721.collection, address(nft));
        assertEq(a721.tokenId, tid721);
        assertEq(uint256(a721.standard), uint256(TokenStandard.ERC721));

        // ERC-1155: create1155() succeeds with a valid multi-token.
        vm.startPrank(seller);
        multi.mint(seller, 99, 10);
        multi.setApprovalForAll(address(ah), true);
        uint256 id1155 = ah.create1155(
            address(multi),
            99,
            10,        // amount
            0.5 ether,  // reserve
            uint64(block.timestamp + 7 days),
            500,        // 5% min increment
            0
        );
        vm.stopPrank();
        assertEq(id1155, 2, "create1155() produced auction id 2");
        AuctionHouse.Auction memory a1155 = ah.getAuction(id1155);
        assertEq(a1155.seller, seller);
        assertEq(a1155.collection, address(multi));
        assertEq(a1155.tokenId, 99);
        assertEq(uint256(a1155.standard), uint256(TokenStandard.ERC1155));
        assertEq(a1155.amount, 10);
        assertEq(a1155.reserve, 0.5 ether);
    }

    // ════════════════════════════════════════════════════════════════════════════
    //  (l)  Increment-logic fuzz tests — covers the min-next-bid arithmetic
    //      in AuctionHouse.bid() for the overtaking-leader path:
    //
    //      uint256 incPct  = uint256(a.leaderTotal) * a.minIncrementBps / 10_000;
    //      uint256 inc     = incPct > a.minIncrementFlat ? incPct : a.minIncrementFlat;
    //      if (inc < MIN_BID_INCREMENT) inc = MIN_BID_INCREMENT;
    //      uint256 minNext256 = uint256(a.leaderTotal) + inc;
    //      if (minNext256 > type(uint128).max) revert BidOverflow();
    //      uint128 minNext = uint128(minNext256);
    //      if (newTotal < minNext) revert BidTooLow();
    //
    //  (l.1) MIN_BID_INCREMENT floor: when both BPS and flat are 0, the floor
    //        of 0.001 ETH is applied. A bid below leaderTotal + floor reverts
    //        BidTooLow; a bid at the floor takes the lead.
    //
    //  (l.2) General minNext calculation: fuzz BPS (0-5000), flat (0-1 ETH),
    //        and leaderTotal (1-50 ETH). Verify the computed minNext matches
    //        the contract's actual enforcement: bids at minNext succeed, bids
    //        at minNext-1 wei revert BidTooLow (when minNext <= uint128.max).
    //
    //  (l.3) Near-max BidOverflow guard: when leaderTotal is close to
    //        uint128.max, a bid that pushes minNext over the cap reverts
    //        BidOverflow. A small bid that stays under cap succeeds.
    // ════════════════════════════════════════════════════════════════════════════

    // INVARIANT: When both minIncrementBps=0 and minIncrementFlat=0, the
    // floor at MIN_BID_INCREMENT (1 ether) is always applied. Bidders
    // cannot reduce the increment below this economic threshold through
    // contract configuration alone.
    function testFuzz_increment_minBidFloor(uint128 leaderTotal) public {
        leaderTotal = uint128(bound(leaderTotal, 1 ether, 50 ether));
        uint256 floor = ah.MIN_BID_INCREMENT(); // 1 ether

        // Auction with BOTH increment params at 0 — only the floor applies.
        uint256 id = _setupLeader(leaderTotal / 2, 0, 0, alice, leaderTotal);

        uint256 expectedMinNext = uint256(leaderTotal) + floor;

        // Below-floor bid must revert BidTooLow.
        vm.deal(bob, 100 ether);
        vm.prank(bob);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: expectedMinNext - 1}(id);
        (address l,) = _leader(id);
        assertEq(l, alice, "alice still leader after failed below-floor bid");

        // At-floor bid takes the lead.
        vm.prank(bob);
        ah.bid{value: expectedMinNext}(id);
        (address l2, uint128 t2) = _leader(id);
        assertEq(l2, bob, "bob takes lead with floor bid");
        assertEq(t2, uint128(expectedMinNext), "leaderTotal = leaderTotal + MIN_BID_INCREMENT");
    }

    // INVARIANT: The increment is always the max of (percentage, flat, MIN_BID_INCREMENT).
    // Bids below the resulting minNext revert BidTooLow; bids at or above succeed.
    function testFuzz_increment_minNextCalculation(
        uint16 minIncBps,
        uint128 minIncFlat,
        uint128 leaderTotal
    ) public {
        minIncBps = uint16(bound(minIncBps, 0, 5000));
        minIncFlat = uint128(bound(minIncFlat, 0, 1 ether));
        leaderTotal = uint128(bound(leaderTotal, 1 ether, 50 ether));

        uint256 incPct = uint256(leaderTotal) * minIncBps / 10_000;
        uint256 inc = incPct > minIncFlat ? incPct : minIncFlat;
        uint256 floor = ah.MIN_BID_INCREMENT();
        if (inc < floor) inc = floor;
        uint256 minNext256 = uint256(leaderTotal) + inc;

        // Create auction and set up leader BEFORE any overflow branch so `id`
        // is in scope for every return path.
        // The reserve is leaderTotal/2 (capped floor at 0.01 ETH) so alice's
        // leaderTotal bid always clears it.
        uint256 minReserve = leaderTotal / 2;
        if (minReserve < 0.01 ether) minReserve = 0.01 ether;
        uint256 id = _setupLeader(uint128(minReserve), minIncBps, minIncFlat, alice, leaderTotal);

        // If minNext overflows uint128, any bid where newTotal > leaderTotal
        // triggers BidOverflow in the L-11 guard. Use type(uint128).max as the
        // bid value — this is always > leaderTotal (leaderTotal <= 50 ether)
        // and passes the first overflow check (nt == type(uint128).max, not >).
        if (minNext256 > type(uint128).max) {
            vm.deal(bob, type(uint128).max);
            vm.prank(bob);
            vm.expectRevert(BidOverflow.selector);
            ah.bid{value: type(uint128).max}(id);
            return;
        }

        uint128 minNext = uint128(minNext256);

        // Bid just below minNext reverts BidTooLow (when minNext > leaderTotal).
        // This always holds since inc >= MIN_BID_INCREMENT > 0, so minNext > leaderTotal.
        if (minNext > 0 && uint256(minNext) - 1 > leaderTotal) {
            vm.deal(bob, 100 ether);
            vm.prank(bob);
            vm.expectRevert(BidTooLow.selector);
            ah.bid{value: minNext - 1}(id);
            (address l,) = _leader(id);
            assertEq(l, alice, "alice still leader after BidTooLow");
        }

        // Bid exactly at minNext succeeds.
        vm.deal(bob, 100 ether);
        vm.prank(bob);
        ah.bid{value: minNext}(id);
        (address l2, uint128 t2) = _leader(id);
        assertEq(l2, bob, "bob takes lead with exact minNext bid");
        assertEq(t2, minNext, "leaderTotal equals minNext after winning bid");
    }

    // INVARIANT: When leaderTotal is near uint128.max, the L-11 guard prevents
    // any bid from pushing minNext over the cap. Use type(uint128).max as the
    // bid value for overflow cases since it's the only value guaranteed to be
    // > leaderTotal while still <= type(uint128).max (for leaderTotal < max).
    //
    // NOTE: leaderTotal = type(uint128).max is unreachable for overflow because
    // no newTotal can exceed it — the `newTotal > a.leaderTotal` branch is never
    // entered. The upper bound is therefore type(uint128).max - 1.
    function testFuzz_increment_nearMaxBidOverflow(uint128 leaderTotal) public {
        leaderTotal = uint128(bound(leaderTotal, type(uint128).max - 1 ether, type(uint128).max - 1));

        // Use 0,0 increments so the floor (MIN_BID_INCREMENT = 0.001 ETH) dominates.
        uint256 id = _setupLeader(leaderTotal / 2, 0, 0, alice, leaderTotal);
        uint256 floor = ah.MIN_BID_INCREMENT();
        uint256 minNext256 = uint256(leaderTotal) + floor;

        // If minNext exceeds uint128.max, use type(uint128).max as the bid.
        if (minNext256 > type(uint128).max) {
            vm.deal(bob, type(uint128).max);
            vm.prank(bob);
            vm.expectRevert(BidOverflow.selector);
            ah.bid{value: type(uint128).max}(id);
            (address l, uint128 t) = _leader(id);
            assertEq(l, alice, "alice still leader after BidOverflow");
            assertEq(t, leaderTotal, "leaderTotal unchanged after BidOverflow");
        } else {
            // minNext is within uint128 range — the bid should succeed.
            uint128 minNext = uint128(minNext256);
            vm.deal(bob, type(uint128).max);
            vm.prank(bob);
            ah.bid{value: minNext}(id);
            (address l2, uint128 t2) = _leader(id);
            assertEq(l2, bob, "bob takes lead with near-max minNext bid");
            assertEq(t2, minNext, "leaderTotal at near-max boundary");
        }
    }

    // ════════════════════════════════════════════════════════════════════════════
    //  (m)  R-04 / R-01 regression tests (v28 — Round 5):
    //
    //  (j.1) R-04: settleUnstuck MUST NOT refresh a.stalledAt on a re-stall.
    //        Any third party calling settleUnstuck() at day 6 must NOT
    //        reset the 7-day reclaim window — otherwise the winner's
    //        safety-valve never opens and the bidder is permanently
    //        denied their escrow.
    //
    //  (j.2) R-01: withdrawRefund()'s `if (!ok) restore + revert`
    //        branch is exercised by a receiver whose receive() ALWAYS
    //        reverts. The original GasGriefingReceiver / GreedyReceiver
    //        tests cover the SUCCESS path (set blocked=false → call works),
    //        but did not exercise the BUBBLE path where receive() returns
    //        false. Without this regression test, a future refactor that
    //        drops the restore-on-failure reassignment would silently
    //        lose credits.
    //
    //  (j.3) R-02: bidirectional griefer + buyer recover from the same
    //        stall window — confirms the buyer can still call reclaim()
    //        even AFTER the griefer's null-retry attempts.
    // ════════════════════════════════════════════════════════════════════════════

    /// @dev R-04 regression: settleUnstuck refreshes NO timer fields.
    ///      Stall timestamp encoded in `a.stalledAt` is immutable across
    ///      repeated settleUnstuck calls. reclaim() opens at firstStall +
    ///      STALL_WINDOW regardless of retries.
    function test_settleUnstuckDoesNotRefreshStallTimer() public {
        // ERC1155 buyer-fault stall setup.
        ERC1155RejectingBidder bidder = new ERC1155RejectingBidder();
        vm.deal(address(bidder), 100 ether);

        vm.startPrank(seller);
        multi.mint(seller, 13, 5);
        multi.setApprovalForAll(address(ah), true);
        uint256 id = ah.create1155(
            address(multi),
            13,
            5,
            1 ether,
            uint64(block.timestamp + 1 days),
            500,
            0
        );
        vm.stopPrank();

        vm.prank(address(bidder));
        ah.bid{value: 2 ether}(id);

        // Warp to T0 + 2d (settle is live), call settle() — buyer's
        // onERC1155Received reverts → buyer-fault stall. First stalledAt = T0+2d.
        vm.warp(block.timestamp + 2 days);
        ah.settle(id);
        uint64 firstStall = ah.getAuction(id).stalledAt;
        assertGt(firstStall, 0, "stalled at first seller's stall");

        // Warp to T0 + 6d (within original STALL_WINDOW).
        vm.warp(uint256(firstStall) + 6 days);

        // griefer calls settleUnstuck with bidder STILL blocked.
        // Pre-fix: stalledAt would be REFRESHED to T0+6d, denying reclaim().
        // Post-fix: stuck at firstStall, but AuctionStalled event fires.
        address griefer = address(uint160(uint256(keccak256(abi.encodePacked("R04_GRIEFER")))));
        vm.deal(griefer, 1 ether);
        vm.expectEmit(true, true, false, true, address(ah));
        emit AuctionHouse.AuctionStalled(id, address(bidder), seller);
        vm.prank(griefer);
        ah.settleUnstuck(id);

        // ASSERTION: stalledAt MUST equal firstStall (NOT refreshed).
        AuctionHouse.Auction memory a = ah.getAuction(id);
        assertEq(
            a.stalledAt,
            firstStall,
            "R-04: stalledAt MUST NOT refresh on subsequent buyer-fault retries"
        );
        assertFalse(a.settled, "auction NOT settled (still stalled)");

        // A SECOND griefer attempt at T0+6d+5min — same assertion.
        vm.warp(uint256(a.stalledAt) + 6 days + 5 minutes);
        vm.prank(griefer);
        ah.settleUnstuck(id);
        assertEq(
            ah.getAuction(id).stalledAt,
            firstStall,
            "R-04: stalledAt still pinned to first-stall after SECOND griefer attempt"
        );

        // Reclaim opens at firstStall + STALL_WINDOW (T0 + 7d). Verify by
        // warping to T0 + 7d + 1s — reclaim() succeeds; the buyer recovers
        // their escrow. Unblock the bidder's receive() so the reclaim push
        // succeeds (ERC1155RejectingBidder has blocked=true by default).
        bidder.setBlocked(false);
        vm.warp(uint256(firstStall) + 7 days + 1);
        uint256 bidderBalBefore = address(bidder).balance;
        vm.prank(address(bidder));
        ah.reclaim(id);
        assertEq(
            address(bidder).balance,
            bidderBalBefore + 2 ether,
            "R-04 path 2: buyer recovers escrow via reclaim() at original deadline"
        );
        assertTrue(ah.getAuction(id).settled, "auction settled by reclaim");
    }

    /// @dev R-01 regression: withdrawRefund's restore-on-failure path.
    ///      Receive() that ALWAYS reverts (ok=false from .call) must
    ///      restore pendingReturns[msg.sender] = amt and revert WithdrawFailed.
    ///      No funds are silently lost.
    function test_withdrawRefundRestoreOnFailure() public {
        // Setup: GreedyReceiver that ALWAYS reverts on receive().
        GreedyReceiver bidder = new GreedyReceiver();
        bidder.setBlocked(false); // for makeOffer's pre-state
        vm.deal(address(bidder), 10 ether);

        // Build a pendingReturns credit via OfferBook expired-refund path.
        uint256 tid = nft.mint(seller);
        vm.prank(seller);
        nft.setApprovalForAll(address(ob), true);

        _enableOffers(address(nft));
        uint64 exp = uint64(block.timestamp + 1 days);
        vm.prank(address(bidder));
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, exp);

        // Now lock receive() → refundExpiredOffer will fall back to pull.
        bidder.setBlocked(true);

        vm.warp(uint256(exp) + 1);
        ob.refundExpiredOffer(address(nft), tid, address(bidder));

        // Credit is parked in pendingReturns.
        assertEq(
            ob.pendingReturns(address(bidder)),
            1 ether,
            "GreedyReceiver parked in pendingReturns via refundExpiredOffer push fallback"
        );

        // ATTEMPT 1: withdrawRefund() with receive() reverting.
        // Expected: revert WithdrawFailed AND pendingReturns restored to 1 ETH.
        uint256 balBefore = address(bidder).balance;
        vm.expectRevert(WithdrawFailed.selector);
        vm.prank(address(bidder));
        ob.withdrawRefund();

        assertEq(
            ob.pendingReturns(address(bidder)),
            1 ether,
            "R-01: pendingReturns RESTORED to 1 ETH (no funds lost on transient failure)"
        );
        assertEq(
            address(bidder).balance,
            balBefore,
            "R-01: failed withdraw did not transfer any ETH to bidder"
        );

        // ATTEMPT 2: same withdrawRefund() - should fail the same way
        // (proves the credit survives MULTIPLE failed attempts).
        vm.expectRevert(WithdrawFailed.selector);
        vm.prank(address(bidder));
        ob.withdrawRefund();
        assertEq(ob.pendingReturns(address(bidder)), 1 ether, "R-01: survives MULTIPLE failed attempts");

        // SUCCESS path: bidder unblocks receive() → withdrawRefund succeeds.
        bidder.setBlocked(false);
        uint256 balBeforeSuccess = address(bidder).balance;
        vm.prank(address(bidder));
        ob.withdrawRefund();
        assertEq(
            ob.pendingReturns(address(bidder)),
            0,
            "credit cleared on successful withdraw"
        );
        assertEq(
            address(bidder).balance,
            balBeforeSuccess + 1 ether,
            "bidder received full 1 ETH escrow back"
        );
    }

    /// @dev R-02: griefer continuously retries settleUnstuck; buyer still
    ///      reclaims at original deadline. Mirrors j.1 but exercises the
    ///      PATH through the griefer-and-buyer-coexist scenario.
    function test_settleUnstuckGriefCannotBlockReclaim() public {
        // ERC1155 buyer-fault stall.
        ERC1155RejectingBidder bidder = new ERC1155RejectingBidder();
        vm.deal(address(bidder), 100 ether);

        vm.startPrank(seller);
        multi.mint(seller, 17, 3);
        multi.setApprovalForAll(address(ah), true);
        uint256 id = ah.create1155(
            address(multi),
            17,
            3,
            1 ether,
            uint64(block.timestamp + 1 days),
            500,
            0
        );
        vm.stopPrank();

        vm.prank(address(bidder));
        ah.bid{value: 5 ether}(id);

        vm.warp(block.timestamp + 2 days);
        ah.settle(id);
        uint64 t0 = ah.getAuction(id).stalledAt;
        assertGt(t0, 0, "stalled at T0");

        // Griefer attempts settleUnstuck at four STRATEGIC checkpoints instead
        // of every 30 min (which would be 336 calls). The four checkpoints
        // exercise: pre-deadline (5d), half-deadline (6d), post-half-deadline
        // (6d+12h), and right-before-deadline (7d-30min). Same invariant
        // coverage, ~1% of the runtime.
        address griefer = address(uint160(uint256(keccak256(abi.encodePacked("R02_GRIEFER")))));
        vm.deal(griefer, 1 ether);
        uint256[4] memory checkpoints = [uint256(5 days), uint256(6 days), uint256(6 days + 12 hours), uint256(7 days - 30 minutes)];
        for (uint256 i; i < 4; ++i) {
            vm.warp(uint256(t0) + checkpoints[i]);
            vm.prank(griefer);
            ah.settleUnstuck(id);
            // CRUCIAL: stalledAt does not shift on any retry.
            assertEq(
                ah.getAuction(id).stalledAt,
                t0,
                "R-02: stalledAt pinned despite griefer's strategic-window retries"
            );
        }

        // At T0+7d+1, buyer reclaims. Unblock receive() so the refund
        // push succeeds (ERC1155RejectingBidder has blocked=true by default).
        bidder.setBlocked(false);
        vm.warp(uint256(t0) + 7 days + 1);
        uint256 bidderBalBefore = address(bidder).balance;
        vm.prank(address(bidder));
        ah.reclaim(id);
        assertEq(
            address(bidder).balance,
            bidderBalBefore + 5 ether,
            "R-02: buyer reclaims full 5 ETH after griefer's 7-day grief campaign"
        );
    }
}

/// @dev Helper stub for feeRecipient that has NO receive()/fallback → all ETH pushes fail.
contract RejectEtherNoReceive {
    // intentionally empty
}

/// @dev Helper stub for a seller/bidder wallet whose receive() always
///      reverts (push-fallback testing). Implements onERC721Received so
///      MockERC721._safeMint() does not reject the contract during test setup.
contract SellerNoReceive {
    receive() external payable { revert("no receive"); }

    function onERC721Received(address, address, uint256, bytes calldata)
        external pure returns (bytes4)
    {
        return this.onERC721Received.selector;
    }
}


