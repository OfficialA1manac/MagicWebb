// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}             from "forge-std/Test.sol";
import {RoyaltyRegistry}  from "../src/RoyaltyRegistry.sol";

contract RoyaltyRegistryTest is Test {
    RoyaltyRegistry reg;
    address admin    = address(this);
    address setter   = address(0xAB);
    address coll     = address(0xCC01);
    address creator1 = address(0xC1);
    address creator2 = address(0xC2);

    function setUp() public {
        reg = new RoyaltyRegistry(admin);
        reg.grantRole(reg.ROYALTY_SETTER_ROLE(), setter);
    }

    // ── Collection royalty ────────────────────────────────────────────────

    function test_setAndGetCollectionRoyalty() public {
        vm.prank(setter);
        reg.setCollectionRoyalty(coll, creator1, 500); // 5%

        (address receiver, uint256 amount) = reg.getRoyalty(coll, 1, 1 ether);
        assertEq(receiver, creator1);
        assertEq(amount, 0.05 ether);
    }

    function test_clearCollectionRoyalty() public {
        vm.prank(setter);
        reg.setCollectionRoyalty(coll, creator1, 500);

        vm.prank(setter);
        reg.clearCollectionRoyalty(coll);

        (address receiver, uint256 amount) = reg.getRoyalty(coll, 1, 1 ether);
        assertEq(receiver, address(0));
        assertEq(amount, 0);
    }

    // ── Token royalty override ────────────────────────────────────────────

    function test_tokenRoyaltyOverridesCollection() public {
        vm.startPrank(setter);
        reg.setCollectionRoyalty(coll, creator1, 500);  // 5% default
        reg.setTokenRoyalty(coll, 42, creator2, 1000); // 10% override for token 42
        vm.stopPrank();

        // Token 42 uses per-token override
        (address r42, uint256 a42) = reg.getRoyalty(coll, 42, 1 ether);
        assertEq(r42, creator2);
        assertEq(a42, 0.10 ether);

        // Other tokens use collection default
        (address r1, uint256 a1) = reg.getRoyalty(coll, 1, 1 ether);
        assertEq(r1, creator1);
        assertEq(a1, 0.05 ether);
    }

    function test_clearTokenRoyaltyFallsBackToCollection() public {
        vm.startPrank(setter);
        reg.setCollectionRoyalty(coll, creator1, 500);
        reg.setTokenRoyalty(coll, 7, creator2, 1000);
        reg.clearTokenRoyalty(coll, 7);
        vm.stopPrank();

        (address r, uint256 a) = reg.getRoyalty(coll, 7, 1 ether);
        assertEq(r, creator1);
        assertEq(a, 0.05 ether);
    }

    // ── Caps ──────────────────────────────────────────────────────────────

    function test_royaltyAboveCapReverts() public {
        vm.prank(setter);
        vm.expectRevert();
        reg.setCollectionRoyalty(coll, creator1, 2_501); // > MAX_ROYALTY_BPS
    }

    function test_royaltyAtCapOk() public {
        vm.prank(setter);
        reg.setCollectionRoyalty(coll, creator1, 2_500); // exactly 25%
        (, uint16 bps) = reg.collectionRoyalty(coll);
        assertEq(bps, 2_500);
    }

    // ── Access control ────────────────────────────────────────────────────

    function test_nonSetterCannotSet() public {
        vm.prank(address(0xBAD));
        vm.expectRevert();
        reg.setCollectionRoyalty(coll, creator1, 500);
    }

    function test_nonSetterCannotClear() public {
        vm.prank(setter);
        reg.setCollectionRoyalty(coll, creator1, 500);

        vm.prank(address(0xBAD));
        vm.expectRevert();
        reg.clearCollectionRoyalty(coll);
    }

    function test_zeroCollectionAddressReverts() public {
        vm.prank(setter);
        vm.expectRevert();
        reg.setCollectionRoyalty(address(0), creator1, 500);
    }

    function test_zeroReceiverReverts() public {
        vm.prank(setter);
        vm.expectRevert();
        reg.setCollectionRoyalty(coll, address(0), 500);
    }

    // ── No royalty configured ─────────────────────────────────────────────

    function test_noRoyaltyReturnsZero() public {
        (address r, uint256 a) = reg.getRoyalty(coll, 1, 1 ether);
        assertEq(r, address(0));
        assertEq(a, 0);
    }
}
