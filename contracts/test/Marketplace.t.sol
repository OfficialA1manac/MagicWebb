// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Test} from "forge-std/Test.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {MockERC721} from "./MockERC721.sol";

contract MarketplaceTest is Test {
    Marketplace mp;
    MockERC721  nft;
    address admin   = address(0xA11CE);
    address creator = address(0xC0DE);
    address seller  = address(0xBEEF);
    address buyer   = address(0xCAFE);
    address other   = address(0xDEAD);

    function setUp() public {
        mp  = new Marketplace(admin, creator, 250);
        nft = new MockERC721();
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
}
