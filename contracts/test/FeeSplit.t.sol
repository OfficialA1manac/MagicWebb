// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {OfferBook} from "../src/OfferBook.sol";
import {MarketplaceCore, NoKeeper, ZeroAddress} from "../src/MarketplaceCore.sol";
import {MarketplaceManager} from "../src/MarketplaceManager.sol";
import {MockERC721} from "./MockERC721.sol";
import {TestHelpers} from "./TestHelpers.sol";

/// @dev A keeper whose receive() always reverts — forces the keeper leg of
///      _payFee onto the pendingReturns fallback.
contract RevertingKeeper {
    receive() external payable { revert("no"); }
}

/// @dev Passes the constructor's hasRole probe but cannot report a keeper:
///      keeper() reverts. Every fee-paying action must revert NoKeeper.
contract FakeManagerNoKeeper {
    function hasRole(bytes32, address) external pure returns (bool) { return false; }
    function keeper() external pure returns (address) { revert("no keeper"); }
}

/// @dev Passes the probe and answers keeper() with address(0).
contract FakeManagerZeroKeeper {
    function hasRole(bytes32, address) external pure returns (bool) { return false; }
    function keeper() external pure returns (address) { return address(0); }
}

/// @dev Passes the probe; keeper() hits the fallback and returns 64 bytes
///      (wrong length) — the decode guard must treat that as "no keeper".
contract FakeManagerBadLength {
    function hasRole(bytes32, address) external pure returns (bool) { return false; }
    fallback() external {
        assembly {
            mstore(0, 1)
            mstore(32, 2)
            return(0, 64)
        }
    }
}

/// @dev Minimal concrete core so _payFee can be driven with an exact wei fee
///      (buy/settle/acceptOffer cannot produce a 3-wei fee under MIN_PRICE).
contract FeeHarness is MarketplaceCore {
    constructor(address recipient, address manager_) MarketplaceCore(recipient, manager_) {}
    function payFee(uint256 fee) external payable { _payFee(fee); }
}

