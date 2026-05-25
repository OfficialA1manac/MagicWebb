// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}        from "forge-std/Test.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {MockERC721}  from "./MockERC721.sol";

/// @dev Tests that exercise MarketplaceCore behaviour (fees, immutability, access control).
contract MarketplaceCoreTest is Test {
    Marketplace mp;
    MockERC721  nft;
    address admin   = address(this);
    address creator = address(0xC0DE);
    address seller  = address(0xBEEF);
    address buyer   = address(0xCAFE);

    function setUp() public {
        mp  = new Marketplace(creator, 250, admin);
        nft = new MockERC721();
        vm.deal(buyer, 10 ether);
    }

    // ── Constructor guards ────────────────────────────────────────────────

    function test_constructorFeeAboveCapReverts() public {
        vm.expectRevert();
        new Marketplace(creator, 1_001, admin);
    }

    function test_constructorFeeAtCapOk() public {
        Marketplace mp2 = new Marketplace(creator, 1_000, admin);
        assertEq(mp2.feeBps(), 1_000);
    }

    function test_constructorZeroVaultReverts() public {
        vm.expectRevert();
        new Marketplace(address(0), 250, admin);
    }

    function test_constructorZeroAdminReverts() public {
        vm.expectRevert();
        new Marketplace(creator, 250, address(0));
    }

    // ── Immutability ──────────────────────────────────────────────────────

    function test_feeVaultImmutable() public view {
        assertEq(mp.feeVault(), creator);
    }

    function test_feeBpsImmutable() public view {
        assertEq(mp.feeBps(), 250);
    }

    // ── Fee routing ───────────────────────────────────────────────────────

    function test_feePushedToFeeVault() public {
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
        // listing fee (150 bps) + sale fee (250 bps) of 1 ether
        assertEq(creator.balance - before_, 0.04 ether);
    }

    // ── Access control ────────────────────────────────────────────────────

    function test_nonAdminCannotPause() public {
        vm.prank(address(0xBAD));
        vm.expectRevert();
        mp.pause();
    }

    function test_adminCanGrantPauserRole() public {
        bytes32 pauserRole = mp.PAUSER_ROLE();
        mp.grantRole(pauserRole, seller);

        vm.prank(seller);
        mp.pause();
        assertTrue(mp.paused());
    }

    function test_pauserCannotGrantRoles() public {
        bytes32 pauserRole = mp.PAUSER_ROLE();
        mp.grantRole(pauserRole, seller);

        // A pauser cannot grant the pauser role to someone else
        vm.prank(seller);
        vm.expectRevert();
        mp.grantRole(pauserRole, buyer);
    }

    // ── Royalty registry ──────────────────────────────────────────────────
}
