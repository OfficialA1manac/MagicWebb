// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {MarketplaceManager, ZeroAddr, NotKeeperOrAdmin} from "../src/MarketplaceManager.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {OfferBook} from "../src/OfferBook.sol";
import {MockERC721} from "./MockERC721.sol";
import {BadManager, BadImplementation, UpgradeNotQueued, UpgradeNotReady, UpgradeExpired} from "../src/MarketplaceCore.sol";
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

    function test_rolesAssigned() public view {
        assertTrue(mgr.hasRole(mgr.DEFAULT_ADMIN_ROLE(), admin));
    }

    function test_grantKeeperRole() public {
        vm.startPrank(admin);
        mgr.grantRole(mgr.KEEPER_ROLE(), address(0xB0B));
        assertTrue(mgr.hasRole(mgr.KEEPER_ROLE(), address(0xB0B)));
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

    function test_zeroManagerCoreListsFreely() public {
        // manager == address(0): no roles, frozen implementation — but every
        // user action still works (nothing is ever gated on the manager).
        Marketplace freeMp = _deployMarketplace(feeRecipient, address(0));
        assertTrue(freeMp.manager() == address(0));

        MockERC721 nft = new MockERC721();
        vm.startPrank(address(0xBEEF));
        uint256 tid = nft.mint(address(0xBEEF));
        nft.setApprovalForAll(address(freeMp), true);
        freeMp.list(address(nft), tid, 1 ether, uint64(24 hours));
        vm.stopPrank();

        (address s, , ,,) = freeMp.listings(address(nft), tid, address(0xBEEF));
        assertEq(s, address(0xBEEF));
    }

    // ── Keeper fleet: self-replenishing, survives admin renounce ────────

    function test_keeperFleet_adminEnrolls() public {
        vm.prank(admin);
        mgr.addKeeper(address(0xFEE1));
        assertTrue(mgr.hasRole(mgr.KEEPER_ROLE(), address(0xFEE1)));
        vm.prank(admin);
        mgr.removeKeeper(address(0xFEE1));
        assertFalse(mgr.hasRole(mgr.KEEPER_ROLE(), address(0xFEE1)));
    }

    function test_keeperFleet_keeperEnrollsKeeper_afterAdminRenounce() public {
        address k1 = address(0xAA1);
        address k2 = address(0xAA2);
        vm.prank(admin);
        mgr.addKeeper(k1);
        // Full immutability: the admin renounces itself. (Cache the role first:
        // vm.prank binds to the NEXT call, which a getter would consume.)
        bytes32 adminRole = mgr.DEFAULT_ADMIN_ROLE();
        vm.prank(admin);
        mgr.renounceRole(adminRole, admin);
        // A live keeper can still rotate in a replacement key forever.
        vm.prank(k1);
        mgr.addKeeper(k2);
        assertTrue(mgr.hasRole(mgr.KEEPER_ROLE(), k2));
        // And retire itself.
        vm.prank(k1);
        mgr.removeKeeper(k1);
        assertFalse(mgr.hasRole(mgr.KEEPER_ROLE(), k1));
    }

    function test_keeperFleet_strangerCannotEnroll() public {
        vm.prank(address(0x999));
        vm.expectRevert(NotKeeperOrAdmin.selector);
        mgr.addKeeper(address(0x999));
    }

    function test_keeperFleet_keeperCannotEvictOthers() public {
        address k1 = address(0xAA1);
        address k2 = address(0xAA2);
        vm.startPrank(admin);
        mgr.addKeeper(k1);
        mgr.addKeeper(k2);
        vm.stopPrank();
        vm.prank(k1);
        vm.expectRevert(NotKeeperOrAdmin.selector);
        mgr.removeKeeper(k2);
        assertTrue(mgr.hasRole(mgr.KEEPER_ROLE(), k2));
    }

    function test_keeperFleet_zeroAddressRejected() public {
        vm.prank(admin);
        vm.expectRevert(ZeroAddr.selector);
        mgr.addKeeper(address(0));
    }

    // ── Manager upgrade timelock (v3: weak-link fix) ─────────────────────

    function _newImpl() internal returns (address) {
        return address(new MarketplaceManager());
    }

    function test_manager_queueUpgrade_nonAdminReverts() public {
        address next = _newImpl();
        vm.prank(address(0x999));
        vm.expectRevert();
        mgr.queueUpgrade(next);
    }

    function test_manager_queueUpgrade_rejectsZeroAndEOA() public {
        vm.startPrank(admin);
        vm.expectRevert(BadImplementation.selector);
        mgr.queueUpgrade(address(0));
        vm.expectRevert(BadImplementation.selector);
        mgr.queueUpgrade(address(0xDEAD)); // EOA: no code
        vm.stopPrank();
    }

    function test_manager_upgrade_requiresQueue() public {
        address next = _newImpl();
        vm.prank(admin);
        vm.expectRevert(UpgradeNotQueued.selector);
        mgr.upgradeTo(next);
    }

    function test_manager_upgrade_wrongImplCannotRideQueue() public {
        address next = _newImpl();
        address other = _newImpl();
        vm.startPrank(admin);
        mgr.queueUpgrade(next);
        vm.expectRevert(UpgradeNotQueued.selector);
        mgr.upgradeTo(other);
        vm.stopPrank();
    }

    function test_manager_upgrade_zeroDelayInstantOnTestnet() public {
        // chainid 31337 -> delay 0: queue then install in the same block.
        address next = _newImpl();
        vm.startPrank(admin);
        mgr.queueUpgrade(next);
        assertEq(mgr.upgradeEta(), uint64(block.timestamp));
        mgr.upgradeTo(next);
        vm.stopPrank();
        assertEq(mgr.pendingImplementation(), address(0), "queue consumed");
        assertEq(mgr.upgradeEta(), 0, "eta cleared");
    }

    function test_manager_upgrade_delayEnforcedOnMainnet() public {
        vm.chainId(14); // Flare — 48h delay
        address next = _newImpl();
        vm.startPrank(admin);
        mgr.queueUpgrade(next);
        vm.expectRevert(UpgradeNotReady.selector);
        mgr.upgradeTo(next);
        vm.warp(block.timestamp + 48 hours);
        mgr.upgradeTo(next);
        vm.stopPrank();
    }

    function test_manager_upgrade_staleQueueExpires() public {
        vm.chainId(14);
        address next = _newImpl();
        vm.startPrank(admin);
        mgr.queueUpgrade(next);
        vm.warp(block.timestamp + 48 hours + 7 days + 1);
        vm.expectRevert(UpgradeExpired.selector);
        mgr.upgradeTo(next);
        vm.stopPrank();
    }

    function test_manager_cancelUpgrade_clearsQueue() public {
        address next = _newImpl();
        vm.startPrank(admin);
        mgr.queueUpgrade(next);
        mgr.cancelUpgrade();
        assertEq(mgr.pendingImplementation(), address(0));
        assertEq(mgr.upgradeEta(), 0);
        vm.expectRevert(UpgradeNotQueued.selector);
        mgr.upgradeTo(next);
        vm.stopPrank();
    }
}
