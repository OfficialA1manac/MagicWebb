// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}       from "forge-std/Test.sol";
import {AuctionHouse, NotActive} from "../src/AuctionHouse.sol";
import {MockERC721} from "./MockERC721.sol";

/// @dev Rejects all incoming ETH — used to exercise the pull-pattern fallback.
contract RejectEther {
    receive() external payable { revert("no ether"); }
}

/// @notice Regression tests for the settle() locked-funds fix (finding H1):
///         a finished auction must never strand the winner's escrow, and must never
///         revert because a payout recipient cannot receive ETH.
contract AuctionHouseSettleSafetyTest is Test {
    AuctionHouse ah;
    MockERC721   nft;
    address feeRecipient = address(0x1111000000000000000000000000000000111100);
    address seller = address(0xBEEF);
    address alice  = address(0xA11CE);
    address carol  = address(0xCED1);

    function setUp() public {
        ah  = new AuctionHouse(feeRecipient);
        nft = new MockERC721();
        vm.deal(alice, 100 ether);
    }

    function _bidTotal(uint128 bidAmount) internal pure returns (uint128) {
        return bidAmount; // bidding is free; msg.value equals the bid
    }

    function _setupAuctionWithBid() internal returns (uint256 id, uint256 tid, uint128 total) {
        vm.startPrank(seller);
        tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        id = ah.create(address(nft), tid, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();

        total = _bidTotal(1 ether);
        vm.prank(alice);
        ah.bid{value: total}(id, 1 ether);
    }

    /// Seller moves the NFT out after the auction ends → settle must refund the winner
    /// in full (the bid) and not leave funds locked.
    function test_settleRefundsWinnerWhenSellerMovedNft() public {
        (uint256 id, uint256 tid, uint128 total) = _setupAuctionWithBid();
        vm.warp(block.timestamp + 2 days);

        // seller front-runs settlement by transferring the NFT elsewhere
        vm.prank(seller);
        nft.transferFrom(seller, carol, tid);

        uint256 aliceBefore = alice.balance;
        ah.settle(id);

        assertEq(alice.balance, aliceBefore + total, "winner fully refunded");
        assertEq(nft.ownerOf(tid), carol, "NFT not delivered to winner");
        assertEq(feeRecipient.balance, 0, "no fee taken on failed settlement");

        // auction is closed — a second settle reverts
        vm.expectRevert(NotActive.selector);
        ah.settle(id);
    }

    /// Seller revokes approval after the auction ends → same full refund of the winner.
    function test_settleRefundsWinnerWhenApprovalRevoked() public {
        (uint256 id, uint256 tid, uint128 total) = _setupAuctionWithBid();
        vm.warp(block.timestamp + 2 days);

        vm.prank(seller);
        nft.setApprovalForAll(address(ah), false);

        uint256 aliceBefore = alice.balance;
        ah.settle(id);

        assertEq(alice.balance, aliceBefore + total, "winner fully refunded");
        assertEq(nft.ownerOf(tid), seller, "NFT stays with seller");
        assertEq(feeRecipient.balance, 0, "no fee taken");
    }

    /// feeRecipient cannot receive ETH → settle still completes: NFT → winner,
    /// the fee is parked in pendingReturns instead of bricking the auction.
    function test_settleCompletesWhenFeeRecipientRejectsEther() public {
        RejectEther rej = new RejectEther();
        AuctionHouse ah2 = new AuctionHouse(address(rej));

        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah2), true);
        uint256 id = ah2.create(address(nft), tid, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();

        uint128 bidAmount = 1 ether;
        uint128 total = _bidTotal(bidAmount);
        vm.prank(alice);
        ah2.bid{value: total}(id, bidAmount);

        vm.warp(block.timestamp + 2 days);
        uint256 fee = uint256(bidAmount) * 150 / 10_000;
        uint256 sellerBefore = seller.balance;
        ah2.settle(id);

        assertEq(nft.ownerOf(tid), alice, "winner receives NFT");
        assertEq(seller.balance, sellerBefore + bidAmount - fee, "seller nets bid minus fee");
        assertEq(ah2.pendingReturns(address(rej)), fee, "bounced fee parked for pull-withdrawal");
    }
}
