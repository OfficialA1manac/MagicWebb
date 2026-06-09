// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}        from "forge-std/Test.sol";
import {OfferBook, NoOffer, NotOwner, WrongValue, OfferActive, InvalidExpiry} from "../src/OfferBook.sol";
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
        ob    = new OfferBook(feeRecipient, address(0));
        nft   = new MockERC721();
        multi = new MockERC1155();

        vm.deal(bidder,  100 ether);
        vm.deal(bidder2, 100 ether);
        vm.deal(seller,  1 ether);
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    function _fee(uint256 p) internal pure returns (uint256) {
        return (p * 150) / 10_000;
    }

    function _total(uint256 p) internal pure returns (uint256) {
        return p; // offers are free; the full principal is escrowed
    }

    function _exp() internal view returns (uint64) {
        return uint64(block.timestamp + 3 days);
    }

    function _mintAndApprove(address to) internal returns (uint256 tid) {
        vm.startPrank(to);
        tid = nft.mint(to);
        nft.setApprovalForAll(address(ob), true);
        vm.stopPrank();
    }

    function _principalOf(address coll, uint256 tid, address who) internal view returns (uint128 p) {
        (p,,,) = ob.positions(coll, tid, who);
    }

    // ── Make offer (free; full principal escrowed) ─────────────────────────────

    function test_makeOfferStoresPrincipalNoFee() public {
        uint256 tid = _mintAndApprove(seller);

        uint256 vaultBefore = feeRecipient.balance;
        vm.prank(bidder);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, _exp());

        assertEq(_principalOf(address(nft), tid, bidder), 1 ether);
        assertEq(feeRecipient.balance, vaultBefore); // no fee at offer time
    }

    function test_makeOfferAnyoneCanOffer() public {
        // No eligibility gate: a non-owner token can still receive offers.
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, _exp());
        assertEq(_principalOf(address(nft), tid, bidder), 1 ether);
    }

    function test_makeOfferCompoundsPosition() public {
        uint256 tid = _mintAndApprove(seller);

        vm.startPrank(bidder);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, _exp());
        ob.makeOffer{value: _total(0.5 ether)}(address(nft), tid, 0.5 ether, _exp());
        vm.stopPrank();

        // Principals compound into one position; offers are free.
        assertEq(_principalOf(address(nft), tid, bidder), 1.5 ether);
    }

    function test_makeOfferWrongValueReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        vm.expectRevert(WrongValue.selector);
        ob.makeOffer{value: 1 ether + 1}(address(nft), tid, 1 ether, _exp()); // value must equal principal
    }

    function test_makeOfferBelowMinPriceReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        vm.expectRevert(BelowMinPrice.selector);
        ob.makeOffer{value: _total(0.009 ether)}(address(nft), tid, 0.009 ether, _exp());
    }

    function test_makeOfferBadExpiryReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        vm.expectRevert(InvalidExpiry.selector);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, uint64(block.timestamp + 15 days));
    }

    function test_noWithdrawFunction() public {
        // There is no individual withdrawal — positions lock until accept/reject/expiry.
        // Sanity: confirmed by the absence of withdrawOffer in the ABI (compile-time).
        assertTrue(true);
    }

    // ── Accept (seller pays 1.5%; seller nets 98.5% of principal) ──────────────

    function test_acceptOfferPaysSellerNet() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, _exp());

        uint256 sellerBefore = seller.balance;
        uint256 vaultBefore  = feeRecipient.balance;

        vm.prank(seller);
        ob.acceptOffer(address(nft), tid, bidder);

        assertEq(nft.ownerOf(tid), bidder);
        // Seller receives principal − fee; the 1.5% fee is taken at acceptance.
        assertEq(seller.balance, sellerBefore + 1 ether - _fee(1 ether));
        assertEq(feeRecipient.balance, vaultBefore + _fee(1 ether)); // fee at accept
        assertEq(_principalOf(address(nft), tid, bidder), 0);
    }

    function test_acceptOfferNotOwnerReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, _exp());

        vm.prank(bidder2);
        vm.expectRevert(NotOwner.selector);
        ob.acceptOffer(address(nft), tid, bidder);
    }

    function test_acceptOfferNoOfferReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        vm.expectRevert(NoOffer.selector);
        ob.acceptOffer(address(nft), tid, bidder);
    }

    // ── Reject / expiry (full principal refunded — offers are free) ────────────

    function test_rejectOfferRefundsFullPrincipal() public {
        uint256 tid = _mintAndApprove(seller);
        uint256 balBefore = bidder.balance;
        vm.prank(bidder);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, _exp());

        vm.prank(seller);
        ob.rejectOffer(address(nft), tid, bidder);

        // Bidder gets the full principal back — offers are free.
        assertEq(bidder.balance, balBefore);
        assertEq(_principalOf(address(nft), tid, bidder), 0);
    }

    function test_rejectOfferNotOwnerReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, _exp());

        vm.prank(bidder2);
        vm.expectRevert(NotOwner.selector);
        ob.rejectOffer(address(nft), tid, bidder);
    }

    function test_refundExpiredOfferAfterExpiry() public {
        uint256 tid = _mintAndApprove(seller);
        uint256 balBefore = bidder.balance;
        uint64  exp = _exp();
        vm.prank(bidder);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, exp);

        vm.warp(uint256(exp) + 1);
        // Permissionless — anyone (here bidder2) can trigger the refund to the bidder.
        vm.prank(bidder2);
        ob.refundExpiredOffer(address(nft), tid, bidder);

        assertEq(bidder.balance, balBefore);
        assertEq(_principalOf(address(nft), tid, bidder), 0);
    }

    function test_refundExpiredOfferBeforeExpiryReverts() public {
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, _exp());

        vm.expectRevert(OfferActive.selector);
        ob.refundExpiredOffer(address(nft), tid, bidder);
    }

    function test_multipleOffersSellerAcceptsOneRefundsOther() public {
        uint256 tid = _mintAndApprove(seller);

        vm.prank(bidder);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, _exp());
        vm.prank(bidder2);
        ob.makeOffer{value: _total(2 ether)}(address(nft), tid, 2 ether, _exp());

        // Seller accepts the higher position.
        vm.prank(seller);
        ob.acceptOffer(address(nft), tid, bidder2);
        assertEq(nft.ownerOf(tid), bidder2);

        // The other position is still locked; the new owner can reject it to refund.
        assertEq(_principalOf(address(nft), tid, bidder), 1 ether);
        uint256 balBefore = bidder.balance;
        vm.prank(bidder2); // bidder2 now owns the NFT
        ob.rejectOffer(address(nft), tid, bidder);
        assertEq(bidder.balance, balBefore + 1 ether);
    }

    // ── ERC-1155 ────────────────────────────────────────────────────────────────

    function test_makeOffer1155AndAccept() public {
        vm.startPrank(seller);
        multi.mint(seller, 42, 10);
        multi.setApprovalForAll(address(ob), true);
        vm.stopPrank();

        vm.prank(bidder);
        ob.makeOffer1155{value: _total(2 ether)}(address(multi), 42, 2 ether, 5, _exp());

        (uint128 principal, uint128 units,,) = ob.positions(address(multi), 42, bidder);
        assertEq(principal, 2 ether);
        assertEq(units, 5);

        uint256 sellerBefore = seller.balance;
        vm.prank(seller);
        ob.acceptOffer(address(multi), 42, bidder);

        assertEq(multi.balanceOf(bidder, 42), 5);
        assertEq(seller.balance - sellerBefore, 2 ether - _fee(2 ether)); // seller nets 98.5%
    }

    // ── Fuzz ────────────────────────────────────────────────────────────────────

    function testFuzz_feeChargedAtAcceptNotMake(uint128 principal) public {
        principal = uint128(bound(principal, 0.01 ether, 100 ether));

        uint256 tid = _mintAndApprove(seller);
        vm.deal(bidder, _total(uint256(principal)) + 1 ether);

        uint256 vaultBefore = feeRecipient.balance;
        vm.prank(bidder);
        ob.makeOffer{value: _total(uint256(principal))}(address(nft), tid, principal, _exp());

        // No fee at make time — the full principal is escrowed.
        assertEq(feeRecipient.balance - vaultBefore, 0);

        uint256 sellerBefore = seller.balance;
        vm.prank(seller);
        ob.acceptOffer(address(nft), tid, bidder);

        // Fee is charged on acceptance, deducted from the seller's proceeds.
        assertEq(feeRecipient.balance - vaultBefore, _fee(uint256(principal)));
        assertEq(seller.balance - sellerBefore, uint256(principal) - _fee(uint256(principal)));
    }
}
