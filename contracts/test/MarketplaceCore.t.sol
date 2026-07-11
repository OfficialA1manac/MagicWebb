// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {Marketplace, NotOwner, NotListed} from "../src/Marketplace.sol";
import {BelowMinPrice, ZeroAddress} from "../src/MarketplaceCore.sol";
import {TestHelpers} from "./TestHelpers.sol";

contract MarketplaceCoreTest is Test, TestHelpers {
    Marketplace mp;
    address creator = address(0xCCCC);

    function setUp() public {
        mp = _deployMarketplace(creator, address(0));
        vm.deal(creator, 100 ether);
    }

    function test_feeRecipientStored() public view {
        // feeRecipient stored in proxy context
        assertEq(mp.feeRecipient(), creator);
    }

    function test_initializeZeroRecipientReverts() public {
        // Deploy impl, expect revert when proxy tries to call initialize() with zero recipient
        Marketplace impl = new Marketplace();
        vm.expectRevert(ZeroAddress.selector);
        new ERC1967Proxy(address(impl), abi.encodeWithSelector(Marketplace.initialize.selector, address(0), address(0)));
    }

    function test_initializeZeroRecipientReverts2() public {
        // Redundant with above but kept for coverage
        Marketplace impl = new Marketplace();
        vm.expectRevert();
        new ERC1967Proxy(address(impl), abi.encodeWithSelector(Marketplace.initialize.selector, address(0), address(0)));
    }
}