/// @title Fee split battery (v3.4): 2% total = 1.5% feeRecipient + 0.5% keeper.
contract FeeSplitTest is Test, TestHelpers {
    event FeeSplit(address indexed feeRecipient, uint256 platformShare, address indexed keeper, uint256 keeperShare);
    event PushFailed(address indexed to, uint256 amount);

    MarketplaceManager mgr;
    Marketplace mp;
    AuctionHouse ah;
    OfferBook ob;
    MockERC721 nft;

    address feeRecipient = address(0xFEE);
    address keeper = address(0xCAFE);
    address seller = address(0x5E11E5);
    address buyer  = address(0xB0B);

    function setUp() public {
        mgr = _deployMarketplaceManager(address(this), keeper);
        mp = _deployMarketplace(feeRecipient, address(mgr));
        ah = _deployAuctionHouse(feeRecipient, address(mgr));
        ob = _deployOfferBook(feeRecipient, address(mgr));
        nft = new MockERC721();
        ob.setOfferEligible(address(nft), true); // mock's ERC-173 owner is this test
        vm.deal(seller, 100 ether);
        vm.deal(buyer, 100 ether);
        vm.startPrank(seller);
        nft.setApprovalForAll(address(mp), true);
        nft.setApprovalForAll(address(ah), true);
        nft.setApprovalForAll(address(ob), true);
        vm.stopPrank();
    }

    function _list(uint128 price) internal returns (uint256 tid) {
        tid = nft.mint(seller);
        vm.prank(seller);
        mp.list(address(nft), tid, price, uint64(24 hours));
    }

    // ── Constants ────────────────────────────────────────────────────────────

    function test_constants_sumToTotal() public view {
        assertEq(mp.PLATFORM_FEE_BPS(), 200, "total 2%");
        assertEq(mp.PLATFORM_SHARE_BPS(), 150, "platform 1.5%");
        assertEq(mp.KEEPER_SHARE_BPS(), 50, "keeper 0.5%");
        assertEq(uint256(mp.PLATFORM_SHARE_BPS()) + mp.KEEPER_SHARE_BPS(), mp.PLATFORM_FEE_BPS(), "shares must sum to total");
    }

    // ── buy(): exact split + event ───────────────────────────────────────────

    function test_buy_splitsFeeAndEmits() public {
        uint128 price = 10 ether;
        uint256 tid = _list(price);
        uint256 fee = uint256(price) * 200 / 10_000;        // 0.2 ether
        uint256 keeperCut = fee * 50 / 200;                   // 0.05 ether
        uint256 platformCut = fee - keeperCut;                // 0.15 ether
        assertEq(platformCut, uint256(price) * 150 / 10_000, "platform share == 1.5% of price");
        assertEq(keeperCut, uint256(price) * 50 / 10_000, "keeper share == 0.5% of price");

        uint256 fB = feeRecipient.balance; uint256 kB = keeper.balance; uint256 sB = seller.balance;
        vm.expectEmit(true, true, false, true, address(mp));
        emit FeeSplit(feeRecipient, platformCut, keeper, keeperCut);
        vm.prank(buyer);
        mp.buy{value: price}(address(nft), tid, seller);

        assertEq(feeRecipient.balance - fB, platformCut, "feeRecipient +1.5%");
        assertEq(keeper.balance - kB, keeperCut, "keeper +0.5%");
        assertEq(seller.balance - sB, uint256(price) - fee, "seller nets 98%");
        assertEq(address(mp).balance, 0, "no residue");
        assertEq(mp.pendingReturns(feeRecipient), 0);
        assertEq(mp.pendingReturns(keeper), 0);
    }

    // ── settle(): the auction path goes through _payFee too ─────────────────

    function test_settle_splitsFee() public {
        uint256 tid = nft.mint(seller);
        vm.prank(seller);
        uint256 id = ah.create(address(nft), tid, 1 ether, uint64(1 hours));
        vm.prank(buyer);
        ah.bid{value: 4 ether}(id);
        vm.warp(block.timestamp + 2 hours);

        uint256 fee = uint256(4 ether) * 200 / 10_000;
        uint256 keeperCut = fee * 50 / 200;
        uint256 fB = feeRecipient.balance; uint256 kB = keeper.balance; uint256 sB = seller.balance;
        vm.expectEmit(true, true, false, true, address(ah));
        emit FeeSplit(feeRecipient, fee - keeperCut, keeper, keeperCut);
        vm.prank(keeper);
        ah.settle(id);

        assertEq(feeRecipient.balance - fB, fee - keeperCut, "feeRecipient +1.5%");
        assertEq(keeper.balance - kB, keeperCut, "keeper +0.5%");
        assertEq(seller.balance - sB, 4 ether - fee, "seller nets 98%");
        assertEq(nft.ownerOf(tid), buyer);
    }

    // ── acceptOffer(): same split ────────────────────────────────────────────

    function test_acceptOffer_splitsFee() public {
        uint256 tid = nft.mint(seller);
        vm.prank(buyer);
        ob.makeOffer{value: 2 ether}(address(nft), tid, 2 ether, uint64(24 hours));

        uint256 fee = uint256(2 ether) * 200 / 10_000;
        uint256 keeperCut = fee * 50 / 200;
        uint256 fB = feeRecipient.balance; uint256 kB = keeper.balance; uint256 sB = seller.balance;
        vm.expectEmit(true, true, false, true, address(ob));
        emit FeeSplit(feeRecipient, fee - keeperCut, keeper, keeperCut);
        vm.prank(seller);
        ob.acceptOffer(address(nft), tid, buyer, 2 ether);

        assertEq(feeRecipient.balance - fB, fee - keeperCut, "feeRecipient +1.5%");
        assertEq(keeper.balance - kB, keeperCut, "keeper +0.5%");
        assertEq(seller.balance - sB, 2 ether - fee, "seller nets 98%");
    }

    // ── Keeper is MANDATORY: no manager → no core; no keeper → no fee → no sale ──

    function test_constructor_zeroManagerReverts() public {
        vm.expectRevert(ZeroAddress.selector);
        new Marketplace(feeRecipient, address(0));
        vm.expectRevert(ZeroAddress.selector);
        new AuctionHouse(feeRecipient, address(0));
        vm.expectRevert(ZeroAddress.selector);
        new OfferBook(feeRecipient, address(0));
    }

    function test_payFee_revertsNoKeeper_whenKeeperCallFails() public {
        FeeHarness h = new FeeHarness(feeRecipient, address(new FakeManagerNoKeeper()));
        vm.deal(address(this), 1 ether);
        vm.expectRevert(NoKeeper.selector);
        h.payFee{value: 1 ether}(1 ether);
    }

    function test_payFee_revertsNoKeeper_whenKeeperIsZero() public {
        FeeHarness h = new FeeHarness(feeRecipient, address(new FakeManagerZeroKeeper()));
        vm.deal(address(this), 1 ether);
        vm.expectRevert(NoKeeper.selector);
        h.payFee{value: 1 ether}(1 ether);
    }

    function test_payFee_revertsNoKeeper_whenReturnLengthWrong() public {
        FeeHarness h = new FeeHarness(feeRecipient, address(new FakeManagerBadLength()));
        vm.deal(address(this), 1 ether);
        vm.expectRevert(NoKeeper.selector);
        h.payFee{value: 1 ether}(1 ether);
    }

    function test_payFee_zeroFeeIsNoopEvenWithoutKeeper() public {
        // fee == 0 short-circuits before the keeper probe.
        FeeHarness h = new FeeHarness(feeRecipient, address(new FakeManagerNoKeeper()));
        vm.recordLogs();
        h.payFee(0);
        assertEq(vm.getRecordedLogs().length, 0);
    }

    /// End-to-end: a Marketplace whose manager cannot name a keeper blocks the
    /// SALE (buy reverts NoKeeper) rather than silently paying the whole fee to
    /// feeRecipient. Listing itself is unaffected (no fee leg).
    function test_buy_revertsNoKeeper_whenManagerHasNoKeeper() public {
        Marketplace blocked = _deployMarketplace(feeRecipient, address(new FakeManagerNoKeeper()));
        uint256 tid = nft.mint(seller);
        vm.startPrank(seller);
        nft.setApprovalForAll(address(blocked), true);
        blocked.list(address(nft), tid, 10 ether, uint64(24 hours));
        vm.stopPrank();

        uint256 fB = feeRecipient.balance; uint256 sB = seller.balance;
        vm.prank(buyer);
        vm.expectRevert(NoKeeper.selector);
        blocked.buy{value: 10 ether}(address(nft), tid, seller);

        assertEq(feeRecipient.balance, fB, "feeRecipient untouched");
        assertEq(seller.balance, sB, "seller untouched");
        assertEq(nft.ownerOf(tid), seller, "NFT stays with seller");
        (address s_,,,,) = blocked.listings(address(nft), tid, seller);
        assertEq(s_, seller, "listing intact");
    }

    // ── Reverting keeper: keeper share falls back to pendingReturns ─────────

    function test_buy_revertingKeeper_fallsBackToPendingReturns() public {
        RevertingKeeper bad = new RevertingKeeper();
        mgr.setKeeper(address(bad)); // this test is the manager admin
        uint128 price = 10 ether;
        uint256 tid = _list(price);
        uint256 fee = uint256(price) * 200 / 10_000;
        uint256 keeperCut = fee * 50 / 200;

        uint256 fB = feeRecipient.balance;
        vm.expectEmit(true, false, false, true, address(mp));
        emit PushFailed(address(bad), keeperCut);
        vm.expectEmit(true, true, false, true, address(mp));
        emit FeeSplit(feeRecipient, fee - keeperCut, address(bad), keeperCut);
        vm.prank(buyer);
        mp.buy{value: price}(address(nft), tid, seller);

        assertEq(feeRecipient.balance - fB, fee - keeperCut, "platform share pushed");
        assertEq(mp.pendingReturns(address(bad)), keeperCut, "keeper share credited");
        assertEq(address(mp).balance, keeperCut, "core holds exactly the credit");

        // The credit is pullable once the keeper can receive — here via a
        // fresh EOA is impossible (credit is keyed to `bad`), so just prove
        // the seller/feeRecipient legs were unaffected and nothing else leaked.
        assertEq(mp.pendingReturns(feeRecipient), 0);
        assertEq(mp.pendingReturns(seller), 0);
    }

    // ── Rotation: the share follows the CURRENT keeper ──────────────────────

    function test_buy_followsRotatedKeeper() public {
        address k2 = address(0xBEEF);
        mgr.setKeeper(k2);
        uint256 tid = _list(10 ether);
        uint256 keeperCut = (uint256(10 ether) * 200 / 10_000) * 50 / 200;
        uint256 k1B = keeper.balance; uint256 k2B = k2.balance;
        vm.prank(buyer);
        mp.buy{value: 10 ether}(address(nft), tid, seller);
        assertEq(keeper.balance, k1B, "old keeper gets nothing");
        assertEq(k2.balance - k2B, keeperCut, "new keeper gets 0.5%");
    }

    // ── Rounding: truncation favours the platform share ─────────────────────

    function test_payFee_roundingFavoursPlatform() public {
        FeeHarness h = new FeeHarness(feeRecipient, address(mgr));
        vm.deal(address(this), 10);
        uint256 fB = feeRecipient.balance; uint256 kB = keeper.balance;

        // fee = 3 wei → keeperCut = 3*50/200 = 0 (truncated), platform = 3.
        // The keeper is still resolved (and reported) — only the share is 0.
        vm.expectEmit(true, true, false, true, address(h));
        emit FeeSplit(feeRecipient, 3, keeper, 0);
        h.payFee{value: 3}(3);
        assertEq(feeRecipient.balance - fB, 3, "platform gets all 3 wei");
        assertEq(keeper.balance, kB, "keeper gets 0");

        // fee = 7 wei → keeperCut = 1 (1.75 truncated), platform = 6.
        vm.expectEmit(true, true, false, true, address(h));
        emit FeeSplit(feeRecipient, 6, keeper, 1);
        h.payFee{value: 7}(7);
        assertEq(feeRecipient.balance - fB, 9);
        assertEq(keeper.balance - kB, 1);
    }

    function test_payFee_zeroIsNoop() public {
        FeeHarness h = new FeeHarness(feeRecipient, address(mgr));
        vm.recordLogs();
        h.payFee(0);
        assertEq(vm.getRecordedLogs().length, 0, "no events for a zero fee");
    }

    /// Fuzz: for any sale, platform + keeper == total fee, and the keeper
    /// share never exceeds a quarter of it.
    function testFuzz_split_conserves(uint256 fee) public {
        fee = bound(fee, 1, 1_000_000 ether);
        FeeHarness h = new FeeHarness(feeRecipient, address(mgr));
        vm.deal(address(this), fee);
        uint256 fB = feeRecipient.balance; uint256 kB = keeper.balance;
        h.payFee{value: fee}(fee);
        uint256 p = feeRecipient.balance - fB;
        uint256 k = keeper.balance - kB;
        assertEq(p + k, fee, "conserved");
        assertEq(k, fee * 50 / 200, "keeper = fee/4 truncated");
        assertGe(p, k * 3, "platform >= 3x keeper");
    }
}
