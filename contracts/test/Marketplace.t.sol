// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}         from "forge-std/Test.sol";
import {Marketplace, BatchTooLarge}  from "../src/Marketplace.sol";
import {MockERC721}   from "./MockERC721.sol";
import {MockERC1155}  from "./MockERC1155.sol";
import {MockERC2981}  from "./MockERC2981.sol";

contract MarketplaceTest is Test {
    Marketplace  mp;
    MockERC721   nft;
    MockERC1155  multi;
    address admin   = address(this);
    address creator = address(0xC0DE);
    address seller  = address(0xBEEF);
    address buyer   = address(0xCAFE);
    address other   = address(0xDEAD);

    function setUp() public {
        mp    = new Marketplace(creator, admin);
        nft   = new MockERC721();
        multi = new MockERC1155();
        vm.deal(buyer, 10 ether);
    }

    // ── Helpers ───────────────────────────────────────────────────────────

    function _list(uint256 price, uint64 exp) internal returns (uint256 id) {
        uint256 fee = (price * 150) / 10_000;
        vm.deal(seller, seller.balance + fee);
        vm.startPrank(seller);
        id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list{value: fee}(address(nft), id, uint128(price), exp);
        vm.stopPrank();
    }

    function _list1155(uint256 tokenId, uint128 units, uint128 price, uint64 exp) internal {
        uint256 fee = (uint256(price) * 150) / 10_000;
        vm.deal(seller, seller.balance + fee);
        vm.startPrank(seller);
        multi.mint(seller, tokenId, units);
        multi.setApprovalForAll(address(mp), true);
        mp.list1155{value: fee}(address(multi), tokenId, units, price, exp);
        vm.stopPrank();
    }

    // ── ERC-721 basic flow ────────────────────────────────────────────────

    function test_listAndBuy() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));

        vm.prank(buyer);
        mp.buy{value: 1 ether}(address(nft), id);

        assertEq(nft.ownerOf(id), buyer);
        // 150 bps listing fee + 150 bps sale fee of 1 ether = 0.03 ether total to creator
        assertEq(creator.balance, 0.03 ether);
        // seller receives 1 ether minus 150 bps = 0.985 ether
        assertEq(seller.balance,  0.985 ether);
    }

    function test_cancel() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.prank(seller);
        mp.cancel(address(nft), id);
        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: 1 ether}(address(nft), id);
    }

    function test_cancelByNonSellerReverts() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.prank(other);
        vm.expectRevert();
        mp.cancel(address(nft), id);
    }

    function test_wrongPriceReverts() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: 0.5 ether}(address(nft), id);
    }

    function test_expiredReverts() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.warp(block.timestamp + 2 days);
        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: 1 ether}(address(nft), id);
    }

    function test_listExpiryInPastReverts() public {
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        vm.expectRevert();
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp));
        vm.stopPrank();
    }

    function test_completedSaleIsFinal() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.prank(buyer);
        mp.buy{value: 1 ether}(address(nft), id);

        vm.prank(seller);
        vm.expectRevert();
        mp.cancel(address(nft), id);

        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: 1 ether}(address(nft), id);

        assertEq(nft.ownerOf(id), buyer);
    }

    // ── ERC-1155 ──────────────────────────────────────────────────────────

    function test_list1155AndBuy() public {
        _list1155(1, 5, 2 ether, uint64(block.timestamp + 1 days));

        vm.prank(buyer);
        mp.buy{value: 2 ether}(address(multi), 1);

        assertEq(multi.balanceOf(buyer,  1), 5);
        assertEq(multi.balanceOf(seller, 1), 0);
        // 150 bps listing fee + 150 bps sale fee of 2 ether = 0.06 ether total to creator
        assertEq(creator.balance, 0.06 ether);
        // seller receives 2 ether minus 150 bps = 1.97 ether
        assertEq(seller.balance,  1.97 ether);
    }

    function test_list1155ZeroAmountReverts() public {
        vm.startPrank(seller);
        multi.mint(seller, 2, 10);
        multi.setApprovalForAll(address(mp), true);
        vm.expectRevert();
        mp.list1155(address(multi), 2, 0, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();
    }

    function test_list1155InsufficientBalanceReverts() public {
        vm.startPrank(seller);
        multi.mint(seller, 3, 2);
        multi.setApprovalForAll(address(mp), true);
        vm.expectRevert();
        mp.list1155(address(multi), 3, 5, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();
    }

    function test_list1155NotApprovedReverts() public {
        vm.startPrank(seller);
        multi.mint(seller, 4, 5);
        vm.expectRevert();
        mp.list1155(address(multi), 4, 5, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();
    }

    function test_buy1155Expired() public {
        _list1155(5, 2, 1 ether, uint64(block.timestamp + 1 hours));
        vm.warp(block.timestamp + 2 hours);
        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: 1 ether}(address(multi), 5);
    }

    function test_overwriteBySecondSellerReverts() public {
        _list1155(20, 5, 1 ether, uint64(block.timestamp + 1 days));
        vm.startPrank(other);
        multi.mint(other, 20, 10);
        multi.setApprovalForAll(address(mp), true);
        vm.expectRevert();
        mp.list1155(address(multi), 20, 10, 2 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();
    }

    function test_originalSellerCanRelist() public {
        _list1155(21, 5, 1 ether, uint64(block.timestamp + 1 days));
        uint256 fee = (2 ether * 150) / 10_000;
        vm.deal(seller, seller.balance + fee);
        vm.prank(seller);
        mp.list1155{value: fee}(address(multi), 21, 5, 2 ether, uint64(block.timestamp + 1 days));
        (address s,,,,) = mp.listings(address(multi), 21);
        assertEq(s, seller);
    }

    function test_cancel1155() public {
        _list1155(6, 3, 1 ether, uint64(block.timestamp + 1 days));
        vm.prank(seller);
        mp.cancel(address(multi), 6);
        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: 1 ether}(address(multi), 6);
    }

    // ── Expiry boundary ───────────────────────────────────────────────────

    function test_listExpiryBeyondMaxReverts() public {
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        vm.expectRevert();
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp + 366 days));
        vm.stopPrank();
    }

    function test_listExpiryAtMaxOk() public {
        uint256 fee = (1 ether * 150) / 10_000;
        vm.deal(seller, fee);
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list{value: fee}(address(nft), id, 1 ether, uint64(block.timestamp + 365 days));
        vm.stopPrank();
        (address s,,,,) = mp.listings(address(nft), id);
        assertEq(s, seller);
    }

    // ── Pause ─────────────────────────────────────────────────────────────

    function test_pauseBlocksBuy() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        mp.pause();
        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: 1 ether}(address(nft), id);
    }

    function test_pauseBlocksList() public {
        mp.pause();
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        vm.expectRevert();
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();
    }

    function test_cancelWorksWhilePaused() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        mp.pause();
        vm.prank(seller); // seller can still cancel when paused
        mp.cancel(address(nft), id);
    }

    function test_unpauseRestoresBuy() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        mp.pause();
        mp.unpause();
        vm.prank(buyer);
        mp.buy{value: 1 ether}(address(nft), id);
        assertEq(nft.ownerOf(id), buyer);
    }

    // ── Invariant ─────────────────────────────────────────────────────────

    function invariant_marketplaceBalanceZero() public view {
        assertEq(address(mp).balance, 0);
    }

    // ── Fuzz tests ────────────────────────────────────────────────────────

    function testFuzz_feesPlusPayoutEqPrice(uint128 price) public {
        price = uint128(bound(price, 0.001 ether, 100 ether));

        Marketplace freshMp = new Marketplace(creator, admin);

        address freshSeller = address(0xF001);
        address freshBuyer  = address(0xF002);
        uint256 listingFee  = (uint256(price) * 150) / 10_000;
        vm.deal(freshSeller, listingFee);
        vm.deal(freshBuyer, uint256(price));

        vm.startPrank(freshSeller);
        uint256 tid = nft.mint(freshSeller);
        nft.setApprovalForAll(address(freshMp), true);
        freshMp.list{value: listingFee}(address(nft), tid, price, uint64(block.timestamp + 1 days));
        vm.stopPrank();

        uint256 creatorBefore = creator.balance;
        uint256 sellerBefore  = freshSeller.balance;

        vm.prank(freshBuyer);
        freshMp.buy{value: uint256(price)}(address(nft), tid);

        uint256 fees   = creator.balance    - creatorBefore;
        uint256 payout = freshSeller.balance - sellerBefore;

        assertLe(fees, uint256(price));
        assertEq(fees + payout, uint256(price));
    }

    function test_relistAfterSale() public {
        address buyer2 = address(0xDEAD2);
        vm.deal(buyer2, 2 ether);

        // First sale: seller → buyer
        uint256 id = _list(1 ether, uint64(block.timestamp + 1 days));
        vm.prank(buyer);
        mp.buy{value: 1 ether}(address(nft), id);
        assertEq(nft.ownerOf(id), buyer);

        // buyer re-lists the token
        uint256 relistFee = (1.5 ether * 150) / 10_000;
        vm.deal(buyer, buyer.balance + relistFee);
        vm.startPrank(buyer);
        nft.setApprovalForAll(address(mp), true);
        mp.list{value: relistFee}(address(nft), id, 1.5 ether, uint64(block.timestamp + 1 days));
        vm.stopPrank();

        // buyer2 buys from buyer
        vm.prank(buyer2);
        mp.buy{value: 1.5 ether}(address(nft), id);
        assertEq(nft.ownerOf(id), buyer2);
        assertGt(buyer.balance, 0);
    }
}

