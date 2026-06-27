// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}        from "forge-std/Test.sol";
import {AuctionHouse, BidTooLow, AuctionLive, AuctionEnded, NotSeller, NotActive, NotSettled, InvalidAmount, CannotCancel} from "../src/AuctionHouse.sol";
import {MockERC721}  from "./MockERC721.sol";
import {MockERC1155} from "./MockERC1155.sol";

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
        ah    = new AuctionHouse(feeRecipient, address(0));
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
        id = ah.create(address(nft), tid, 1 ether, uint64(block.timestamp + 7 days), 500, 0);
        vm.stopPrank();
    }

    function _bid(uint256 id, address who, uint128 amt) internal {
        vm.prank(who);
        ah.bid{value: amt}(id);
    }

    function _leader(uint256 id) internal view returns (address l, uint128 t) {
        (,,,,,,,,,, l, t,,) = ah.auctions(id);
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

    function test_subReserveAccumulatesButNoLead() public {
        (uint256 id,) = _create();
        _bid(id, alice, 0.4 ether);
        (address l,) = _leader(id);
        assertEq(l, address(0));                 // below reserve → no leader
        assertEq(ah.cumulative(id, alice), 0.4 ether);
        _bid(id, alice, 0.6 ether);              // cumulative now 1 ether == reserve
        (address l2, uint128 t2) = _leader(id);
        assertEq(l2, alice);
        assertEq(t2, 1 ether);
    }

    function test_outbidNoRefundThenReclaim() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        _bid(id, bob, 2 ether);                  // bob leads; alice NOT refunded
        assertEq(ah.cumulative(id, alice), 1 ether, "alice escrow stays");
        assertEq(alice.balance, 99 ether, "alice not refunded on outbid");
        (address l, uint128 t) = _leader(id);
        assertEq(l, bob); assertEq(t, 2 ether);
        // alice tops up cumulatively: 1 + 1.2 = 2.2 > 2 + 5% → reclaims lead
        _bid(id, alice, 1.2 ether);
        (address l2, uint128 t2) = _leader(id);
        assertEq(l2, alice); assertEq(t2, 2.2 ether);
        assertEq(ah.cumulative(id, alice), 2.2 ether);
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

    function test_belowReserveFirstBidAccumulates() public {
        (uint256 id,) = _create();
        _bid(id, bob, 0.9 ether);                // below reserve, no leader yet → accumulates
        (address l,) = _leader(id);
        assertEq(l, address(0));
        assertEq(ah.cumulative(id, bob), 0.9 ether);
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
    ///      truncation. A bid that would push newTotal over uint128 max
    ///      reverts with BidOverflow.
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

        // Bob accumulates close to the ceiling, then tops up to
        // overflow his own cumulative (exercising the BidOverflow
        // guard on the bidder's cumulative, not the leader-minNext path).
        uint128 bobFirst = nearMax - 1 ether;
        vm.deal(bob, uint256(bobFirst) + 10 ether);
        vm.prank(bob);
        ah.bid{value: bobFirst}(id);
        assertEq(ah.cumulative(id, bob), bobFirst, "bob accumulated close to max");

        // Bob tops up by enough to push cumulative past uint128 max.
        vm.prank(bob);
        vm.expectRevert(BidOverflow.selector);
        ah.bid{value: 1.5 ether}(id);

        // Carol bids below leaderTotal — accumulates without overflow.
        uint128 smallBid = 0.01 ether;
        vm.deal(carol, uint256(smallBid) + 1 ether);
        vm.prank(carol);
        ah.bid{value: smallBid}(id);
        assertEq(ah.cumulative(id, carol), smallBid, "carol escrow accumulated");
        (address l2, uint128 t2) = _leader(id);
        assertEq(l2, alice, "alice still leader");
        assertEq(t2, nearMax, "leaderTotal unchanged");
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
        (,,,,,,uint64 newEnd,,,,,,,) = ah.auctions(id);
        assertEq(newEnd, uint64(block.timestamp) + ah.EXTENSION_WINDOW());
    }

    // ── Settle ──────────────────────────────────────────────────────────────────

    function test_settleDistributesAndConsumesWinner() public {
        (uint256 id, uint256 tid) = _create();
        _bid(id, alice, 1 ether);
        _bid(id, bob, 3 ether);                  // bob wins
        vm.warp(block.timestamp + 8 days);

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
        vm.warp(block.timestamp + 8 days);
        vm.prank(carol);                          // not keeper, not party
        ah.settle(id);
        (,,,bool settled,,,,,,,,,,) = ah.auctions(id);
        assertTrue(settled);
    }

    function test_settleBeforeEndReverts() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        vm.expectRevert(AuctionLive.selector);
        ah.settle(id);
    }

    function test_settleNoLeaderCancels() public {
        (uint256 id,) = _create();
        _bid(id, alice, 0.5 ether);               // below reserve → no leader
        vm.warp(block.timestamp + 8 days);
        ah.settle(id);
        (,,,bool settled,,,,,,,,,,) = ah.auctions(id);
        assertTrue(settled);
        assertEq(ah.cumulative(id, alice), 0.5 ether, "refundable, not consumed");
    }

    function test_doubleSettleReverts() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        vm.warp(block.timestamp + 8 days);
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
        vm.warp(block.timestamp + 8 days);
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

    function test_cancelEarlyRefundsAllViaLosers() public {
        // v21 — audit-#6: cancelEarly is now BLOCKED once a qualifying leader
        // exists (leaderTotal >= reserve). To exercise the original "refund
        // everyone via refundLosers" path we keep both bids BELOW the reserve
        // (1 ETH). No leader is set, cancelEarly proceeds, and refundLosers
        // returns every escrow to its bidder.
        (uint256 id,) = _create();
        _bid(id, alice, 0.4 ether);                // below reserve → no leader
        _bid(id, bob,   0.5 ether);                // below reserve → still no leader
        vm.prank(seller);
        ah.cancelEarly(id);
        address[] memory batch = new address[](2);
        batch[0] = alice; batch[1] = bob;
        uint256 aB = alice.balance; uint256 bB = bob.balance;
        ah.refundLosers(id, batch);
        assertEq(alice.balance, aB + 0.4 ether);
        assertEq(bob.balance,   bB + 0.5 ether);  // full escrow returned
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
        _bid(id, alice, 0.5 ether);                // below reserve
        _bid(id, bob,   1.5 ether);                // newLead, clears reserve
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
        _bid(id, alice, 1 ether);
        _bid(id, bob, 2 ether);
        _bid(id, carol, 1.5 ether);
        // contract holds alice+bob+carol
        assertEq(address(ah).balance, 4.5 ether);
        assertEq(
            uint256(ah.cumulative(id, alice)) + ah.cumulative(id, bob) + ah.cumulative(id, carol),
            4.5 ether
        );
    }

    // ── ERC-1155 ────────────────────────────────────────────────────────────────

    function test_erc1155SettleTransfersAmount() public {
        vm.startPrank(seller);
        multi.mint(seller, 7, 5);
        multi.setApprovalForAll(address(ah), true);
        uint256 id = ah.create1155(address(multi), 7, 5, 1 ether, uint64(block.timestamp + 7 days), 500, 0);
        vm.stopPrank();
        _bid(id, alice, 2 ether);
        vm.warp(block.timestamp + 8 days);
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
        uint256 id = ah.create(address(nft), tid, amt, uint64(block.timestamp + 7 days), 0, 0);
        vm.stopPrank();
        _bid(id, alice, amt);
        vm.warp(block.timestamp + 8 days);
        uint256 sb = seller.balance; uint256 vb = feeRecipient.balance;
        ah.settle(id);
        assertEq(feeRecipient.balance - vb, _fee(amt));
        assertEq(seller.balance - sb, uint256(amt) - _fee(amt));
    }
}
