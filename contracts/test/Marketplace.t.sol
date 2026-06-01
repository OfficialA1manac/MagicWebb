// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}         from "forge-std/Test.sol";
import {Marketplace, BatchTooLarge, NotListed, WrongPrice, Expired, NotOwner, InvalidExpiry, InvalidAmount}  from "../src/Marketplace.sol";
import {BelowMinPrice} from "../src/MarketplaceCore.sol";
import {MockERC721}   from "./MockERC721.sol";
import {MockERC1155}  from "./MockERC1155.sol";

contract MarketplaceTest is Test {
    Marketplace  mp;
    MockERC721   nft;
    MockERC1155  multi;
    address creator = address(0xC0DE);
    address seller  = address(0xBEEF);
    address buyer   = address(0xCAFE);
    address other   = address(0xDEAD);

    function setUp() public {
        mp    = new Marketplace(creator);
        nft   = new MockERC721();
        multi = new MockERC1155();
        vm.deal(buyer, 10 ether);
    }

    // ── Helpers ───────────────────────────────────────────────────────────

    /// @dev Lists a freshly-minted ERC-721. Listing is free under the new fee model.
    function _list(uint256 price, uint64 exp) internal returns (uint256 id) {
        vm.startPrank(seller);
        id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), id, uint128(price), exp);
        vm.stopPrank();
    }

    function _list1155(uint256 tokenId, uint128 units, uint128 price, uint64 exp) internal {
        vm.startPrank(seller);
        multi.mint(seller, tokenId, units);
        multi.setApprovalForAll(address(mp), true);
        mp.list1155(address(multi), tokenId, units, price, exp);
        vm.stopPrank();
    }

    function _buyTotal(uint128 price) internal pure returns (uint256) {
        return uint256(price) + (uint256(price) * 150) / 10_000;
    }

    // ── ERC-721 basic flow ────────────────────────────────────────────────

    function test_listAndBuy() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));

        vm.prank(buyer);
        mp.buy{value: _buyTotal(1 ether)}(address(nft), id, seller);

        assertEq(nft.ownerOf(id), buyer);
        // Buyer paid 1 ether + 0.015 ether. Fee = 0.015 to creator, principal = 1 ether to seller.
        assertEq(creator.balance, 0.015 ether);
        assertEq(seller.balance,  1 ether);
    }

    function test_listIsFree() public {
        // Seller should not need any FLR balance to create a listing
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();

        (address sellerAddr,,,,) = mp.listings(address(nft), id, seller);
        assertEq(sellerAddr, seller);
    }

    function test_listBelowMinPriceReverts() public {
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        vm.expectRevert(BelowMinPrice.selector);
        mp.list(address(nft), id, 0.001 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();
    }

    function test_cancel() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.prank(seller);
        mp.cancel(address(nft), id);
        vm.prank(buyer);
        vm.expectRevert(NotListed.selector);
        mp.buy{value: _buyTotal(1 ether)}(address(nft), id, seller);
    }

    function test_cancelByNonSellerReverts() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.prank(other);
        vm.expectRevert(NotListed.selector);
        mp.cancel(address(nft), id);
    }

    function test_wrongPriceReverts() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.prank(buyer);
        vm.expectRevert(WrongPrice.selector);
        // Sending only the price without the 1.5% premium must revert
        mp.buy{value: 1 ether}(address(nft), id, seller);
    }

    function test_buyWithExtraValueReverts() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.prank(buyer);
        vm.expectRevert(WrongPrice.selector);
        mp.buy{value: _buyTotal(1 ether) + 1}(address(nft), id, seller);
    }

    function test_expiredReverts() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.warp(block.timestamp + 2 days);
        vm.prank(buyer);
        vm.expectRevert(Expired.selector);
        mp.buy{value: _buyTotal(1 ether)}(address(nft), id, seller);
    }

    function test_listExpiryInPastReverts() public {
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        vm.expectRevert(InvalidExpiry.selector);
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp));
        vm.stopPrank();
    }

    function test_buyWithStaleSellerReverts() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));

        // Seller transfers the NFT out — listing is now stale.
        vm.prank(seller);
        nft.transferFrom(seller, other, id);

        vm.prank(buyer);
        // ERC721 transferFrom from non-owner reverts inside _transferToken
        vm.expectRevert();
        mp.buy{value: _buyTotal(1 ether)}(address(nft), id, seller);
    }

    function test_relistingBySameSellerOverwrites() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));

        vm.prank(seller);
        mp.list(address(nft), id, 2 ether, uint64(block.timestamp + 2 days));

        (,,,uint128 price,) = mp.listings(address(nft), id, seller);
        assertEq(price, 2 ether);
    }

    // ── ERC-1155: per-holder stacked listings ─────────────────────────────

    function test_list1155PerHolderStacks() public {
        // Two different sellers can list the same 1155 token concurrently
        address sellerB = address(0xBABE);

        _list1155(7, 5, 1 ether, uint64(block.timestamp + 1 days));

        vm.startPrank(sellerB);
        multi.mint(sellerB, 7, 3);
        multi.setApprovalForAll(address(mp), true);
        mp.list1155(address(multi), 7, 3, 2 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();

        (address sA,,,,) = mp.listings(address(multi), 7, seller);
        (address sB,,,,) = mp.listings(address(multi), 7, sellerB);
        assertEq(sA, seller);
        assertEq(sB, sellerB);
    }

    function test_buy1155ByHolder() public {
        _list1155(7, 5, 1 ether, uint64(block.timestamp + 1 days));

        vm.prank(buyer);
        mp.buy{value: _buyTotal(1 ether)}(address(multi), 7, seller);

        assertEq(multi.balanceOf(buyer, 7), 5);
        assertEq(seller.balance, 1 ether);
        assertEq(creator.balance, 0.015 ether);
    }

    // ── Batch list (free) ─────────────────────────────────────────────────

    function test_batchListNoValueRequired() public {
        vm.startPrank(seller);
        uint256 id1 = nft.mint(seller);
        uint256 id2 = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);

        Marketplace.BatchItem[] memory items = new Marketplace.BatchItem[](2);
        items[0] = Marketplace.BatchItem({coll: address(nft), id: id1, price: 1 ether, expiresAt: uint64(block.timestamp + 1 days)});
        items[1] = Marketplace.BatchItem({coll: address(nft), id: id2, price: 2 ether, expiresAt: uint64(block.timestamp + 1 days)});
        mp.batchList(items);
        vm.stopPrank();

        (address sA,,,,) = mp.listings(address(nft), id1, seller);
        (address sB,,,,) = mp.listings(address(nft), id2, seller);
        assertEq(sA, seller);
        assertEq(sB, seller);
    }

    function test_batchListEmptyReverts() public {
        Marketplace.BatchItem[] memory items = new Marketplace.BatchItem[](0);
        vm.expectRevert(BatchTooLarge.selector);
        mp.batchList(items);
    }

    // ── Fee invariant ──────────────────────────────────────────────────────

    function testFuzz_buyFeeExact(uint128 price) public {
        price = uint128(bound(price, 0.01 ether, 50 ether));
        vm.deal(buyer, _buyTotal(price) + 1 ether);

        uint256 id = _list(price, uint64(block.timestamp + 1 days));

        uint256 sellerBefore  = seller.balance;
        uint256 creatorBefore = creator.balance;

        vm.prank(buyer);
        mp.buy{value: _buyTotal(price)}(address(nft), id, seller);

        uint256 expectedFee = (uint256(price) * 150) / 10_000;
        assertEq(seller.balance  - sellerBefore,  price);
        assertEq(creator.balance - creatorBefore, expectedFee);
    }
}
