// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}         from "forge-std/Test.sol";
import {Marketplace, BatchTooLarge}  from "../src/Marketplace.sol";
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
        mp = new Marketplace();
        mp.initialize(creator, address(0));
        nft   = new MockERC721();
        multi = new MockERC1155();
        vm.deal(buyer, 10 ether);
    }

    // ── Fixed listing durations ───────────────────────────────────────────────
    
    // ── Durations
    uint64 constant _DURATION_3MIN  = 3 minutes;
    uint64 constant _DURATION_15MIN = 15 minutes;
    uint64 constant _DURATION_30MIN = 30 minutes;
    uint64 constant _DURATION_1HR   = 1 hours;
    uint64 constant _DURATION_4HR   = 4 hours;
    uint64 constant _DURATION_24HR  = 24 hours;

    // ── Helpers ───────────────────────────────────────────────────────────────

    /// @dev 1.5% platform fee, deducted from the seller's proceeds.
    function _fee(uint256 price) internal pure returns (uint256) {
        return (price * 150) / 10_000;
    }

    /// @dev Total msg.value a buyer sends: exactly `price` (no buyer-side fee).
    function _total(uint256 price) internal pure returns (uint256) {
        return price;
    }

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

    // ── ERC-721 basic flow ──────────────────────────────────────────────────────

    function test_listAndBuy() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + _DURATION_24HR));

        vm.prank(buyer);
        mp.buy{value: _total(1 ether)}(address(nft), id, seller);

        assertEq(nft.ownerOf(id), buyer);
        // Seller pays the 1.5% fee; it reaches the creator/feeRecipient.
        assertEq(creator.balance, 0.015 ether);
        // Seller receives 98.5% of the asking price.
        assertEq(seller.balance,  1 ether - 0.015 ether);
    }

    function test_buyWrongSellerReverts() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + _DURATION_24HR));
        vm.prank(buyer);
        vm.expectRevert(); // no listing under `other`
        mp.buy{value: _total(1 ether)}(address(nft), id, other);
    }

    function test_cancel() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + _DURATION_24HR));
        vm.prank(seller);
        mp.cancel(address(nft), id);
        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: _total(1 ether)}(address(nft), id, seller);
    }

    function test_cancelByNonSellerReverts() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + _DURATION_24HR));
        vm.prank(other);
        vm.expectRevert();
        mp.cancel(address(nft), id);
    }

    function test_wrongPriceReverts() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + _DURATION_24HR));
        vm.prank(buyer);
        vm.expectRevert(); // value must equal the price exactly
        mp.buy{value: 1 ether + 1}(address(nft), id, seller);
    }

    function test_belowMinPriceReverts() public {
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        vm.expectRevert();
        mp.list(address(nft), id, 0.009 ether, uint64(block.timestamp + _DURATION_24HR));
        vm.stopPrank();
    }

    function test_expiredReverts() public {
        uint256 id = _list(1 ether, uint64(block.timestamp + _DURATION_1HR));
        vm.warp(block.timestamp + 2 hours);
        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: _total(1 ether)}(address(nft), id, seller);
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
        uint256 id = _list(1 ether, uint64(block.timestamp + _DURATION_24HR));
        vm.prank(buyer);
        mp.buy{value: _total(1 ether)}(address(nft), id, seller);

        vm.prank(seller);
        vm.expectRevert();
        mp.cancel(address(nft), id);

        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: _total(1 ether)}(address(nft), id, seller);

        assertEq(nft.ownerOf(id), buyer);
    }

    // ── ERC-1155 ────────────────────────────────────────────────────────────────

    function test_list1155AndBuy() public {
        _list1155(1, 5, 2 ether, uint64(block.timestamp + _DURATION_24HR));

        vm.prank(buyer);
        mp.buy{value: _total(2 ether)}(address(multi), 1, seller);

        assertEq(multi.balanceOf(buyer,  1), 5);
        assertEq(multi.balanceOf(seller, 1), 0);
        assertEq(creator.balance, 0.03 ether);           // 1.5% of 2 ether
        assertEq(seller.balance,  2 ether - 0.03 ether); // seller nets 98.5%
    }

    function test_list1155ZeroAmountReverts() public {
        vm.startPrank(seller);
        multi.mint(seller, 2, 10);
        multi.setApprovalForAll(address(mp), true);
        vm.expectRevert();
        mp.list1155(address(multi), 2, 0, 1 ether, uint64(block.timestamp + _DURATION_24HR));
        vm.stopPrank();
    }

    function test_list1155InsufficientBalanceReverts() public {
        vm.startPrank(seller);
        multi.mint(seller, 3, 2);
        multi.setApprovalForAll(address(mp), true);
        vm.expectRevert();
        mp.list1155(address(multi), 3, 5, 1 ether, uint64(block.timestamp + _DURATION_24HR));
        vm.stopPrank();
    }

    function test_list1155NotApprovedReverts() public {
        vm.startPrank(seller);
        multi.mint(seller, 4, 5);
        vm.expectRevert();
        mp.list1155(address(multi), 4, 5, 1 ether, uint64(block.timestamp + _DURATION_24HR));
        vm.stopPrank();
    }

    function test_buy1155Expired() public {
        _list1155(5, 2, 1 ether, uint64(block.timestamp + _DURATION_1HR));
        vm.warp(block.timestamp + 2 hours);
        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: _total(1 ether)}(address(multi), 5, seller);
    }

    /// @dev Per-holder stacked ERC-1155 listings: a second holder lists the SAME id
    ///      under their own key. Both listings coexist (no exclusivity).
    function test_secondHolderListsSeparately() public {
        _list1155(20, 5, 1 ether, uint64(block.timestamp + _DURATION_24HR));
        vm.startPrank(other);
        multi.mint(other, 20, 10);
        multi.setApprovalForAll(address(mp), true);
        mp.list1155(address(multi), 20, 10, 2 ether, uint64(block.timestamp + _DURATION_24HR));
        vm.stopPrank();

        (address s1,,,uint128 p1,) = mp.listings(address(multi), 20, seller);
        (address s2,,,uint128 p2,) = mp.listings(address(multi), 20, other);
        assertEq(s1, seller); assertEq(p1, 1 ether);
        assertEq(s2, other);  assertEq(p2, 2 ether);
    }

    function test_originalSellerCanRelistAfterCancel() public {
        _list1155(21, 5, 1 ether, uint64(block.timestamp + _DURATION_24HR));
        // Seller must cancel first to relist at a different price (no duplicate listings).
        vm.prank(seller);
        mp.cancel(address(multi), 21);
        vm.prank(seller);
        mp.list1155(address(multi), 21, 5, 2 ether, uint64(block.timestamp + _DURATION_24HR));
        (address s,,,uint128 p,) = mp.listings(address(multi), 21, seller);
        assertEq(s, seller);
        assertEq(p, 2 ether);
    }

    function test_cancel1155() public {
        _list1155(6, 3, 1 ether, uint64(block.timestamp + _DURATION_24HR));
        vm.prank(seller);
        mp.cancel(address(multi), 6);
        vm.prank(buyer);
        vm.expectRevert();
        mp.buy{value: _total(1 ether)}(address(multi), 6, seller);
    }

    // ── Expiry boundary (max 24 hours) ───────────────────────────────────────────

    function test_listExpiryBeyondMaxReverts() public {
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        vm.expectRevert();
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp + 25 hours));
        vm.stopPrank();
    }

    function test_listExpiryAtMaxOk() public {
        vm.startPrank(seller);
        uint256 id = nft.mint(seller);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), id, 1 ether, uint64(block.timestamp + 24 hours));
        vm.stopPrank();
        (address s,,,,) = mp.listings(address(nft), id, seller);
        assertEq(s, seller);
    }

    // ── Invariant ─────────────────────────────────────────────────────────────

    function invariant_marketplaceBalanceZero() public view {
        assertEq(address(mp).balance, 0);
    }

    // ── Fuzz ────────────────────────────────────────────────────────────────────

    function testFuzz_sellerPaysFee(uint128 price) public {
        price = uint128(bound(price, 1 ether, 100 ether));

        Marketplace freshMp = new Marketplace();
        freshMp.initialize(creator, address(0));

        address freshSeller = address(0xF001);
        address freshBuyer  = address(0xF002);
        vm.deal(freshBuyer, _total(uint256(price)));

        vm.startPrank(freshSeller);
        uint256 tid = nft.mint(freshSeller);
        nft.setApprovalForAll(address(freshMp), true);
        freshMp.list(address(nft), tid, price, uint64(block.timestamp + _DURATION_24HR));
        vm.stopPrank();

        uint256 creatorBefore = creator.balance;
        uint256 sellerBefore  = freshSeller.balance;

        vm.prank(freshBuyer);
        freshMp.buy{value: _total(uint256(price))}(address(nft), tid, freshSeller);

        uint256 fees   = creator.balance     - creatorBefore;
        uint256 payout = freshSeller.balance - sellerBefore;

        // Seller nets price − fee; platform gets exactly the 1.5% fee.
        assertEq(payout, uint256(price) - _fee(uint256(price)));
        assertEq(fees,   _fee(uint256(price)));
        assertEq(address(freshMp).balance, 0);
    }

    function test_relistAfterSale() public {
        address buyer2 = address(0xDEAD2);
        vm.deal(buyer2, 2 ether);

        // First sale: seller → buyer
        uint256 id = _list(1 ether, uint64(block.timestamp + _DURATION_24HR));
        vm.prank(buyer);
        mp.buy{value: _total(1 ether)}(address(nft), id, seller);
        assertEq(nft.ownerOf(id), buyer);

        // buyer re-lists the token (free)
        vm.startPrank(buyer);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), id, 1.5 ether, uint64(block.timestamp + _DURATION_24HR));
        vm.stopPrank();

        // buyer2 buys from buyer
        vm.prank(buyer2);
        mp.buy{value: _total(1.5 ether)}(address(nft), id, buyer);
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
        uint64 exp = uint64(block.timestamp + _DURATION_24HR);

        Marketplace.BatchItem[] memory items = new Marketplace.BatchItem[](3);
        items[0] = Marketplace.BatchItem({coll: address(nft), id: t1, price: 1 ether, expiresAt: exp});
        items[1] = Marketplace.BatchItem({coll: address(nft), id: t2, price: 2 ether, expiresAt: exp});
        items[2] = Marketplace.BatchItem({coll: address(nft), id: t3, price: 3 ether, expiresAt: exp});

        mp.batchList(items); // free — no value
        vm.stopPrank();

        (address s1,,,uint128 p1,) = mp.listings(address(nft), t1, seller);
        (address s2,,,uint128 p2,) = mp.listings(address(nft), t2, seller);
        (address s3,,,uint128 p3,) = mp.listings(address(nft), t3, seller);
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
            items[i] = Marketplace.BatchItem({coll: address(nft), id: tid, price: 1 ether, expiresAt: uint64(block.timestamp + _DURATION_24HR)});
        }
        vm.expectRevert(BatchTooLarge.selector);
        mp.batchList(items);
        vm.stopPrank();
    }
}
