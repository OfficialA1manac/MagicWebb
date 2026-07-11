// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {MarketplaceManager, ZeroAddr} from "../src/MarketplaceManager.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {OfferBook} from "../src/OfferBook.sol";
import {MockERC721} from "./MockERC721.sol";
import {EntriesHalted, BadManager} from "../src/MarketplaceCore.sol";
import {TestHelpers} from "./TestHelpers.sol";

contract MarketplaceManagerTest is Test, TestHelpers {
    MarketplaceManager mgr;
    Marketplace mp;
    AuctionHouse ah;
    OfferBook ob;
    address admin = address(0xAD);
    address feeRecipient = address(0xFEE);

    function setUp() public {
        mgr = _deployMarketplaceManager(admin);
        mp = _deployMarketplace(feeRecipient, address(mgr));
        ah = _deployAuctionHouse(feeRecipient, address(mgr));
        ob = _deployOfferBook(feeRecipient, address(mgr));
        vm.prank(admin);
        mgr.setCoreContracts(address(mp), address(ah), address(ob));
    }

    function test_rolesAssigned() public {
        assertTrue(mgr.hasRole(mgr.DEFAULT_ADMIN_ROLE(), admin));
        assertTrue(mgr.hasRole(mgr.OPERATOR_ROLE(), admin));
    }

    function test_grantKeeperRole() public {
        vm.startPrank(admin);
        mgr.grantRole(mgr.KEEPER_ROLE(), address(0xB0B));
        assertTrue(mgr.hasRole(mgr.KEEPER_ROLE(), address(0xB0B)));
        vm.stopPrank();
    }

    function test_pauseUnpauseEntries() public {
        assertFalse(mgr.entriesPaused());
        vm.prank(admin);
        mgr.pauseEntries();
        assertTrue(mgr.entriesPaused());
        vm.prank(admin);
        mgr.unpauseEntries();
        assertFalse(mgr.entriesPaused());
    }

    function test_nonOperatorCantPause() public {
        vm.prank(address(0x999));
        vm.expectRevert();
        mgr.pauseEntries();
    }

    function test_entriesHaltedWhenPaused() public {
        vm.prank(admin);
        mgr.pauseEntries();

        MockERC721 nft = new MockERC721();
        vm.startPrank(address(0xBEEF));
        nft.mint(address(0xBEEF));
        nft.setApprovalForAll(address(mp), true);
        vm.expectRevert(EntriesHalted.selector);
        mp.list(address(nft), 0, 1 ether, uint64(block.timestamp + 24 hours));
        vm.stopPrank();
    }

    function test_coreRejectsEOAManager() public {
        // Deploy a Marketplace impl, then try to deploy proxy with an EOA as manager
        address rando = address(0x999);
        Marketplace impl = new Marketplace();
        vm.expectRevert(BadManager.selector);
        new ERC1967Proxy(address(impl), abi.encodeWithSelector(Marketplace.initialize.selector, feeRecipient, rando));
    }

    function test_coreRejectsNonManagerContract() public {
        // Deploy a Marketplace impl, try to deploy proxy with a non-manager contract as manager
        MockERC721 nonManager = new MockERC721();
        Marketplace impl = new Marketplace();
        vm.expectRevert();
        new ERC1967Proxy(address(impl), abi.encodeWithSelector(Marketplace.initialize.selector, feeRecipient, address(nonManager)));
    }

    function test_zeroManagerCoreIgnoresBreaker() public {
        // When manager is address(0), entries are always allowed
        Marketplace freeMp = _deployMarketplace(feeRecipient, address(0));
        assertTrue(freeMp.manager() == address(0));

        // Even with a paused manager elsewhere, entriesAllowed on freeMp calls address(0)
        // which returns false from staticcall, so entryGate treats it as "open"
        MockERC721 nft = new MockERC721();
        vm.startPrank(address(0xBEEF));
        uint256 tid = nft.mint(address(0xBEEF));
        nft.setApprovalForAll(address(freeMp), true);
        freeMp.list(address(nft), tid, 1 ether, uint64(block.timestamp + 24 hours));
        vm.stopPrank();

        (address s, , ,,) = freeMp.listings(address(nft), tid, address(0xBEEF));
        assertEq(s, address(0xBEEF));
    }
}
