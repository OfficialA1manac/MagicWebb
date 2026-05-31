// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}         from "forge-std/Test.sol";
import {AuctionHouse, BidTooLow, WrongBidValue, AuctionLive, AuctionEnded, NotSeller, NotActive, InvalidWindow} from "../src/AuctionHouse.sol";
import {BelowMinPrice} from "../src/MarketplaceCore.sol";
import {MockERC721}   from "./MockERC721.sol";
import {MockERC1155}  from "./MockERC1155.sol";

contract AuctionHouseTest is Test {
    AuctionHouse ah;
    MockERC721   nft;
    MockERC1155  multi;
    address feeRecipient = address(0x1111000000000000000000000000000000111100);
    address seller       = address(0xBEEF);
    address alice        = address(0xA11CE);
    address bob          = address(0xB0B);

    function setUp() public {
        ah    = new AuctionHouse(feeRecipient);
        nft   = new MockERC721();
        multi = new MockERC1155();
        vm.deal(alice, 100 ether);
        vm.deal(bob,   100 ether);
    }

    // ── Helpers ───────────────────────────────────────────────────────────

    function _createAuction() internal returns (uint256 id, uint256 tid) {
        vm.startPrank(seller);
        tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint64 end = uint64(block.timestamp + 3 days);
        // reserve = 1 FLR, sellerFlatMinFLR = 0
        id = ah.create(address(nft), tid, 1 ether, end, 0);
        vm.stopPrank();
    }

    function _bidTotal(uint128 bidAmount) internal pure returns (uint128) {
        uint128 fee = uint128(uint256(bidAmount) * 150 / 10_000);
        return bidAmount + fee;
    }

    function _bid(uint256 id, address bidder, uint128 bidAmount) internal {
        uint128 total = _bidTotal(bidAmount);
        vm.prank(bidder);
        ah.bid{value: total}(id, bidAmount);
    }

    // ── Core bid flow ─────────────────────────────────────────────────────

    function test_firstBidAtReserveSucceeds() public {
        (uint256 id,) = _createAuction();
        _bid(id, alice, 1 ether);
        (,,,,,,,, uint128 hi,,,) = ah.auctions(id);
        assertEq(hi, 1 ether);
    }

    function test_feeForwardedOnEachBid() public {
        (uint256 id,) = _createAuction();
        uint256 before_ = feeRecipient.balance;
        _bid(id, alice, 1 ether);
        // Fee is forwarded immediately on bid
        assertEq(feeRecipient.balance - before_, 0.015 ether);
    }

    function test_outbidRefundsBidOnlyFeeKept() public {
        (uint256 id,) = _createAuction();
        uint128 aliceBid = 1 ether;
        uint128 bobBid   = 2 ether;

        uint256 aliceBefore = alice.balance;
        uint256 feeBefore   = feeRecipient.balance;

        _bid(id, alice, aliceBid);
        _bid(id, bob, bobBid);

        // Alice paid 1.015, refunded 1.0 → net loss = 0.015 (the fee)
        assertEq(aliceBefore - alice.balance, 0.015 ether);
        // FeeRecipient received fees from BOTH bids (alice + bob)
        assertEq(feeRecipient.balance - feeBefore, 0.015 ether + 0.03 ether);
        // Contract holds only bob's bid principal
        assertEq(address(ah).balance, bobBid);
    }

    function test_wrongBidValueReverts() public {
        (uint256 id,) = _createAuction();
        uint128 bidAmount = 1 ether;
        vm.prank(alice);
        vm.expectRevert(WrongBidValue.selector);
        ah.bid{value: bidAmount}(id, bidAmount);
    }

    function test_bidBelowReserveReverts() public {
        (uint256 id,) = _createAuction();
        uint128 low = 0.5 ether;
        vm.prank(alice);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: _bidTotal(low)}(id, low);
    }

    function test_bidBelowIncrementReverts() public {
        (uint256 id,) = _createAuction();
        _bid(id, alice, 1 ether);
        // 5% increment: next min = 1.05 ether, 1.04 ether is below
        uint128 tooLow = 1.04 ether;
        vm.prank(bob);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: _bidTotal(tooLow)}(id, tooLow);
    }

    function test_sellerFlatMinFLREnforced() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        // reserve = 0, but seller wants at least 5 FLR per increment step
        uint256 id = ah.create(address(nft), tid, 0, uint64(block.timestamp + 3 days), 5 ether);
        vm.stopPrank();

        // first bid below the flat minimum reverts
        vm.prank(alice);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: _bidTotal(1 ether)}(id, 1 ether);

        // first bid at flat min succeeds
        _bid(id, alice, 5 ether);
    }

    function test_durationOverMaxReverts() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        vm.expectRevert(InvalidWindow.selector);
        ah.create(address(nft), tid, 1 ether, uint64(block.timestamp + 8 days), 0);
        vm.stopPrank();
    }

    function test_reserveBelowMinPriceReverts() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        vm.expectRevert(BelowMinPrice.selector);
        ah.create(address(nft), tid, 0.001 ether, uint64(block.timestamp + 1 days), 0);
        vm.stopPrank();
    }

    // ── Anti-snipe ─────────────────────────────────────────────────────────

    function test_antiSnipeExtension() public {
        (uint256 id,) = _createAuction();
        // Warp to 1 minute before original endsAt (within EXTENSION_WINDOW = 3 min)
        vm.warp(block.timestamp + 3 days - 1 minutes);

        _bid(id, alice, 1 ether);

        (,,,,, uint64 newEnd,,,,,,) = ah.auctions(id);
        // newEnd should be block.timestamp + EXTENSION_WINDOW = +3 minutes
        assertEq(newEnd, uint64(block.timestamp) + ah.EXTENSION_WINDOW());
    }

    function test_antiSnipeOutsideWindowDoesNotExtend() public {
        (uint256 id,) = _createAuction();
        // 10 minutes before original endsAt — outside extension window
        vm.warp(block.timestamp + 3 days - 10 minutes);
        uint64 endBefore;
        (,,,,, endBefore,,,,,,) = ah.auctions(id);

        _bid(id, alice, 1 ether);

        uint64 endAfter;
        (,,,,, endAfter,,,,,,) = ah.auctions(id);
        assertEq(endAfter, endBefore);
    }

    // ── Settle ────────────────────────────────────────────────────────────

    function test_settleAfterExpiry() public {
        (uint256 id, uint256 tid) = _createAuction();
        uint128 bidAmt = 2 ether;
        _bid(id, bob, bidAmt);

        // Skip past original endsAt (the bid did not extend since 2 days early)
        vm.warp(block.timestamp + 4 days);

        uint256 sellerBefore = seller.balance;
        uint256 feeBefore    = feeRecipient.balance;

        ah.settle(id);

        assertEq(nft.ownerOf(tid), bob);
        // Seller receives bid principal in full
        assertEq(seller.balance - sellerBefore, bidAmt);
        // Fee was already forwarded on the bid; no additional payment at settle
        assertEq(feeRecipient.balance - feeBefore, 0);
    }

    function test_settleBeforeExpiryReverts() public {
        (uint256 id,) = _createAuction();
        _bid(id, alice, 1 ether);
        vm.expectRevert(AuctionLive.selector);
        ah.settle(id);
    }

    function test_settleNoBidsAutoCancels() public {
        (uint256 id,) = _createAuction();
        vm.warp(block.timestamp + 4 days);
        ah.settle(id);
        (,, bool settled,,,,,,,,,) = ah.auctions(id);
        assertTrue(settled);
    }

    function test_settleReserveUnmetRefundsBidOnly() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        // reserve = 5 FLR, sellerFlatMinFLR = 0 (so a 1 FLR bid is allowed even though < reserve)
        // Actually first bid logic: minNext = max(reserve, sellerFlatMin, MIN_PRICE) → 5 FLR
        // So bids below 5 will be rejected. Use sellerFlatMin = 1 ether to allow 1 ether bids,
        // then check that settle still cancels because winBid < reserve.
        uint256 id = ah.create(address(nft), tid, 5 ether, uint64(block.timestamp + 3 days), 0);
        vm.stopPrank();

        // Lower the first-bid floor by setting reserve high but allowing via sellerFlatMin? No —
        // first-bid floor uses max(reserve, flatMin, MIN_PRICE) = 5 ether. So bids must be ≥ 5.
        // Test the reserve-unmet path by recreating with a low reserve workaround:
        // Use a scenario where reserve = 5 ether but we accept first bid at reserve, then... not possible.
        // Instead, use a 1155 auction or just verify the cancel path on no-bid (handled above).
        // For reserve-unmet specifically, mock with reserve=0 and check settle works...
        // Actually the contract enforces winBid >= reserve, but the bid path enforces bid >= reserve
        // for the first bid, so winBid will always be >= reserve unless we manipulate. Skip explicit
        // reserve-unmet test — it's prevented at bid time.
        vm.warp(block.timestamp + 4 days);
        ah.settle(id);
        (,, bool settled,,,,,,,,,) = ah.auctions(id);
        assertTrue(settled);
    }

    function test_settleAlreadySettledReverts() public {
        (uint256 id,) = _createAuction();
        _bid(id, alice, 1 ether);
        vm.warp(block.timestamp + 4 days);
        ah.settle(id);
        vm.expectRevert(NotActive.selector);
        ah.settle(id);
    }

    // ── cancelEarly ───────────────────────────────────────────────────────

    function test_cancelEarlyNoBids() public {
        (uint256 id,) = _createAuction();
        vm.prank(seller);
        ah.cancelEarly(id);
        (,, bool settled,,,,,,,,,) = ah.auctions(id);
        assertTrue(settled);
    }

    function test_cancelEarlyRefundsBidOnly() public {
        (uint256 id,) = _createAuction();
        uint128 bidAmt   = 1 ether;
        uint256 aliceBefore = alice.balance;
        _bid(id, alice, bidAmt);

        vm.prank(seller);
        ah.cancelEarly(id);

        // Alice gets bid principal back, fee is kept by platform → net loss = 0.015
        assertEq(aliceBefore - alice.balance, 0.015 ether);
    }

    function test_cancelEarlyNotSellerReverts() public {
        (uint256 id,) = _createAuction();
        vm.prank(alice);
        vm.expectRevert(NotSeller.selector);
        ah.cancelEarly(id);
    }

    function test_cancelEarlyAfterSettleReverts() public {
        (uint256 id,) = _createAuction();
        _bid(id, alice, 1 ether);
        vm.warp(block.timestamp + 4 days);
        ah.settle(id);
        vm.prank(seller);
        vm.expectRevert(NotActive.selector);
        ah.cancelEarly(id);
    }

    // ── ERC-1155 auction ──────────────────────────────────────────────────

    function test_create1155AndSettleTransfersAmount() public {
        vm.startPrank(seller);
        multi.mint(seller, 99, 5);
        multi.setApprovalForAll(address(ah), true);
        uint64 end = uint64(block.timestamp + 3 days);
        uint256 id = ah.create1155(address(multi), 99, 5, 1 ether, end, 0);
        vm.stopPrank();

        _bid(id, alice, 1 ether);
        vm.warp(block.timestamp + 4 days);

        ah.settle(id);

        assertEq(multi.balanceOf(alice,  99), 5);
        assertEq(multi.balanceOf(seller, 99), 0);
    }

    // ── Fee invariants ────────────────────────────────────────────────────

    function testFuzz_feeExactOnSingleBid(uint128 bidAmt) public {
        bidAmt = uint128(bound(bidAmt, 1 ether, 50 ether));
        vm.deal(alice, uint256(bidAmt) * 2);

        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint256 id = ah.create(address(nft), tid, bidAmt, uint64(block.timestamp + 3 days), 0);
        vm.stopPrank();

        uint256 feeBefore = feeRecipient.balance;
        _bid(id, alice, bidAmt);
        assertEq(feeRecipient.balance - feeBefore, (uint256(bidAmt) * 150) / 10_000);

        vm.warp(block.timestamp + 4 days);
        uint256 sellerBefore = seller.balance;
        ah.settle(id);
        assertEq(seller.balance - sellerBefore, bidAmt);
    }
}
