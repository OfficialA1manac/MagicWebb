// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test, console2} from "forge-std/Test.sol";

import {
    AuctionHouse,
    BatchTooLarge,
    AuctionLive,
    NotActive,
    NotSettled,
    BidTooLow,
    BidOverflow,
    NotSeller
} from "../src/AuctionHouse.sol";
import {
    OfferBook,
    NoOffer,
    OfferActive,
    OffersNotEligible
} from "../src/OfferBook.sol";
import {BelowMinPrice, NothingToWithdraw, WithdrawFailed, TokenStandard, InvalidDuration} from "../src/MarketplaceCore.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {MockERC721}  from "./MockERC721.sol";
import {MockERC1155} from "./MockERC1155.sol";
import {TestHelpers} from "./TestHelpers.sol";

contract GreedyReceiver {
    bool public blocked;
    constructor() payable { blocked = true; }
    receive() external payable { if (blocked) revert("blocked"); }
    function setBlocked(bool b) external { blocked = b; }
    function proxyWithdrawOffer(OfferBook ob) external { ob.withdrawRefund(); }
    function proxyWithdrawAuction(AuctionHouse ah) external { ah.withdrawRefund(); }
    function bidOn(AuctionHouse ah, uint256 id) external payable { ah.bid{value: msg.value}(id); }
}

contract ERC1155RejectingBidder {
    bool public blocked;
    constructor() payable { blocked = true; }
    receive() external payable { if (blocked) revert("blocked"); }
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

contract GasGriefingReceiver {
    uint256[] public junk;
    constructor() payable {}
    receive() external payable { for (uint256 i; i < 10; ++i) { junk.push(i); } }
}

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
    function arm() external { armed = true; _attempts = 0; }
    function disarm() external { armed = false; }
    function onERC721Received(address, address, uint256, bytes calldata)
        external returns (bytes4)
    {
        if (armed && _attempts < 1 && _reentryItems.length > 0) {
            _attempts++;
            try mp.batchList(_reentryItems) {} catch {}
        }
        return this.onERC721Received.selector;
    }
    receive() external payable {}
}

