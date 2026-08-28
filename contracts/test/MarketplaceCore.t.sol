// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {Marketplace, NotOwner, NotListed} from "../src/Marketplace.sol";
import {
    BelowMinPrice, ZeroAddress, NotAdmin, NoManager, BadImplementation,
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
    function _gatedMarketplace() internal returns (Marketplace) {
        MarketplaceManager mgr = _deployMarketplaceManager(address(this));
        return _deployMarketplace(creator, address(mgr));
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

    // ── Upgrade timelock ────────────────────────────────────────────────────

    function test_upgradeDelay_isZeroOnLocalChain() public view {
        // forge's default chainid is 31337 — a dev/test chain, so upgrades are
        // instant while the marketplace is in its testing phase.
        assertEq(mp.upgradeDelay(), 0);
    }

    function test_upgradeDelay_isFortyEightHoursOnUnknownChain() public {
        vm.chainId(19); // Songbird
        assertEq(mp.upgradeDelay(), 48 hours);
    }

    function test_ungatedProxy_cannotBeUpgradedAtAll() public {
        // mp has manager == address(0): the implementation is frozen, rather
        // than upgradable by anyone as it was before the timelock landed.
        Marketplace next = new Marketplace();
        vm.expectRevert(NoManager.selector);
        mp.queueUpgrade(address(next));
        vm.expectRevert(NoManager.selector);
        mp.upgradeTo(address(next));
    }

    function test_queueUpgrade_nonAdminReverts() public {
        Marketplace gated = _gatedMarketplace();
        Marketplace next = new Marketplace();
        vm.prank(address(0xDEAD));
        vm.expectRevert(NotAdmin.selector);
        gated.queueUpgrade(address(next));
    }

    function test_queueUpgrade_rejectsZeroAndEOA() public {
        Marketplace gated = _gatedMarketplace();
        vm.expectRevert(BadImplementation.selector);
        gated.queueUpgrade(address(0));
        vm.expectRevert(BadImplementation.selector);
        gated.queueUpgrade(address(0xDEAD)); // EOA: no code
    }

    // Uses upgradeTo, not upgradeToAndCall(impl, ""): on OZ 4.x
    // upgradeToAndCall passes forceCall=true, so empty calldata still
    // delegatecalls into the new implementation's (nonexistent) fallback and
    // reverts. Real upgrades either carry migration calldata or use upgradeTo.
    function test_upgrade_requiresQueueAndDelay() public {
        // Pin a mainnet chain id: local 31337 has delay 0, which makes the
        // UpgradeNotReady leg unreachable. Flare (14) keeps the 48h delay.
        vm.chainId(14);
        Marketplace gated = _gatedMarketplace();
        Marketplace next = new Marketplace();
        address before = _implOf(address(gated));

        // Nothing queued.
        vm.expectRevert(UpgradeNotQueued.selector);
        gated.upgradeTo(address(next));

        gated.queueUpgrade(address(next));
        assertEq(gated.pendingImplementation(), address(next));
        assertEq(gated.upgradeEta(), uint64(block.timestamp) + 48 hours);

        // Queued, but the timer has not run out.
        vm.expectRevert(UpgradeNotReady.selector);
        gated.upgradeTo(address(next));

        // A different implementation cannot ride the queued slot.
        vm.warp(block.timestamp + 48 hours);
        Marketplace other = new Marketplace();
        vm.expectRevert(UpgradeNotQueued.selector);
        gated.upgradeTo(address(other));

        gated.upgradeTo(address(next));
        assertEq(_implOf(address(gated)), address(next), "implementation swapped");
        assertTrue(before != address(next), "sanity: new impl differs");
        assertEq(gated.pendingImplementation(), address(0), "queue cleared");
        assertEq(gated.upgradeEta(), 0, "eta cleared");

        // The consumed queue entry cannot be replayed.
        vm.expectRevert(UpgradeNotQueued.selector);
        gated.upgradeTo(address(next));
    }

    function test_upgrade_staleQueueExpires() public {
        vm.chainId(14); // 48h delay chain — see test_upgrade_requiresQueueAndDelay
        Marketplace gated = _gatedMarketplace();
        Marketplace next = new Marketplace();
        gated.queueUpgrade(address(next));
        vm.warp(block.timestamp + 48 hours + 7 days + 1);
        vm.expectRevert(UpgradeExpired.selector);
        gated.upgradeTo(address(next));
    }

    function test_upgrade_zeroDelay_sameBlockInstall() public {
        // Testnet/dev chains (31337 here) have delay 0: queue then install in
        // the same block. Gate is block.timestamp < upgradeEta, false when
        // eta == now.
        Marketplace gated = _gatedMarketplace();
        Marketplace next = new Marketplace();
        gated.queueUpgrade(address(next));
        gated.upgradeTo(address(next));
        assertEq(_implOf(address(gated)), address(next));
    }

    function test_cancelUpgrade_clearsQueue() public {
        Marketplace gated = _gatedMarketplace();
        Marketplace next = new Marketplace();
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
}
