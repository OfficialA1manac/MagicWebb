// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Test} from "forge-std/Test.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {MockERC721} from "./MockERC721.sol";

contract MarketplaceCoreTest is Test {
    Marketplace mp;
    MockERC721  nft;
    address admin   = address(0xA11CE);
    address creator = address(0xC0DE);
    address other   = address(0xDEAD);
    address seller  = address(0xBEEF);
    address buyer   = address(0xCAFE);

    function setUp() public {
        mp  = new Marketplace(admin, creator, 250);
        nft = new MockERC721();
        vm.deal(buyer, 10 ether);
    }

    function test_setFeeBpsCap() public {
        vm.prank(admin);
        vm.expectRevert();
        mp.setFeeBps(1_001);
    }

    function test_setFeeBpsAtCapOk() public {
        vm.prank(admin);
        mp.setFeeBps(1_000);
        assertEq(mp.feeBps(), 1_000);
    }

    function test_setFeeVaultZeroReverts() public {
        vm.prank(admin);
        vm.expectRevert();
        mp.setFeeVault(address(0));
    }

    function test_setFeeBpsByNonAdminReverts() public {
        vm.prank(other);
        vm.expectRevert();
        mp.setFeeBps(100);
    }

    function test_pauseBlocksBuy() public {
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();

        vm.prank(admin);
        mp.pause();

        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: 1 ether}(address(nft), id);
    }

    function test_unpauseRestoresBuy() public {
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();

        vm.startPrank(admin);
        mp.pause();
        mp.unpause();
        vm.stopPrank();

        vm.prank(buyer);
        mp.buy{value: 1 ether}(address(nft), id);
        assertEq(nft.ownerOf(id), buyer);
    }

    function test_constructorFeeAboveCapReverts() public {
        vm.expectRevert();
        new Marketplace(admin, creator, 1_001);
    }

    function test_constructorZeroAdminReverts() public {
        vm.expectRevert();
        new Marketplace(address(0), creator, 250);
    }

    function test_constructorZeroVaultReverts() public {
        vm.expectRevert();
        new Marketplace(admin, address(0), 250);
    }

    function test_feePushedToCreator() public {
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();

        uint256 before_ = creator.balance;
        vm.prank(buyer);
        mp.buy{value: 1 ether}(address(nft), id);
        assertEq(creator.balance - before_, 0.025 ether);
    }
}
