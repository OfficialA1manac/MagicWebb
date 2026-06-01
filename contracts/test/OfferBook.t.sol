// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}        from "forge-std/Test.sol";
import {OfferBook, NotOwner, NoPosition, InvalidAmount, InvalidDuration, PositionExpired, PositionLive} from "../src/OfferBook.sol";
import {BelowMinPrice} from "../src/MarketplaceCore.sol";
import {MockERC721}  from "./MockERC721.sol";
import {MockERC1155} from "./MockERC1155.sol";

contract OfferBookTest is Test {
    OfferBook   ob;
    MockERC721  nft;
    MockERC1155 multi;

    address feeRecipient = address(0x1111000000000000000000000000000000111100);
    address seller       = address(0xBEEF);
    address bidder       = address(0xA11CE);
    address bidder2      = address(0xB0B);

    function setUp() public {
        ob    = new OfferBook(feeRecipient);
        nft   = new MockERC721();
        multi = new MockERC1155();

        vm.deal(bidder,  100 ether);
        vm.deal(bidder2, 100 ether);
        vm.deal(seller,  1 ether);
    }

    // ── Helpers ───────────────────────────────────────────────────────────

    function _mintAndApprove(address to) internal returns (uint256 tid) {
        vm.startPrank(to);
        tid = nft.mint(to);
        nft.setApprovalForAll(address(ob), true);
        vm.stopPrank();
    }

    function _offerTotal(uint128 offer) internal pure returns (uint256) {
        return uint256(offer) + (uint256(offer) * 150) / 10_000;
    }

    function _readPosition(uint256 tid, address b) internal view returns (uint128 totalOffer, uint128 totalFee, uint64 expiresAt) {
        (uint128 o, uint128 f, , uint64 e) = ob.positions(address(nft), tid, b);
        return (o, f, e);
    }

    // ── Eligibility (informational) ───────────────────────────────────────

    function test_markOfferEligible() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        ob.markOfferEligible(address(nft), tid);
        assertEq(ob.eligible(address(nft), tid), seller);
    }

    function test_markOfferEligibleNotOwnerReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        vm.expectRevert(NotOwner.selector);
        ob.markOfferEligible(address(nft), tid);
    }

    function test_removeOfferEligible() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        ob.markOfferEligible(address(nft), tid);
        vm.prank(seller);
        ob.removeOfferEligible(address(nft), tid);
        assertEq(ob.eligible(address(nft), tid), address(0));
    }

    // ── Make offer (stacked, no withdrawal) ──────────────────────────────

    function test_makeOfferStoresPosition() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        ob.makeOffer{value: _offerTotal(1 ether)}(address(nft), tid, 1 ether, 0);

        (uint128 offer, uint128 fee, uint64 exp) = _readPosition(tid, bidder);
        assertEq(offer, 1 ether);
        assertEq(fee, 0.015 ether);
        assertEq(exp, uint64(block.timestamp) + ob.DEFAULT_DURATION());
    }

    function test_makeOfferWithoutEligibilityWorks() public {
        // Spec: making an offer does NOT require the owner to opt in.
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        ob.makeOffer{value: _offerTotal(1 ether)}(address(nft), tid, 1 ether, 0);
        (uint128 offer,,) = _readPosition(tid, bidder);
        assertEq(offer, 1 ether);
    }

    function test_makeOfferFeeForwarded() public {
        uint256 tid = _mintAndApprove(seller);
        uint256 feeBefore = feeRecipient.balance;
        vm.prank(bidder);
        ob.makeOffer{value: _offerTotal(1 ether)}(address(nft), tid, 1 ether, 0);
        assertEq(feeRecipient.balance - feeBefore, 0.015 ether);
    }

    function test_stackedPositionCompounds() public {
        uint256 tid = _mintAndApprove(seller);
        uint64 firstExp;

        vm.prank(bidder);
        ob.makeOffer{value: _offerTotal(1 ether)}(address(nft), tid, 1 ether, 1 days);
        (,, firstExp) = _readPosition(tid, bidder);

        // Move forward a bit but stay within expiry
        vm.warp(block.timestamp + 12 hours);

        vm.prank(bidder);
        ob.makeOffer{value: _offerTotal(0.5 ether)}(address(nft), tid, 0.5 ether, 0);

        (uint128 offer, uint128 fee, uint64 exp) = _readPosition(tid, bidder);
        // Principal compounds
        assertEq(offer, 1.5 ether);
        assertEq(fee, 0.015 ether + 0.0075 ether);
        // Expiry NOT extended
        assertEq(exp, firstExp);
    }

    function test_makeOfferBelowMinPriceReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        vm.expectRevert(BelowMinPrice.selector);
        ob.makeOffer{value: _offerTotal(0.001 ether)}(address(nft), tid, 0.001 ether, 0);
    }

    function test_makeOfferWrongValueReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        vm.expectRevert(InvalidAmount.selector);
        // Only sending the offer, not the fee
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, 0);
    }

    function test_makeOfferDurationOverMaxReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        vm.expectRevert(InvalidDuration.selector);
        ob.makeOffer{value: _offerTotal(1 ether)}(address(nft), tid, 1 ether, 15 days);
    }

    function test_makeOfferAfterExpiryReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        ob.makeOffer{value: _offerTotal(1 ether)}(address(nft), tid, 1 ether, 1 days);

        vm.warp(block.timestamp + 2 days);

        vm.prank(bidder);
        vm.expectRevert(PositionExpired.selector);
        ob.makeOffer{value: _offerTotal(0.5 ether)}(address(nft), tid, 0.5 ether, 0);
    }

    // ── refundExpired ─────────────────────────────────────────────────────

    function test_refundExpiredReturnsPrincipalOnly() public {
        uint256 tid = _mintAndApprove(seller);
        uint256 balBefore = bidder.balance;

        vm.prank(bidder);
        ob.makeOffer{value: _offerTotal(2 ether)}(address(nft), tid, 2 ether, 1 days);

        // 2.03 paid, 0.03 fee forwarded → balance after = -2.03
        assertEq(balBefore - bidder.balance, _offerTotal(2 ether));

        vm.warp(block.timestamp + 2 days);
        ob.refundExpired(address(nft), tid, bidder);

        // Bidder gets back 2 ether principal. Fee 0.03 ether stays with platform.
        // Net loss = 0.03 ether
        assertEq(balBefore - bidder.balance, 0.03 ether);
        (uint128 offer,,) = _readPosition(tid, bidder);
        assertEq(offer, 0);
    }

    function test_refundExpiredBeforeExpiryReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        ob.makeOffer{value: _offerTotal(1 ether)}(address(nft), tid, 1 ether, 1 days);

        vm.expectRevert(PositionLive.selector);
        ob.refundExpired(address(nft), tid, bidder);
    }

    function test_refundExpiredNoPositionReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.expectRevert(NoPosition.selector);
        ob.refundExpired(address(nft), tid, bidder);
    }

    // ── Accept offer (free for seller) ────────────────────────────────────

    function test_acceptOfferSellerGetsFullAmount() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        ob.makeOffer{value: _offerTotal(1 ether)}(address(nft), tid, 1 ether, 0);

        uint256 sellerBefore = seller.balance;

        vm.prank(seller);
        ob.acceptOffer(address(nft), tid, bidder);

        // NFT transferred
        assertEq(nft.ownerOf(tid), bidder);
        // Seller receives the FULL offer principal (no fee at accept time)
        assertEq(seller.balance - sellerBefore, 1 ether);
        // Position cleared
        (uint128 offer,,) = _readPosition(tid, bidder);
        assertEq(offer, 0);
    }

    function test_acceptOfferNotOwnerReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        ob.makeOffer{value: _offerTotal(1 ether)}(address(nft), tid, 1 ether, 0);

        vm.prank(bidder2);
        vm.expectRevert(NotOwner.selector);
        ob.acceptOffer(address(nft), tid, bidder);
    }

    function test_acceptOfferNoPositionReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        vm.expectRevert(NoPosition.selector);
        ob.acceptOffer(address(nft), tid, bidder);
    }

    function test_acceptOfferExpiredReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        ob.makeOffer{value: _offerTotal(1 ether)}(address(nft), tid, 1 ether, 1 days);

        vm.warp(block.timestamp + 2 days);
        vm.prank(seller);
        vm.expectRevert(PositionExpired.selector);
        ob.acceptOffer(address(nft), tid, bidder);
    }

    function test_acceptStackedPositionAtomic() public {
        uint256 tid = _mintAndApprove(seller);

        vm.prank(bidder);
        ob.makeOffer{value: _offerTotal(1 ether)}(address(nft), tid, 1 ether, 0);
        vm.prank(bidder);
        ob.makeOffer{value: _offerTotal(0.5 ether)}(address(nft), tid, 0.5 ether, 0);

        uint256 sellerBefore = seller.balance;
        vm.prank(seller);
        ob.acceptOffer(address(nft), tid, bidder);

        // Seller receives the compounded principal: 1.5 ether
        assertEq(seller.balance - sellerBefore, 1.5 ether);
    }

    function test_multipleOffersSellerPicksOne() public {
        uint256 tid = _mintAndApprove(seller);

        vm.prank(bidder);
        ob.makeOffer{value: _offerTotal(1 ether)}(address(nft), tid, 1 ether, 1 days);
        vm.prank(bidder2);
        ob.makeOffer{value: _offerTotal(2 ether)}(address(nft), tid, 2 ether, 1 days);

        // Seller picks bidder2's higher offer
        vm.prank(seller);
        ob.acceptOffer(address(nft), tid, bidder2);
        assertEq(nft.ownerOf(tid), bidder2);

        // bidder's position is still active; refund only available after expiry
        (uint128 offer,,) = _readPosition(tid, bidder);
        assertEq(offer, 1 ether);
        vm.warp(block.timestamp + 2 days);
        uint256 balBefore = bidder.balance;
        ob.refundExpired(address(nft), tid, bidder);
        assertEq(bidder.balance - balBefore, 1 ether);
    }

    // ── ERC-1155 offers ────────────────────────────────────────────────────

    function test_makeOffer1155Success() public {
        vm.startPrank(seller);
        multi.mint(seller, 42, 10);
        multi.setApprovalForAll(address(ob), true);
        vm.stopPrank();

        vm.prank(bidder);
        ob.makeOffer1155{value: _offerTotal(2 ether)}(address(multi), 42, 2 ether, 5, 0);

        (uint128 amt, uint128 fee, uint128 units,,) = ob.positions1155(address(multi), 42, bidder);
        assertEq(amt, 2 ether);
        assertEq(fee, 0.03 ether);
        assertEq(units, 5);
    }

    function test_acceptOffer1155SellerGetsFullAmount() public {
        vm.startPrank(seller);
        multi.mint(seller, 42, 10);
        multi.setApprovalForAll(address(ob), true);
        vm.stopPrank();

        vm.prank(bidder);
        ob.makeOffer1155{value: _offerTotal(2 ether)}(address(multi), 42, 2 ether, 5, 0);

        uint256 sellerBefore = seller.balance;
        vm.prank(seller);
        ob.acceptOffer1155(address(multi), 42, bidder);

        assertEq(multi.balanceOf(bidder, 42), 5);
        assertEq(seller.balance - sellerBefore, 2 ether);
    }

    // ── Fee invariants ────────────────────────────────────────────────────

    function testFuzz_feeOnEveryDeposit(uint128 amount) public {
        amount = uint128(bound(amount, 0.01 ether, 50 ether));
        vm.deal(bidder, _offerTotal(amount) * 2 + 1 ether);

        uint256 tid = _mintAndApprove(seller);
        uint256 feeBefore = feeRecipient.balance;

        vm.prank(bidder);
        ob.makeOffer{value: _offerTotal(amount)}(address(nft), tid, amount, 0);

        uint256 expected = (uint256(amount) * 150) / 10_000;
        assertEq(feeRecipient.balance - feeBefore, expected);
    }
}
