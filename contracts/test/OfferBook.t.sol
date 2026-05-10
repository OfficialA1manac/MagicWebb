// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Test} from "forge-std/Test.sol";
import {OfferBook} from "../src/OfferBook.sol";
import {MockERC721} from "./MockERC721.sol";

contract OfferBookTest is Test {
    OfferBook  ob;
    MockERC721 nft;
    address admin    = address(0xA11CE);
    address creator  = address(0xC0DE);
    uint256 bidderPk = 0xB1DD3R;
    address bidder;
    address seller   = address(0xBEEF);

    function setUp() public {
        bidder = vm.addr(bidderPk);
        ob  = new OfferBook(admin, creator, 250);
        nft = new MockERC721();
        vm.deal(bidder, 10 ether);
    }

    function _sign(OfferBook.Offer memory o) internal view returns (bytes memory) {
        bytes32 digest = ob.hashOffer(o);
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(bidderPk, digest);
        return abi.encodePacked(r, s, v);
    }

    function _mintAndDeposit(uint256 amount) internal returns (uint256 tid) {
        vm.prank(seller);
        tid = nft.mint(seller);
        vm.prank(bidder);
        ob.deposit{value: amount}();
    }

    function test_acceptOffer() public {
        uint256 tid = _mintAndDeposit(1 ether);

        OfferBook.Offer memory o = OfferBook.Offer({
            bidder: bidder, collection: address(nft), tokenId: tid,
            amount: 1 ether, expiresAt: uint64(block.timestamp + 1 days), nonce: 1
        });
        bytes memory sig = _sign(o);

        vm.startPrank(seller);
        nft.setApprovalForAll(address(ob), true);
        ob.acceptOffer(o, sig, tid);
        vm.stopPrank();

        assertEq(nft.ownerOf(tid), bidder);
        assertEq(creator.balance, 0.025 ether);
        assertEq(seller.balance,  0.975 ether);
        assertTrue(ob.usedNonce(bidder, 1));
    }

    function test_collectionWideOffer() public {
        uint256 tid = _mintAndDeposit(1 ether);

        OfferBook.Offer memory o = OfferBook.Offer({
            bidder: bidder, collection: address(nft), tokenId: 0,
            amount: 1 ether, expiresAt: uint64(block.timestamp + 1 days), nonce: 2
        });
        bytes memory sig = _sign(o);

        vm.startPrank(seller);
        nft.setApprovalForAll(address(ob), true);
        ob.acceptOffer(o, sig, tid);
        vm.stopPrank();

        assertEq(nft.ownerOf(tid), bidder);
    }

    function test_expiredReverts() public {
        _mintAndDeposit(1 ether);

        OfferBook.Offer memory o = OfferBook.Offer({
            bidder: bidder, collection: address(nft), tokenId: 1,
            amount: 1 ether, expiresAt: uint64(block.timestamp + 1), nonce: 3
        });
        bytes memory sig = _sign(o);
        vm.warp(block.timestamp + 1 days);

        vm.prank(seller);
        vm.expectRevert();
        ob.acceptOffer(o, sig, 1);
    }

    function test_wrongTokenReverts() public {
        uint256 tid = _mintAndDeposit(1 ether);

        OfferBook.Offer memory o = OfferBook.Offer({
            bidder: bidder, collection: address(nft), tokenId: tid + 99,
            amount: 1 ether, expiresAt: uint64(block.timestamp + 1 days), nonce: 4
        });
        bytes memory sig = _sign(o);

        vm.startPrank(seller);
        nft.setApprovalForAll(address(ob), true);
        vm.expectRevert();
        ob.acceptOffer(o, sig, tid);
        vm.stopPrank();
    }

    function test_cancelledNonceCannotAccept() public {
        uint256 tid = _mintAndDeposit(1 ether);

        OfferBook.Offer memory o = OfferBook.Offer({
            bidder: bidder, collection: address(nft), tokenId: tid,
            amount: 1 ether, expiresAt: uint64(block.timestamp + 1 days), nonce: 5
        });
        bytes memory sig = _sign(o);

        vm.prank(bidder);
        ob.cancelOffer(5);

        vm.startPrank(seller);
        nft.setApprovalForAll(address(ob), true);
        vm.expectRevert();
        ob.acceptOffer(o, sig, tid);
        vm.stopPrank();
    }

    function test_depositWithdrawRoundTrip() public {
        vm.prank(bidder);
        ob.deposit{value: 2 ether}();
        assertEq(ob.deposits(bidder), 2 ether);

        vm.prank(bidder);
        ob.withdraw(0.5 ether);
        assertEq(ob.deposits(bidder), 1.5 ether);
        assertEq(bidder.balance, 8.5 ether);
    }

    function test_withdrawOverBalanceReverts() public {
        vm.startPrank(bidder);
        ob.deposit{value: 1 ether}();
        vm.expectRevert();
        ob.withdraw(2 ether);
        vm.stopPrank();
    }
}
