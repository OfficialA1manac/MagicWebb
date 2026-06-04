// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}         from "forge-std/Test.sol";
import {AuctionHouse, BidTooLow, WrongBidValue, AuctionLive, AuctionEnded, NotSeller} from "../src/AuctionHouse.sol";
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

    // ── Helpers ───────────────────────────────────────────────────────────────

    function _createAuction() internal returns (uint256 id, uint256 tid) {
        vm.startPrank(seller);
        tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint64 end = uint64(block.timestamp + 7 days);
        id = ah.create(address(nft), tid, 1 ether, end, 500, 0);
        vm.stopPrank();
    }

    /// @dev Required msg.value for a bidAmount (bid + 1.5% fee).
    function _bidTotal(uint128 bidAmount) internal pure returns (uint128) {
        uint128 fee = uint128(uint256(bidAmount) * 150 / 10_000);
        return bidAmount + fee;
    }

    function _fee(uint128 bidAmount) internal pure returns (uint256) {
        return uint256(bidAmount) * 150 / 10_000;
    }

    function _bid(uint256 id, address bidder, uint128 bidAmount) internal {
        uint128 total = _bidTotal(bidAmount);
        vm.prank(bidder);
        ah.bid{value: total}(id, bidAmount);
    }

    // ── Core bid flow ─────────────────────────────────────────────────────────

    function test_firstBidAtReserveSucceeds() public {
        (uint256 id,) = _createAuction();
        _bid(id, alice, 1 ether);
        (,,,,,,,,, uint128 hi,,,,) = ah.auctions(id);
        assertEq(hi, 1 ether);
    }

    /// @dev Outbid bidder is refunded their BID only; the platform keeps their 1.5% fee.
    function test_outbidRefundsBidOnly() public {
        (uint256 id,) = _createAuction();
        uint128 aliceBid = 1 ether;
        uint128 bobBid   = 2 ether;

        uint256 vaultBefore = feeRecipient.balance;

        _bid(id, alice, aliceBid);
        _bid(id, bob, bobBid);

        // Alice lost only her fee (bid returned).
        assertApproxEqAbs(alice.balance, 100 ether - _fee(aliceBid), 1);
        // Platform pocketed Alice's fee immediately on the outbid.
        assertEq(feeRecipient.balance, vaultBefore + _fee(aliceBid));
        // Contract now holds exactly Bob's payment.
        assertEq(address(ah).balance, _bidTotal(bobBid));
    }

    function test_wrongBidValueReverts() public {
        (uint256 id,) = _createAuction();
        uint128 bidAmount = 1 ether;
        vm.prank(alice);
        vm.expectRevert(WrongBidValue.selector);
        ah.bid{value: bidAmount}(id, bidAmount); // missing fee
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
        // minIncrementBps = 500 (5%) → next min = 1.05 ether; 1.01 ether is below
        uint128 tooLow = 1.01 ether;
        vm.prank(bob);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: _bidTotal(tooLow)}(id, tooLow);
    }

    // ── Flat minimum increment ──────────────────────────────────────────────────

    function test_flatMinIncrementEnforced() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        // 1% bps but a flat 0.5 ether floor — flat dominates on a 1 ether high bid.
        uint256 id = ah.create(address(nft), tid, 1 ether, uint64(block.timestamp + 7 days), 100, 0.5 ether);
        vm.stopPrank();

        _bid(id, alice, 1 ether);
        // pct increment = 0.01 ether, flat = 0.5 ether → min next = 1.5 ether. 1.2 too low.
        uint128 tooLow = 1.2 ether;
        vm.prank(bob);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: _bidTotal(tooLow)}(id, tooLow);

        _bid(id, bob, 1.5 ether);
        (,,,,,,,,, uint128 hi,,,,) = ah.auctions(id);
        assertEq(hi, 1.5 ether);
    }

    // ── Anti-snipe extension ────────────────────────────────────────────────────

    function test_antiSnipeExtendsEnd() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint64 end = uint64(block.timestamp + 1 hours);
        uint256 id = ah.create(address(nft), tid, 1 ether, end, 500, 0);
        vm.stopPrank();

        // Warp into the final 3-minute window, then bid.
        vm.warp(end - 1 minutes);
        _bid(id, alice, 1 ether);

        (,,,,,, uint64 newEnd,,,,,,,) = ah.auctions(id);
        assertEq(newEnd, uint64(block.timestamp) + ah.EXTENSION_WINDOW());
    }

    // ── Create guards ───────────────────────────────────────────────────────────

    function test_reserveBelowMinReverts() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        vm.expectRevert();
        ah.create(address(nft), tid, 0.009 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();
    }

    function test_durationBeyondMaxReverts() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        vm.expectRevert();
        ah.create(address(nft), tid, 1 ether, uint64(block.timestamp + 8 days), 500, 0);
        vm.stopPrank();
    }

    // ── Settle ────────────────────────────────────────────────────────────────

    function test_settleAfterExpiry() public {
        (uint256 id, uint256 tid) = _createAuction();
        uint128 bidAmt = 2 ether;
        _bid(id, bob, bidAmt);

        vm.warp(block.timestamp + 8 days);

        uint256 feeExpected  = _fee(bidAmt); // exact premium paid
        uint256 vaultBefore  = feeRecipient.balance;
        uint256 sellerBefore = seller.balance;

        ah.settle(id);

        assertEq(nft.ownerOf(tid), bob);
        assertEq(feeRecipient.balance, vaultBefore  + feeExpected);
        assertEq(seller.balance,       sellerBefore + bidAmt); // seller gets 100%
    }

    function test_settleBeforeExpiryReverts() public {
        (uint256 id,) = _createAuction();
        _bid(id, alice, 1 ether);
        vm.expectRevert(AuctionLive.selector);
        ah.settle(id);
    }

    function test_settleNoBidsCancelsInactive() public {
        (uint256 id,) = _createAuction();
        vm.warp(block.timestamp + 8 days);
        ah.settle(id);
        (,,,bool settled,,,,,,,,,,) = ah.auctions(id);
        assertTrue(settled);
    }

    function test_settleAlreadySettledReverts() public {
        (uint256 id,) = _createAuction();
        _bid(id, alice, 1 ether);
        vm.warp(block.timestamp + 8 days);
        ah.settle(id);
        vm.expectRevert();
        ah.settle(id);
    }

    // ── cancelIfInactive (triggered via settle) ────────────────────────────────

    function test_cancelIfInactiveAfterWindow() public {
        (uint256 id,) = _createAuction();
        vm.warp(block.timestamp + ah.NO_BID_CANCEL_WINDOW() + 1);
        ah.settle(id);
        (,,,bool settled,,,,,,,,,,) = ah.auctions(id);
        assertTrue(settled);
    }

    function test_cancelIfInactiveTooEarlyReverts() public {
        (uint256 id,) = _createAuction();
        vm.warp(block.timestamp + ah.NO_BID_CANCEL_WINDOW() - 1);
        vm.expectRevert(AuctionLive.selector);
        ah.settle(id);
    }

    function test_cancelIfInactiveWithBidsReverts() public {
        (uint256 id,) = _createAuction();
        _bid(id, alice, 1 ether);
        vm.warp(block.timestamp + ah.NO_BID_CANCEL_WINDOW() + 1);
        vm.expectRevert(AuctionLive.selector);
        ah.settle(id);
    }

    // ── cancelEarly ───────────────────────────────────────────────────────────

    function test_cancelEarlyNoBids() public {
        (uint256 id,) = _createAuction();
        vm.prank(seller);
        ah.cancelEarly(id);
        (,,,bool settled,,,,,,,,,,) = ah.auctions(id);
        assertTrue(settled);
    }

    /// @dev Seller cancel refunds the bidder their BID only; the platform keeps the fee.
    function test_cancelEarlyWithBidRefundsBidOnly() public {
        (uint256 id,) = _createAuction();
        uint128 bidAmt   = 1 ether;
        uint256 vaultBefore = feeRecipient.balance;
        _bid(id, alice, bidAmt);

        vm.prank(seller);
        ah.cancelEarly(id);

        assertApproxEqAbs(alice.balance, 100 ether - _fee(bidAmt), 1);
        assertEq(feeRecipient.balance, vaultBefore + _fee(bidAmt));
    }

    function test_cancelEarlyNotSellerReverts() public {
        (uint256 id,) = _createAuction();
        vm.prank(alice);
        vm.expectRevert(NotSeller.selector);
        ah.cancelEarly(id);
    }

    function test_cancelEarlyAfterExpiryReverts() public {
        (uint256 id,) = _createAuction();
        vm.warp(block.timestamp + 8 days);
        vm.prank(seller);
        vm.expectRevert(AuctionEnded.selector);
        ah.cancelEarly(id);
    }

    // ── ERC-1155 auction ────────────────────────────────────────────────────────

    function test_create1155AndSettleTransfersAmount() public {
        vm.startPrank(seller);
        multi.mint(seller, 99, 5);
        multi.setApprovalForAll(address(ah), true);
        uint64 end = uint64(block.timestamp + 7 days);
        uint256 id = ah.create1155(address(multi), 99, 5, 1 ether, end, 500, 0);
        vm.stopPrank();

        _bid(id, alice, 1 ether);
        vm.warp(block.timestamp + 8 days);

        uint256 vaultBefore = feeRecipient.balance;
        ah.settle(id);

        assertEq(multi.balanceOf(alice,  99), 5);
        assertEq(multi.balanceOf(seller, 99), 0);
        assertGt(feeRecipient.balance, vaultBefore);
    }

    // ── Fee invariants ──────────────────────────────────────────────────────────

    function testFuzz_feeExactAtSettle(uint128 bidAmt) public {
        bidAmt = uint128(bound(bidAmt, 1 ether, 50 ether));
        vm.deal(alice, uint256(bidAmt) * 2);

        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint256 id = ah.create(address(nft), tid, bidAmt, uint64(block.timestamp + 7 days), 0, 0);
        vm.stopPrank();

        _bid(id, alice, bidAmt);
        vm.warp(block.timestamp + 8 days);

        uint256 vaultBefore  = feeRecipient.balance;
        uint256 sellerBefore = seller.balance;

        ah.settle(id);

        uint256 feeActual    = feeRecipient.balance - vaultBefore;
        uint256 sellerActual = seller.balance       - sellerBefore;

        assertEq(feeActual,    _fee(bidAmt));
        assertEq(sellerActual, uint256(bidAmt));
        assertEq(feeActual + sellerActual, _bidTotal(bidAmt));
    }

    function testFuzz_bidIncrementEnforced(uint128 reserve, uint16 incBps) public {
        reserve = uint128(bound(reserve, 0.01 ether, 10 ether));
        incBps  = uint16(bound(incBps, 100, ah.MAX_MIN_INCREMENT_BPS()));

        vm.deal(alice, uint256(reserve) * 3);
        vm.deal(bob,   uint256(reserve) * 3);

        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint256 id  = ah.create(address(nft), tid, reserve,
            uint64(block.timestamp + 7 days), incBps, 0);
        vm.stopPrank();

        _bid(id, alice, reserve);

        uint128 tooLow = reserve;
        vm.prank(bob);
        vm.expectRevert(BidTooLow.selector);
        ah.bid{value: _bidTotal(tooLow)}(id, tooLow);

        uint256 inc      = uint256(reserve) * incBps / 10_000;
        uint128 validBid = uint128(uint256(reserve) + (inc == 0 ? 1 : inc) + 1);
        vm.deal(bob, uint256(_bidTotal(validBid)) + 1 ether);
        _bid(id, bob, validBid);
        (,,,,,,,,, uint128 hi,,,,) = ah.auctions(id);
        assertEq(hi, validBid);
    }

    function testFuzz_contractHoldsOnlyCurrentBid(uint128 firstBid, uint128 secondBid) public {
        firstBid  = uint128(bound(firstBid,  1 ether, 10 ether));
        uint256 inc      = uint256(firstBid) * 500 / 10_000;
        uint256 minNext  = uint256(firstBid) + (inc == 0 ? 1 : inc);
        secondBid = uint128(bound(secondBid, minNext, minNext + 50 ether));

        vm.deal(alice, uint256(_bidTotal(firstBid))  + 1 ether);
        vm.deal(bob,   uint256(_bidTotal(secondBid)) + 1 ether);

        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint256 id  = ah.create(address(nft), tid, firstBid,
            uint64(block.timestamp + 7 days), 0, 0);
        vm.stopPrank();

        _bid(id, alice, firstBid);
        _bid(id, bob,   secondBid);

        // Contract holds exactly bob's payment — alice's bid was refunded (fee kept).
        assertEq(address(ah).balance, _bidTotal(secondBid));
        // Alice keeps her leftover float plus her returned bid, minus her fee.
        assertApproxEqAbs(alice.balance, uint256(firstBid) + 1 ether, 1);
    }
}
