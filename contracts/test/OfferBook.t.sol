// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {OfferBook, NoOffer, OfferActive, OfferExpired, NotOwner, NotApproved, WrongValue, NotKeeper, OffersNotEligible, PrincipalChanged} from "../src/OfferBook.sol";
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

    // 2% total (1.5% feeRecipient + 0.5% keeper); seller nets principal - _fee.
    function _fee(uint128 v) internal pure returns (uint256) { return uint256(v) * 200 / 10_000; }

    function test_makeOfferEscrowsPrincipal() public {
        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), 0, 1 ether, uint64(24 hours));
        (uint128 p,,,) = ob.positions(address(nft), 0, alice);
        assertEq(p, 1 ether);
    }

    function test_makeOfferEditReplacesNotCompounds() public {
        vm.prank(alice);
        ob.makeOffer{value: 5 ether}(address(nft), 0, 5 ether, uint64(24 hours));

        vm.prank(alice);
        ob.makeOffer{value: 2 ether}(address(nft), 0, 2 ether, uint64(4 hours));

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
        ob.makeOffer{value: 2 ether}(address(nft), tid, 2 ether, uint64(24 hours));

        uint256 sb = seller.balance;
        vm.prank(seller);
        ob.acceptOffer(address(nft), tid, alice, 2 ether);
        assertEq(nft.ownerOf(tid), alice);
        assertEq(seller.balance, sb + 2 ether - _fee(2 ether));
    }

    function test_acceptOffer_expired_reverts() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ob), true);
        vm.stopPrank();

        vm.prank(alice);
        ob.makeOffer{value: 2 ether}(address(nft), tid, 2 ether, uint64(3 minutes));

        // Past expiry the position belongs to the refund path: the owner must
        // not be able to force the trade (e.g. front-running refundExpiredOffer).
        vm.warp(block.timestamp + 3 minutes);
        vm.prank(seller);
        vm.expectRevert(OfferExpired.selector);
        ob.acceptOffer(address(nft), tid, alice, 2 ether);

        // The bidder's escrow is still fully recoverable via the refund path.
        uint256 ab = alice.balance;
        vm.prank(alice); // bidder can always reclaim their own expired escrow
        ob.refundExpiredOffer(address(nft), tid, alice);
        assertEq(alice.balance, ab + 2 ether);
    }

    function test_rejectOfferRefundsFull() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ob), true);
        vm.stopPrank();

        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, uint64(24 hours));

        vm.prank(seller);
        ob.rejectOffer(address(nft), tid, alice);
        (uint128 p,,,) = ob.positions(address(nft), tid, alice);
        assertEq(p, 0);
        assertEq(alice.balance, 100 ether);
    }

    function test_cancelOfferFullRefund() public {
        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), 0, 1 ether, uint64(24 hours));

        uint256 aBefore = alice.balance;
        vm.prank(alice);
        ob.cancelOffer(address(nft), 0);
        assertEq(alice.balance, aBefore + 1 ether);
    }

    function test_cancelExpiredReverts() public {
        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), 0, 1 ether, uint64(3 minutes));

        vm.warp(block.timestamp + 5 minutes);
        vm.prank(alice);
        vm.expectRevert(OfferExpired.selector);
        ob.cancelOffer(address(nft), 0);
    }

    function test_refundExpiredOffer() public {
        // manager is set in setUp → refundExpiredOffer requires KEEPER_ROLE.
        // Grant bob KEEPER_ROLE.
        MarketplaceManager mgr = MarketplaceManager(ob.manager());
        mgr.setKeeper(bob);

        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), 0, 1 ether, uint64(3 minutes));

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
        ob.makeOffer1155{value: 1 ether}(address(multi), 7, 1 ether, 3, uint64(24 hours));

        vm.prank(seller);
        ob.acceptOffer(address(multi), 7, alice, 1 ether);
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
        ob.makeOffer{value: uint256(principal)}(address(nft), tid, principal, uint64(24 hours));
        // No fee at makeOffer — alice's full amount escrowed.
        (uint128 pEscrow,,,) = ob.positions(address(nft), tid, alice);
        assertEq(pEscrow, principal);

        uint256 sb = seller.balance;
        vm.prank(seller);
        ob.acceptOffer(address(nft), tid, alice, principal);
        uint256 fee = uint256(principal) * 200 / 10_000;
        assertEq(seller.balance, sb + uint256(principal) - fee);
    }

    /// A bidder who re-prices downward while the seller's acceptOffer sits in
    /// the mempool must not get the NFT at the new price.
    function test_acceptOffer_repricedUnderSeller_reverts() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ob), true);
        vm.stopPrank();

        vm.prank(alice);
        ob.makeOffer{value: 5 ether}(address(nft), tid, 5 ether, 24 hours);

        // Alice front-runs with an edit down to 1 ether.
        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, 1 days);

        vm.prank(seller);
        vm.expectRevert(PrincipalChanged.selector);
        ob.acceptOffer(address(nft), tid, alice, 5 ether);
        assertEq(nft.ownerOf(tid), seller, "NFT stays with seller");

        // Accepting the price actually on-chain still works.
        uint256 sb = seller.balance;
        vm.prank(seller);
        ob.acceptOffer(address(nft), tid, alice, 1 ether);
        assertEq(nft.ownerOf(tid), alice);
        assertEq(seller.balance, sb + 1 ether - _fee(1 ether));
    }

    /// Exits stay unstoppable: the bidder reclaims their own expired escrow
    /// even with a manager configured and no keeper available.
    function test_refundExpiredOffer_bidderSelfReclaim() public {
        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), 0, 1 ether, uint64(3 minutes));

        vm.warp(block.timestamp + 5 minutes);
        uint256 aBefore = alice.balance;
        vm.prank(alice);
        ob.refundExpiredOffer(address(nft), 0, alice);
        assertEq(alice.balance, aBefore + 1 ether);
        (uint128 p,,,) = ob.positions(address(nft), 0, alice);
        assertEq(p, 0, "position cleared");
    }

    /// Third parties still cannot move someone else's escrow without the role.
    function test_refundExpiredOffer_thirdParty_stillNeedsKeeper() public {
        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), 0, 1 ether, uint64(3 minutes));

        vm.warp(block.timestamp + 5 minutes);
        vm.prank(bob);
        vm.expectRevert(NotKeeper.selector);
        ob.refundExpiredOffer(address(nft), 0, alice);
    }

    /// Durations are validated on-chain; anything but the fifteen shared values reverts.
    function test_makeOffer_badDuration_revertsInvalidDuration() public {
        vm.prank(alice);
        vm.expectRevert(InvalidDuration.selector);
        ob.makeOffer{value: 1 ether}(address(nft), 0, 1 ether, 7 minutes);
        vm.prank(alice);
        vm.expectRevert(InvalidDuration.selector);
        ob.makeOffer{value: 1 ether}(address(nft), 0, 1 ether, 2 hours + 1);
        vm.prank(alice);
        vm.expectRevert(InvalidDuration.selector);
        ob.makeOffer{value: 1 ether}(address(nft), 0, 1 ether, 0);
    }

    /// The contract computes expiresAt = block.timestamp + duration, so a wallet
    /// never has to guess the mining block's timestamp.
    function test_makeOffer_expiryComputedOnChain() public {
        vm.warp(1_700_000_000);
        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), 0, 1 ether, 15 minutes);
        (, , uint64 expiresAt, ) = ob.positions(address(nft), 0, alice);
        assertEq(expiresAt, 1_700_000_000 + 15 minutes);
    }
}
