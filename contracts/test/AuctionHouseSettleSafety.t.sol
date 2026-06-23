// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}        from "forge-std/Test.sol";
import {AuctionHouse, NotActive} from "../src/AuctionHouse.sol";
import {MockERC721}  from "./MockERC721.sol";

/// Recipient that rejects all ETH (no receive/fallback) → forces pull-fallback.
contract RejectEther {
    // no receive(); any plain transfer reverts
}

contract AuctionHouseSettleSafetyTest is Test {
    AuctionHouse ah;
    MockERC721   nft;
    address feeRecipient = address(0x1111000000000000000000000000000000111100);
    address seller = address(0xBEEF);
    address alice  = address(0xA11CE);
    address carol  = address(0xCab01);

    function setUp() public {
        ah  = new AuctionHouse(feeRecipient, address(0));
        nft = new MockERC721();
        vm.deal(alice, 100 ether);
    }

    function _fee(uint128 v) internal pure returns (uint256) { return uint256(v) * 150 / 10_000; }

    function _setup() internal returns (uint256 id, uint256 tid) {
        vm.startPrank(seller);
        tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        id = ah.create(address(nft), tid, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();
        vm.prank(alice);
        ah.bid{value: 2 ether}(id);
    }

    /// Audit-#2: when the seller hijacks delivery (moves the NFT elsewhere) after
    /// endsAt, settle() parks the auction INSTEAD of auto-refunding. After
    /// STALL_WINDOW elapses, reclaim() refunds the winner in full.
    function test_settleParksWhenSellerMovedNftThenReclaim() public {
        (uint256 id, uint256 tid) = _setup();
        vm.warp(block.timestamp + 2 days);
        vm.prank(seller);
        nft.transferFrom(seller, carol, tid);

        uint256 before = alice.balance;
        ah.settle(id);

        // settle() parks — no winner refund yet, no fee.
        assertEq(alice.balance, before, "winner NOT auto-refunded on failed delivery");
        assertEq(ah.cumulative(id, alice), 0, "winner escrow consumed from ledger");
        assertEq(nft.ownerOf(tid), carol, "NFT not delivered to winner");
        assertEq(feeRecipient.balance, 0, "no fee on parked settlement");

        // Re-trying settle() is now blocked (NotActive or NotStalled; either is OK).
        vm.expectRevert();
        ah.settle(id);

        // reclaim() must wait STALL_WINDOW before paying the winner.
        vm.warp(block.timestamp + ah.STALL_WINDOW() - 1);
        vm.expectRevert();
        ah.reclaim(id);

        vm.warp(block.timestamp + 1);
        ah.reclaim(id);
        assertEq(alice.balance, before + 2 ether, "winner fully refunded via reclaim");
        assertEq(nft.ownerOf(tid), carol, "NFT still with carol (delivery never succeeded)");
    }

    /// Audit-#2 sibling: seller revokes approval → same parked-then-reclaim path.
    function test_settleParksWhenApprovalRevokedThenReclaim() public {
        (uint256 id, uint256 tid) = _setup();
        vm.warp(block.timestamp + 2 days);
        vm.prank(seller);
        nft.setApprovalForAll(address(ah), false);

        uint256 before = alice.balance;
        ah.settle(id);

        // settle() parks: no payout, no fee.
        assertEq(alice.balance, before, "winner NOT auto-refunded on approval revoke");
        assertEq(ah.cumulative(id, alice), 0, "winner escrow consumed from ledger");
        assertEq(nft.ownerOf(tid), seller, "NFT stays with seller (delivery never succeeded)");
        assertEq(feeRecipient.balance, 0, "no fee on parked settlement");

        // reclaim() is gated by STALL_WINDOW.
        vm.warp(block.timestamp + ah.STALL_WINDOW() - 1);
        vm.expectRevert();
        ah.reclaim(id);

        vm.warp(block.timestamp + 1);
        ah.reclaim(id);
        assertEq(alice.balance, before + 2 ether, "winner fully refunded via reclaim");
        assertEq(nft.ownerOf(tid), seller, "NFT still with seller after reclaim");
    }

    /// feeRecipient cannot receive ETH → settle still completes: NFT → winner,
    /// seller paid bid−fee, the bounced fee parked in pendingReturns.
    function test_settleCompletesWhenFeeRecipientRejectsEther() public {
        RejectEther rej = new RejectEther();
        AuctionHouse ah2 = new AuctionHouse(address(rej), address(0));

        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah2), true);
        uint256 id = ah2.create(address(nft), tid, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();

        uint128 bidAmt = 2 ether;
        vm.prank(alice);
        ah2.bid{value: bidAmt}(id);

        vm.warp(block.timestamp + 2 days);
        uint256 sellerBefore = seller.balance;
        ah2.settle(id);

        assertEq(nft.ownerOf(tid), alice, "winner receives NFT");
        assertEq(seller.balance, sellerBefore + bidAmt - _fee(bidAmt), "seller nets bid minus fee");
        assertEq(ah2.pendingReturns(address(rej)), _fee(bidAmt), "bounced fee parked");
    }
}
