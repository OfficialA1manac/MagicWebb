// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {Marketplace, NotOwner, NotListed, Expired, NotApproved, WrongPrice, BatchTooLarge, InvalidExpiry} from "../src/Marketplace.sol";
import {MockERC721} from "./MockERC721.sol";
import {MockERC1155} from "./MockERC1155.sol";
import {TokenStandard, BelowMinPrice, InvalidDuration} from "../src/MarketplaceCore.sol";
import {TestHelpers} from "./TestHelpers.sol";

contract MarketplaceTest is Test, TestHelpers {
    Marketplace mp;
    MockERC721 nft;
    MockERC1155 multi;
    address creator = address(0x1111000000000000000000000000000000111100);
    address seller = address(0xBEEF);
    address buyer = address(0xCAFE);

    function setUp() public {
        mp = _deployMarketplace(creator, address(0));
        nft = new MockERC721();
        multi = new MockERC1155();
        vm.deal(seller, 100 ether);
        vm.deal(buyer, 100 ether);
    }

    uint64 constant _LIST_24HR = 24 hours;

    function test_listAndBuyFlow() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), tid, 1 ether, uint64(block.timestamp + _LIST_24HR));
        vm.stopPrank();

        (address s, , , uint128 p,) = mp.listings(address(nft), tid, seller);
        assertEq(s, seller);
        assertEq(p, 1 ether);

        vm.prank(buyer);
        mp.buy{value: 1 ether}(address(nft), tid, seller);
        assertEq(nft.ownerOf(tid), buyer);
    }

    function test_list1155AndBuyFlow() public {
        vm.startPrank(seller);
        multi.mint(seller, 42, 10);
        multi.setApprovalForAll(address(mp), true);
        mp.list1155(address(multi), 42, 5, 2 ether, uint64(block.timestamp + _LIST_24HR));
        vm.stopPrank();

        vm.prank(buyer);
        mp.buy{value: 2 ether}(address(multi), 42, seller);
        assertEq(multi.balanceOf(buyer, 42), 5);
    }

    function test_cancelRemovesListing() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), tid, 1 ether, uint64(block.timestamp + _LIST_24HR));
        vm.stopPrank();

        vm.prank(seller);
        mp.cancel(address(nft), tid);
        (address s, , ,,) = mp.listings(address(nft), tid, seller);
        assertEq(s, address(0));
    }

    function test_buyWrongPriceReverts() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), tid, 1 ether, uint64(block.timestamp + _LIST_24HR));
        vm.stopPrank();

        vm.prank(buyer);
        vm.expectRevert(WrongPrice.selector);
        mp.buy{value: 0.5 ether}(address(nft), tid, seller);
    }

    function test_buyExpiredReverts() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), tid, 1 ether, uint64(block.timestamp + 3 minutes));
        vm.stopPrank();

        vm.warp(block.timestamp + 5 minutes);
        vm.prank(buyer);
        vm.expectRevert(Expired.selector);
        mp.buy{value: 1 ether}(address(nft), tid, seller);
    }

    function test_editPriceUpdatesListing() public {
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), tid, 1 ether, uint64(block.timestamp + _LIST_24HR));
        vm.stopPrank();

        vm.prank(seller);
        mp.editPrice(address(nft), tid, 2 ether);
        (,,,uint128 np,) = mp.listings(address(nft), tid, seller);
        assertEq(np, 2 ether);
    }

    function test_batchListAtomically() public {
        vm.startPrank(seller);
        uint256 t1 = nft.mint(seller);
        uint256 t2 = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        vm.stopPrank();

        Marketplace.BatchItem[] memory items = new Marketplace.BatchItem[](2);
        items[0] = Marketplace.BatchItem(address(nft), t1, 1 ether, uint64(block.timestamp + _LIST_24HR));
        items[1] = Marketplace.BatchItem(address(nft), t2, 2 ether, uint64(block.timestamp + _LIST_24HR));

        vm.prank(seller);
        mp.batchList(items);

        (, , , uint128 p1,) = mp.listings(address(nft), t1, seller);
        (, , , uint128 p2,) = mp.listings(address(nft), t2, seller);
        assertEq(uint256(p1), 1 ether);
        assertEq(uint256(p2), 2 ether);
    }

    function test_cleanExpiredOnlyKeeper() public {
        // manager == address(0) → permissionless. Works as fallback.
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), tid, 1 ether, uint64(block.timestamp + 3 minutes));
        vm.stopPrank();

        vm.warp(block.timestamp + 5 minutes);
        mp.cleanExpired(address(nft), tid, seller);
        (address s, , ,,) = mp.listings(address(nft), tid, seller);
        assertEq(s, address(0));
    }

    function testFuzz_sellerPaysFee(uint128 price) public {
        price = uint128(bound(price, 1 ether, 50 ether));
        Marketplace freshMp = _deployMarketplace(creator, address(0));
        MockERC721 nft2 = new MockERC721();
        address s2 = address(0xBEEF);
        vm.deal(s2, 100 ether);
        address b2 = address(0xCAFE);
        vm.deal(b2, uint256(price) + 1 ether);

        vm.startPrank(s2);
        uint256 tid = nft2.mint(s2);
        nft2.setApprovalForAll(address(freshMp), true);
        freshMp.list(address(nft2), tid, price, uint64(block.timestamp + 24 hours));
        vm.stopPrank();

        uint256 sb = s2.balance;
        vm.prank(b2);
        freshMp.buy{value: uint256(price)}(address(nft2), tid, s2);
        uint256 fee = uint256(price) * 150 / 10_000;
        assertEq(s2.balance, sb + uint256(price) - fee);
    }
}
