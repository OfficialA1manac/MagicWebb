// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}        from "forge-std/Test.sol";
import {OfferBook, NotEligible, NoOffer, NotOwner, OfferExists, ZeroOffer} from "../src/OfferBook.sol";
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

    // ── Eligibility ───────────────────────────────────────────────────────

    function test_markEligible() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        ob.markEligible(address(nft), tid);
        assertEq(ob.eligible(address(nft), tid), seller);
    }

    function test_markEligibleNotOwnerReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        vm.expectRevert(NotOwner.selector);
        ob.markEligible(address(nft), tid);
    }

    function test_removeEligible() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        ob.markEligible(address(nft), tid);
        vm.prank(seller);
        ob.removeEligible(address(nft), tid);
        assertEq(ob.eligible(address(nft), tid), address(0));
    }

    function test_removeEligibleNotMarkerReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        ob.markEligible(address(nft), tid);
        vm.prank(bidder);
        vm.expectRevert(NotOwner.selector);
        ob.removeEligible(address(nft), tid);
    }

    // ── Make / Withdraw Offer (ERC-721) ───────────────────────────────────

    function test_makeOfferRequiresEligible() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        vm.expectRevert(NotEligible.selector);
        ob.makeOffer{value: 1 ether}(address(nft), tid);
    }

    function test_makeOfferStoresAmount() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        ob.markEligible(address(nft), tid);

        vm.prank(bidder);
        ob.makeOffer{value: 1 ether}(address(nft), tid);

        assertEq(ob.offers(address(nft), tid, bidder), 1 ether);
    }

    function test_makeOfferAccumulatesOnRepeatCall() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        ob.markEligible(address(nft), tid);

        vm.prank(bidder);
        ob.makeOffer{value: 1 ether}(address(nft), tid);
        vm.prank(bidder);
        ob.makeOffer{value: 0.5 ether}(address(nft), tid);

        assertEq(ob.offers(address(nft), tid, bidder), 1.5 ether);
    }

    function test_makeOfferZeroValueReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        ob.markEligible(address(nft), tid);
        vm.prank(bidder);
        vm.expectRevert(ZeroOffer.selector);
        ob.makeOffer{value: 0}(address(nft), tid);
    }

    function test_withdrawOfferReturnsFullAmount() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        ob.markEligible(address(nft), tid);

        uint256 balBefore = bidder.balance;
        vm.prank(bidder);
        ob.makeOffer{value: 2 ether}(address(nft), tid);

        vm.prank(bidder);
        ob.withdrawOffer(address(nft), tid);

        assertEq(bidder.balance, balBefore);
        assertEq(ob.offers(address(nft), tid, bidder), 0);
    }

    function test_withdrawOfferNoOfferReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        vm.expectRevert(NoOffer.selector);
        ob.withdrawOffer(address(nft), tid);
    }

    // ── Accept Offer (ERC-721) ────────────────────────────────────────────

    function test_acceptOfferSuccess() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        ob.markEligible(address(nft), tid);

        vm.prank(bidder);
        ob.makeOffer{value: 1 ether}(address(nft), tid);

        uint256 sellerBefore = seller.balance;
        uint256 vaultBefore  = feeRecipient.balance;

        vm.prank(seller);
        ob.acceptOffer(address(nft), tid, bidder);

        // NFT transferred to bidder
        assertEq(nft.ownerOf(tid), bidder);
        // Seller received payment minus fee
        uint256 fee = (1 ether * 150) / 10_000;
        assertEq(seller.balance,       sellerBefore + 1 ether - fee);
        assertEq(feeRecipient.balance, vaultBefore  + fee);
        // Offer cleared
        assertEq(ob.offers(address(nft), tid, bidder), 0);
        // Eligibility cleared automatically
        assertEq(ob.eligible(address(nft), tid), address(0));
    }

    function test_acceptOfferNotOwnerReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        ob.markEligible(address(nft), tid);
        vm.prank(bidder);
        ob.makeOffer{value: 1 ether}(address(nft), tid);

        vm.prank(bidder2);
        vm.expectRevert(NotOwner.selector);
        ob.acceptOffer(address(nft), tid, bidder);
    }

    function test_acceptOfferNoOfferReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        ob.markEligible(address(nft), tid);

        vm.prank(seller);
        vm.expectRevert(NoOffer.selector);
        ob.acceptOffer(address(nft), tid, bidder);
    }

    function test_acceptOfferWorksAfterEligibilityRemoved() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        ob.markEligible(address(nft), tid);

        vm.prank(bidder);
        ob.makeOffer{value: 1 ether}(address(nft), tid);

        // Seller removes eligibility, but can still accept existing offer
        vm.prank(seller);
        ob.removeEligible(address(nft), tid);

        vm.prank(seller);
        ob.acceptOffer(address(nft), tid, bidder);
        assertEq(nft.ownerOf(tid), bidder);
    }

    function test_multipleOffersSellerPicksOne() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        ob.markEligible(address(nft), tid);

        vm.prank(bidder);
        ob.makeOffer{value: 1 ether}(address(nft), tid);
        vm.prank(bidder2);
        ob.makeOffer{value: 2 ether}(address(nft), tid);

        // Seller picks bidder2's higher offer
        vm.prank(seller);
        ob.acceptOffer(address(nft), tid, bidder2);
        assertEq(nft.ownerOf(tid), bidder2);

        // bidder1's offer is still there — they must withdraw
        assertEq(ob.offers(address(nft), tid, bidder), 1 ether);
        uint256 balBefore = bidder.balance;
        vm.prank(bidder);
        ob.withdrawOffer(address(nft), tid);
        assertEq(bidder.balance, balBefore + 1 ether);
    }

    // ── ERC-1155 offers ────────────────────────────────────────────────────

    function test_makeOffer1155Success() public {
        vm.startPrank(seller);
        multi.mint(seller, 42, 10);
        multi.setApprovalForAll(address(ob), true);
        ob.markEligible1155(address(multi), 42);
        vm.stopPrank();

        vm.prank(bidder);
        ob.makeOffer1155{value: 2 ether}(address(multi), 42, 5);

        (uint128 amt, uint128 units) = ob.offers1155(address(multi), 42, bidder);
        assertEq(amt,   2 ether);
        assertEq(units, 5);
    }

    function test_makeOffer1155DuplicateReverts() public {
        vm.startPrank(seller);
        multi.mint(seller, 42, 10);
        multi.setApprovalForAll(address(ob), true);
        ob.markEligible1155(address(multi), 42);
        vm.stopPrank();

        vm.prank(bidder);
        ob.makeOffer1155{value: 2 ether}(address(multi), 42, 5);

        vm.prank(bidder);
        vm.expectRevert(OfferExists.selector);
        ob.makeOffer1155{value: 1 ether}(address(multi), 42, 3);
    }

    function test_acceptOffer1155Success() public {
        vm.startPrank(seller);
        multi.mint(seller, 42, 10);
        multi.setApprovalForAll(address(ob), true);
        ob.markEligible1155(address(multi), 42);
        vm.stopPrank();

        vm.prank(bidder);
        ob.makeOffer1155{value: 2 ether}(address(multi), 42, 5);

        uint256 sellerBefore = seller.balance;
        vm.prank(seller);
        ob.acceptOffer1155(address(multi), 42, bidder);

        assertEq(multi.balanceOf(bidder, 42), 5);
        uint256 fee = (2 ether * 150) / 10_000;
        assertEq(seller.balance - sellerBefore, 2 ether - fee);
        assertEq(ob.eligible1155(address(multi), 42), address(0));
    }

    function test_withdrawOffer1155ReturnsFullAmount() public {
        vm.startPrank(seller);
        multi.mint(seller, 42, 10);
        multi.setApprovalForAll(address(ob), true);
        ob.markEligible1155(address(multi), 42);
        vm.stopPrank();

        uint256 balBefore = bidder.balance;
        vm.prank(bidder);
        ob.makeOffer1155{value: 2 ether}(address(multi), 42, 5);

        vm.prank(bidder);
        ob.withdrawOffer1155(address(multi), 42);

        assertEq(bidder.balance, balBefore);
        (uint128 amt,) = ob.offers1155(address(multi), 42, bidder);
        assertEq(amt, 0);
    }

    // ── Fuzz ─────────────────────────────────────────────────────────────

    function testFuzz_feeOnAcceptedOfferIsExact(uint128 amount) public {
        amount = uint128(bound(amount, 0.001 ether, 100 ether));

        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        ob.markEligible(address(nft), tid);

        vm.deal(bidder, uint256(amount) + 1 ether);
        vm.prank(bidder);
        ob.makeOffer{value: amount}(address(nft), tid);

        uint256 vaultBefore  = feeRecipient.balance;
        uint256 sellerBefore = seller.balance;

        vm.prank(seller);
        ob.acceptOffer(address(nft), tid, bidder);

        uint256 feeActual  = feeRecipient.balance - vaultBefore;
        uint256 feeExpected = (uint256(amount) * 150) / 10_000;
        assertEq(feeActual, feeExpected);
        assertEq(seller.balance - sellerBefore, uint256(amount) - feeExpected);
    }
}
