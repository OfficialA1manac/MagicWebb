// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {Marketplace, NotOwner, NotListed} from "../src/Marketplace.sol";
import {
    BelowMinPrice, ZeroAddress, NotAdmin, NoManager, BadImplementation, BadManager,
    UpgradeNotQueued, UpgradeNotReady, UpgradeExpired
} from "../src/MarketplaceCore.sol";
import {MarketplaceManager} from "../src/MarketplaceManager.sol";
import {TestHelpers} from "./TestHelpers.sol";

contract MarketplaceCoreTest is Test, TestHelpers {
    Marketplace mp;
    address creator = address(0xCCCC);

    /// ERC-1967 implementation slot: keccak256("eip1967.proxy.implementation") - 1.
    bytes32 constant IMPL_SLOT = 0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc;

    function setUp() public {
        mp = _deployMarketplace(creator, address(0));
        vm.deal(creator, 100 ether);
    }

    function _implOf(address proxy) internal view returns (address) {
        return address(uint160(uint256(vm.load(proxy, IMPL_SLOT))));
    }

    /// Deploys a Marketplace whose upgrade authority is a real manager with
    /// this test contract as DEFAULT_ADMIN_ROLE.
    function _gatedPair() internal returns (Marketplace, MarketplaceManager) {
        MarketplaceManager mgr = _deployMarketplaceManager(address(this));
        return (_deployMarketplace(creator, address(mgr)), mgr);
    }

    function test_feeRecipientStored() public view {
        // v3.4: feeRecipient is an implementation immutable, read through the
        // proxy — this also proves immutables resolve across the delegatecall.
        assertEq(mp.feeRecipient(), creator);
    }

    function test_constructorZeroRecipientReverts() public {
        // v3.4: validation moved from the initializer to the implementation
        // constructor (fee recipient + manager are immutables).
        vm.expectRevert(ZeroAddress.selector);
        new Marketplace(address(0), address(0));
    }

    function test_constructorRejectsEOAManager() public {
        // A typo'd/EOA manager would silently disable keeper roles and freeze
        // upgrades — the constructor probe must reject it.
        vm.expectRevert(BadManager.selector);
        new Marketplace(creator, address(0xDEAD));
    }

    function test_constructorRejectsNonManagerContract() public {
        // A contract that doesn't answer the hasRole probe is not a manager.
        vm.expectRevert(BadManager.selector);
        new Marketplace(creator, address(this));
    }

    function test_initializeTakesNoArgsAndRunsOnce() public {
        // The proxy initializer is gated: a second call must revert.
        vm.expectRevert();
        mp.initialize();
    }

    // ── Upgrades: INSTANT on every chain (v3.4 owner directive) ────────────

    function test_upgradeDelay_isZeroEverywhere() public {
        assertEq(mp.upgradeDelay(), 0); // 31337 local
        vm.chainId(114);
        assertEq(mp.upgradeDelay(), 0); // Coston2
        vm.chainId(19);
        assertEq(mp.upgradeDelay(), 0); // Songbird — instant, per owner
        vm.chainId(14);
        assertEq(mp.upgradeDelay(), 0); // Flare — instant, per owner
    }

    function test_ungatedProxy_cannotBeUpgradedAtAll() public {
        // mp has manager == address(0): the implementation is frozen, rather
        // than upgradable by anyone as it was before the timelock landed.
        Marketplace next = new Marketplace(creator, address(0));
        vm.expectRevert(NoManager.selector);
        mp.queueUpgrade(address(next));
        vm.expectRevert(NoManager.selector);
        mp.upgradeTo(address(next));
    }

    function test_queueUpgrade_nonAdminReverts() public {
        (Marketplace gated, MarketplaceManager mgr) = _gatedPair();
        Marketplace next = new Marketplace(creator, address(mgr));
        vm.prank(address(0xDEAD));
        vm.expectRevert(NotAdmin.selector);
        gated.queueUpgrade(address(next));
    }

    function test_queueUpgrade_rejectsZeroAndEOA() public {
        (Marketplace gated,) = _gatedPair();
        vm.expectRevert(BadImplementation.selector);
        gated.queueUpgrade(address(0));
        vm.expectRevert(BadImplementation.selector);
        gated.queueUpgrade(address(0xDEAD)); // EOA: no code
    }

    // Uses upgradeTo, not upgradeToAndCall(impl, ""): on OZ 4.x
    // upgradeToAndCall passes forceCall=true, so empty calldata still
    // delegatecalls into the new implementation's (nonexistent) fallback and
    // reverts. Real upgrades either carry migration calldata or use upgradeTo.
    function test_upgrade_instantOnMainnetChain() public {
        // v3.4: delay is 0 everywhere — queue and install run back-to-back on
        // a MAINNET chain id. The queue still enforces the exact-impl match
        // and one-shot consumption.
        vm.chainId(14); // Flare
        (Marketplace gated, MarketplaceManager mgr) = _gatedPair();
        Marketplace next = new Marketplace(creator, address(mgr));
        address before = _implOf(address(gated));

        // Nothing queued.
        vm.expectRevert(UpgradeNotQueued.selector);
        gated.upgradeTo(address(next));

        gated.queueUpgrade(address(next));
        assertEq(gated.pendingImplementation(), address(next));
        assertEq(gated.upgradeEta(), uint64(block.timestamp), "eta == now: instant");

        // A different implementation cannot ride the queued slot.
        Marketplace other = new Marketplace(creator, address(mgr));
        vm.expectRevert(UpgradeNotQueued.selector);
        gated.upgradeTo(address(other));

        // Same-block install — no warp needed.
        gated.upgradeTo(address(next));
        assertEq(_implOf(address(gated)), address(next), "implementation swapped");
        assertTrue(before != address(next), "sanity: new impl differs");
        assertEq(gated.pendingImplementation(), address(0), "queue cleared");
        assertEq(gated.upgradeEta(), 0, "eta cleared");

        // v3.4 invariant: immutables survive upgradeTo (they live in the NEW
        // implementation's bytecode, constructed with the same args).
        assertEq(gated.feeRecipient(), creator, "feeRecipient after upgrade");
        assertEq(gated.manager(), address(mgr), "manager after upgrade");

        // The consumed queue entry cannot be replayed.
        vm.expectRevert(UpgradeNotQueued.selector);
        gated.upgradeTo(address(next));
    }

    function test_upgrade_staleQueueExpires() public {
        // MAX_UPGRADE_WINDOW (7d) still applies with a zero delay: a queued
        // approval left sitting for a week cannot be exercised.
        vm.chainId(14);
        (Marketplace gated, MarketplaceManager mgr) = _gatedPair();
        Marketplace next = new Marketplace(creator, address(mgr));
        gated.queueUpgrade(address(next));
        vm.warp(block.timestamp + 7 days + 1);
        vm.expectRevert(UpgradeExpired.selector);
        gated.upgradeTo(address(next));
    }

    function test_upgrade_managerRepoint() public {
        // v3.4 manager-replacement flow: a new core impl baking a NEW manager
        // is installed under the OLD manager's authority; afterwards the new
        // manager's admin controls the next upgrade.
        (Marketplace gated, MarketplaceManager mgrA) = _gatedPair();
        MarketplaceManager mgrB = new MarketplaceManager(address(0xB0B), TEST_SENTINEL_KEEPER);

        Marketplace nextImpl = new Marketplace(creator, address(mgrB));
        gated.queueUpgrade(address(nextImpl));   // authorized by mgrA's admin (this)
        gated.upgradeTo(address(nextImpl));
        assertEq(gated.manager(), address(mgrB), "re-pointed to manager B");

        // Old admin (this, via mgrA) no longer authorizes:
        Marketplace nextB = new Marketplace(creator, address(mgrB));
        vm.expectRevert(NotAdmin.selector);
        gated.queueUpgrade(address(nextB));

        // New admin does:
        vm.startPrank(address(0xB0B));
        gated.queueUpgrade(address(nextB));
        gated.upgradeTo(address(nextB));
        vm.stopPrank();
        assertEq(_implOf(address(gated)), address(nextB));
        // Sanity: mgrA's authority genuinely lapsed for gating.
        assertTrue(address(mgrA) != address(mgrB));
    }

    function test_cancelUpgrade_clearsQueue() public {
        (Marketplace gated, MarketplaceManager mgr) = _gatedPair();
        Marketplace next = new Marketplace(creator, address(mgr));
        gated.queueUpgrade(address(next));

        vm.prank(address(0xDEAD));
        vm.expectRevert(NotAdmin.selector);
        gated.cancelUpgrade();

        gated.cancelUpgrade();
        assertEq(gated.pendingImplementation(), address(0));
        assertEq(gated.upgradeEta(), 0);

        vm.expectRevert(UpgradeNotQueued.selector);
        gated.upgradeTo(address(next));
    }

    function test_sealedManager_freezesCoreUpgrades() public {
        // renounceAdmin on the (plain) manager must kill core upgrades: the
        // cores' _requireAdmin consults exactly the hasRole probe that now
        // answers false for everyone.
        (Marketplace gated, MarketplaceManager mgr) = _gatedPair();
        mgr.renounceAdmin();
        Marketplace next = new Marketplace(creator, address(mgr));
        vm.expectRevert(NotAdmin.selector);
        gated.queueUpgrade(address(next));
        vm.expectRevert(NotAdmin.selector);
        mgr.setKeeper(address(0xFEED));
    }
}
