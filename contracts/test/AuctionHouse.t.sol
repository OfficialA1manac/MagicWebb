// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}        from "forge-std/Test.sol";
import {AuctionHouse, BidTooLow, AuctionLive, AuctionEnded, NotSeller, NotActive, NotSettled, InvalidAmount, CannotCancel, BidOverflow, NotKeeper} from "../src/AuctionHouse.sol";
import {MarketplaceManager} from "../src/MarketplaceManager.sol";
import {MockERC721}  from "./MockERC721.sol";
import {MockERC1155} from "./MockERC1155.sol";

/// @dev Contract bidder that blocks ERC-1155 onERC1155Received (buyer-fault).
contract MaliciousBidderForTest {
    bool public blockERC1155Receive = true;

    constructor() payable {}

    receive() external payable {}

    function setBlockERC1155Receive(bool b) external { blockERC1155Receive = b; }

    function onERC1155Received(address, address, uint256, uint256, bytes calldata)
        external view returns (bytes4)
    {
        if (blockERC1155Receive) revert("no ERC1155");
        return this.onERC1155Received.selector;
    }

    function bidOn(AuctionHouse ah, uint256 id) external payable {
        ah.bid{value: msg.value}(id);
    }
}

contract AuctionHouseTest is Test {
    AuctionHouse ah;
    MockERC721   nft;
    MockERC1155  multi;
    address feeRecipient = address(0x1111000000000000000000000000000000111100);
    address seller       = address(0xBEEF);
    address alice        = address(0xA11CE);
    address bob          = address(0xB0B);
    address carol        = address(0xCab01);

    function setUp() public {
        ah = new AuctionHouse();
        ah.initialize(feeRecipient, address(0));
        nft   = new MockERC721();
        multi = new MockERC1155();
        vm.deal(alice, 100 ether);
        vm.deal(bob,   100 ether);
        vm.deal(carol, 100 ether);
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    function _fee(uint128 v) internal pure returns (uint256) { return uint256(v) * 150 / 10_000; }

    function _create() internal returns (uint256 id, uint256 tid) {
        vm.startPrank(seller);
        tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        id = ah.create(address(nft), tid, 1 ether, uint64(block.timestamp + 24 hours), 500, 0);
        vm.stopPrank();
        // Auctions auto-activate on creation — no separate activateAuction() needed.
    }

    function _bid(uint256 id, address who, uint128 amt) internal {
        vm.prank(who);
        ah.bid{value: amt}(id);
    }

    function _leader(uint256 id) internal view returns (address l, uint128 t) {
        // Positions: seller=0, startsAt=1, minIncrementBps=2, settled=3, active=4,
        // standard=5, collection=6, endsAt=7, tokenId=8, reserve=9, amount=10,
        // leader=11, leaderTotal=12, minIncrementFlat=13
        (,,,,,,,,,,, l, t,) = ah.auctions(id);
    }

    // ── Cumulative bidding ──────────────────────────────────────────────────────

    function test_firstBidAtReserveLeads() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        (address l, uint128 t) = _leader(id);
        assertEq(l, alice);
        assertEq(t, 1 ether);
        assertEq(ah.cumulative(id, alice), 1 ether);
    }

    // ── Sub-reserve bids now revert (no accumulator path) ─────────────────

    function test_subReserveFirstBidReverts() public {
        (uint256 id,) = _create();
        vm.prank(alice);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: 0.4 ether}(id);            // below reserve → reverts
    }

    function test_outbidNoRefundThenReclaim() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        _bid(id, bob, 2 ether);                  // bob leads; alice NOT refunded
        assertEq(ah.cumulative(id, alice), 1 ether, "alice escrow stays");
        assertEq(alice.balance, 99 ether, "alice not refunded on outbid");
        (address l, uint128 t) = _leader(id);
        assertEq(l, bob); assertEq(t, 2 ether);
        // alice tops up cumulatively: 1 + 2 = 3 > 2 + 1 (MIN_BID_INCREMENT) → reclaims lead
        _bid(id, alice, 2 ether);
        (address l2, uint128 t2) = _leader(id);
        assertEq(l2, alice); assertEq(t2, 3 ether);
        assertEq(ah.cumulative(id, alice), 3 ether);
    }

    function test_outbidEmitsNotification() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        vm.expectEmit(true, true, false, true, address(ah));
        emit AuctionHouse.OutbidNotification(id, alice, 2 ether);
        _bid(id, bob, 2 ether);
    }

    function test_takeLeadBelowIncrementReverts() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);                // leader 1 ether, 5% inc → min next 1.05
        vm.prank(bob);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: 1.01 ether}(id);           // 1.01 < 1.05 and > leaderTotal → reverts
    }

    function test_subReserveFirstBidReverts2() public {
        (uint256 id,) = _create();
        vm.prank(bob);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: 0.9 ether}(id);            // below reserve → reverts
    }

    function test_zeroBidReverts() public {
        (uint256 id,) = _create();
        vm.prank(alice);
        vm.expectRevert(InvalidAmount.selector);
        ah.bid{value: 0}(id);
    }

    // ── L-11: near-max leaderTotal overflow guard ────────────────────────────

    /// @dev L-11 regression: when leaderTotal is near uint128 max, the
    ///      minNext comparison must operate in uint256 to avoid silent
    ///      truncation. With sub-leader accumulation removed, bob's
    ///      overtake attempt triggers BidOverflow (minNext > uint128.max)
    ///      rather than BidTooLow — proving the guard works in uint256.
    function test_nearMaxLeaderBidDoesNotTruncate() public {
        (uint256 id,) = _create();
        // Set up alice as leader at near uint128 max.
        uint128 nearMax = type(uint128).max - 0.01 ether;
        vm.deal(alice, uint256(nearMax) + 50 ether);
        vm.prank(alice);
        ah.bid{value: nearMax}(id);
        (address l, uint128 t) = _leader(id);
        assertEq(l, alice);
        assertEq(t, nearMax, "alice leads at nearMax");

        // Bob tries to overtake — minNext = nearMax + MIN_BID_INCREMENT
        // overflows uint128, triggering BidOverflow (L-11 guard).
        // type(uint128).max is the only value guaranteed > nearMax while
        // still <= uint128.max, so it reaches the overtake branch.
        vm.deal(bob, type(uint128).max);
        vm.prank(bob);
        vm.expectRevert(BidOverflow.selector);
        ah.bid{value: type(uint128).max}(id);

        // Verify alice is still leader and total unchanged.
        (address l2, uint128 t2) = _leader(id);
        assertEq(l2, alice, "alice still leader after BidOverflow");
        assertEq(t2, nearMax, "leaderTotal unchanged");

        // Bob's cumulative overflow guard: a bid that pushes bob's own
        // cumulative past uint128 max also reverts BidOverflow.
        // First bid: bob becomes leader (no leader yet? No — alice leads).
        // Deploy fresh auction for the cumulative overflow path.
        (uint256 id2,) = _create();
        uint128 bobFirst = nearMax - 1 ether;
        vm.deal(bob, uint256(bobFirst) + 10 ether);
        vm.prank(bob);
        ah.bid{value: bobFirst}(id2);
        assertEq(ah.cumulative(id2, bob), bobFirst, "bob accumulated close to max");

        // Bob tops up by enough to push cumulative past uint128 max.
        vm.prank(bob);
        vm.expectRevert(BidOverflow.selector);
        ah.bid{value: 1.5 ether}(id2);
    }

    // ── Anti-snipe ────────────────────────────────────────────────────────────

    function test_antiSnipeExtends() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint64 end = uint64(block.timestamp + 1 hours);
        uint256 id = ah.create(address(nft), tid, 1 ether, end, 500, 0);
        vm.stopPrank();
        vm.warp(end - 1 minutes);
        _bid(id, alice, 1 ether);
        // endsAt at position 7, struct has 14 fields total
        (,,,,,,,uint64 newEnd,,,,,,) = ah.auctions(id);
        assertEq(newEnd, uint64(block.timestamp) + ah.EXTENSION_WINDOW());
    }

    // ── Settle ──────────────────────────────────────────────────────────────────

    function test_settleDistributesAndConsumesWinner() public {
        (uint256 id, uint256 tid) = _create();
        _bid(id, alice, 1 ether);
        _bid(id, bob, 3 ether);                  // bob wins
        vm.warp(block.timestamp + 30 hours);

        uint256 sellerBefore = seller.balance;
        uint256 vaultBefore  = feeRecipient.balance;
        ah.settle(id);                            // permissionless (test contract calls)

        assertEq(nft.ownerOf(tid), bob);
        assertEq(feeRecipient.balance, vaultBefore + _fee(3 ether));
        assertEq(seller.balance, sellerBefore + 3 ether - _fee(3 ether));
        assertEq(ah.cumulative(id, bob), 0, "winner escrow consumed");
        assertEq(ah.cumulative(id, alice), 1 ether, "loser escrow awaits refund");
    }

    function test_settlePermissionlessByAnyone() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        vm.warp(block.timestamp + 30 hours);
        vm.prank(carol);                          // not keeper, not party
        ah.settle(id);
        // settled at position 3
        // settled at position 3, struct has 14 fields total
        (,,,bool settled,,,,,,,,,,) = ah.auctions(id);
        assertTrue(settled);
    }

    function test_settleBeforeEndReverts() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        vm.expectRevert(AuctionLive.selector);
        ah.settle(id);
    }

    function test_settleNoBidsCancels() public {
        (uint256 id,) = _create();
        // No bids — no leader. Settle cancels the auction.
        vm.warp(block.timestamp + 30 hours);
        ah.settle(id);
        (,,,bool settled,,,,,,,,,,) = ah.auctions(id);
        assertTrue(settled);
    }

    function test_doubleSettleReverts() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        vm.warp(block.timestamp + 30 hours);
        ah.settle(id);
        vm.expectRevert(NotActive.selector);
        ah.settle(id);
    }

    // ── refundLosers ──────────────────────────────────────────────────────────

    function test_refundLosersPaysNonWinnersSkipsWinner() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        _bid(id, bob, 2 ether);
        _bid(id, carol, 3 ether);                 // carol wins
        vm.warp(block.timestamp + 30 hours);
        ah.settle(id);

        uint256 aBefore = alice.balance;
        uint256 bBefore = bob.balance;
        address[] memory batch = new address[](3);
        batch[0] = alice; batch[1] = bob; batch[2] = carol;
        ah.refundLosers(id, batch);

        assertEq(alice.balance, aBefore + 1 ether);
        assertEq(bob.balance,   bBefore + 2 ether);
        assertEq(ah.cumulative(id, alice), 0);
        assertEq(ah.cumulative(id, bob), 0);
        // idempotent: second call refunds nothing
        uint256 aMid = alice.balance;
        ah.refundLosers(id, batch);
        assertEq(alice.balance, aMid);
    }

    function test_refundLosersBeforeSettleReverts() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        address[] memory batch = new address[](1);
        batch[0] = alice;
        vm.expectRevert(NotSettled.selector);
        ah.refundLosers(id, batch);
    }

    // ── cancelEarly ─────────────────────────────────────────────────────────────

    function test_cancelEarlyNoBids() public {
        // cancelEarly works when there are no qualifying bids.
        // Sub-reserve bids now revert, so cancel-only works for bid-less auctions.
        (uint256 id,) = _create();
        vm.prank(seller);
        ah.cancelEarly(id);
        (,,,bool settled,,,,,,,,,,) = ah.auctions(id);
        assertTrue(settled);
    }

    // ── cancelEarly reserve-met invariant (audit-#6) ───────────────────────────
    function test_cancelEarlyAfterReserveMetReverts() public {
        (uint256 id,) = _create();                 // reserve = 1 ETH
        _bid(id, alice, 1 ether);                  // alice meets reserve → leader
        vm.prank(seller);
        vm.expectRevert(CannotCancel.selector);
        ah.cancelEarly(id);                        // cannot cancel: leader has
    }                                              //   qualified the auction

    function test_cancelEarlyAfterLeaderOvertakesReserveReverts() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);                  // alice meets reserve → leader
        _bid(id, bob,   2 ether);                  // bob overtakes, clears reserve
        vm.prank(seller);
        vm.expectRevert(CannotCancel.selector);
        ah.cancelEarly(id);
    }

    function test_cancelEarlyNotSellerReverts() public {
        (uint256 id,) = _create();
        vm.prank(alice);
        vm.expectRevert(NotSeller.selector);
        ah.cancelEarly(id);
    }

    // ── Escrow invariant ──────────────────────────────────────────────────────

    function test_escrowEqualsSumOfCumulatives() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);                  // alice leads at 1
        _bid(id, bob, 3 ether);                    // bob overtakes at 3
        _bid(id, carol, 5 ether);                  // carol overtakes at 5
        // contract holds alice+bob+carol = 1+3+5 = 9
        assertEq(address(ah).balance, 9 ether);
        assertEq(
            uint256(ah.cumulative(id, alice)) + ah.cumulative(id, bob) + ah.cumulative(id, carol),
            9 ether
        );
    }

    // ── ERC-1155 ────────────────────────────────────────────────────────────────

    function test_erc1155SettleTransfersAmount() public {
        vm.startPrank(seller);
        multi.mint(seller, 7, 5);
        multi.setApprovalForAll(address(ah), true);
        uint256 id = ah.create1155(address(multi), 7, 5, 1 ether, uint64(block.timestamp + 24 hours), 500, 0);
        vm.stopPrank();
        _bid(id, alice, 2 ether);
        vm.warp(block.timestamp + 30 hours);
        uint256 sellerBefore = seller.balance;
        ah.settle(id);
        assertEq(multi.balanceOf(alice, 7), 5);
        assertEq(seller.balance, sellerBefore + 2 ether - _fee(2 ether));
    }

    // ── Fuzz: fee math at settle ──────────────────────────────────────────────

    function testFuzz_feeExactAtSettle(uint128 amt) public {
        amt = uint128(bound(amt, 1 ether, 50 ether));
        vm.deal(alice, uint256(amt) + 1 ether);
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint256 id = ah.create(address(nft), tid, amt, uint64(block.timestamp + 24 hours), 0, 0);
        vm.stopPrank();
        _bid(id, alice, amt);
        vm.warp(block.timestamp + 30 hours);
        uint256 sb = seller.balance; uint256 vb = feeRecipient.balance;
        ah.settle(id);
        assertEq(feeRecipient.balance - vb, _fee(amt));
        assertEq(seller.balance - sb, uint256(amt) - _fee(amt));
    }

    // ── Exact reserve match boundary ──────────────────────────────────────────

    /// @dev Bid amount that equals the reserve exactly (not above) must still
    ///      take the lead. The condition is `newTotal >= a.reserve`.
    function test_exactReserveMatchTakesLead() public {
        (uint256 id,) = _create();                 // reserve = 1 ether
        _bid(id, alice, 1 ether);                  // exactly the reserve
        (address l, uint128 t) = _leader(id);
        assertEq(l, alice, "exact reserve match must lead");
        assertEq(t, 1 ether);
    }

    /// @dev Bid one wei below reserve must revert (sub-reserve bids no longer accumulate).
    function test_oneWeiBelowReserveReverts() public {
        (uint256 id,) = _create();                 // reserve = 1 ether
        vm.prank(alice);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: 1 ether - 1}(id);            // 1 wei below → reverts
    }

    // ── Anti-snipe: non-newLead does NOT extend ───────────────────────────────

    /// @dev Only new-lead bids trigger anti-snipe extension. Sub-leader bids
    ///      now revert entirely (can't accumulate below the leader).
    function test_antiSnipeOnlyExtendsOnNewLead() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint64 end = uint64(block.timestamp + 1 hours);
        uint256 id = ah.create(address(nft), tid, 1 ether, end, 500, 0);
        vm.stopPrank();
        _bid(id, alice, 2 ether);                  // alice leads at 2 ETH
        vm.warp(end - 1 minutes);                  // inside extension window
        // Bob tries sub-leader bid — must revert BidTooLow.
        vm.prank(bob);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: 0.5 ether}(id);
        // endsAt unchanged — timer not extended.
        (,,,,,,,uint64 newEnd,,,,,,) = ah.auctions(id);
        assertEq(newEnd, end, "timer NOT extended");
        // Now bob overtakes with a qualifying bid → timer extends.
        _bid(id, bob, 3 ether);
        (,,,,,,,newEnd,,,,,,) = ah.auctions(id);
        assertGt(newEnd, end, "timer extended on newLead");
    }

    // ── Leader self top-up ────────────────────────────────────────────────────

    /// @dev When the current leader bids more, leaderTotal rises but no
    ///      OutbidNotification is emitted (leadership unchanged).
    function test_leaderSelfTopUpIncreasesTotal() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        (address l, uint128 t) = _leader(id);
        assertEq(l, alice); assertEq(t, 1 ether);
        _bid(id, alice, 0.5 ether);                // self top-up
        (address l2, uint128 t2) = _leader(id);
        assertEq(l2, alice, "alice still leader");
        assertEq(t2, 1.5 ether, "leaderTotal increased");
        assertEq(ah.cumulative(id, alice), 1.5 ether);
    }

    // ── MIN_BID_INCREMENT floor for 0/0 increment config ──────────────────────        /// @dev audit-#5: When both minIncrementBps and minIncrementFlat are 0,
        ///      the bid() path falls through to MIN_BID_INCREMENT (1 ether)
        ///      to prevent 1-wei collusive loop that extends the timer forever.
    function test_minBidIncrementFloorPreventsOneWeiLoop() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        // minIncBps=0, minIncFlat=0 → floor is MIN_BID_INCREMENT
        uint256 id = ah.create(address(nft), tid, 1 ether, uint64(block.timestamp + 24 hours), 0, 0);
        vm.stopPrank();
        _bid(id, alice, 1 ether);                  // alice leads
        // Bob tries to overtake with just 1 wei above — must be rejected.
        vm.prank(bob);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: 1 ether + 1 wei}(id);        // 1 wei above leader, but MIN_BID_INCREMENT=1E

        // Bob bids enough to clear MIN_BID_INCREMENT.
        uint128 qualifying = 1 ether + ah.MIN_BID_INCREMENT();
        vm.deal(bob, uint256(qualifying) + 10 ether);
        _bid(id, bob, qualifying);
        (address l,) = _leader(id);
        assertEq(l, bob, "bob leads after meeting min increment floor");
    }

    // ── 3-tier settle() gate ────────────────────────────────────────────────
    // 1. KEEPER_ROLE — settles immediately after endsAt.
    // 2. Seller or auction winner — settles after endsAt + 5 minutes.
    // 3. Permissionless — anyone settles after endsAt + DURATION_24HR + 1hr.

    function test_settle_keeperAlwaysAllowed() public {
        // Deploy with a manager so the keeper gate is active.
        MarketplaceManager mgr = new MarketplaceManager();
        mgr.initialize(address(this));
        AuctionHouse gated = new AuctionHouse();
        gated.initialize(feeRecipient, address(mgr));
        mgr.grantRole(mgr.KEEPER_ROLE(), bob);

        // Create an auction on the gated AuctionHouse.
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(block.timestamp + 3 minutes), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        vm.warp(block.timestamp + 10 minutes);

        // Bob has KEEPER_ROLE — settles immediately.
        vm.prank(bob);
        gated.settle(id);
        (,,,bool settled,,,,,,,,,,) = gated.auctions(id);
        assertTrue(settled);
    }

    function test_settle_nonKeeperBlockedBeforeGrace() public {
        MarketplaceManager mgr = new MarketplaceManager();
        mgr.initialize(address(this));
        AuctionHouse gated = new AuctionHouse();
        gated.initialize(feeRecipient, address(mgr));
        mgr.grantRole(mgr.KEEPER_ROLE(), bob);

        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(block.timestamp + 3 minutes), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        vm.warp(block.timestamp + 10 minutes);

        // Carol is NOT the keeper — blocked before grace period.
        vm.prank(carol);
        vm.expectRevert(NotKeeper.selector);
        gated.settle(id);
        // Confirm auction still unsettled after the blocked attempt.
        (,,,bool settled,,,,,,,,,,) = gated.auctions(id);
        assertFalse(settled);
    }

    function test_settle_nonKeeperAllowedAfterGrace() public {
        MarketplaceManager mgr = new MarketplaceManager();
        mgr.initialize(address(this));
        AuctionHouse gated = new AuctionHouse();
        gated.initialize(feeRecipient, address(mgr));
        mgr.grantRole(mgr.KEEPER_ROLE(), bob);

        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(block.timestamp + 24 hours), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        // Warp past endsAt + DURATION_24HR + 1 hour (25 hours after endsAt).
        vm.warp(block.timestamp + 24 hours + 24 hours + 2 hours);

        // Carol is NOT the keeper, but the grace period has elapsed.
        vm.prank(carol);
        gated.settle(id);
        (,,,bool settled,,,,,,,,,,) = gated.auctions(id);
        assertTrue(settled);
    }

    function test_settle_permissionlessWithNoManager() public {
        // Existing test contract deploys with address(0) manager.
        // This test proves the zero-manager fallback works: anyone can settle.
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        vm.warp(block.timestamp + 30 hours);
        vm.prank(carol); // carol is not the seller, not a bidder, no role
        ah.settle(id);
        (,,,bool settled,,,,,,,,,,) = ah.auctions(id);
        assertTrue(settled);
    }

    // ── 3-tier gate: seller/winner 5-minute window ─────────────────────────

    function test_settle_sellerBlockedBefore5Min() public {
        MarketplaceManager mgr = new MarketplaceManager();
        mgr.initialize(address(this));
        AuctionHouse gated = new AuctionHouse();
        gated.initialize(feeRecipient, address(mgr));
        mgr.grantRole(mgr.KEEPER_ROLE(), bob);

        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(block.timestamp + 3 minutes), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        // Warp to 3 minutes after endsAt — seller is within the 5-min window.
        vm.warp(block.timestamp + 3 minutes + 3 minutes);

        // Seller tries to settle before the 5-minute cooldown elapses.
        vm.prank(seller);
        vm.expectRevert(NotKeeper.selector);
        gated.settle(id);
        // Confirm auction still unsettled.
        (,,,bool settled,,,,,,,,,,) = gated.auctions(id);
        assertFalse(settled);
    }

    function test_settle_sellerAllowedAfter5Min() public {
        MarketplaceManager mgr = new MarketplaceManager();
        mgr.initialize(address(this));
        AuctionHouse gated = new AuctionHouse();
        gated.initialize(feeRecipient, address(mgr));
        mgr.grantRole(mgr.KEEPER_ROLE(), bob);

        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(block.timestamp + 3 minutes), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        // Warp to 6 minutes after endsAt — past the 5-minute cooldown.
        vm.warp(block.timestamp + 3 minutes + 6 minutes);

        // Seller settles after the 5-minute cooldown.
        uint256 sellerBefore = seller.balance;
        vm.prank(seller);
        gated.settle(id);
        (,,,bool settled,,,,,,,,,,) = gated.auctions(id);
        assertTrue(settled);
        // Seller received proceeds (net of fee).
        assertGt(seller.balance, sellerBefore);
    }

    function test_settle_winnerAllowedAfter5Min() public {
        MarketplaceManager mgr = new MarketplaceManager();
        mgr.initialize(address(this));
        AuctionHouse gated = new AuctionHouse();
        gated.initialize(feeRecipient, address(mgr));
        mgr.grantRole(mgr.KEEPER_ROLE(), bob);

        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(block.timestamp + 3 minutes), 500, 0);
        vm.stopPrank();
        // Alice is the winner.
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        // Warp to 6 minutes after endsAt.
        vm.warp(block.timestamp + 3 minutes + 6 minutes);

        // Alice (winner) settles — should pass via tier 2.
        vm.prank(alice);
        gated.settle(id);
        (,,,bool settled,,,,,,,,,,) = gated.auctions(id);
        assertTrue(settled);
        // Alice now owns the NFT.
        assertEq(nft.ownerOf(tid), alice);
    }

    function test_settle_randomBlockedBefore25Hr() public {
        MarketplaceManager mgr = new MarketplaceManager();
        mgr.initialize(address(this));
        AuctionHouse gated = new AuctionHouse();
        gated.initialize(feeRecipient, address(mgr));
        mgr.grantRole(mgr.KEEPER_ROLE(), bob);

        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(block.timestamp + 24 hours), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        // Warp to 10 hours after endsAt — past 5min but before 25hr.
        vm.warp(block.timestamp + 24 hours + 10 hours);

        // Carol is NOT the keeper, NOT the seller, NOT the winner.
        vm.prank(carol);
        vm.expectRevert(NotKeeper.selector);
        gated.settle(id);
        (,,,bool settled,,,,,,,,,,) = gated.auctions(id);
        assertFalse(settled);
    }

    // ── Permissionless refundLosers() ────────────────────────────────────────
    // refundLosers() is ungated — anyone can call it after settlement.

    function test_refundLosers_permissionlessWithManager() public {
        MarketplaceManager mgr = new MarketplaceManager();
        mgr.initialize(address(this));
        AuctionHouse gated = new AuctionHouse();
        gated.initialize(feeRecipient, address(mgr));
        mgr.grantRole(mgr.KEEPER_ROLE(), bob);


        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(block.timestamp + 3 minutes), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        vm.prank(bob);
        gated.bid{value: 2 ether}(id); // bob wins + has KEEPER_ROLE
        vm.warp(block.timestamp + 10 minutes);

        // Keeper settles.
        vm.prank(bob);
        gated.settle(id);

        // Carol (no role) calls refundLosers — permissionless, should succeed.
        address[] memory batch = new address[](1);
        batch[0] = alice;
        uint256 aBefore = alice.balance;
        vm.prank(carol);
        gated.refundLosers(id, batch);
        assertEq(alice.balance, aBefore + 1 ether);
    }


}
