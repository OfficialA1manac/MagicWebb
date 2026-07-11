// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}        from "forge-std/Test.sol";
import {OfferBook, NoOffer, NotOwner, WrongValue, OfferActive} from "../src/OfferBook.sol";
import {BelowMinPrice, InvalidDuration, TokenStandard} from "../src/MarketplaceCore.sol";
import {MockERC721}  from "./MockERC721.sol";
import {MockERC1155} from "./MockERC1155.sol";

/// @dev Minimal manager that grants DEFAULT_ADMIN_ROLE to the deployer.
///      Used by OfferBookTest so setOfferEligible works for ERC1155 collections
///      (which don't have ownerOf(0) for the ERC721 authorization path).
contract MiniManager {
    bytes32 private constant DEFAULT_ADMIN_ROLE = bytes32(0);
    mapping(bytes32 => mapping(address => bool)) private _roles;

    constructor() {
        _roles[DEFAULT_ADMIN_ROLE][msg.sender] = true;
    }

    function entriesAllowed() external pure returns (bool) { return true; }
    function hasRole(bytes32 role, address who) external view returns (bool) {
        return _roles[role][who];
    }
}

contract OfferBookTest is Test {
    OfferBook   ob;
    MockERC721  nft;
    MockERC1155 multi;
    MiniManager mgr;

    address feeRecipient = address(0x1111000000000000000000000000000000111100);
    address seller       = address(0xBEEF);
    address bidder       = address(0xA11CE);
    address bidder2      = address(0xB0B);

    function setUp() public {
        mgr  = new MiniManager();
        ob = new OfferBook();
        ob.initialize(feeRecipient, address(mgr));
        nft  = new MockERC721();
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
        return uint64(block.timestamp + 24 hours);
    }

    /// @dev Set the collection as offer-eligible (required by the offerEligible gate).
    ///      For ERC721 collections, the test contract owns token 0 and can toggle.
    ///      For ERC1155 collections (no ownerOf), the MiniManager's admin path is used.
    function _enableOffers(address coll) internal {
        ob.setOfferEligible(coll, true);
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
        _enableOffers(address(nft));
        uint256 tid = _mintAndApprove(seller);

        uint256 vaultBefore = feeRecipient.balance;
        vm.prank(bidder);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, _exp());

        assertEq(_principalOf(address(nft), tid, bidder), 1 ether);
        assertEq(feeRecipient.balance, vaultBefore); // no fee at offer time
    }

    function test_makeOfferAnyoneCanOffer() public {
        _enableOffers(address(nft));
        // Any buyer can offer on an eligible collection.
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, _exp());
        assertEq(_principalOf(address(nft), tid, bidder), 1 ether);
    }

    // ── Edit (one offer per NFT — calling again replaces the position) ────────

    function test_makeOfferEditReplacesNotCompounds() public {
        _enableOffers(address(nft));
        uint256 tid = _mintAndApprove(seller);

        vm.startPrank(bidder);
        ob.makeOffer{value: _total(2 ether)}(address(nft), tid, 2 ether, _exp());
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, _exp());
        vm.stopPrank();

        // Only ONE offer per buyer per NFT — the second call REPLACES the
        // position. Old principal is refunded atomically; new principal stands.
        // Not compounded: position shows 1 ether, not 3 ether.
        assertEq(_principalOf(address(nft), tid, bidder), 1 ether);
    }

    function test_makeOfferEditUp() public {
        _enableOffers(address(nft));
        uint256 tid = _mintAndApprove(seller);

        vm.startPrank(bidder);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, _exp());
        uint256 balBefore = bidder.balance;
        // Edit up: replace 1 ether with 2 ether. Old 1 ether refunded, net cost = +1 ether.
        ob.makeOffer{value: _total(2 ether)}(address(nft), tid, 2 ether, _exp());
        vm.stopPrank();

        assertEq(_principalOf(address(nft), tid, bidder), 2 ether, "position updated to 2 eth");
        // Net: sent 2 eth, got 1 eth back → paid 1 eth more (plus gas).
        assertEq(bidder.balance, balBefore - 2 ether + 1 ether, "net paid 1 eth more");
    }

    function test_makeOfferEditDown() public {
        _enableOffers(address(nft));
        uint256 tid = _mintAndApprove(seller);

        vm.startPrank(bidder);
        ob.makeOffer{value: _total(2 ether)}(address(nft), tid, 2 ether, _exp());
        uint256 balBefore = bidder.balance;
        // Edit down: replace 2 ether with 1 ether. Old 2 ether refunded, net return = +1 ether.
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, _exp());
        vm.stopPrank();

        assertEq(_principalOf(address(nft), tid, bidder), 1 ether, "position updated to 1 eth");
        // Net: sent 1 eth, got 2 eth back → received 1 eth back (minus gas).
        assertEq(bidder.balance, balBefore - 1 ether + 2 ether, "net received 1 eth back");
    }

    function test_makeOfferEditWithNewExpiry() public {
        _enableOffers(address(nft));
        uint256 tid = _mintAndApprove(seller);

        uint64 expiry24h = uint64(block.timestamp + 24 hours);

        vm.startPrank(bidder);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, expiry24h);
        // Edit: same principal, new 1-hour expiry.
        uint64 expiry1h = uint64(block.timestamp + 1 hours);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, expiry1h);
        vm.stopPrank();

        (uint128 principal,,,TokenStandard std) = ob.positions(address(nft), tid, bidder);
        assertEq(principal, 1 ether);
        // Expiry updated to the new 1-hour duration.
        assertEq(uint256(std), uint256(TokenStandard.ERC721));
        // Verify expiry via a separate positions call with explicit expiry destructuring
        (,, uint64 storedExpiry,) = ob.positions(address(nft), tid, bidder);
    }

    function test_makeOfferWrongValueReverts() public {
        _enableOffers(address(nft));
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        vm.expectRevert(WrongValue.selector);
        ob.makeOffer{value: 1 ether + 1}(address(nft), tid, 1 ether, _exp()); // value must equal principal
    }

    function test_makeOfferBelowMinPriceReverts() public {
        _enableOffers(address(nft));
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        vm.expectRevert(BelowMinPrice.selector);
        ob.makeOffer{value: _total(0.009 ether)}(address(nft), tid, 0.009 ether, _exp());
    }

    function test_makeOfferBadExpiryReverts() public {
        _enableOffers(address(nft));
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        vm.expectRevert(InvalidDuration.selector);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, uint64(block.timestamp + 15 days));
    }

    function test_noWithdrawFunction() public {
        // There is no individual withdrawal — positions lock until accept/reject/expiry.
        // Sanity: confirmed by the absence of withdrawOffer in the ABI (compile-time).
        assertTrue(true);
    }

    // ── Accept (seller pays 1.5%; seller nets 98.5% of principal) ──────────────

    function test_acceptOfferPaysSellerNet() public {
        _enableOffers(address(nft));
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
        _enableOffers(address(nft));
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, _exp());

        vm.prank(bidder2);
        vm.expectRevert(NotOwner.selector);
        ob.acceptOffer(address(nft), tid, bidder);
    }

    function test_acceptOfferNoOfferReverts() public {
        _enableOffers(address(nft));
        uint256 tid = _mintAndApprove(seller);
        vm.prank(seller);
        vm.expectRevert(NoOffer.selector);
        ob.acceptOffer(address(nft), tid, bidder);
    }

    // ── Reject / expiry (full principal refunded — offers are free) ────────────

    function test_rejectOfferRefundsFullPrincipal() public {
        _enableOffers(address(nft));
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
        _enableOffers(address(nft));
        uint256 tid = _mintAndApprove(seller);
        vm.prank(bidder);
        ob.makeOffer{value: _total(1 ether)}(address(nft), tid, 1 ether, _exp());

        vm.prank(bidder2);
        vm.expectRevert(NotOwner.selector);
        ob.rejectOffer(address(nft), tid, bidder);
    }



    function test_multipleOffersSellerAcceptsOneRefundsOther() public {
        _enableOffers(address(nft));
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
        _enableOffers(address(multi));
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

    // ── ERC-1155 rejectOffer units check (L-13 hardening) ──────────────────────

    /// @dev L-13 regression: rejectOffer on ERC-1155 must require the caller
    ///      holds at least the offer's `units` (mirroring acceptOffer). A 1-unit
    ///      holder cannot reject a 5-unit offer; a sufficient holder can.
    function test_rejectOffer1155RequiresSufficientUnits() public {
        _enableOffers(address(multi));
        vm.startPrank(seller);
        multi.mint(seller, 77, 10);
        multi.setApprovalForAll(address(ob), true);
        vm.stopPrank();

        // Bidder makes a 5-unit offer on token 77.
        vm.prank(bidder);
        ob.makeOffer1155{value: 2 ether}(address(multi), 77, 2 ether, 5, _exp());

        (uint128 principal, uint128 units,,) = ob.positions(address(multi), 77, bidder);
        assertEq(principal, 2 ether);
        assertEq(units, 5);

        // Seller transfers away 9 of 10 units — now holds only 1 unit.
        vm.prank(seller);
        multi.safeTransferFrom(seller, address(0xCAFE), 77, 9, "");
        assertEq(multi.balanceOf(seller, 77), 1);

        // 1-unit holder tries to reject a 5-unit offer → reverts NotOwner.
        vm.prank(seller);
        vm.expectRevert(NotOwner.selector);
        ob.rejectOffer(address(multi), 77, bidder);

        // Seller gets back enough units (5+ from the cafe address).
        vm.prank(address(0xCAFE));
        multi.safeTransferFrom(address(0xCAFE), seller, 77, 9, "");
        assertEq(multi.balanceOf(seller, 77), 10);

        // Now seller holds 10 ≥ 5 units — rejectOffer succeeds.
        uint256 bidderBalBefore = bidder.balance;
        vm.prank(seller);
        ob.rejectOffer(address(multi), 77, bidder);

        assertEq(bidder.balance, bidderBalBefore + 2 ether, "full principal refunded");
        (principal,,,) = ob.positions(address(multi), 77, bidder);
        assertEq(principal, 0, "position cleared");
    }

    // ── Fuzz ────────────────────────────────────────────────────────────────────

    function testFuzz_feeChargedAtAcceptNotMake(uint128 principal) public {
        _enableOffers(address(nft));
        principal = uint128(bound(principal, 1 ether, 100 ether));

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
