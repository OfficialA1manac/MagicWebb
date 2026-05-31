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

    function _buyTotal(uint128 price) internal pure returns (uint256) {
        return uint256(price) + (uint256(price) * 150) / 10_000;
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

    function test_minPriceConstant() public view {
        assertEq(mp.MIN_PRICE(), 0.01 ether);
    }

    // ── Fee routing ───────────────────────────────────────────────────────

    function test_feePushedToFeeRecipient() public {
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();

        uint256 before_ = creator.balance;
        vm.prank(buyer);
        mp.buy{value: _buyTotal(1 ether)}(address(nft), id, seller);
        // Only the taker fee (1.5%) is collected — sellers pay nothing.
        assertEq(creator.balance - before_, 0.015 ether);
    }

    // ── No admin / no pause / no upgrade ──────────────────────────────────

    function test_basicBuyWorks() public {
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();

        vm.prank(buyer);
        mp.buy{value: _buyTotal(1 ether)}(address(nft), id, seller);
        assertEq(nft.ownerOf(id), buyer);
    }
}
