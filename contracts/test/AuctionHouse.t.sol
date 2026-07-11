// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {AuctionHouse, BidTooLow, AuctionLive, AuctionEnded, NotSeller, NotActive, NotSettled, InvalidAmount, CannotCancel, BidOverflow, NotKeeper} from "../src/AuctionHouse.sol";
import {MarketplaceManager} from "../src/MarketplaceManager.sol";
import {MockERC721} from "./MockERC721.sol";
import {MockERC1155} from "./MockERC1155.sol";
import {TestHelpers} from "./TestHelpers.sol";

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

contract AuctionHouseTest is Test, TestHelpers {
    AuctionHouse ah;
    MockERC721 nft;
    MockERC1155 multi;
    address feeRecipient = address(0x1111000000000000000000000000000000111100);
    address seller = address(0xBEEF);
    address alice = address(0xA11CE);
    address bob = address(0xB0B);
    address carol = address(0xCab01);

    function setUp() public {
        ah = _deployAuctionHouse(feeRecipient, address(0));
        nft = new MockERC721();
        multi = new MockERC1155();
        vm.deal(alice, 100 ether);
        vm.deal(bob, 100 ether);
        vm.deal(carol, 100 ether);
    }

    function _fee(uint128 v) internal pure returns (uint256) { return uint256(v) * 150 / 10_000; }

    function _create() internal returns (uint256 id, uint256 tid) {
        vm.startPrank(seller);
        tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        id = ah.create(address(nft), tid, 1 ether, uint64(block.timestamp + 24 hours), 500, 0);
        vm.stopPrank();
    }

    function _bid(uint256 id, address who, uint128 amt) internal {
        vm.prank(who);
        ah.bid{value: amt}(id);
    }

    function _leader(uint256 id) internal view returns (address l, uint128 t) {
        (,,,,,,,,,,, l, t,) = ah.auctions(id);
    }

    function test_firstBidAtReserveLeads() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        (address l, uint128 t) = _leader(id);
        assertEq(l, alice);
        assertEq(t, 1 ether);
        assertEq(ah.cumulative(id, alice), 1 ether);
    }

    function test_subReserveFirstBidReverts() public {
        (uint256 id,) = _create();
        vm.prank(alice);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: 0.4 ether}(id);
    }

    function test_outbidNoRefundThenReclaim() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        _bid(id, bob, 2 ether);
        assertEq(ah.cumulative(id, alice), 1 ether, "alice escrow stays");
        assertEq(alice.balance, 99 ether, "alice not refunded on outbid");
        (address l, uint128 t) = _leader(id);
        assertEq(l, bob); assertEq(t, 2 ether);
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
        _bid(id, alice, 1 ether);
        vm.prank(bob);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: 1.01 ether}(id);
    }

    function test_subReserveFirstBidReverts2() public {
        (uint256 id,) = _create();
        vm.prank(bob);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: 0.9 ether}(id);
    }

    function test_zeroBidReverts() public {
        (uint256 id,) = _create();
        vm.prank(alice);
        vm.expectRevert(InvalidAmount.selector);
        ah.bid{value: 0}(id);
    }

    function test_nearMaxLeaderBidDoesNotTruncate() public {
        (uint256 id,) = _create();
        uint128 nearMax = type(uint128).max - 0.01 ether;
        vm.deal(alice, uint256(nearMax) + 50 ether);
        vm.prank(alice);
        ah.bid{value: nearMax}(id);
        (address l, uint128 t) = _leader(id);
        assertEq(l, alice);
        assertEq(t, nearMax, "alice leads at nearMax");

        vm.deal(bob, type(uint128).max);
        vm.prank(bob);
        vm.expectRevert(BidOverflow.selector);
        ah.bid{value: type(uint128).max}(id);
        (address l2, uint128 t2) = _leader(id);
        assertEq(l2, alice, "alice still leader after BidOverflow");
        assertEq(t2, nearMax, "leaderTotal unchanged");

        (uint256 id2,) = _create();
        uint128 bobFirst = nearMax - 1 ether;
        vm.deal(bob, uint256(bobFirst) + 10 ether);
        vm.prank(bob);
        ah.bid{value: bobFirst}(id2);
        assertEq(ah.cumulative(id2, bob), bobFirst, "bob accumulated close to max");
        vm.prank(bob);
        vm.expectRevert(BidOverflow.selector);
        ah.bid{value: 1.5 ether}(id2);
    }

    function test_antiSnipeExtends() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint64 end = uint64(block.timestamp + 1 hours);
        uint256 id = ah.create(address(nft), tid, 1 ether, end, 500, 0);
        vm.stopPrank();
        vm.warp(end - 1 minutes);
        _bid(id, alice, 1 ether);
        (,,,,,,,uint64 newEnd,,,,,,) = ah.auctions(id);
        assertEq(newEnd, uint64(block.timestamp) + ah.EXTENSION_WINDOW());
    }

    function test_settleDistributesAndConsumesWinner() public {
        (uint256 id, uint256 tid) = _create();
        _bid(id, alice, 1 ether);
        _bid(id, bob, 3 ether);
        vm.warp(block.timestamp + 30 hours);
        uint256 sellerBefore = seller.balance;
        uint256 vaultBefore = feeRecipient.balance;
        ah.settle(id);
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
        vm.prank(carol);
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

    function test_settleNoBidsCancels() public {
        (uint256 id,) = _create();
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

    function test_refundLosersPaysNonWinnersSkipsWinner() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        _bid(id, bob, 2 ether);
        _bid(id, carol, 3 ether);
        vm.warp(block.timestamp + 30 hours);
        ah.settle(id);
        uint256 aBefore = alice.balance;
        uint256 bBefore = bob.balance;
        address[] memory batch = new address[](3);
        batch[0] = alice; batch[1] = bob; batch[2] = carol;
        ah.refundLosers(id, batch);
        assertEq(alice.balance, aBefore + 1 ether);
        assertEq(bob.balance, bBefore + 2 ether);
        assertEq(ah.cumulative(id, alice), 0);
        assertEq(ah.cumulative(id, bob), 0);
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

    function test_cancelEarlyNoBids() public {
        (uint256 id,) = _create();
        vm.prank(seller);
        ah.cancelEarly(id);
        (,,,bool settled,,,,,,,,,,) = ah.auctions(id);
        assertTrue(settled);
    }

    function test_cancelEarlyAfterReserveMetReverts() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        vm.prank(seller);
        vm.expectRevert(CannotCancel.selector);
        ah.cancelEarly(id);
    }

    function test_cancelEarlyAfterLeaderOvertakesReserveReverts() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        _bid(id, bob, 2 ether);
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

    function test_escrowEqualsSumOfCumulatives() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        _bid(id, bob, 3 ether);
        _bid(id, carol, 5 ether);
        assertEq(address(ah).balance, 9 ether);
        assertEq(
            uint256(ah.cumulative(id, alice)) + ah.cumulative(id, bob) + ah.cumulative(id, carol),
            9 ether
        );
    }

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

    function test_exactReserveMatchTakesLead() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        (address l, uint128 t) = _leader(id);
        assertEq(l, alice, "exact reserve match must lead");
        assertEq(t, 1 ether);
    }

    function test_oneWeiBelowReserveReverts() public {
        (uint256 id,) = _create();
        vm.prank(alice);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: 1 ether - 1}(id);
    }

    function test_antiSnipeOnlyExtendsOnNewLead() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint64 end = uint64(block.timestamp + 1 hours);
        uint256 id = ah.create(address(nft), tid, 1 ether, end, 500, 0);
        vm.stopPrank();
        _bid(id, alice, 2 ether);
        vm.warp(end - 1 minutes);
        vm.prank(bob);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: 0.5 ether}(id);
        (,,,,,,,uint64 newEnd,,,,,,) = ah.auctions(id);
        assertEq(newEnd, end, "timer NOT extended");
        _bid(id, bob, 3 ether);
        (,,,,,,,newEnd,,,,,,) = ah.auctions(id);
        assertGt(newEnd, end, "timer extended on newLead");
    }

    function test_leaderSelfTopUpIncreasesTotal() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        (address l, uint128 t) = _leader(id);
        assertEq(l, alice); assertEq(t, 1 ether);
        _bid(id, alice, 0.5 ether);
        (address l2, uint128 t2) = _leader(id);
        assertEq(l2, alice, "alice still leader");
        assertEq(t2, 1.5 ether, "leaderTotal increased");
        assertEq(ah.cumulative(id, alice), 1.5 ether);
    }

    function test_minBidIncrementFloorPreventsOneWeiLoop() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint256 id = ah.create(address(nft), tid, 1 ether, uint64(block.timestamp + 24 hours), 0, 0);
        vm.stopPrank();
        _bid(id, alice, 1 ether);
        vm.prank(bob);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: 1 ether + 1 wei}(id);
        uint128 qualifying = 1 ether + ah.MIN_BID_INCREMENT();
        vm.deal(bob, uint256(qualifying) + 10 ether);
        _bid(id, bob, qualifying);
        (address l,) = _leader(id);
        assertEq(l, bob, "bob leads after meeting min increment floor");
    }

    // ── 3-tier settle() gate ────────────────────────────────────────────────

    function test_settle_keeperAlwaysAllowed() public {
        MarketplaceManager gatedMgr = _deployMarketplaceManager(address(this));
        AuctionHouse gated = _deployAuctionHouse(feeRecipient, address(gatedMgr));
        gatedMgr.grantRole(gatedMgr.KEEPER_ROLE(), bob);
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(block.timestamp + 3 minutes), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        vm.warp(block.timestamp + 10 minutes);
        vm.prank(bob);
        gated.settle(id);
        (,,,bool settled,,,,,,,,,,) = gated.auctions(id);
        assertTrue(settled);
    }

    function test_settle_nonKeeperBlockedBeforeGrace() public {
        MarketplaceManager gatedMgr = _deployMarketplaceManager(address(this));
        AuctionHouse gated = _deployAuctionHouse(feeRecipient, address(gatedMgr));
        gatedMgr.grantRole(gatedMgr.KEEPER_ROLE(), bob);
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(block.timestamp + 3 minutes), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        vm.warp(block.timestamp + 10 minutes);
        vm.prank(carol);
        vm.expectRevert(NotKeeper.selector);
        gated.settle(id);
        (,,,bool settled,,,,,,,,,,) = gated.auctions(id);
        assertFalse(settled);
    }

    function test_settle_nonKeeperAllowedAfterGrace() public {
        MarketplaceManager gatedMgr = _deployMarketplaceManager(address(this));
        AuctionHouse gated = _deployAuctionHouse(feeRecipient, address(gatedMgr));
        gatedMgr.grantRole(gatedMgr.KEEPER_ROLE(), bob);
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(block.timestamp + 24 hours), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        vm.warp(block.timestamp + 24 hours + 24 hours + 2 hours);
        vm.prank(carol);
        gated.settle(id);
        (,,,bool settled,,,,,,,,,,) = gated.auctions(id);
        assertTrue(settled);
    }

    function test_settle_permissionlessWithNoManager() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        vm.warp(block.timestamp + 30 hours);
        vm.prank(carol);
        ah.settle(id);
        (,,,bool settled,,,,,,,,,,) = ah.auctions(id);
        assertTrue(settled);
    }

    function test_settle_sellerBlockedBefore5Min() public {
        MarketplaceManager gatedMgr = _deployMarketplaceManager(address(this));
        AuctionHouse gated = _deployAuctionHouse(feeRecipient, address(gatedMgr));
        gatedMgr.grantRole(gatedMgr.KEEPER_ROLE(), bob);
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(block.timestamp + 3 minutes), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        vm.warp(block.timestamp + 3 minutes + 3 minutes);
        vm.prank(seller);
        vm.expectRevert(NotKeeper.selector);
        gated.settle(id);
        (,,,bool settled,,,,,,,,,,) = gated.auctions(id);
        assertFalse(settled);
    }

    function test_settle_sellerAllowedAfter5Min() public {
        MarketplaceManager gatedMgr = _deployMarketplaceManager(address(this));
        AuctionHouse gated = _deployAuctionHouse(feeRecipient, address(gatedMgr));
        gatedMgr.grantRole(gatedMgr.KEEPER_ROLE(), bob);
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(block.timestamp + 3 minutes), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        vm.warp(block.timestamp + 3 minutes + 6 minutes);
        uint256 sellerBefore = seller.balance;
        vm.prank(seller);
        gated.settle(id);
        (,,,bool settled,,,,,,,,,,) = gated.auctions(id);
        assertTrue(settled);
        assertGt(seller.balance, sellerBefore);
    }

    function test_settle_winnerAllowedAfter5Min() public {
        MarketplaceManager gatedMgr = _deployMarketplaceManager(address(this));
        AuctionHouse gated = _deployAuctionHouse(feeRecipient, address(gatedMgr));
        gatedMgr.grantRole(gatedMgr.KEEPER_ROLE(), bob);
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(block.timestamp + 3 minutes), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        vm.warp(block.timestamp + 3 minutes + 6 minutes);
        vm.prank(alice);
        gated.settle(id);
        (,,,bool settled,,,,,,,,,,) = gated.auctions(id);
        assertTrue(settled);
        assertEq(nft.ownerOf(tid), alice);
    }

    function test_settle_randomBlockedBefore25Hr() public {
        MarketplaceManager gatedMgr = _deployMarketplaceManager(address(this));
        AuctionHouse gated = _deployAuctionHouse(feeRecipient, address(gatedMgr));
        gatedMgr.grantRole(gatedMgr.KEEPER_ROLE(), bob);
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(block.timestamp + 24 hours), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        vm.warp(block.timestamp + 24 hours + 10 hours);
        vm.prank(carol);
        vm.expectRevert(NotKeeper.selector);
        gated.settle(id);
        (,,,bool settled,,,,,,,,,,) = gated.auctions(id);
        assertFalse(settled);
    }

    function test_refundLosers_permissionlessWithManager() public {
        MarketplaceManager gatedMgr = _deployMarketplaceManager(address(this));
        AuctionHouse gated = _deployAuctionHouse(feeRecipient, address(gatedMgr));
        gatedMgr.grantRole(gatedMgr.KEEPER_ROLE(), bob);
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(block.timestamp + 3 minutes), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        vm.prank(bob);
        gated.bid{value: 2 ether}(id);
        vm.warp(block.timestamp + 10 minutes);
        vm.prank(bob);
        gated.settle(id);
        address[] memory batch = new address[](1);
        batch[0] = alice;
        uint256 aBefore = alice.balance;
        vm.prank(carol);
        gated.refundLosers(id, batch);
        assertEq(alice.balance, aBefore + 1 ether);
    }
}
