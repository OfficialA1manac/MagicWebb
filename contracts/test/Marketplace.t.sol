// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}         from "forge-std/Test.sol";
import {Marketplace}  from "../src/Marketplace.sol";
import {MockERC721}   from "./MockERC721.sol";
import {MockERC1155}  from "./MockERC1155.sol";
import {MockERC2981}  from "./MockERC2981.sol";

contract MarketplaceTest is Test {
    Marketplace  mp;
    MockERC721   nft;
    MockERC1155  multi;
    address admin   = address(this);
    address creator = address(0xC0DE);
    address seller  = address(0xBEEF);
    address buyer   = address(0xCAFE);
    address other   = address(0xDEAD);

    function setUp() public {
        mp    = new Marketplace(creator, 250, admin);
        nft   = new MockERC721();
        multi = new MockERC1155();
        vm.deal(buyer, 10 ether);
    }

    // ── Helpers ───────────────────────────────────────────────────────────

    function _list(uint256 price, uint64 exp) internal returns (uint256 id) {
        vm.startPrank(seller);
        id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), id, uint128(price), exp);
        vm.stopPrank();
    }

    function _list1155(uint256 tokenId, uint128 units, uint128 price, uint64 exp) internal {
        vm.startPrank(seller);
        multi.mint(seller, tokenId, units);
        multi.setApprovalForAll(address(mp), true);
        mp.list1155(address(multi), tokenId, units, price, exp);
        vm.stopPrank();
    }

    // ── ERC-721 basic flow ────────────────────────────────────────────────

    function test_listAndBuy() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));

        vm.prank(buyer);
        mp.buy{value: 1 ether}(address(nft), id);

        assertEq(nft.ownerOf(id), buyer);
        assertEq(creator.balance, 0.025 ether); // 250 bps of 1 ether, no royalty
        assertEq(seller.balance,  0.975 ether);
    }

    function test_cancel() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.prank(seller);
        mp.cancel(address(nft), id);
        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: 1 ether}(address(nft), id);
    }

    function test_cancelByNonSellerReverts() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.prank(other);
        vm.expectRevert();
        mp.cancel(address(nft), id);
    }

    function test_wrongPriceReverts() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: 0.5 ether}(address(nft), id);
    }

    function test_expiredReverts() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.warp(block.timestamp + 2 days);
        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: 1 ether}(address(nft), id);
    }

    function test_listExpiryInPastReverts() public {
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        vm.expectRevert();
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp));
        vm.stopPrank();
    }

    function test_completedSaleIsFinal() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.prank(buyer);
        mp.buy{value: 1 ether}(address(nft), id);

        vm.prank(seller);
        vm.expectRevert();
        mp.cancel(address(nft), id);

        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: 1 ether}(address(nft), id);

        assertEq(nft.ownerOf(id), buyer);
    }

    // ── ERC-1155 ──────────────────────────────────────────────────────────

    function test_list1155AndBuy() public {
        _list1155(1, 5, 2 ether, uint64(block.timestamp + 1 days));

        vm.prank(buyer);
        mp.buy{value: 2 ether}(address(multi), 1);

        assertEq(multi.balanceOf(buyer,  1), 5);
        assertEq(multi.balanceOf(seller, 1), 0);
        assertEq(creator.balance, 0.05 ether);
        assertEq(seller.balance,  1.95 ether);
    }

    function test_list1155ZeroAmountReverts() public {
        vm.startPrank(seller);
        multi.mint(seller, 2, 10);
        multi.setApprovalForAll(address(mp), true);
        vm.expectRevert();
        mp.list1155(address(multi), 2, 0, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();
    }

    function test_list1155InsufficientBalanceReverts() public {
        vm.startPrank(seller);
        multi.mint(seller, 3, 2);
        multi.setApprovalForAll(address(mp), true);
        vm.expectRevert();
        mp.list1155(address(multi), 3, 5, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();
    }

    function test_list1155NotApprovedReverts() public {
        vm.startPrank(seller);
        multi.mint(seller, 4, 5);
        vm.expectRevert();
        mp.list1155(address(multi), 4, 5, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();
    }

    function test_buy1155Expired() public {
        _list1155(5, 2, 1 ether, uint64(block.timestamp + 1 hours));
        vm.warp(block.timestamp + 2 hours);
        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: 1 ether}(address(multi), 5);
    }

    function test_overwriteBySecondSellerReverts() public {
        _list1155(20, 5, 1 ether, uint64(block.timestamp + 1 days));
        vm.startPrank(other);
        multi.mint(other, 20, 10);
        multi.setApprovalForAll(address(mp), true);
        vm.expectRevert();
        mp.list1155(address(multi), 20, 10, 2 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();
    }

    function test_originalSellerCanRelist() public {
        _list1155(21, 5, 1 ether, uint64(block.timestamp + 1 days));
        vm.prank(seller);
        mp.list1155(address(multi), 21, 5, 2 ether, uint64(block.timestamp + 1 days));
        (address s,,,,) = mp.listings(address(multi), 21);
        assertEq(s, seller);
    }

    function test_cancel1155() public {
        _list1155(6, 3, 1 ether, uint64(block.timestamp + 1 days));
        vm.prank(seller);
        mp.cancel(address(multi), 6);
        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: 1 ether}(address(multi), 6);
    }

    // ── Expiry boundary ───────────────────────────────────────────────────

    function test_listExpiryBeyondMaxReverts() public {
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        vm.expectRevert();
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp + 366 days));
        vm.stopPrank();
    }

    function test_listExpiryAtMaxOk() public {
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp + 365 days));
        vm.stopPrank();
        (address s,,,,) = mp.listings(address(nft), id);
        assertEq(s, seller);
    }

    // ── Royalty split ─────────────────────────────────────────────────────

    function test_erc2981RoyaltyAppliedOnBuy() public {
        address royaltyRecv = address(0xAA01);
        vm.deal(royaltyRecv, 0);

        // ERC-721 with 5% (500 bps) native ERC-2981
        MockERC2981 royaltyNft = new MockERC2981(royaltyRecv, 500);

        vm.startPrank(seller);
        uint256 tid = royaltyNft.mint(seller);
        royaltyNft.setApprovalForAll(address(mp), true);
        mp.list(address(royaltyNft), tid, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();

        uint256 creatorBefore  = creator.balance;
        uint256 sellerBefore   = seller.balance;
        uint256 royaltyBefore  = royaltyRecv.balance;

        vm.prank(buyer);
        mp.buy{value: 1 ether}(address(royaltyNft), tid);

        assertEq(royaltyNft.ownerOf(tid), buyer);

        // fee = 250 bps of 1 ether = 0.025 ether
        // royalty = 500 bps of 1 ether = 0.05 ether
        // seller gets 1 - 0.025 - 0.05 = 0.925 ether
        assertEq(creator.balance   - creatorBefore, 0.025 ether);
        assertEq(royaltyRecv.balance - royaltyBefore, 0.05 ether);
        assertEq(seller.balance    - sellerBefore,  0.925 ether);
    }

    // ── Pause ─────────────────────────────────────────────────────────────

    function test_pauseBlocksBuy() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        mp.pause();
        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: 1 ether}(address(nft), id);
    }

    function test_pauseBlocksList() public {
        mp.pause();
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        vm.expectRevert();
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();
    }

    function test_cancelWorksWhilePaused() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        mp.pause();
        vm.prank(seller); // seller can still cancel when paused
        mp.cancel(address(nft), id);
    }

    function test_unpauseRestoresBuy() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        mp.pause();
        mp.unpause();
        vm.prank(buyer);
        mp.buy{value: 1 ether}(address(nft), id);
        assertEq(nft.ownerOf(id), buyer);
    }

    // ── Invariant ─────────────────────────────────────────────────────────

    function invariant_marketplaceBalanceZero() public view {
        assertEq(address(mp).balance, 0);
    }
}
