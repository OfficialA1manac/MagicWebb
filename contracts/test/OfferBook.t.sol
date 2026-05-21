// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}        from "forge-std/Test.sol";
import {OfferBook, OfferExpired, OfferUsed, InvalidSig, NotOwner, InsufficientFunds} from "../src/OfferBook.sol";
import {MockERC721}  from "./MockERC721.sol";
import {MockERC1155} from "./MockERC1155.sol";

contract OfferBookTest is Test {
    OfferBook  ob;
    MockERC721  nft;
    MockERC1155 multi;

    address admin    = address(this);
    address feeVault = address(0x1111000000000000000000000000000000111100);
    uint16  feeBps   = 250;
    address seller   = address(0xBEEF);

    uint256 bidderKey;
    address bidder;

    function setUp() public {
        // use a valid private key (must be in [1, secp256k1 order))
        bidderKey = 0xA11CEBEEF;
        bidder    = vm.addr(bidderKey);

        ob    = new OfferBook(feeVault, feeBps, admin);
        nft   = new MockERC721();
        multi = new MockERC1155();

        vm.deal(bidder, 100 ether);
        vm.deal(seller, 1 ether);
    }

    // ── Helpers ───────────────────────────────────────────────────────────

    function _sign(OfferBook.Offer memory o) internal view returns (bytes memory) {
        bytes32 digest = ob.hashOffer(o);
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(bidderKey, digest);
        return abi.encodePacked(r, s, v);
    }

    function _sign1155(OfferBook.Offer1155 memory o) internal view returns (bytes memory) {
        bytes32 digest = ob.hashOffer1155(o);
        (uint8 v, bytes32 r, bytes32 s) = vm.sign(bidderKey, digest);
        return abi.encodePacked(r, s, v);
    }

    function _makeOffer(uint256 tokenId, uint128 amount, uint64 expiresAt, uint64 nonce)
        internal view returns (OfferBook.Offer memory)
    {
        return OfferBook.Offer({
            bidder:     bidder,
            collection: address(nft),
            tokenId:    tokenId,
            amount:     amount,
            expiresAt:  expiresAt,
            nonce:      nonce
        });
    }

    function _mintAndApprove(address to) internal returns (uint256 tid) {
        vm.startPrank(to);
        tid = nft.mint(to);
        nft.setApprovalForAll(address(ob), true);
        vm.stopPrank();
    }

    // ── Deposit / Withdraw ────────────────────────────────────────────────

    function test_depositAndWithdraw() public {
        vm.prank(bidder);
        ob.deposit{value: 5 ether}();
        assertEq(ob.deposits(bidder), 5 ether);

        vm.prank(bidder);
        ob.withdraw(3 ether);
        assertEq(ob.deposits(bidder), 2 ether);
    }

    function test_withdrawExceedsBalanceReverts() public {
        vm.prank(bidder);
        ob.deposit{value: 1 ether}();

        vm.expectRevert();
        vm.prank(bidder);
        ob.withdraw(2 ether);
    }

    // ── Accept offer ──────────────────────────────────────────────────────

    function test_acceptOfferSuccess() public {
        uint256 tid = _mintAndApprove(seller);

        vm.prank(bidder);
        ob.deposit{value: 1 ether}();

        OfferBook.Offer memory o = _makeOffer(tid, 1 ether, uint64(block.timestamp + 1 days), 1);
        bytes memory sig = _sign(o);

        uint256 sellerBefore = seller.balance;

        vm.prank(seller);
        ob.acceptOffer(o, sig, tid);

        // NFT transferred to bidder
        assertEq(nft.ownerOf(tid), bidder);
        // Seller received payment minus fees
        assertGt(seller.balance, sellerBefore);
        // Bidder deposit reduced
        assertLt(ob.deposits(bidder), 1 ether);
        // Nonce consumed
        assertTrue(ob.usedNonce(bidder, 1));
    }

    function test_expiredOfferReverts() public {
        uint256 tid = _mintAndApprove(seller);

        vm.prank(bidder);
        ob.deposit{value: 1 ether}();

        OfferBook.Offer memory o = _makeOffer(tid, 1 ether, uint64(block.timestamp - 1), 2);
        bytes memory sig = _sign(o);

        vm.expectRevert(OfferExpired.selector);
        vm.prank(seller);
        ob.acceptOffer(o, sig, tid);
    }

    function test_reusedNonceReverts() public {
        uint256 tid = _mintAndApprove(seller);

        vm.prank(bidder);
        ob.deposit{value: 2 ether}();

        OfferBook.Offer memory o = _makeOffer(tid, 1 ether, uint64(block.timestamp + 1 days), 3);
        bytes memory sig = _sign(o);

        vm.prank(seller);
        ob.acceptOffer(o, sig, tid);

        // Mint a second token and try to replay same nonce
        uint256 tid2 = _mintAndApprove(seller);
        vm.prank(bidder);
        ob.deposit{value: 1 ether}();

        OfferBook.Offer memory o2 = _makeOffer(tid2, 1 ether, uint64(block.timestamp + 1 days), 3);
        bytes memory sig2 = _sign(o2);

        vm.expectRevert(OfferUsed.selector);
        vm.prank(seller);
        ob.acceptOffer(o2, sig2, tid2);
    }

    function test_cancelOfferPreemptsNonce() public {
        uint256 tid = _mintAndApprove(seller);

        vm.prank(bidder);
        ob.deposit{value: 1 ether}();

        vm.prank(bidder);
        ob.cancelOffer(5);

        OfferBook.Offer memory o = _makeOffer(tid, 1 ether, uint64(block.timestamp + 1 days), 5);
        bytes memory sig = _sign(o);

        vm.expectRevert(OfferUsed.selector);
        vm.prank(seller);
        ob.acceptOffer(o, sig, tid);
    }

    function test_collectionWideOfferAccepted() public {
        uint256 tid = _mintAndApprove(seller);

        vm.prank(bidder);
        ob.deposit{value: 1 ether}();

        // tokenId == 0 means collection-wide
        OfferBook.Offer memory o = _makeOffer(0, 1 ether, uint64(block.timestamp + 1 days), 6);
        bytes memory sig = _sign(o);

        vm.prank(seller);
        ob.acceptOffer(o, sig, tid);  // tid is the actual token delivered

        assertEq(nft.ownerOf(tid), bidder);
    }

    function test_invalidSigReverts() public {
        uint256 tid = _mintAndApprove(seller);

        vm.prank(bidder);
        ob.deposit{value: 1 ether}();

        OfferBook.Offer memory o = _makeOffer(tid, 1 ether, uint64(block.timestamp + 1 days), 7);
        bytes memory sig = bytes("bad_sig_padding_to_65_bytes_______________________xxxxxxxxxxxxxxx");

        vm.expectRevert();
        vm.prank(seller);
        ob.acceptOffer(o, sig, tid);
    }

    // ── ERC-1155 offer ────────────────────────────────────────────────────

    function test_acceptOffer1155Success() public {
        vm.prank(bidder);
        ob.deposit{value: 2 ether}();

        vm.startPrank(seller);
        multi.mint(seller, 42, 10);
        multi.setApprovalForAll(address(ob), true);
        vm.stopPrank();

        OfferBook.Offer1155 memory o = OfferBook.Offer1155({
            bidder:     bidder,
            collection: address(multi),
            tokenId:    42,
            units:      5,
            amount:     2 ether,
            expiresAt:  uint64(block.timestamp + 1 days),
            nonce:      20
        });
        bytes memory sig = _sign1155(o);

        vm.prank(seller);
        ob.acceptOffer1155(o, sig);

        assertEq(multi.balanceOf(bidder, 42), 5);
        assertTrue(ob.usedNonce(bidder, 20));
        assertLt(ob.deposits(bidder), 2 ether);
    }

    function test_collectionWideOfferRejectedForWrongCollection() public {
        MockERC721 nft2 = new MockERC721();

        vm.prank(bidder);
        ob.deposit{value: 1 ether}();

        // Collection-wide offer for address(nft)
        OfferBook.Offer memory o = _makeOffer(0, 1 ether, uint64(block.timestamp + 1 days), 30);
        bytes memory sig = _sign(o);

        // Seller owns token from nft2, not nft
        vm.startPrank(seller);
        uint256 tid2 = nft2.mint(seller);
        nft2.setApprovalForAll(address(ob), true);
        vm.stopPrank();

        vm.expectRevert();
        vm.prank(seller);
        ob.acceptOffer(o, sig, tid2);
    }

    // ── Fuzz tests ────────────────────────────────────────────────────────

    /// @dev For any valid amount, deposit + full withdraw must restore original balance (minus gas).
    function testFuzz_depositWithdrawInvariant(uint128 amount) public {
        amount = uint128(bound(amount, 1, 50 ether));
        vm.deal(bidder, amount + 1 ether);

        vm.prank(bidder);
        ob.deposit{value: amount}();
        assertEq(ob.deposits(bidder), amount);

        vm.prank(bidder);
        ob.withdraw(amount);
        assertEq(ob.deposits(bidder), 0);
    }

    /// @dev Fee paid on any accepted offer must never exceed feeBps% of sale price.
    function testFuzz_feeNeverExceedsCap(uint128 amount) public {
        amount = uint128(bound(amount, 0.001 ether, 100 ether));

        uint256 tid = _mintAndApprove(seller);

        vm.deal(bidder, uint256(amount) + 1 ether);
        vm.prank(bidder);
        ob.deposit{value: amount}();

        OfferBook.Offer memory o = _makeOffer(tid, amount, uint64(block.timestamp + 1 days), 8);
        bytes memory sig = _sign(o);

        uint256 sellerBefore  = seller.balance;
        uint256 vaultBefore   = feeVault.balance;

        vm.prank(seller);
        ob.acceptOffer(o, sig, tid);

        uint256 feeActual = feeVault.balance - vaultBefore;
        uint256 feeCap    = (uint256(amount) * feeBps) / 10_000;
        assertLe(feeActual, feeCap);

        // Seller + vault received at most `amount` total (no value creation)
        uint256 totalOut = (seller.balance - sellerBefore) + feeActual;
        assertLe(totalOut, amount);
    }
}
