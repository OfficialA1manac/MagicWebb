// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {AuctionHouse, BidTooLow, AuctionLive, AuctionEnded, NotSeller, NotActive, NotSettled, InvalidAmount, CannotCancel, BidOverflow, NotAuthorized} from "../src/AuctionHouse.sol";
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
        ah = _deployAuctionHouse(feeRecipient, address(_deployMarketplaceManager()));
        nft = new MockERC721();
        multi = new MockERC1155();
        vm.deal(alice, 100 ether);
        vm.deal(bob, 100 ether);
        vm.deal(carol, 100 ether);
    }

    // 2% total fee = 1.5% feeRecipient + 0.5% sentinel keeper (TEST_SENTINEL_KEEPER).
    // _fee is the seller-side deduction; feeRecipient receives _platformCut, keeper _keeperCut.
    function _fee(uint128 v) internal pure returns (uint256) { return uint256(v) * 200 / 10_000; }

    function _create() internal returns (uint256 id, uint256 tid) {
        vm.startPrank(seller);
        tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        id = ah.create(address(nft), tid, 1 ether, uint64(24 hours));
        vm.stopPrank();
    }

    function _bid(uint256 id, address who, uint128 amt) internal {
        vm.prank(who);
        ah.bid{value: amt}(id);
    }

    function _leader(uint256 id) internal view returns (address l, uint128 t) {
        // v3.4: leaderTotal is a derived view (== cumulative[id][leader]), not
        // a stored field — read it alongside the packed struct.
        l = ah.getAuction(id).leader;
        t = ah.leaderTotal(id);
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

    function test_create_reserveAboveUint96RevertsBidOverflow() public {
        // v3.4 repack: reserve is stored uint96 (external ABI keeps uint128);
        // _create rejects anything wider outright.
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        vm.expectRevert(BidOverflow.selector);
        ah.create(address(nft), tid, uint128(type(uint96).max) + 1, uint64(24 hours));
        // Exactly uint96.max still creates.
        uint256 id = ah.create(address(nft), tid, type(uint96).max, uint64(24 hours));
        vm.stopPrank();
        assertEq(ah.getAuction(id).reserve, type(uint96).max);
    }

    function test_create1155_amountAboveUint96RevertsBidOverflow() public {
        // v3.4 repack: amount is stored uint96 alongside reserve. Mint past
        // the cap so the balance check (NotSeller) passes and the width bound
        // is what reverts.
        uint128 tooWide = uint128(type(uint96).max) + 1;
        vm.startPrank(seller);
        multi.mint(seller, 7, uint256(tooWide));
        multi.setApprovalForAll(address(ah), true);
        vm.expectRevert(BidOverflow.selector);
        ah.create1155(address(multi), 7, tooWide, 1 ether, uint64(24 hours));
        vm.stopPrank();
    }

    function test_antiSnipeExtends() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint64 end = uint64(block.timestamp + 1 hours);
        uint256 id = ah.create(address(nft), tid, 1 ether, 1 hours);
        vm.stopPrank();
        vm.warp(end - 1 minutes);
        _bid(id, alice, 1 ether);
        uint64 newEnd = ah.getAuction(id).endsAt;
        assertEq(newEnd, uint64(block.timestamp) + ah.EXTENSION_WINDOW());
    }

    function test_settleDistributesAndConsumesWinner() public {
        (uint256 id, uint256 tid) = _create();
        _bid(id, alice, 1 ether);
        _bid(id, bob, 3 ether);
        vm.warp(block.timestamp + 30 hours);
        uint256 sellerBefore = seller.balance;
        uint256 vaultBefore = feeRecipient.balance;
        uint256 kBefore = TEST_SENTINEL_KEEPER.balance;
        vm.prank(seller);
        ah.settle(id);
        assertEq(nft.ownerOf(tid), bob);
        assertEq(feeRecipient.balance, vaultBefore + _platformCut(3 ether));
        assertEq(TEST_SENTINEL_KEEPER.balance - kBefore, _keeperCut(3 ether));
        assertEq(seller.balance, sellerBefore + 3 ether - _fee(3 ether));
        assertEq(ah.cumulative(id, bob), 0, "winner escrow consumed");
        assertEq(ah.cumulative(id, alice), 1 ether, "loser escrow awaits refund");
    }

    function test_settle_thirdPartyReverts() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        vm.warp(block.timestamp + 30 hours);
        vm.prank(carol);
        vm.expectRevert(NotAuthorized.selector);
        ah.settle(id);
        bool settled = ah.getAuction(id).settled;
        assertFalse(settled);
    }

    function test_settle_winnerAllowed() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        vm.warp(block.timestamp + 30 hours);
        vm.prank(alice); // winner settles: parties never need the keeper
        ah.settle(id);
        bool settled = ah.getAuction(id).settled;
        assertTrue(settled);
    }

    function test_settle_sellerAllowed() public {
        (uint256 id,) = _create();
        _bid(id, bob, 1 ether);
        vm.warp(block.timestamp + 30 hours);
        vm.prank(seller); // seller settles: parties never need the keeper
        ah.settle(id);
        bool settled = ah.getAuction(id).settled;
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
        vm.prank(seller);
        ah.settle(id);
        bool settled = ah.getAuction(id).settled;
        assertTrue(settled);
    }

    function test_doubleSettleReverts() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        vm.warp(block.timestamp + 30 hours);
        vm.prank(seller);
        ah.settle(id);
        vm.expectRevert(NotActive.selector);
        vm.prank(seller);
        ah.settle(id);
    }

    function test_refundLosersPaysNonWinnersSkipsWinner() public {
        (uint256 id,) = _create();
        _bid(id, alice, 1 ether);
        _bid(id, bob, 2 ether);
        _bid(id, carol, 3 ether);
        vm.warp(block.timestamp + 30 hours);
        vm.prank(seller);
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
        bool settled = ah.getAuction(id).settled;
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
        uint256 id = ah.create1155(address(multi), 7, 5, 1 ether, uint64(24 hours));
        vm.stopPrank();
        _bid(id, alice, 2 ether);
        vm.warp(block.timestamp + 30 hours);
        uint256 sellerBefore = seller.balance;
        vm.prank(seller);
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
        uint256 id = ah.create(address(nft), tid, amt, uint64(24 hours));
        vm.stopPrank();
        _bid(id, alice, amt);
        vm.warp(block.timestamp + 30 hours);
        uint256 sb = seller.balance; uint256 vb = feeRecipient.balance; uint256 kb = TEST_SENTINEL_KEEPER.balance;
        vm.prank(seller);
        ah.settle(id);
        assertEq(feeRecipient.balance - vb, _platformCut(amt));
        assertEq(TEST_SENTINEL_KEEPER.balance - kb, _keeperCut(amt));
        assertEq(seller.balance - sb, uint256(amt) - _fee(amt));
    }

    function test_bid_flatOneTokenIncrement() public {
        // v3.3: ONE rule everywhere — overtaking costs exactly leader + 1
        // native token, and cumulative escrow counts. Alice leads at 10;
        // bob's 10.999... total must revert, 11 total takes the lead.
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint256 id = ah.create(address(nft), tid, 1 ether, uint64(24 hours));
        vm.stopPrank();
        _bid(id, alice, 10 ether);
        vm.prank(bob);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: 11 ether - 1}(id);
        _bid(id, bob, 11 ether);
        (address l, uint128 t) = _leader(id);
        assertEq(l, bob, "bob overtakes at exactly leader + 1 token");
        assertEq(t, 11 ether, "leaderTotal is bob's cumulative");
        // Owner's worked example: leader 500 total, challenger already has
        // 200 escrowed -> must send 301+ so cumulative reaches 501+.
        vm.prank(alice); // alice has 10 escrowed, leader is 11
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: 1 ether}(id); // 10 + 1 = 11 == leader, not leader+1
        vm.prank(alice);
        ah.bid{value: 2 ether}(id); // 10 + 2 = 12 >= 11 + 1
        (address l2, uint128 t2) = _leader(id);
        assertEq(l2, alice, "top-up counts existing escrow toward the +1 rule");
        assertEq(t2, 12 ether, "cumulative model intact");
    }

    function test_bid_flatIncrementIgnored() public {
        // v3.4 deleted the vestigial minIncrementFlat field outright — the only
        // overtaking step is the marketplace-wide 1-ether floor.
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint256 id = ah.create(address(nft), tid, 1 ether, uint64(24 hours));
        vm.stopPrank();
        _bid(id, alice, 1 ether);
        _bid(id, bob, 2 ether); // leader + 1-ether floor, no BidOverflow
        (address l,) = _leader(id);
        assertEq(l, bob, "flat increment is ignored; 1-ether floor applies");
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
        uint256 id = ah.create(address(nft), tid, 1 ether, 1 hours);
        vm.stopPrank();
        _bid(id, alice, 2 ether);
        vm.warp(end - 1 minutes);
        vm.prank(bob);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: 0.5 ether}(id);
        uint64 newEnd = ah.getAuction(id).endsAt;
        assertEq(newEnd, end, "timer NOT extended");
        _bid(id, bob, 3 ether);
        newEnd = ah.getAuction(id).endsAt;
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
        uint256 id = ah.create(address(nft), tid, 1 ether, uint64(24 hours));
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

    // ── settle() gate: keeper + seller/winner only ────────────────────────────────────────────────

    function test_settle_keeperAlwaysAllowed() public {
        MarketplaceManager gatedMgr = _deployMarketplaceManager(address(this));
        AuctionHouse gated = _deployAuctionHouse(feeRecipient, address(gatedMgr));
        gatedMgr.setKeeper(bob);
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(3 minutes));
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        vm.warp(block.timestamp + 10 minutes);
        vm.prank(bob);
        gated.settle(id);
        bool settled = gated.getAuction(id).settled;
        assertTrue(settled);
    }

    function test_settle_thirdPartyBlocked() public {
        MarketplaceManager gatedMgr = _deployMarketplaceManager(address(this));
        AuctionHouse gated = _deployAuctionHouse(feeRecipient, address(gatedMgr));
        gatedMgr.setKeeper(bob);
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(3 minutes));
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        vm.warp(block.timestamp + 10 minutes);
        vm.prank(carol);
        vm.expectRevert(NotAuthorized.selector);
        gated.settle(id);
        bool settled = gated.getAuction(id).settled;
        assertFalse(settled);
    }

    function test_settle_sellerAllowedImmediately() public {
        MarketplaceManager gatedMgr = _deployMarketplaceManager(address(this));
        AuctionHouse gated = _deployAuctionHouse(feeRecipient, address(gatedMgr));
        gatedMgr.setKeeper(bob);
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(3 minutes));
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        // Product rule 2026-08-23: seller and winner settle any time after
        // endsAt — no cooldown. A third party still cannot.
        vm.warp(block.timestamp + 3 minutes + 1);
        vm.prank(address(0xD00D));
        vm.expectRevert(NotAuthorized.selector);
        gated.settle(id);
        vm.prank(seller);
        gated.settle(id);
        bool settled = gated.getAuction(id).settled;
        assertTrue(settled);
    }

    function test_settle_sellerAllowedAfter5Min() public {
        MarketplaceManager gatedMgr = _deployMarketplaceManager(address(this));
        AuctionHouse gated = _deployAuctionHouse(feeRecipient, address(gatedMgr));
        gatedMgr.setKeeper(bob);
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(3 minutes));
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        vm.warp(block.timestamp + 3 minutes + 6 minutes);
        uint256 sellerBefore = seller.balance;
        vm.prank(seller);
        gated.settle(id);
        bool settled = gated.getAuction(id).settled;
        assertTrue(settled);
        assertGt(seller.balance, sellerBefore);
    }

    function test_settle_winnerAllowedAfter5Min() public {
        MarketplaceManager gatedMgr = _deployMarketplaceManager(address(this));
        AuctionHouse gated = _deployAuctionHouse(feeRecipient, address(gatedMgr));
        gatedMgr.setKeeper(bob);
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(3 minutes));
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        vm.warp(block.timestamp + 3 minutes + 6 minutes);
        vm.prank(alice);
        gated.settle(id);
        bool settled = gated.getAuction(id).settled;
        assertTrue(settled);
        assertEq(nft.ownerOf(tid), alice);
    }

    function test_settle_randomBlockedForever() public {
        MarketplaceManager gatedMgr = _deployMarketplaceManager(address(this));
        AuctionHouse gated = _deployAuctionHouse(feeRecipient, address(gatedMgr));
        gatedMgr.setKeeper(bob);
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(24 hours));
        vm.stopPrank();
        vm.prank(alice);
        gated.bid{value: 1 ether}(id);
        // No time ever unlocks third-party settlement — not 25h, not 30 days.
        vm.warp(block.timestamp + 30 days);
        vm.prank(carol);
        vm.expectRevert(NotAuthorized.selector);
        gated.settle(id);
        bool settled = gated.getAuction(id).settled;
        assertFalse(settled);
    }

    function test_refundLosers_permissionlessWithManager() public {
        MarketplaceManager gatedMgr = _deployMarketplaceManager(address(this));
        AuctionHouse gated = _deployAuctionHouse(feeRecipient, address(gatedMgr));
        gatedMgr.setKeeper(bob);
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(gated), true);
        uint256 id = gated.create(address(nft), tid, 1 ether, uint64(3 minutes));
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
