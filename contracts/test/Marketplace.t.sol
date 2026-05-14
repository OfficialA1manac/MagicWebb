// SPDX-License-Identifier: MIT
pragma solidity 0.8.24;

import {Test} from "forge-std/Test.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {MockERC721} from "./MockERC721.sol";
import {MockERC1155} from "./MockERC1155.sol";

contract MarketplaceTest is Test {
    Marketplace  mp;
    MockERC721   nft;
    MockERC1155  multi;
    address creator = address(0xC0DE);
    address seller  = address(0xBEEF);
    address buyer   = address(0xCAFE);
    address other   = address(0xDEAD);

    function setUp() public {
        mp    = new Marketplace(creator, 250);
        nft   = new MockERC721();
        multi = new MockERC1155();
        vm.deal(buyer, 10 ether);
    }

    function _list(uint256 price, uint64 exp) internal returns (uint256 id) {
        vm.startPrank(seller);
        id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), id, uint128(price), exp);
        vm.stopPrank();
    }

    function test_listAndBuy() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));

        vm.prank(buyer);
        mp.buy{value: 1 ether}(address(nft), id);

        assertEq(nft.ownerOf(id), buyer);
        assertEq(creator.balance, 0.025 ether);
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

    // Immutability: once a sale settles, there is no path that returns the NFT to the seller
    // or refunds the buyer. The listing is deleted, the transfer is in-place, and the contract
    // exposes no admin function that could rewind state.
    function test_completedSaleIsFinal() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.prank(buyer);
        mp.buy{value: 1 ether}(address(nft), id);

        // Listing wiped; buyer cannot un-buy; seller cannot cancel; no admin path.
        vm.prank(seller);
        vm.expectRevert();
        mp.cancel(address(nft), id);

        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: 1 ether}(address(nft), id);

        assertEq(nft.ownerOf(id), buyer);
    }

    // ---------- ERC1155 ----------

    function _list1155(uint256 tokenId, uint128 units, uint128 price, uint64 exp) internal {
        vm.startPrank(seller);
        multi.mint(seller, tokenId, units);
        multi.setApprovalForAll(address(mp), true);
        mp.list1155(address(multi), tokenId, units, price, exp);
        vm.stopPrank();
    }

    function test_list1155AndBuy() public {
        _list1155(1, 5, 2 ether, uint64(block.timestamp + 1 days));

        vm.prank(buyer);
        mp.buy{value: 2 ether}(address(multi), 1);

        assertEq(multi.balanceOf(buyer, 1), 5);
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
}
