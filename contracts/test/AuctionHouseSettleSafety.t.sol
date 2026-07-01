// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}        from "forge-std/Test.sol";
import {AuctionHouse, NotActive, NotStalled, StallNotOver, BidTooLow, AuctionEnded} from "../src/AuctionHouse.sol";
import {MockERC721}  from "./MockERC721.sol";
import {MockERC1155} from "./MockERC1155.sol";

/// Recipient that rejects all ETH (no receive/fallback) → forces pull-fallback.
contract RejectEther {
    // no receive(); any plain transfer reverts
}

/// @dev Contract bidder that can be toggled to reject ERC1155 onERC1155Received.
contract MaliciousBidder {
    bool public blockReceive;
    bool public blockERC1155Receive;

    constructor() payable {
        blockReceive = true;
        blockERC1155Receive = true;
    }

    receive() external payable {
        if (blockReceive) revert("blocked");
    }

    function setBlockReceive(bool b) external { blockReceive = b; }
    function setBlockERC1155Receive(bool b) external { blockERC1155Receive = b; }

    function onERC1155Received(address, address, uint256, uint256, bytes calldata)
        external view returns (bytes4)
    {
        if (blockERC1155Receive) revert("no ERC1155");
        return this.onERC1155Received.selector;
    }

    function onERC721Received(address, address, uint256, bytes calldata)
        external pure returns (bytes4)
    {
        return this.onERC721Received.selector;
    }

    function bidOn(AuctionHouse ah, uint256 id) external payable {
        ah.bid{value: msg.value}(id);
    }
}

