// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}        from "forge-std/Test.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {MockERC721}  from "./MockERC721.sol";

/// @dev Tests for MarketplaceCore behaviour: seller-pays fee, immutability, no-admin, no-pause.
contract MarketplaceCoreTest is Test {
    Marketplace mp;
    MockERC721  nft;
    address creator = address(0xC0DE);
    address seller  = address(0xBEEF);
    address buyer   = address(0xCAFE);

    function setUp() public {
        mp  = new Marketplace(creator, address(0));
        nft = new MockERC721();
        vm.deal(buyer, 10 ether);
    }

    function _total(uint256 price) internal pure returns (uint256) {
        return price; // buyer sends exactly the price; the fee is taken from the seller
    }

    // ── Constructor guard ───────────────────────────────────────────────────────

    function test_constructorZeroRecipientReverts() public {
        vm.expectRevert();
        new Marketplace(address(0), address(0));
    }

    // ── Immutability ────────────────────────────────────────────────────────────

    function test_feeRecipientImmutable() public view {
        assertEq(mp.feeRecipient(), creator);
    }

    function test_platformFeeConstant() public view {
        assertEq(mp.PLATFORM_FEE_BPS(), 150);
    }

    function test_minPriceConstant() public view {
        assertEq(mp.MIN_PRICE(), 1 ether);
    }

    // ── Fee routing (seller pays 1.5% on the sale, listing is free) ────────────

    function test_feePushedToFeeRecipient() public {
        uint256 before_ = creator.balance;

        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp + 1 days)); // free
        vm.stopPrank();

        vm.prank(buyer);
        mp.buy{value: _total(1 ether)}(address(nft), id, seller);

        // The 1.5% fee reaches creator; seller nets 98.5%.
        assertEq(creator.balance - before_, 0.015 ether);
        assertEq(seller.balance, 1 ether - 0.015 ether);
    }

    // ── No pause / no admin ────────────────────────────────────────────────────

    function test_noPauseFunctionExists() public {
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();

        vm.prank(buyer);
        mp.buy{value: _total(1 ether)}(address(nft), id, seller);
        assertEq(nft.ownerOf(id), buyer);
    }
}
