// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {MarketplaceManager, ZeroAddr, NotAdmin} from "../src/MarketplaceManager.sol";
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
    address keeper = address(0xCAFE);
    address feeRecipient = address(0xFEE);

    function setUp() public {
        mgr = _deployMarketplaceManager(admin, keeper);
        mp = _deployMarketplace(feeRecipient, address(mgr));
        ah = _deployAuctionHouse(feeRecipient, address(mgr));
        ob = _deployOfferBook(feeRecipient, address(mgr));
    }

    // ── Authority shim truth table ───────────────────────────────────────────

    function test_shim_truthTable() public view {
        assertTrue(mgr.hasRole(mgr.DEFAULT_ADMIN_ROLE(), admin));
        assertTrue(mgr.hasRole(mgr.KEEPER_ROLE(), keeper));
        assertFalse(mgr.hasRole(mgr.KEEPER_ROLE(), admin), "admin is not keeper");
        assertFalse(mgr.hasRole(mgr.DEFAULT_ADMIN_ROLE(), keeper), "keeper is not admin");
        assertFalse(mgr.hasRole(mgr.KEEPER_ROLE(), address(0x999)));
        assertFalse(mgr.hasRole(keccak256("SOME_OTHER_ROLE"), admin), "unknown roles answer false");
        assertFalse(mgr.hasRole(mgr.KEEPER_ROLE(), address(0)), "zero address never holds a role");
    }

    function test_initialize_rejectsZeroAddresses() public {
        MarketplaceManager impl = new MarketplaceManager();
        vm.expectRevert(ZeroAddr.selector);
        new ERC1967Proxy(address(impl), abi.encodeWithSelector(MarketplaceManager.initialize.selector, address(0), keeper));
        vm.expectRevert(ZeroAddr.selector);
        new ERC1967Proxy(address(impl), abi.encodeWithSelector(MarketplaceManager.initialize.selector, admin, address(0)));
    }

    // ── Single keeper: only the admin can replace it, nobody can add ─────────

    function test_setKeeper_adminReplaces() public {
        address k2 = address(0xAA2);
        vm.prank(admin);
        mgr.setKeeper(k2);
        assertEq(mgr.keeper(), k2);
        assertTrue(mgr.hasRole(mgr.KEEPER_ROLE(), k2));
        // ONE keeper: the old key lost authority the moment it was replaced.
        assertFalse(mgr.hasRole(mgr.KEEPER_ROLE(), keeper), "replaced keeper must lose authority");
    }

    function test_setKeeper_keeperCannotChangeKeeperSet() public {
        // The v3.1 self-replenishing fleet is gone: the keeper key itself has
        // ZERO ability to alter settlement authority.
        vm.prank(keeper);
        vm.expectRevert(NotAdmin.selector);
        mgr.setKeeper(address(0xE011));
    }

    function test_setKeeper_strangerReverts() public {
        vm.prank(address(0x999));
        vm.expectRevert(NotAdmin.selector);
        mgr.setKeeper(address(0x999));
    }

    function test_setKeeper_zeroAddressRejected() public {
        vm.prank(admin);
        vm.expectRevert(ZeroAddr.selector);
        mgr.setKeeper(address(0));
    }

    function test_noGrantSurfaceExists() public {
        // The AccessControl grant machinery must be gone at the ABI level:
        // calling the old selectors hits the fallback and reverts.
        (bool ok,) = address(mgr).call(abi.encodeWithSignature("addKeeper(address)", address(0xB0B)));
        assertFalse(ok, "addKeeper must not exist");
        (ok,) = address(mgr).call(abi.encodeWithSignature("grantRole(bytes32,address)", mgr.KEEPER_ROLE(), address(0xB0B)));
        assertFalse(ok, "grantRole must not exist");
        (ok,) = address(mgr).call(abi.encodeWithSignature("removeKeeper(address)", keeper));
        assertFalse(ok, "removeKeeper must not exist");
    }

    // ── renounceAdmin: the one-way seal ──────────────────────────────────────

    function test_renounceAdmin_seals() public {
        vm.prank(admin);
        mgr.renounceAdmin();
        assertEq(mgr.admin(), address(0));
        assertFalse(mgr.hasRole(mgr.DEFAULT_ADMIN_ROLE(), admin));

        // Keeper survives the seal and keeps settling.
        assertTrue(mgr.hasRole(mgr.KEEPER_ROLE(), keeper));

        // Every admin path is dead — including for the ex-admin.
        // (The fresh impl is created BEFORE expectRevert: CREATE would
        // otherwise consume the expected revert.)
        address nextImpl = address(new MarketplaceManager());
        vm.startPrank(admin);
        vm.expectRevert(NotAdmin.selector);
        mgr.setKeeper(address(0xAA2));
        vm.expectRevert(NotAdmin.selector);
        mgr.queueUpgrade(address(this));
        vm.expectRevert(NotAdmin.selector);
        mgr.cancelUpgrade();
        vm.expectRevert(NotAdmin.selector);
        mgr.renounceAdmin();
        vm.expectRevert();
        mgr.upgradeTo(nextImpl);
        vm.stopPrank();
    }

    function test_renounceAdmin_onlyAdmin() public {
        vm.prank(keeper);
        vm.expectRevert(NotAdmin.selector);
        mgr.renounceAdmin();
        vm.prank(address(0x999));
        vm.expectRevert(NotAdmin.selector);
        mgr.renounceAdmin();
    }

    // ── Core wiring (unchanged consult protocol) ─────────────────────────────

    function test_coreRejectsEOAManager() public {
        address rando = address(0x999);
        Marketplace impl = new Marketplace();
        vm.expectRevert(BadManager.selector);
        new ERC1967Proxy(address(impl), abi.encodeWithSelector(Marketplace.initialize.selector, feeRecipient, rando));
    }

    function test_coreRejectsNonManagerContract() public {
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

    // ── Manager upgrade timelock (v3: weak-link fix) ─────────────────────

    function _newImpl() internal returns (address) {
        return address(new MarketplaceManager());
    }

    function test_manager_queueUpgrade_nonAdminReverts() public {
        address next = _newImpl();
        vm.prank(address(0x999));
        vm.expectRevert(NotAdmin.selector);
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
