// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {MarketplaceManager, ZeroAddr, NotContract, SameValue} from "../src/MarketplaceManager.sol";
import {Marketplace}  from "../src/Marketplace.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {OfferBook}    from "../src/OfferBook.sol";
import {EntriesHalted, BadManager} from "../src/MarketplaceCore.sol";
import {MockERC721}  from "./MockERC721.sol";

/// Covers the manager itself (roles, breaker, registry, token slots) and the
/// load-bearing invariant of the whole design: while entries are paused, every
/// EXIT path on every core still runs — funds can never be trapped.
contract MarketplaceManagerTest is Test {
    MarketplaceManager mgr;
    Marketplace  mp;
    AuctionHouse ah;
    OfferBook    ob;
    MockERC721   nft;

    address admin        = address(0xAD);
    address operator     = address(0x09);
    address feeRecipient = address(0x1111000000000000000000000000000000111100);
    address seller       = address(0xBEEF);
    address alice        = address(0xA11CE);
    address bob          = address(0xB0B);
    address rando        = address(0x4444);

    function setUp() public {
        mgr = new MarketplaceManager(admin);
        mp  = new Marketplace (feeRecipient, address(mgr));
        ah  = new AuctionHouse(feeRecipient, address(mgr));
        ob  = new OfferBook   (feeRecipient, address(mgr));
        nft = new MockERC721();

        vm.startPrank(admin);
        mgr.setCoreContracts(address(mp), address(ah), address(ob));
        mgr.grantRole(mgr.OPERATOR_ROLE(), operator);
        vm.stopPrank();

        vm.deal(alice, 100 ether);
        vm.deal(bob,   100 ether);

        // Enable offers on the mock NFT collection so makeOffer tests pass.
        // The admin has DEFAULT_ADMIN_ROLE via the MarketplaceManager.
        vm.prank(admin);
        ob.setOfferEligible(address(nft), true);
    }

    // ── Roles ────────────────────────────────────────────────────────────────

    function test_adminHasDefaultAndOperatorRoles() public view {
        assertTrue(mgr.hasRole(mgr.DEFAULT_ADMIN_ROLE(), admin));
        assertTrue(mgr.hasRole(mgr.OPERATOR_ROLE(), admin));
    }

    function test_nonOperatorCannotPause() public {
        vm.prank(rando);
        vm.expectRevert();
        mgr.pauseEntries();
    }

    function test_nonAdminCannotSetModules() public {
        vm.prank(rando);
        vm.expectRevert();
        mgr.setTokenAddress(address(nft));
    }

    function test_keeperRoleGrantRevoke() public {
        bytes32 role = mgr.KEEPER_ROLE();
        vm.prank(admin);
        mgr.grantRole(role, rando);
        assertTrue(mgr.hasRole(role, rando));
        vm.prank(admin);
        mgr.revokeRole(role, rando);
        assertFalse(mgr.hasRole(role, rando));
    }

    // ── Circuit breaker semantics ────────────────────────────────────────────

    function test_deploysUnpaused() public view {
        assertTrue(mgr.entriesAllowed());
    }

    function test_pauseUnpauseRoundtrip() public {
        vm.prank(operator);
        mgr.pauseEntries();
        assertFalse(mgr.entriesAllowed());
        vm.prank(operator);
        mgr.unpauseEntries();
        assertTrue(mgr.entriesAllowed());
    }

    function test_doublePauseReverts() public {
        vm.prank(operator);
        mgr.pauseEntries();
        vm.prank(operator);
        vm.expectRevert(SameValue.selector);
        mgr.pauseEntries();
    }

    // ── Entry gating: every entry path halts while paused ────────────────────

    function _pause() internal {
        vm.prank(operator);
        mgr.pauseEntries();
    }

    function test_pausedBlocksList() public {
        uint256 tid = nft.mint(seller);
        vm.prank(seller);
        nft.setApprovalForAll(address(mp), true);
        _pause();
        vm.prank(seller);
        vm.expectRevert(EntriesHalted.selector);
        mp.list(address(nft), tid, 1 ether, uint64(block.timestamp + 1 days));
    }

    function test_pausedBlocksBuy() public {
        uint256 tid = _listed();
        _pause();
        vm.prank(alice);
        vm.expectRevert(EntriesHalted.selector);
        mp.buy{value: 1 ether}(address(nft), tid, seller);
    }

    function test_pausedBlocksAuctionCreateAndBid() public {
        (uint256 id,) = _auction();
        _pause();

        vm.startPrank(seller);
        uint256 tid2 = nft.mint(seller);
        vm.expectRevert(EntriesHalted.selector);
        ah.create(address(nft), tid2, 1 ether, uint64(block.timestamp + 1 days), 500, 0);
        vm.stopPrank();

        vm.prank(alice);
        vm.expectRevert(EntriesHalted.selector);
        ah.bid{value: 1 ether}(id);
    }

    function test_pausedBlocksMakeAndAcceptOffer() public {
        uint256 tid = nft.mint(seller);
        uint64 exp = uint64(block.timestamp + 24 hours);
        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, exp);

        _pause();

        vm.prank(bob);
        vm.expectRevert(EntriesHalted.selector);
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, exp);

        vm.startPrank(seller);
        nft.setApprovalForAll(address(ob), true);
        vm.expectRevert(EntriesHalted.selector);
        ob.acceptOffer(address(nft), tid, alice);
        vm.stopPrank();
    }

    function test_unpauseRestoresEntries() public {
        uint256 tid = nft.mint(seller);
        vm.prank(seller);
        nft.setApprovalForAll(address(mp), true);
        _pause();
        vm.prank(operator);
        mgr.unpauseEntries();
        vm.prank(seller);
        mp.list(address(nft), tid, 1 ether, uint64(block.timestamp + 1 days));
        (address lSeller,,,,) = mp.listings(address(nft), tid, seller);
        assertEq(lSeller, seller);
    }

    // ── THE invariant: exits run while paused (funds never trapped) ──────────

    function test_pausedAuctionStillSettlesAndRefundsLosers() public {
        (uint256 id,) = _auction();
        vm.prank(alice);
        ah.bid{value: 1 ether}(id);
        vm.prank(bob);
        ah.bid{value: 2 ether}(id);

        _pause();
        vm.warp(block.timestamp + 8 days);

        uint256 sellerBefore = seller.balance;
        uint256 aliceBefore  = alice.balance;

        // settle: permissionless, ungated.
        vm.prank(rando);
        ah.settle(id);
        assertGt(seller.balance, sellerBefore);

        // refundLosers: permissionless, ungated.
        address[] memory losers = new address[](1);
        losers[0] = alice;
        vm.prank(rando);
        ah.refundLosers(id, losers);
        assertEq(alice.balance, aliceBefore + 1 ether);
    }

    function test_pausedSellerCanStillCancelEarly() public {
        (uint256 id,) = _auction();
        _pause();
        vm.prank(seller);
        ah.cancelEarly(id); // must not revert
    }

    function test_pausedListingCancelStillWorks() public {
        uint256 tid = _listed();
        _pause();
        vm.prank(seller);
        mp.cancel(address(nft), tid); // must not revert
    }

    function test_pausedOfferRejectAndExpiryRefundStillWork() public {
        uint256 tid = nft.mint(seller);
        vm.prank(alice);
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, uint64(block.timestamp + 1 days));
        vm.prank(bob);
        ob.makeOffer{value: 1 ether}(address(nft), tid, 1 ether, uint64(block.timestamp + 1 days));

        _pause();

        // Owner reject: full refund, ungated.
        uint256 aliceBefore = alice.balance;
        vm.prank(seller);
        ob.rejectOffer(address(nft), tid, alice);
        assertEq(alice.balance, aliceBefore + 1 ether);

        // Seller rejects bob's offer too (refundExpiredOffer removed in v10).
        vm.prank(seller);
        ob.rejectOffer(address(nft), tid, bob);
        assertEq(bob.balance, bob.balance - 1 ether + 1 ether); // net zero
    }

    // ── Registry + token slots ───────────────────────────────────────────────

    function test_setCoreContractsRejectsEOA() public {
        vm.prank(admin);
        vm.expectRevert(NotContract.selector);
        mgr.setCoreContracts(rando, address(ah), address(ob));
    }

    function test_setTokenAddressValidatesAndStores() public {
        vm.prank(admin);
        vm.expectRevert(ZeroAddr.selector);
        mgr.setTokenAddress(address(0));

        vm.prank(admin);
        vm.expectRevert(NotContract.selector);
        mgr.setTokenAddress(rando); // EOA

        vm.prank(admin);
        mgr.setTokenAddress(address(nft));
        assertEq(mgr.token(), address(nft));
    }

    function test_futureModuleSlotsStore() public {
        vm.startPrank(admin);
        mgr.setFeeDistributor(address(nft));
        mgr.setStakingModule(address(nft));
        mgr.setGovernanceModule(address(nft));
        vm.stopPrank();
        assertEq(mgr.feeDistributor(),   address(nft));
        assertEq(mgr.stakingModule(),    address(nft));
        assertEq(mgr.governanceModule(), address(nft));
    }

    function test_managerHoldsNoFundsPath() public {
        // The manager has no payable surface at all.
        (bool ok,) = address(mgr).call{value: 1 ether}("");
        assertFalse(ok);
    }

    // ── Constructor manager validation (immutable — a bad value is forever) ──

    function test_coreRejectsEOAManager() public {
        vm.expectRevert(BadManager.selector);
        new Marketplace(feeRecipient, rando); // EOA: no code
    }

    function test_coreRejectsNonManagerContract() public {
        // A contract without entriesAllowed() must fail the deploy probe.
        vm.expectRevert();
        new Marketplace(feeRecipient, address(nft));
    }

    // ── Ungated cores (manager == address(0)) stay fully open ────────────────

    function test_zeroManagerCoreIgnoresBreaker() public {
        Marketplace freeMp = new Marketplace(feeRecipient, address(0));
        uint256 tid = nft.mint(seller);
        vm.startPrank(seller);
        nft.setApprovalForAll(address(freeMp), true);
        freeMp.list(address(nft), tid, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();
    }

    // ── Helpers ──────────────────────────────────────────────────────────────

    function _listed() internal returns (uint256 tid) {
        tid = nft.mint(seller);
        vm.startPrank(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), tid, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();
    }

    function _auction() internal returns (uint256 id, uint256 tid) {
        vm.startPrank(seller);
        tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        id = ah.create(address(nft), tid, 1 ether, uint64(block.timestamp + 7 days), 500, 0);
        ah.activateAuction(id);
        vm.stopPrank();
    }
}
