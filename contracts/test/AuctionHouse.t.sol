// SPDX-License-Identifier: MIT
pragma solidity 0.8.24;

import {Test} from "forge-std/Test.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {MockERC721} from "./MockERC721.sol";

contract AuctionHouseTest is Test {
    AuctionHouse ah;
    MockERC721 nft;
    address feeVault = address(0x1111000000000000000000000000000000111100);
    uint16 feeBps = 250;
    address seller = address(0xBEEF);
    address alice = address(0xA11CE);
    address bob = address(0xB0B);

    function setUp() public {
        ah = new AuctionHouse(feeVault, feeBps);
        nft = new MockERC721();
        vm.deal(alice, 100 ether);
        vm.deal(bob, 100 ether);
    }

    function _createAuction() internal returns (uint256 id, uint256 tid) {
        vm.startPrank(seller);
        tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint64 start = uint64(block.timestamp);
        uint64 end = uint64(block.timestamp + 7 days);
        id = ah.create(address(nft), tid, 1 ether, start, end, 500);
        vm.stopPrank();
    }

    function test_outbidLoserGetsFullRefundViaPendingReturns() public {
        (uint256 id,) = _createAuction();

        vm.prank(alice);
        ah.bid{value: 1 ether}(id);

        vm.prank(bob);
        ah.bid{value: 2 ether}(id);

        assertEq(ah.pendingReturns(alice), 1 ether);
        // Alice's 1 ETH is a pull liability; Bob's 2 ETH is the active high bid — contract holds 3 ETH until Alice withdraws.
        assertEq(address(ah).balance, 3 ether);

        uint256 balBefore = alice.balance;
        vm.prank(alice);
        ah.withdrawRefund();
        assertEq(alice.balance, balBefore + 1 ether);
        assertEq(ah.pendingReturns(alice), 0);
    }

    function test_leaderCompoundsBidWithIncrementOnly() public {
        (uint256 id,) = _createAuction();

        vm.prank(alice);
        ah.bid{value: 1 ether}(id);

        // Raise own bid by 0.6 ether → 1.6 total (min increment 500 bps of 1 ether = 0.05 ether → min next 1.05)
        vm.prank(alice);
        ah.bid{value: 0.6 ether}(id);

        (
            ,
            ,
            ,
            ,
            ,
            ,
            ,
            ,
            ,
            uint128 hi,
            ,
        ) = ah.auctions(id);
        assertEq(hi, 1.6 ether);
        assertEq(ah.pendingReturns(alice), 0);
        assertEq(address(ah).balance, 1.6 ether);
    }

    function test_feeOnlyOnSettleAfterTransfer() public {
        (uint256 id, uint256 tid) = _createAuction();

        vm.prank(bob);
        ah.bid{value: 2 ether}(id);

        vm.warp(block.timestamp + 8 days);

        uint256 vaultBefore = feeVault.balance;
        uint256 sellerBefore = seller.balance;

        uint256 fee = (uint256(2 ether) * uint256(feeBps)) / 10_000;
        uint256 sellerPayout = 2 ether - fee;

        ah.settle(id);

        assertEq(nft.ownerOf(tid), bob);
        assertEq(feeVault.balance, vaultBefore + fee);
        assertEq(seller.balance, sellerBefore + sellerPayout);
    }
}