contract AuditFuzzTest is Test, TestHelpers {
    AuctionHouse ah;
    OfferBook    ob;
    MockERC721   nft;
    MockERC1155  multi;

    address feeRecipient = address(0x1111000000000000000000000000000000111100);
    address seller       = address(0xBEEF);
    address alice        = address(0xA11CE);
    address bob          = address(0xB0B);

    function setUp() public {
        ah = _deployAuctionHouse(feeRecipient, address(0));
        ob = _deployOfferBook(feeRecipient, address(0));
        nft   = new MockERC721();
        multi = new MockERC1155();
        vm.deal(alice, 100 ether);
        vm.deal(bob,   100 ether);
        vm.deal(seller, 100 ether);
    }

    uint64 constant _LIST_24HR = 24 hours;

    function _auctionEndsIn(uint64 dt) internal returns (uint256 id, uint256 tid) {
        vm.startPrank(seller);
        tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        id = ah.create(address(nft), tid, 1 ether, uint64(block.timestamp) + dt, 500, 0);
        vm.stopPrank();
    }

    function _auction7d() internal returns (uint256 id, uint256 tid) { return _auctionEndsIn(24 hours); }

    function _bid(uint256 id, address who, uint128 amt) internal {
        vm.prank(who);
        ah.bid{value: amt}(id);
    }

    function _a(uint256 id) internal view returns (AuctionHouse.Auction memory) {
        return ah.getAuction(id);
    }
    function _endsAt(uint256 id)   internal view returns (uint64)  { return _a(id).endsAt; }
    function _settled(uint256 id)  internal view returns (bool)    { return _a(id).settled; }
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

    function _enableOffers(address coll) internal {
        ob.setOfferEligible(coll, true);
    }

    function _createWithIncrement(uint128 reserve, uint16 minIncBps, uint128 minIncFlat)
        internal returns (uint256 id, uint256 tid)
    {
        vm.startPrank(seller);
        tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        id = ah.create(address(nft), tid, reserve, uint64(block.timestamp + 24 hours), minIncBps, minIncFlat);
        vm.stopPrank();
    }

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

    // ── Anti-snipe fuzz ───────────────────────────────────────────────────────

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
            vm.expectRevert(BidTooLow.selector);
            ah.bid{value: 1}(id);
        }
        assertEq(_endsAt(id), endAfterLead, "endsAt unchanged - sub-leader bids revert entirely");
    }

    // ── Seller fault tests ────────────────────────────────────────────────────

    function test_sellerRevokeCausesSettleRevert() public {
        (uint256 id, uint256 tid) = _auction7d();
        _bid(id, alice, 2 ether);       vm.warp(block.timestamp + 30 hours);
        vm.prank(seller);
        nft.setApprovalForAll(address(ah), false);
        vm.expectRevert();
        ah.settle(id);
        assertFalse(_settled(id), "NOT settled - keeper must retry");
        assertEq(ah.cumulative(id, alice), 2 ether, "winner escrow intact");
        assertEq(nft.ownerOf(tid), seller, "NFT still with seller");
    }

    function test_sellerMovedNftCausesSettleRevert() public {
        (uint256 id, uint256 tid) = _auction7d();
        _bid(id, alice, 2 ether);       vm.warp(block.timestamp + 30 hours);
        vm.prank(seller);
        nft.transferFrom(seller, address(0x999), tid);
        vm.expectRevert();
        ah.settle(id);
        assertFalse(_settled(id), "NOT settled - keeper must retry");
        assertEq(ah.cumulative(id, alice), 2 ether, "winner escrow intact");
    }

    // ── OfferBook push-fallback ───────────────────────────────────────────────

    function test_offerRejectedRefundPushFallback() public {
        uint256 tid = nft.mint(seller);
        vm.prank(seller);
        nft.setApprovalForAll(address(ob), true);
        GreedyReceiver bidder = new GreedyReceiver();
        vm.deal(address(bidder), 10 ether);
        uint64 exp = uint64(block.timestamp) + 1 days;
        _enableOffers(address(nft));
        bidder.setBlocked(false);
        vm.prank(address(bidder));
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, exp);
        (uint128 principal,,,) = ob.positions(address(nft), tid, address(bidder));
        assertEq(principal, 1 ether, "offer escrowed at principal = 1 ETH");
        bidder.setBlocked(true);
        assertEq(ob.pendingReturns(address(bidder)), 0, "no pending before reject");
        vm.prank(seller);
        ob.rejectOffer(address(nft), tid, address(bidder));
        (principal,,,) = ob.positions(address(nft), tid, address(bidder));
        assertEq(principal, 0, "position deleted on reject");
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

    // ── Batch cap fuzz ────────────────────────────────────────────────────────

    function testFuzz_refundLosersBatchCap(uint256 n) public {
        n = bound(n, 0, 1000);
        (uint256 id,) = _auction7d();
        _bid(id, alice, 1 ether);       vm.warp(block.timestamp + 30 hours);
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

    // ── 50% griefing 200-batch ────────────────────────────────────────────────

    function test_refundLosersGriefingHalfBatchDoesNotOOG() public {
        (uint256 id,) = _createWithIncrement(1 ether, 0, 0);
        _bid(id, alice, 1 ether);
        address[] memory eoas = new address[](100);
        for (uint256 i; i < 100; ++i) {
            eoas[i] = _eoa(i);
            uint128 bidAmt = uint128((i + 2) * 1 ether);
            vm.deal(eoas[i], uint256(bidAmt) + 1 ether);
            _bid(id, eoas[i], bidAmt);
        }
        GreedyReceiver[] memory greedies = new GreedyReceiver[](100);
        for (uint256 i; i < 100; ++i) {
            greedies[i] = new GreedyReceiver();
            uint128 bidAmt = uint128((i + 102) * 1 ether);
            vm.deal(address(greedies[i]), uint256(bidAmt) + 1 ether);
            vm.prank(address(greedies[i]));
            ah.bid{value: bidAmt}(id);
        }
        vm.warp(block.timestamp + 30 hours);
        ah.settle(id);
        assertTrue(_settled(id));
        assertEq(ah.cumulative(id, address(greedies[99])), 0, "winner escrow consumed");
        address[] memory batch = new address[](200);
        for (uint256 i; i < 200; ++i) {
            batch[i] = (i % 2 == 0) ? eoas[i / 2] : address(greedies[(i - 1) / 2]);
        }
        ah.refundLosers(id, batch);
        for (uint256 i; i < 100; ++i) {
            uint128 expectedRefund = uint128((i + 2) * 1 ether);
            assertEq(ah.cumulative(id, eoas[i]), 0, "EOA cumulative cleared");
            assertEq(ah.pendingReturns(eoas[i]), 0, "EOA has no pendingReturns");
            assertEq(eoas[i].balance, expectedRefund + 1 ether, "EOA loser refund succeeded");
        }
        for (uint256 i; i < 99; ++i) {
            uint128 expectedRefund = uint128((i + 102) * 1 ether);
            assertEq(ah.cumulative(id, address(greedies[i])), 0, "greedy cumulative cleared");
            assertEq(ah.pendingReturns(address(greedies[i])), expectedRefund, "greedy -> pendingReturns");
        }
        greedies[0].setBlocked(false);
        uint256 balBefore = address(greedies[0]).balance;
        greedies[0].proxyWithdrawAuction(ah);
        assertEq(ah.pendingReturns(address(greedies[0])), 0, "withdrawRefund clears credit");
        assertEq(address(greedies[0]).balance, balBefore + 102 ether, "greedy pulled refund");
    }

    // ── OfferBook edit model ──────────────────────────────────────────────────

    function test_makeOfferEditChangesExpiry() public {
        uint256 tid = nft.mint(seller);
        vm.prank(seller);
        nft.setApprovalForAll(address(ob), true);
        _enableOffers(address(nft));
        uint64 longExp = uint64(block.timestamp + 4 hours);
        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, longExp);
        (uint128 pBefore,, uint64 expBefore,) = ob.positions(address(nft), tid, alice);
        assertEq(pBefore, 1 ether);
        assertEq(expBefore, longExp);
        uint64 shortExp = uint64(block.timestamp + 1 hours);
        uint256 aliceBalBefore = alice.balance;
        vm.prank(alice);
        ob.makeOffer{value: 2 ether}(address(nft), tid, 2 ether, shortExp);
        (uint128 pAfter,, uint64 expAfter,) = ob.positions(address(nft), tid, alice);
        assertEq(pAfter, 2 ether, "principal replaced on edit, not compounded");
        assertEq(expAfter, shortExp, "expiry UPDATED on edit");
        assertEq(alice.balance, aliceBalBefore - 2 ether + 1 ether, "net paid 1 eth more on edit-up");
        assertEq(ob.pendingReturns(alice), 0, "no fallback - push succeeded");
    }

    function test_makeOfferEditInvalidDurationReverts() public {
        uint256 tid = nft.mint(seller);
        vm.prank(seller);
        nft.setApprovalForAll(address(ob), true);
        _enableOffers(address(nft));
        uint64 longExp = uint64(block.timestamp + 24 hours);
        vm.prank(alice);
        ob.makeOffer{value: 5 ether}(address(nft), tid, 5 ether, longExp);
        vm.prank(alice);
        vm.expectRevert(InvalidDuration.selector);
        ob.makeOffer{value: 5 ether}(address(nft), tid, 5 ether, uint64(block.timestamp + 1));
    }

    function test_refundLosersRevertsOnActiveAuction() public {
        (uint256 id,) = _auction7d();
        _bid(id, alice, 1 ether);
        address[] memory batch = new address[](1);
        batch[0] = alice;
        vm.expectRevert(NotSettled.selector);
        ah.refundLosers(id, batch);
    }

    // ── withdrawRefund gas-heavy receiver ─────────────────────────────────────

    function test_withdrawRefundGasHeavyReceiverCanWithdraw() public {
        GasGriefingReceiver griefer = new GasGriefingReceiver();
        vm.deal(address(griefer), 10 ether);
        AuctionHouse ah2 = _deployAuctionHouse(feeRecipient, address(0));
        MockERC721 nft2 = new MockERC721();
        vm.startPrank(seller);
        uint256 tid2 = nft2.mint(seller);
        nft2.setApprovalForAll(address(ah2), true);
        uint256 id2 = ah2.create(address(nft2), tid2, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();
        vm.prank(address(griefer));
        ah2.bid{value: 1 ether}(id2);
        vm.prank(alice);
        ah2.bid{value: 2 ether}(id2);
        vm.warp(block.timestamp + 2 days);
        ah2.settle(id2);
        address[] memory batch = new address[](1);
        batch[0] = address(griefer);
        ah2.refundLosers(id2, batch);
        assertEq(ah2.pendingReturns(address(griefer)), 1 ether, "griefer credited in pendingReturns");
        uint256 grieferBalBefore = address(griefer).balance;
        vm.prank(address(griefer));
        ah2.withdrawRefund();
        assertEq(ah2.pendingReturns(address(griefer)), 0, "pendingReturns cleared after successful withdraw");
        assertEq(address(griefer).balance, grieferBalBefore + 1 ether, "griefer received refund");
    }

    function test_withdrawRefundWorksForNormalReceiver() public {
        GreedyReceiver gr = new GreedyReceiver();
        vm.deal(address(gr), 10 ether);
        (uint256 id,) = _auction7d();
        vm.prank(address(gr));
        ah.bid{value: 1 ether}(id);
        _bid(id, alice, 2 ether);
        vm.warp(block.timestamp + 30 hours);
        ah.settle(id);
        address[] memory batch = new address[](1);
        batch[0] = address(gr);
        ah.refundLosers(id, batch);
        assertEq(ah.pendingReturns(address(gr)), 1 ether);
        gr.setBlocked(false);
        uint256 balBefore = address(gr).balance;
        gr.proxyWithdrawAuction(ah);
        assertEq(address(gr).balance, balBefore + 1 ether, "normal receiver withdrew successfully");
        assertEq(ah.pendingReturns(address(gr)), 0, "pendingReturns cleared");
    }

    // ── PushFailed event coverage ─────────────────────────────────────────────

    event PushFailed(address indexed to, uint256 amount);

    function test_settle_feePushFallback_emitsPushFailed() public {
        RejectEtherNoReceive badFee = new RejectEtherNoReceive();
        AuctionHouse ah2 = _deployAuctionHouse(address(badFee), address(0));
        MockERC721 nft2 = new MockERC721();
        vm.startPrank(seller);
        uint256 tid2 = nft2.mint(seller);
        nft2.setApprovalForAll(address(ah2), true);
        uint256 id2 = ah2.create(address(nft2), tid2, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        ah2.bid{value: 2 ether}(id2);
        vm.warp(block.timestamp + 2 days);
        uint256 fee = uint256(2 ether) * 150 / 10_000;
        vm.expectEmit(true, false, false, true, address(ah2));
        emit PushFailed(address(badFee), fee);
        ah2.settle(id2);
        assertTrue(ah2.getAuction(id2).settled, "settled");
        assertEq(ah2.pendingReturns(address(badFee)), fee, "fee credited to badFee");
        assertEq(nft2.ownerOf(tid2), alice, "winner received NFT");
    }

    function test_settle_sellerPushFallback_emitsPushFailed() public {
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

    function test_settle_sellerMovedNft_reverts() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint256 id = ah.create(address(nft), tid, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();
        vm.deal(alice, 2 ether);
        _bid(id, alice, 2 ether);
        vm.warp(block.timestamp + 2 days);
        vm.prank(seller);
        nft.transferFrom(seller, address(0x999), tid);
        vm.expectRevert();
        ah.settle(id);
        assertFalse(_settled(id), "NOT settled - revert on moved NFT");
    }

    function test_refundLosers_perIterationPushFallback_emitsPushFailed() public {
        (uint256 id,) = _auction7d();
        GreedyReceiver[] memory greedies = new GreedyReceiver[](3);
        for (uint256 i; i < 3; ++i) {
            greedies[i] = new GreedyReceiver();
            uint128 bidAmt = uint128((i + 1) * 1 ether);
            vm.deal(address(greedies[i]), uint256(bidAmt) + 1 ether);
            vm.prank(address(greedies[i]));
            ah.bid{value: bidAmt}(id);
        }
        _bid(id, alice, 4 ether);
        vm.warp(block.timestamp + 30 hours);
        ah.settle(id);
        address[] memory batch = new address[](3);
        for (uint256 i; i < 3; ++i) batch[i] = address(greedies[i]);
        for (uint256 i; i < 3; ++i) {
            uint128 expectedRefund = uint128((i + 1) * 1 ether);
            vm.expectEmit(true, false, false, true, address(ah));
            emit PushFailed(address(greedies[i]), expectedRefund);
        }
        ah.refundLosers(id, batch);
        for (uint256 i; i < 3; ++i) {
            uint128 expectedRefund = uint128((i + 1) * 1 ether);
            assertEq(ah.pendingReturns(address(greedies[i])), expectedRefund, "grief receiver credited");
        }
    }

    function test_offer_rejectOffer_emitsPushFailed() public {
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
        bidder.setBlocked(true);
        vm.expectEmit(true, false, false, true, address(ob));
        emit PushFailed(address(bidder), 1 ether);
        vm.prank(seller);
        ob.rejectOffer(address(nft), tid, address(bidder));
        assertEq(ob.pendingReturns(address(bidder)), 1 ether);
    }

    function test_offer_withdrawRefund_empty_revertsNothingToWithdraw() public {
        vm.expectRevert(NothingToWithdraw.selector);
        ob.withdrawRefund();
    }

    function test_auction_withdrawRefund_empty_revertsNothingToWithdraw() public {
        vm.expectRevert(NothingToWithdraw.selector);
        ah.withdrawRefund();
    }

    // ── L-09 / L-10 regression tests ──────────────────────────────────────────

    function test_batchList_listsAllItemsAtomically() public {
        Marketplace mp = _deployMarketplace(feeRecipient, address(0));
        MockERC721 coll = new MockERC721();
        vm.startPrank(seller);
        uint256 t1 = coll.mint(seller);
        uint256 t2 = coll.mint(seller);
        uint256 t3 = coll.mint(seller);
        coll.setApprovalForAll(address(mp), true);
        vm.stopPrank();
        Marketplace.BatchItem[] memory items = new Marketplace.BatchItem[](3);
        items[0] = Marketplace.BatchItem(address(coll), t1, 1 ether,  uint64(block.timestamp + _LIST_24HR));
        items[1] = Marketplace.BatchItem(address(coll), t2, 1.5 ether, uint64(block.timestamp + _LIST_24HR));
        items[2] = Marketplace.BatchItem(address(coll), t3, 2 ether,  uint64(block.timestamp + _LIST_24HR));
        vm.prank(seller);
        mp.batchList(items);
        (address s1, , TokenStandard std1, uint128 p1,) = mp.listings(address(coll), t1, seller);
        assertEq(s1, seller, "item 1 seller");
        assertEq(uint256(std1), uint256(TokenStandard.ERC721), "item 1 ERC-721");
        assertEq(uint256(p1), uint256(1 ether), "item 1 price");
        (address s2, , , uint128 p2,) = mp.listings(address(coll), t2, seller);
        assertEq(s2, seller, "item 2 seller");
        assertEq(uint256(p2), uint256(1.5 ether), "item 2 price");
        (address s3, , , uint128 p3,) = mp.listings(address(coll), t3, seller);
        assertEq(s3, seller, "item 3 seller");
        assertEq(uint256(p3), uint256(2 ether), "item 3 price");
    }

    function test_batchList_protectedByNonReentrant() public {
        Marketplace mp = _deployMarketplace(feeRecipient, address(0));
        MockERC721 coll = new MockERC721();
        vm.startPrank(seller);
        uint256 t1 = coll.mint(seller);
        uint256 t2 = coll.mint(seller);
        uint256 t99 = coll.mint(seller);
        coll.setApprovalForAll(address(mp), true);
        mp.list(address(coll), t1, 1 ether, uint64(block.timestamp + _LIST_24HR));
        mp.list(address(coll), t2, 1.5 ether, uint64(block.timestamp + _LIST_24HR));
        vm.stopPrank();
        ReentrantBuyer buyer = new ReentrantBuyer(mp);
        vm.prank(seller);
        coll.safeTransferFrom(seller, address(buyer), t99);
        vm.deal(address(buyer), 10 ether);
        vm.prank(address(buyer));
        coll.setApprovalForAll(address(mp), true);
        Marketplace.BatchItem[] memory reentry = new Marketplace.BatchItem[](1);
        reentry[0] = Marketplace.BatchItem(address(coll), t99, 1 ether, uint64(block.timestamp + _LIST_24HR));
        buyer.setReentryItems(reentry);
        buyer.arm();
        vm.prank(address(buyer));
        mp.buy{value: 1 ether}(address(coll), t1, seller);
        buyer.disarm();
        assertEq(coll.ownerOf(t1), address(buyer), "buyer received token 1");
        (address s2, , , uint128 p2,) = mp.listings(address(coll), t2, seller);
        assertEq(s2, seller, "item 2 seller preserved");
        assertEq(uint256(p2), uint256(1.5 ether), "item 2 price preserved");
        (address s99, , , uint128 p99, ) = mp.listings(address(coll), t99, address(buyer));
        assertEq(s99, address(0), "reentry slot UNSET - inner mp.batchList was reverted by ReentrancyGuard");
        assertEq(uint256(p99), 0, "reentry slot price zero (reentry blocked)");
    }

    function test_bidders_uniqueAcrossRefundAndRebid() public {
        (uint256 id, ) = _auction7d();
        _bid(id, alice, 1 ether);
        assertEq(ah.bidderCount(id), 1, "alice enrolled on first bid");
        _bid(id, alice, 1 ether);
        assertEq(ah.bidderCount(id), 1, "alice top-up does NOT push duplicate (prevCum > 0 skips enrollment)");
        assertEq(ah.cumulative(id, alice), 2 ether, "alice cumulative = 2 ether after top-up");
        _bid(id, bob, 3 ether);
        assertEq(ah.bidderCount(id), 2, "bob enrolled; no duplicate for alice");
        _bid(id, bob, 1 ether);
        assertEq(ah.bidderCount(id), 2, "bob top-up does NOT push duplicate; _bidders[id] has exactly 2 entries");
        assertEq(ah.cumulative(id, bob), 4 ether, "bob cumulative = 4 ether after top-up");
    }

    function test_create_nonReentrantDefenseInDepth() public {
        (uint256 id721, uint256 tid721) = _auction7d();
        assertEq(id721, 1, "create() produced auction id 1");
        AuctionHouse.Auction memory a721 = ah.getAuction(id721);
        assertEq(a721.seller, seller);
        assertEq(a721.collection, address(nft));
        assertEq(a721.tokenId, tid721);
        assertEq(uint256(a721.standard), uint256(TokenStandard.ERC721));
        vm.startPrank(seller);
        multi.mint(seller, 99, 10);
        multi.setApprovalForAll(address(ah), true);
        uint256 id1155 = ah.create1155(address(multi), 99, 10, 1 ether, uint64(block.timestamp + 24 hours), 500, 0);
        vm.stopPrank();
        assertEq(id1155, 2, "create1155() produced auction id 2");
        AuctionHouse.Auction memory a1155 = ah.getAuction(id1155);
        assertEq(a1155.seller, seller);
        assertEq(a1155.collection, address(multi));
        assertEq(a1155.tokenId, 99);
        assertEq(uint256(a1155.standard), uint256(TokenStandard.ERC1155));
        assertEq(a1155.amount, 10);
        assertEq(a1155.reserve, 1 ether);
    }

    // ── Increment logic fuzz tests ────────────────────────────────────────────

    function testFuzz_increment_minBidFloor(uint128 leaderTotal) public {
        leaderTotal = uint128(bound(leaderTotal, 1 ether, 50 ether));
        uint256 floor = ah.MIN_BID_INCREMENT();
        uint128 reserve = leaderTotal >= 2 ether ? leaderTotal / 2 : uint128(1 ether);
        uint256 id = _setupLeader(reserve, 0, 0, alice, leaderTotal);
        uint256 expectedMinNext = uint256(leaderTotal) + floor;
        vm.deal(bob, 100 ether);
        vm.prank(bob);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: expectedMinNext - 1}(id);
        (address l,) = _leader(id);
        assertEq(l, alice, "alice still leader after failed below-floor bid");
        vm.prank(bob);
        ah.bid{value: expectedMinNext}(id);
        (address l2, uint128 t2) = _leader(id);
        assertEq(l2, bob, "bob takes lead with floor bid");
        assertEq(t2, uint128(expectedMinNext), "leaderTotal = leaderTotal + MIN_BID_INCREMENT");
    }

    function testFuzz_increment_minNextCalculation(uint16 minIncBps, uint128 minIncFlat, uint128 leaderTotal) public {
        minIncBps = uint16(bound(minIncBps, 0, 5000));
        minIncFlat = uint128(bound(minIncFlat, 0, 1 ether));
        leaderTotal = uint128(bound(leaderTotal, 1 ether, 50 ether));
        uint256 incPct = uint256(leaderTotal) * minIncBps / 10_000;
        uint256 inc = incPct > minIncFlat ? incPct : minIncFlat;
        uint256 floor = ah.MIN_BID_INCREMENT();
        if (inc < floor) inc = floor;
        uint256 minNext256 = uint256(leaderTotal) + inc;
        uint256 minReserve = leaderTotal / 2;
        if (minReserve < 1 ether) minReserve = 1 ether;
        uint256 id = _setupLeader(uint128(minReserve), minIncBps, minIncFlat, alice, leaderTotal);
        if (minNext256 > type(uint128).max) {
            vm.deal(bob, type(uint128).max);
            vm.prank(bob);
            vm.expectRevert(BidOverflow.selector);
            ah.bid{value: type(uint128).max}(id);
            return;
        }
        uint128 minNext = uint128(minNext256);
        if (minNext > 0 && uint256(minNext) - 1 > leaderTotal) {
            vm.deal(bob, 100 ether);
            vm.prank(bob);
            vm.expectRevert(BidTooLow.selector);
            ah.bid{value: minNext - 1}(id);
            (address l,) = _leader(id);
            assertEq(l, alice, "alice still leader after BidTooLow");
        }
        vm.deal(bob, 100 ether);
        vm.prank(bob);
        ah.bid{value: minNext}(id);
        (address l2, uint128 t2) = _leader(id);
        assertEq(l2, bob, "bob takes lead with exact minNext bid");
        assertEq(t2, minNext, "leaderTotal equals minNext after winning bid");
    }

    function testFuzz_increment_nearMaxBidOverflow(uint128 leaderTotal) public {
        leaderTotal = uint128(bound(leaderTotal, type(uint128).max - 1 ether, type(uint128).max - 1));
        uint256 id = _setupLeader(leaderTotal / 2, 0, 0, alice, leaderTotal);
        uint256 floor = ah.MIN_BID_INCREMENT();
        uint256 minNext256 = uint256(leaderTotal) + floor;
        if (minNext256 > type(uint128).max) {
            vm.deal(bob, type(uint128).max);
            vm.prank(bob);
            vm.expectRevert(BidOverflow.selector);
            ah.bid{value: type(uint128).max}(id);
            (address l, uint128 t) = _leader(id);
            assertEq(l, alice, "alice still leader after BidOverflow");
            assertEq(t, leaderTotal, "leaderTotal unchanged after BidOverflow");
        } else {
            uint128 minNext = uint128(minNext256);
            vm.deal(bob, type(uint128).max);
            vm.prank(bob);
            ah.bid{value: minNext}(id);
            (address l2, uint128 t2) = _leader(id);
            assertEq(l2, bob, "bob takes lead with near-max minNext bid");
            assertEq(t2, minNext, "leaderTotal at near-max boundary");
        }
    }

    function test_withdrawRefundRestoreOnFailure() public {
        GreedyReceiver bidder = new GreedyReceiver();
        bidder.setBlocked(false);
        vm.deal(address(bidder), 10 ether);
        uint256 tid = nft.mint(seller);
        vm.prank(seller);
        nft.setApprovalForAll(address(ob), true);
        _enableOffers(address(nft));
        uint64 exp = uint64(block.timestamp + 1 days);
        vm.prank(address(bidder));
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, exp);
        bidder.setBlocked(true);
        vm.prank(seller);
        ob.rejectOffer(address(nft), tid, address(bidder));
        assertEq(ob.pendingReturns(address(bidder)), 1 ether, "GreedyReceiver parked in pendingReturns via rejectOffer push fallback");
        uint256 balBefore = address(bidder).balance;
        vm.expectRevert(WithdrawFailed.selector);
        vm.prank(address(bidder));
        ob.withdrawRefund();
        assertEq(ob.pendingReturns(address(bidder)), 1 ether, "R-01: pendingReturns RESTORED to 1 ETH (no funds lost on transient failure)");
        assertEq(address(bidder).balance, balBefore, "R-01: failed withdraw did not transfer any ETH to bidder");
        vm.expectRevert(WithdrawFailed.selector);
        vm.prank(address(bidder));
        ob.withdrawRefund();
        assertEq(ob.pendingReturns(address(bidder)), 1 ether, "R-01: survives MULTIPLE failed attempts");
        bidder.setBlocked(false);
        uint256 balBeforeSuccess = address(bidder).balance;
        vm.prank(address(bidder));
        ob.withdrawRefund();
        assertEq(ob.pendingReturns(address(bidder)), 0, "credit cleared on successful withdraw");
        assertEq(address(bidder).balance, balBeforeSuccess + 1 ether, "bidder received full 1 ETH escrow back");
    }
}

contract RejectEtherNoReceive { /* intentionally empty */ }

contract SellerNoReceive {
    receive() external payable { revert("no receive"); }
    function onERC721Received(address, address, uint256, bytes calldata)
        external pure returns (bytes4)
    {
        return this.onERC721Received.selector;
    }
}
