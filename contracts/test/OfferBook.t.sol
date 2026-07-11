// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {OfferBook, NoOffer, OfferActive, OfferExpired, NotOwner, NotApproved, WrongValue, NotKeeper, OffersNotEligible} from "../src/OfferBook.sol";
import {MarketplaceManager} from "../src/MarketplaceManager.sol";
import {MockERC721} from "./MockERC721.sol";
import {MockERC1155} from "./MockERC1155.sol";
import {TokenStandard, InvalidDuration, BelowMinPrice} from "../src/MarketplaceCore.sol";
import {TestHelpers} from "./TestHelpers.sol";

contract OfferBookTest is Test, TestHelpers {
    OfferBook ob;
    MockERC721 nft;
    MockERC1155 multi;
    address feeRecipient = address(0x1111000000000000000000000000000000111100);
    address seller = address(0xBEEF);
    address alice = address(0xA11CE);
    address bob = address(0xB0B);

    function setUp() public {
        MarketplaceManager mgr = _deployMarketplaceManager(address(this));
        ob = _deployOfferBook(feeRecipient, address(mgr));
        nft = new MockERC721();
        multi = new MockERC1155();
        vm.deal(alice, 100 ether);
        vm.deal(bob, 100 ether);
        vm.deal(seller, 100 ether);
        // Token 0 is owned by this test → enable offers
        ob.setOfferEligible(address(nft), true);
        ob.setOfferEligible(address(multi), true);
    }

    function _fee(uint128 v) internal pure returns (uint256) { return uint256(v) * 150 / 10_000; }

    function test_makeOfferEscrowsPrincipal() public {
        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), 0, 1 ether, uint64(block.timestamp + 24 hours));
        (uint128 p,,,) = ob.positions(address(nft), 0, alice);
        assertEq(p, 1 ether);
    }

    function test_makeOfferEditReplacesNotCompounds() public {
        vm.prank(alice);
        ob.makeOffer{value: 5 ether}(address(nft), 0, 5 ether, uint64(block.timestamp + 24 hours));

        vm.prank(alice);
        ob.makeOffer{value: 2 ether}(address(nft), 0, 2 ether, uint64(block.timestamp + 4 hours));

        (uint128 p,,,) = ob.positions(address(nft), 0, alice);
        // Not compounded: position shows 2 ether, not 7 ether.
        assertEq(p, 2 ether);
        // Alice received 5 ether refund from edit (old principal returned).
        assertEq(alice.balance, 100 ether - 2 ether, "net paid 2 ether after edit-down");
    }

    function test_acceptOfferSellerPaid() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ob), true);
        vm.stopPrank();

        vm.prank(alice);
        ob.makeOffer{value: 2 ether}(address(nft), tid, 2 ether, uint64(block.timestamp + 24 hours));

        uint256 sb = seller.balance;
        vm.prank(seller);
        ob.acceptOffer(address(nft), tid, alice);
        assertEq(nft.ownerOf(tid), alice);
        assertEq(seller.balance, sb + 2 ether - _fee(2 ether));
    }

    function test_rejectOfferRefundsFull() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ob), true);
        vm.stopPrank();

        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, uint64(block.timestamp + 24 hours));

        vm.prank(seller);
        ob.rejectOffer(address(nft), tid, alice);
        (uint128 p,,,) = ob.positions(address(nft), tid, alice);
        assertEq(p, 0);
        assertEq(alice.balance, 100 ether);
    }

    function test_cancelOfferFullRefund() public {
        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), 0, 1 ether, uint64(block.timestamp + 24 hours));

        uint256 aBefore = alice.balance;
        vm.prank(alice);
        ob.cancelOffer(address(nft), 0);
        assertEq(alice.balance, aBefore + 1 ether);
    }

    function test_cancelExpiredReverts() public {
        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), 0, 1 ether, uint64(block.timestamp + 3 minutes));

        vm.warp(block.timestamp + 5 minutes);
        vm.prank(alice);
        vm.expectRevert(OfferExpired.selector);
        ob.cancelOffer(address(nft), 0);
    }

    function test_refundExpiredOffer() public {
        // manager is set in setUp → refundExpiredOffer requires KEEPER_ROLE.
        // Grant bob KEEPER_ROLE.
        MarketplaceManager mgr = MarketplaceManager(ob.manager());
        mgr.grantRole(mgr.KEEPER_ROLE(), bob);

        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), 0, 1 ether, uint64(block.timestamp + 3 minutes));

        vm.warp(block.timestamp + 5 minutes);
        uint256 aBefore = alice.balance;
        vm.prank(bob);
        ob.refundExpiredOffer(address(nft), 0, alice);
        assertEq(alice.balance, aBefore + 1 ether);
    }

    function test_acceptOffer1155TransfersUnits() public {
        vm.startPrank(seller);
        multi.mint(seller, 7, 5);
        multi.setApprovalForAll(address(ob), true);
        vm.stopPrank();

        vm.prank(alice);
        ob.makeOffer1155{value: 1 ether}(address(multi), 7, 1 ether, 3, uint64(block.timestamp + 24 hours));

        vm.prank(seller);
        ob.acceptOffer(address(multi), 7, alice);
        assertEq(multi.balanceOf(alice, 7), 3);
    }

    function testFuzz_feeChargedAtAcceptNotMake(uint128 principal) public {
        principal = uint128(bound(principal, 1 ether, 50 ether));
        vm.deal(alice, uint256(principal) + 10 ether);
        vm.deal(seller, 10 ether);

        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ob), true);
        vm.stopPrank();

        vm.prank(alice);
        ob.makeOffer{value: uint256(principal)}(address(nft), tid, principal, uint64(block.timestamp + 24 hours));
        // No fee at makeOffer — alice's full amount escrowed.
        (uint128 pEscrow,,,) = ob.positions(address(nft), tid, alice);
        assertEq(pEscrow, principal);

        uint256 sb = seller.balance;
        vm.prank(seller);
        ob.acceptOffer(address(nft), tid, alice);
        uint256 fee = uint256(principal) * 150 / 10_000;
        assertEq(seller.balance, sb + uint256(principal) - fee);
    }
}
