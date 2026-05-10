// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Test} from "forge-std/Test.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {MockERC721} from "./MockERC721.sol";

/// @dev Helper that reverts on receive — simulates a malicious bidder that would block push-refund.
contract RevertingBidder {
    AuctionHouse immutable ah;
    constructor(AuctionHouse _ah) payable { ah = _ah; }
    function placeBid(uint256 id, uint256 amt) external { ah.bid{value: amt}(id); }
    function tryWithdraw() external { ah.withdrawRefund(); }
    receive() external payable { revert("nope"); }
}

contract AuctionHouseTest is Test {
    AuctionHouse ah;
    MockERC721   nft;
    address admin   = address(0xA11CE);
    address creator = address(0xC0DE);
    address seller  = address(0xBEEF);
    address bidderA = address(0xAAA);
    address bidderB = address(0xBBB);
    address bidderC = address(0xCCC);

    function setUp() public {
        ah  = new AuctionHouse(admin, creator, 250);
        nft = new MockERC721();
        vm.deal(bidderA, 10 ether);
        vm.deal(bidderB, 10 ether);
        vm.deal(bidderC, 10 ether);
    }

    function _create(uint128 reserve) internal returns (uint256 id, uint256 tid) {
        vm.startPrank(seller);
        tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        id  = ah.create(address(nft), tid, reserve, uint64(block.timestamp), uint64(block.timestamp + 1 days), 500);
        vm.stopPrank();
    }

    function test_createBidSettle() public {
        (uint256 id, uint256 tid) = _create(0.5 ether);

        vm.prank(bidderA);
        ah.bid{value: 0.5 ether}(id);

        vm.prank(bidderB);
        ah.bid{value: 0.6 ether}(id);

        // pull-pattern: A's refund credited but balance not yet returned
        assertEq(bidderA.balance, 9.5 ether);
        assertEq(ah.pendingReturns(bidderA), 0.5 ether);

        vm.prank(bidderA);
        ah.withdrawRefund();
        assertEq(bidderA.balance, 10 ether);
        assertEq(ah.pendingReturns(bidderA), 0);

        vm.warp(block.timestamp + 2 days);
        ah.settle(id);

        assertEq(nft.ownerOf(tid), bidderB);
        assertEq(creator.balance, 0.015 ether);
    }

    function test_bidTooLowReverts() public {
        (uint256 id,) = _create(1 ether);
        vm.prank(bidderA);
        vm.expectRevert();
        ah.bid{value: 0.9 ether}(id);
    }

    function test_settleNoBidsReverts() public {
        (uint256 id,) = _create(1 ether);
        vm.warp(block.timestamp + 2 days);
        vm.expectRevert();
        ah.settle(id);
    }

    /// @dev Proves the DOS fix: a contract bidder that reverts on receive() cannot block outbids.
    function test_maliciousBidderCannotBlockOutbid() public {
        (uint256 id,) = _create(0.5 ether);

        RevertingBidder mal = new RevertingBidder{value: 1 ether}(ah);
        mal.placeBid(id, 0.5 ether);

        // Outbid would have reverted under push-refund; passes under pull-refund.
        vm.prank(bidderC);
        ah.bid{value: 0.6 ether}(id);

        assertEq(ah.pendingReturns(address(mal)), 0.5 ether);

        // Malicious withdraw still fails (its own receive reverts) — but other users unaffected.
        vm.expectRevert();
        mal.tryWithdraw();
    }

    function test_withdrawRefundEmptyReverts() public {
        vm.prank(bidderA);
        vm.expectRevert();
        ah.withdrawRefund();
    }

    function test_cancelWithBidsReverts() public {
        (uint256 id,) = _create(0.5 ether);
        vm.prank(bidderA);
        ah.bid{value: 0.5 ether}(id);
        vm.prank(seller);
        vm.expectRevert();
        ah.cancel(id);
    }
}