contract AuctionHouseSettleSafetyTest is Test {
    AuctionHouse ah;
    MockERC721   nft;
    MockERC1155  multi;
    address feeRecipient = address(0x1111000000000000000000000000000000111100);
    address seller = address(0xBEEF);
    address alice  = address(0xA11CE);
    address carol  = address(0xCab01);

    function setUp() public {
        ah  = new AuctionHouse(feeRecipient, address(0));
        nft = new MockERC721();
        multi = new MockERC1155();
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

    /// C-02 fix: when the seller moves the NFT away, settle() refunds immediately.
    function test_settleRefundsImmediatelyWhenSellerMovedNft() public {
        (uint256 id, uint256 tid) = _setup();
        vm.warp(block.timestamp + 2 days);
        vm.prank(seller);
        nft.transferFrom(seller, carol, tid);

        uint256 before = alice.balance;
        ah.settle(id);

        assertEq(alice.balance, before + 2 ether, "winner auto-refunded when seller moved NFT");
        assertEq(ah.cumulative(id, alice), 0, "winner escrow consumed from ledger");
        assertEq(nft.ownerOf(tid), carol, "NFT not delivered to winner");
        assertEq(feeRecipient.balance, 0, "no fee on cancelled settlement");

        AuctionHouse.Auction memory a = ah.getAuction(id);
        assertTrue(a.settled);
    }

    /// C-02 fix: seller revokes approval → immediate refund, not stall.
    function test_settleRefundsImmediatelyWhenApprovalRevoked() public {
        (uint256 id, uint256 tid) = _setup();
        vm.warp(block.timestamp + 2 days);
        vm.prank(seller);
        nft.setApprovalForAll(address(ah), false);

        uint256 before = alice.balance;
        ah.settle(id);

        assertEq(alice.balance, before + 2 ether, "winner auto-refunded when seller revoked approval");
        assertEq(ah.cumulative(id, alice), 0, "winner escrow consumed from ledger");
        assertEq(nft.ownerOf(tid), seller, "NFT stays with seller");
        assertEq(feeRecipient.balance, 0, "no fee on cancelled settlement");

        AuctionHouse.Auction memory a = ah.getAuction(id);
        assertTrue(a.settled);
    }

    /// C-01 fix: ERC721 uses transferFrom — normal settlement succeeds.
    function test_erc721TransferFromSettleWorks() public {
        (uint256 id, uint256 tid) = _setup();
        vm.warp(block.timestamp + 2 days);

        ah.settle(id);

        assertEq(nft.ownerOf(tid), alice, "NFT delivered via transferFrom");
        AuctionHouse.Auction memory a = ah.getAuction(id);
        assertTrue(a.settled);
    }

    /// C-01 fix: for ERC1155, buyer-fault stall path — seller is ready
    /// (approved + owns) but safeTransferFrom fails due to buyer's receiver.
    function test_erc1155BuyerFaultCausesStall() public {
        MaliciousBidder bidder = new MaliciousBidder();
        vm.deal(address(bidder), 100 ether);

        vm.startPrank(seller);
        multi.mint(seller, 7, 5);
        multi.setApprovalForAll(address(ah), true);
        uint256 id = ah.create1155(address(multi), 7, 5, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();

        vm.prank(address(bidder));
        ah.bid{value: 2 ether}(id);
        vm.warp(block.timestamp + 2 days);

        // settle() → buyer's receiver reverts → seller is ready → stall.
        ah.settle(id);

        AuctionHouse.Auction memory a = ah.getAuction(id);
        assertFalse(a.settled, "auction NOT settled (stalled)");
        assertGt(a.stalledAt, 0, "stalledAt is set");

        // Bidder fixes their contract and settleUnstuck succeeds.
        bidder.setBlockERC1155Receive(false);
        ah.settleUnstuck(id);

        a = ah.getAuction(id);
        assertTrue(a.settled, "settled after unstuck");
        assertEq(multi.balanceOf(address(bidder), 7), 5, "bidder received ERC1155");
    }

    /// C-01/C-02: calling settle() on a stalled auction must revert with NotStalled.
    /// The stall path parks the auction for settleUnstuck() / reclaim(); a direct
    /// settle() call after stall must be rejected so the caller doesn't bypass
    /// the STALL_WINDOW waiting period.
    function test_settleOnStalledAuctionReverts() public {
        MaliciousBidder bidder = new MaliciousBidder();
        vm.deal(address(bidder), 100 ether);

        vm.startPrank(seller);
        multi.mint(seller, 7, 5);
        multi.setApprovalForAll(address(ah), true);
        uint256 id = ah.create1155(address(multi), 7, 5, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();

        vm.prank(address(bidder));
        ah.bid{value: 2 ether}(id);
        vm.warp(block.timestamp + 2 days);

        // settle() stalls (ERC-1155 buyer-fault)
        ah.settle(id);
        AuctionHouse.Auction memory a = ah.getAuction(id);
        assertGt(a.stalledAt, 0, "stalled");
        assertFalse(a.settled, "not settled");

        // Calling settle() again on a stalled auction must revert.
        vm.expectRevert(NotStalled.selector);
        ah.settle(id);
    }

    /// R-04: calling settleUnstuck() on a stalled auction where the buyer
    /// still hasn't fixed their contract must NOT refresh stalledAt.
    /// The first-stall timestamp is immutable so the reclaim() 7-day window
    /// is deterministic — repeated settleUnstuck calls cannot reset it.
    function test_settleUnstuckBuyerFaultPreservesStalledAt() public {
        MaliciousBidder bidder = new MaliciousBidder();
        vm.deal(address(bidder), 100 ether);

        vm.startPrank(seller);
        multi.mint(seller, 7, 5);
        multi.setApprovalForAll(address(ah), true);
        uint256 id = ah.create1155(address(multi), 7, 5, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();

        vm.prank(address(bidder));
        ah.bid{value: 2 ether}(id);
        vm.warp(block.timestamp + 2 days);

        // First settle → stalls
        ah.settle(id);
        uint64 firstStalled = ah.getAuction(id).stalledAt;
        assertGt(firstStalled, 0, "stalled");

        // Warp forward 1 day — buyer still hasn't fixed
        vm.warp(block.timestamp + 1 days);

        // settleUnstuck with buyer still blocking → fails again
        // R-04: stalledAt MUST NOT be refreshed
        ah.settleUnstuck(id);
        AuctionHouse.Auction memory a = ah.getAuction(id);
        assertEq(a.stalledAt, firstStalled, "stalledAt unchanged (R-04)");
        assertFalse(a.settled, "still not settled");

        // Warp past STALL_WINDOW from original stall time
        vm.warp(uint256(firstStalled) + ah.STALL_WINDOW());
        uint256 winnerBefore = address(bidder).balance;
        ah.reclaim(id);
        assertEq(address(bidder).balance, winnerBefore + 2 ether, "reclaim refunds winner");
        assertTrue(ah.getAuction(id).settled, "settled after reclaim");
    }

    /// feeRecipient cannot receive ETH → settle still completes.
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