contract BatchListTest is MarketplaceTest {
    function test_batchList_listsAllTokens() public {
        vm.startPrank(seller);
        nft.setApprovalForAll(address(mp), true);
        uint256 t1 = nft.mint(seller);
        uint256 t2 = nft.mint(seller);
        uint256 t3 = nft.mint(seller);
        uint64 exp = uint64(block.timestamp + 7 days);

        Marketplace.BatchItem[] memory items = new Marketplace.BatchItem[](3);
        items[0] = Marketplace.BatchItem({coll: address(nft), id: t1, price: 1 ether, expiresAt: exp});
        items[1] = Marketplace.BatchItem({coll: address(nft), id: t2, price: 2 ether, expiresAt: exp});
        items[2] = Marketplace.BatchItem({coll: address(nft), id: t3, price: 3 ether, expiresAt: exp});

        uint256 totalFee = ((1 ether + 2 ether + 3 ether) * 150) / 10_000;
        vm.deal(seller, seller.balance + totalFee);
        mp.batchList{value: totalFee}(items);
        vm.stopPrank();

        (address s1,,,uint128 p1,) = mp.listings(address(nft), t1);
        (address s2,,,uint128 p2,) = mp.listings(address(nft), t2);
        (address s3,,,uint128 p3,) = mp.listings(address(nft), t3);
        assertEq(s1, seller); assertEq(p1, 1 ether);
        assertEq(s2, seller); assertEq(p2, 2 ether);
        assertEq(s3, seller); assertEq(p3, 3 ether);
    }

    function test_batchList_revertsOnEmpty() public {
        vm.prank(seller);
        Marketplace.BatchItem[] memory items = new Marketplace.BatchItem[](0);
        vm.expectRevert(BatchTooLarge.selector);
        mp.batchList(items);
    }

    function test_batchList_revertsOver50() public {
        vm.startPrank(seller);
        nft.setApprovalForAll(address(mp), true);
        Marketplace.BatchItem[] memory items = new Marketplace.BatchItem[](51);
        for (uint256 i; i < 51; i++) {
            uint256 tid = nft.mint(seller);
            items[i] = Marketplace.BatchItem({coll: address(nft), id: tid, price: 1 ether, expiresAt: uint64(block.timestamp + 7 days)});
        }
        vm.expectRevert(BatchTooLarge.selector);
        mp.batchList(items);
        vm.stopPrank();
    }
}
