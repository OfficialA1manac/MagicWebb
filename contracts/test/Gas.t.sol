// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {Marketplace} from "../src/Marketplace.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {OfferBook} from "../src/OfferBook.sol";
import {MarketplaceManager} from "../src/MarketplaceManager.sol";
import {MockERC721} from "./MockERC721.sol";
import {TestHelpers} from "./TestHelpers.sol";

/// @title Gas snapshot battery (v3.4)
/// @notice One deterministic test per measured hot op, for `forge snapshot
///         --match-contract GasTest`. Compare .gas-snapshot across changes to
///         attribute per-item deltas (v3.4 targets vs v3.2 baseline: create
///         −66k, first bid −44k, makeOffer −22k, buy −7k, settle −6.8k+).
///         These are relative-tracking numbers — absolute values include
///         forge test-harness overhead and the ERC1967 proxy hop.
contract GasTest is Test, TestHelpers {
    MarketplaceManager mgr;
    Marketplace mp;
    AuctionHouse ah;
    OfferBook ob;
    MockERC721 nft;

    address seller = address(0x5E11E5);
    address buyer  = address(0xB0B);
    address bidder2 = address(0xB1D2);
    address fee    = address(0xFEE);
    address admin  = address(0xAD);
    address keeper = address(0xCAFE);

    function setUp() public {
        // A REAL manager (plain, unproxied -- the v3.4 shape) so the keeper
        // consult path (hasRole staticcall from settle) is measured, not
        // short-circuited by manager == address(0).
        mgr = new MarketplaceManager(admin, keeper);
        mp = _deployMarketplace(fee, address(mgr));
        ah = _deployAuctionHouse(fee, address(mgr));
        ob = _deployOfferBook(fee, address(mgr));
        nft = new MockERC721();

        vm.deal(seller, 1_000 ether);
        vm.deal(buyer, 1_000 ether);
        vm.deal(bidder2, 1_000 ether);

        // Offers: the mock's ERC-173 owner is THIS test contract.
        ob.setOfferEligible(address(nft), true);

        // Warm approvals so op measurements exclude one-time approval costs.
        vm.startPrank(seller);
        nft.setApprovalForAll(address(mp), true);
        nft.setApprovalForAll(address(ah), true);
        nft.setApprovalForAll(address(ob), true);
        vm.stopPrank();
    }

    function _mintTo(address to) internal returns (uint256) {
        return nft.mint(to);
    }

    function test_gas_list() public {
        uint256 id = _mintTo(seller);
        vm.prank(seller);
        mp.list(address(nft), id, 10 ether, uint64(24 hours));
    }

    function test_gas_buy() public {
        uint256 id = _mintTo(seller);
        vm.prank(seller);
        mp.list(address(nft), id, 10 ether, uint64(24 hours));
        vm.prank(buyer);
        mp.buy{value: 10 ether}(address(nft), id, seller);
    }

    function test_gas_cancel() public {
        uint256 id = _mintTo(seller);
        vm.startPrank(seller);
        mp.list(address(nft), id, 10 ether, uint64(24 hours));
        mp.cancel(address(nft), id);
        vm.stopPrank();
    }

    function test_gas_auctionCreate() public {
        uint256 id = _mintTo(seller);
        vm.prank(seller);
        ah.create(address(nft), id, 5 ether, uint64(24 hours));
    }

    function test_gas_firstBid() public {
        uint256 id = _mintTo(seller);
        vm.prank(seller);
        uint256 auctionId = ah.create(address(nft), id, 5 ether, uint64(24 hours));
        vm.prank(buyer);
        ah.bid{value: 5 ether}(auctionId);
    }

    function test_gas_overtakeBid() public {
        uint256 id = _mintTo(seller);
        vm.prank(seller);
        uint256 auctionId = ah.create(address(nft), id, 5 ether, uint64(24 hours));
        vm.prank(buyer);
        ah.bid{value: 5 ether}(auctionId);
        vm.prank(bidder2);
        ah.bid{value: 6 ether}(auctionId); // clears 5 + MIN_BID_INCREMENT
    }

    function test_gas_settle() public {
        uint256 id = _mintTo(seller);
        vm.prank(seller);
        uint256 auctionId = ah.create(address(nft), id, 5 ether, uint64(24 hours));
        vm.prank(buyer);
        ah.bid{value: 5 ether}(auctionId);
        vm.warp(block.timestamp + 24 hours + 1);
        // Keeper settles: exercises the manager hasRole consult (the path
        // v3.4's unproxied manager was built to shorten).
        vm.prank(keeper);
        ah.settle(auctionId);
    }

    function test_gas_makeOffer() public {
        uint256 id = _mintTo(seller);
        vm.prank(buyer);
        ob.makeOffer{value: 2 ether}(address(nft), id, 2 ether, uint64(24 hours));
    }

    function test_gas_acceptOffer() public {
        uint256 id = _mintTo(seller);
        vm.prank(buyer);
        ob.makeOffer{value: 2 ether}(address(nft), id, 2 ether, uint64(24 hours));
        vm.prank(seller);
        ob.acceptOffer(address(nft), id, buyer, 2 ether);
    }

    function test_gas_refundLosers10() public {
        uint256 id = _mintTo(seller);
        vm.prank(seller);
        uint256 auctionId = ah.create(address(nft), id, 1 ether, uint64(24 hours));
        // 10 escalating bidders; the 10th leads, the first 9 are losers.
        address[] memory batch = new address[](10);
        for (uint256 i; i < 10; ++i) {
            address b = address(uint160(0xA000 + i));
            batch[i] = b;
            vm.deal(b, 100 ether);
            vm.prank(b);
            ah.bid{value: (i + 1) * 1 ether + i * 1 ether}(auctionId); // strictly increasing cumulatives
        }
        vm.warp(block.timestamp + 24 hours + 1);
        vm.prank(seller);
        ah.settle(auctionId);
        ah.refundLosers(auctionId, batch);
    }
}
