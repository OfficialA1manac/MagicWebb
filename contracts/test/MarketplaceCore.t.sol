// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}        from "forge-std/Test.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {MockERC721}  from "./MockERC721.sol";

/// @dev Tests for MarketplaceCore behaviour: fees, immutability, no-admin, no-pause.
contract MarketplaceCoreTest is Test {
    Marketplace mp;
    MockERC721  nft;
    address creator = address(0xC0DE);
    address seller  = address(0xBEEF);
    address buyer   = address(0xCAFE);

    function setUp() public {
        mp  = new Marketplace(creator);
        nft = new MockERC721();
        vm.deal(buyer, 10 ether);
    }

    // ── Constructor guard ─────────────────────────────────────────────────

    function test_constructorZeroRecipientReverts() public {
        vm.expectRevert();
        new Marketplace(address(0));
    }

    // ── Immutability ──────────────────────────────────────────────────────

    function test_feeRecipientImmutable() public view {
        assertEq(mp.feeRecipient(), creator);
    }

    function test_platformFeeConstant() public view {
        assertEq(mp.PLATFORM_FEE_BPS(), 150);
    }

    // ── Fee routing ───────────────────────────────────────────────────────

    function test_feePushedToFeeRecipient() public {
        uint256 listingFee = (1 ether * 150) / 10_000;
        vm.deal(seller, listingFee);
        uint256 before_ = creator.balance;
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list{value: listingFee}(address(nft), id, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();

        vm.prank(buyer);
        mp.buy{value: 1 ether}(address(nft), id);
        // listing fee (150 bps) + sale fee (150 bps) of 1 ether = 300 bps = 0.03 ether
        assertEq(creator.balance - before_, 0.03 ether);
    }

    // ── No pause / no admin ───────────────────────────────────────────────

    function test_noPauseFunctionExists() public {
        // Marketplace no longer has pause/unpause — just verify basic buy works normally
        uint256 listingFee = (1 ether * 150) / 10_000;
        vm.deal(seller, listingFee);
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list{value: listingFee}(address(nft), id, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();

        vm.prank(buyer);
        mp.buy{value: 1 ether}(address(nft), id);
        assertEq(nft.ownerOf(id), buyer);
    }
}
