// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {MarketplaceManager, ZeroAddr, NotAdmin, SameAdmin, NotPendingAdmin} from "../src/MarketplaceManager.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {OfferBook} from "../src/OfferBook.sol";
import {MockERC721} from "./MockERC721.sol";
import {BadManager, ZeroAddress, NotAdmin as CoreNotAdmin} from "../src/MarketplaceCore.sol";
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

    function test_constructor_rejectsZeroAddresses() public {
        // v3.4: the manager is a plain contract — validation lives in the
        // constructor, there is no initializer.
        vm.expectRevert(ZeroAddr.selector);
        new MarketplaceManager(address(0), keeper);
        vm.expectRevert(ZeroAddr.selector);
        new MarketplaceManager(admin, address(0));
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

    function test_noUpgradeSurfaceExists() public {
        // v3.4: the manager is UNPROXIED plain bytecode. Every upgrade-related
        // selector must be absent at the ABI level — there is nothing to
        // upgrade and no proxy to point elsewhere.
        (bool ok,) = address(mgr).call(abi.encodeWithSignature("upgradeTo(address)", address(0xB0B)));
        assertFalse(ok, "upgradeTo must not exist");
        (ok,) = address(mgr).call(abi.encodeWithSignature("upgradeToAndCall(address,bytes)", address(0xB0B), ""));
        assertFalse(ok, "upgradeToAndCall must not exist");
        (ok,) = address(mgr).call(abi.encodeWithSignature("queueUpgrade(address)", address(0xB0B)));
        assertFalse(ok, "queueUpgrade must not exist");
        (ok,) = address(mgr).call(abi.encodeWithSignature("cancelUpgrade()"));
        assertFalse(ok, "cancelUpgrade must not exist");
        (ok,) = address(mgr).call(abi.encodeWithSignature("proxiableUUID()"));
        assertFalse(ok, "proxiableUUID must not exist (not UUPS)");
        (ok,) = address(mgr).call(abi.encodeWithSignature("initialize(address,address)", admin, keeper));
        assertFalse(ok, "initialize must not exist (plain constructor)");
    }

    // ── renounceAdmin: the one-way seal ──────────────────────────────────────

    function test_renounceAdmin_seals() public {
        vm.prank(admin);
        mgr.renounceAdmin();
        assertEq(mgr.admin(), address(0));
        assertFalse(mgr.hasRole(mgr.DEFAULT_ADMIN_ROLE(), admin));

        // Keeper survives the seal and keeps settling.
        assertTrue(mgr.hasRole(mgr.KEEPER_ROLE(), keeper));

        // Every admin path on the manager is dead — including for the ex-admin.
        vm.startPrank(admin);
        vm.expectRevert(NotAdmin.selector);
        mgr.setKeeper(address(0xAA2));
        vm.expectRevert(NotAdmin.selector);
        mgr.renounceAdmin();
        vm.stopPrank();

        // And the seal reaches the cores: their upgrade gate consults exactly
        // this manager's hasRole probe. (Impl constructed BEFORE expectRevert
        // so CREATE doesn't consume the expected revert.)
        Marketplace nextImpl = new Marketplace(feeRecipient, address(mgr));
        vm.prank(admin);
        vm.expectRevert(CoreNotAdmin.selector);
        mp.queueUpgrade(address(nextImpl));
    }

    // ── transferAdmin / acceptAdmin: two-step rotation ───────────────────────

    event AdminTransferStarted(address indexed current, address indexed pending);
    event AdminTransferCancelled(address indexed pending);
    event AdminTransferred(address indexed previous, address indexed current);
    event AuditLog(bytes32 indexed action, address indexed actor, address indexed subject, bytes32 extra);

    address admin2 = address(0xAD2);

    function test_transferAdmin_happyPath_events() public {
        assertEq(mgr.pendingAdmin(), address(0));

        vm.expectEmit(true, true, true, true, address(mgr));
        emit AdminTransferStarted(admin, admin2);
        vm.expectEmit(true, true, true, true, address(mgr));
        emit AuditLog("TRANSFER_ADMIN", admin, admin2, 0);
        vm.prank(admin);
        mgr.transferAdmin(admin2);

        // Offer alone changes nothing: old admin keeps power, offeree has none.
        assertEq(mgr.pendingAdmin(), admin2);
        assertEq(mgr.admin(), admin);
        assertTrue(mgr.hasRole(mgr.DEFAULT_ADMIN_ROLE(), admin));
        assertFalse(mgr.hasRole(mgr.DEFAULT_ADMIN_ROLE(), admin2), "pending admin holds no power yet");

        vm.expectEmit(true, true, true, true, address(mgr));
        emit AdminTransferred(admin, admin2);
        vm.expectEmit(true, true, true, true, address(mgr));
        emit AuditLog("ACCEPT_ADMIN", admin2, admin, 0);
        vm.prank(admin2);
        mgr.acceptAdmin();

        assertEq(mgr.admin(), admin2);
        assertEq(mgr.pendingAdmin(), address(0), "pending cleared on accept");
        assertTrue(mgr.hasRole(mgr.DEFAULT_ADMIN_ROLE(), admin2));
        assertFalse(mgr.hasRole(mgr.DEFAULT_ADMIN_ROLE(), admin), "old admin lost authority");
        // Keeper untouched by an admin hand-off.
        assertEq(mgr.keeper(), keeper);
    }

    function test_acceptAdmin_nonPendingReverts() public {
        // Nothing pending: everyone reverts, including the current admin.
        vm.prank(admin2);
        vm.expectRevert(NotPendingAdmin.selector);
        mgr.acceptAdmin();
        vm.prank(admin);
        vm.expectRevert(NotPendingAdmin.selector);
        mgr.acceptAdmin();

        vm.prank(admin);
        mgr.transferAdmin(admin2);
        // Pending set: only admin2 may accept.
        vm.prank(keeper);
        vm.expectRevert(NotPendingAdmin.selector);
        mgr.acceptAdmin();
        vm.prank(address(0x999));
        vm.expectRevert(NotPendingAdmin.selector);
        mgr.acceptAdmin();
        vm.prank(admin);
        vm.expectRevert(NotPendingAdmin.selector);
        mgr.acceptAdmin();
        assertEq(mgr.admin(), admin);
    }

    function test_transferAdmin_onlyAdmin() public {
        vm.prank(keeper);
        vm.expectRevert(NotAdmin.selector);
        mgr.transferAdmin(admin2);
        vm.prank(address(0x999));
        vm.expectRevert(NotAdmin.selector);
        mgr.transferAdmin(admin2);
        vm.prank(keeper);
        vm.expectRevert(NotAdmin.selector);
        mgr.cancelAdminTransfer();
        assertEq(mgr.pendingAdmin(), address(0));
    }

    function test_transferAdmin_zeroAndSameRevert() public {
        vm.startPrank(admin);
        vm.expectRevert(ZeroAddr.selector);
        mgr.transferAdmin(address(0));
        vm.expectRevert(SameAdmin.selector);
        mgr.transferAdmin(admin);
        vm.stopPrank();
        assertEq(mgr.pendingAdmin(), address(0));
    }

    function test_cancelAdminTransfer_clearsPending() public {
        vm.prank(admin);
        mgr.transferAdmin(admin2);
        assertEq(mgr.pendingAdmin(), admin2);

        vm.expectEmit(true, true, true, true, address(mgr));
        emit AdminTransferCancelled(admin2);
        vm.expectEmit(true, true, true, true, address(mgr));
        emit AuditLog("CANCEL_ADMIN_TRANSFER", admin, admin2, 0);
        vm.prank(admin);
        mgr.cancelAdminTransfer();

        assertEq(mgr.pendingAdmin(), address(0));
        assertEq(mgr.admin(), admin);
        // The withdrawn offeree can no longer accept.
        vm.prank(admin2);
        vm.expectRevert(NotPendingAdmin.selector);
        mgr.acceptAdmin();
    }

    function test_transferAdmin_replacesOutstandingOffer() public {
        address admin3 = address(0xAD3);
        vm.startPrank(admin);
        mgr.transferAdmin(admin2);
        mgr.transferAdmin(admin3);
        vm.stopPrank();
        assertEq(mgr.pendingAdmin(), admin3);
        vm.prank(admin2);
        vm.expectRevert(NotPendingAdmin.selector);
        mgr.acceptAdmin();
    }

    function test_renounceAdmin_wipesPending() public {
        vm.prank(admin);
        mgr.transferAdmin(admin2);
        vm.prank(admin);
        mgr.renounceAdmin();
        assertEq(mgr.admin(), address(0));
        assertEq(mgr.pendingAdmin(), address(0), "seal must wipe the pending offer");
        // No pending key can resurrect the role after the seal.
        vm.prank(admin2);
        vm.expectRevert(NotPendingAdmin.selector);
        mgr.acceptAdmin();
    }

    function test_acceptAdmin_reachesCoreUpgradeGate() public {
        vm.prank(admin);
        mgr.transferAdmin(admin2);
        vm.prank(admin2);
        mgr.acceptAdmin();

        // Impls constructed BEFORE expectRevert so CREATE doesn't consume it.
        Marketplace nextA = new Marketplace(feeRecipient, address(mgr));
        Marketplace nextB = new Marketplace(feeRecipient, address(mgr));

        // OLD admin: every core's _requireAdmin probes this manager -> dead.
        vm.prank(admin);
        vm.expectRevert(CoreNotAdmin.selector);
        mp.queueUpgrade(address(nextA));

        // NEW admin: queue + instant install succeed.
        vm.startPrank(admin2);
        mp.queueUpgrade(address(nextB));
        assertEq(mp.pendingImplementation(), address(nextB));
        mp.upgradeTo(address(nextB));
        vm.stopPrank();
        assertEq(mp.pendingImplementation(), address(0));
    }

    function test_oldAdminCannotActAfterAccept() public {
        vm.prank(admin);
        mgr.transferAdmin(admin2);
        vm.prank(admin2);
        mgr.acceptAdmin();

        vm.startPrank(admin);
        vm.expectRevert(NotAdmin.selector);
        mgr.setKeeper(address(0xAA2));
        vm.expectRevert(NotAdmin.selector);
        mgr.transferAdmin(admin);
        vm.expectRevert(NotAdmin.selector);
        mgr.cancelAdminTransfer();
        vm.expectRevert(NotAdmin.selector);
        mgr.renounceAdmin();
        vm.stopPrank();

        // And the new admin can hand it straight back if it wants to.
        vm.prank(admin2);
        mgr.transferAdmin(admin);
        vm.prank(admin);
        mgr.acceptAdmin();
        assertEq(mgr.admin(), admin);
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
        // v3.4: the probe runs in the implementation constructor.
        vm.expectRevert(BadManager.selector);
        new Marketplace(feeRecipient, address(0x999));
    }

    function test_coreRejectsNonManagerContract() public {
        MockERC721 nonManager = new MockERC721();
        vm.expectRevert();
        new Marketplace(feeRecipient, address(nonManager));
    }

    function test_zeroManagerCoreReverts() public {
        // The manager is MANDATORY: the keeper fee split resolves through it,
        // so a core cannot be constructed without one.
        vm.expectRevert(ZeroAddress.selector);
        new Marketplace(feeRecipient, address(0));
    }

    function test_managedCoreListsFreely() public {
        // With a manager installed every user action still works — nothing
        // user-facing is ever gated on the manager beyond the keeper consults.
        Marketplace freeMp = _deployMarketplace(feeRecipient, address(mgr));
        assertTrue(freeMp.manager() == address(mgr));

        MockERC721 nft = new MockERC721();
        vm.startPrank(address(0xBEEF));
        uint256 tid = nft.mint(address(0xBEEF));
        nft.setApprovalForAll(address(freeMp), true);
        freeMp.list(address(nft), tid, 1 ether, uint64(24 hours));
        vm.stopPrank();

        (address s, , ,,) = freeMp.listings(address(nft), tid, address(0xBEEF));
        assertEq(s, address(0xBEEF));
    }
}
