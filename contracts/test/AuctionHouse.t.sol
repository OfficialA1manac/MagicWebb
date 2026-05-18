// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {MockERC721} from "./MockERC721.sol";

contract AuctionHouseTest is Test {
    AuctionHouse ah;
    MockERC721 nft;
    address feeVault = address(0x1111000000000000000000000000000000111100);
    uint16 feeBps = 250;
    address seller = address(0xBEEF);
    address alice = address(0xA11CE);
    address bob = address(0xB0B);

    function setUp() public {
        ah = new AuctionHouse(feeVault, feeBps);
        nft = new MockERC721();
        vm.deal(alice, 100 ether);
        vm.deal(bob, 100 ether);
    }

    function _createAuction() internal returns (uint256 id, uint256 tid) {
        vm.startPrank(seller);
        tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint64 start = uint64(block.timestamp);
        uint64 end = uint64(block.timestamp + 7 days);
        id = ah.create(address(nft), tid, 1 ether, start, end, 500);
        vm.stopPrank();
    }

    function test_outbidLoserGetsFullRefundViaPendingReturns() public {
        (uint256 id,) = _createAuction();

        vm.prank(alice);
        ah.bid{value: 1 ether}(id);

        vm.prank(bob);
        ah.bid{value: 2 ether}(id);

        assertEq(ah.pendingReturns(alice), 1 ether);
        assertEq(address(ah).balance, 3 ether);

        uint256 balBefore = alice.balance;
        vm.prank(alice);
        ah.withdrawRefund();
        assertEq(alice.balance, balBefore + 1 ether);
        assertEq(ah.pendingReturns(alice), 0);
    }

    function test_leaderCompoundsBidWithIncrementOnly() public {
        (uint256 id,) = _createAuction();

        vm.prank(alice);
        ah.bid{value: 1 ether}(id);

        // Raise own bid by 0.6 ether → 1.6 total (min increment 500 bps of 1 ether = 0.05 ether)
        vm.prank(alice);
        ah.bid{value: 0.6 ether}(id);

        (
            ,   // seller
            ,   // startsAt
            ,   // minIncrementBps
            ,   // settled
            ,   // standard
            ,   // collection
            ,   // endsAt
            ,   // originalEndsAt
            ,   // tokenId
            ,   // reserve
            uint128 hi, // highestBid
            ,   // highestBidder
        ) = ah.auctions(id);
        assertEq(hi, 1.6 ether);
        assertEq(ah.pendingReturns(alice), 0);
        assertEq(address(ah).balance, 1.6 ether);
    }

    function test_feeOnlyOnSettleAfterTransfer() public {
        (uint256 id, uint256 tid) = _createAuction();

        vm.prank(bob);
        ah.bid{value: 2 ether}(id);

        vm.warp(block.timestamp + 8 days);

        uint256 vaultBefore = feeVault.balance;
        uint256 sellerBefore = seller.balance;

        uint256 fee = (uint256(2 ether) * uint256(feeBps)) / 10_000;
        uint256 sellerPayout = 2 ether - fee;

        ah.settle(id);

        assertEq(nft.ownerOf(tid), bob);
        assertEq(feeVault.balance, vaultBefore + fee);
        assertEq(seller.balance, sellerBefore + sellerPayout);
    }

    // Anti-snipe: bid within 5-min window extends endsAt
    function test_antiSnipeExtends() public {
        (uint256 id,) = _createAuction();

        vm.warp(block.timestamp + 7 days - 2 minutes);

        vm.prank(alice);
        ah.bid{value: 1 ether}(id);

        (,,,,,, uint64 endsAt,,,,,,) = ah.auctions(id);
        assertGt(endsAt, uint64(block.timestamp + 7 days - 2 minutes));
    }

    // Anti-snipe cap: extension is clamped to originalEndsAt + ANTI_SNIPE_MAX_EXTENSION.
    // Uses vm.store to force auction's endsAt near the cap boundary, then verifies one bid
    // produces exactly cap (not cap + ANTI_SNIPE_WINDOW).
    function test_antiSnipeCapRespected() public {
        (uint256 id,) = _createAuction();

        // Place initial bid so highestBidder/highestBid are set
        vm.prank(alice);
        ah.bid{value: 1 ether}(id);

        // ---- Manipulate endsAt via vm.store ----
        // auctions mapping is at storage slot 1 (nextAuctionId is slot 0).
        // Struct slot 1: collection(160 bits) | endsAt(64 bits) | originalEndsAt(32 bits)
        bytes32 base = keccak256(abi.encode(uint256(id), uint256(1)));
        bytes32 slot1 = bytes32(uint256(base) + 1);
        bytes32 word  = vm.load(address(ah), slot1);

        uint160 coll    = uint160(uint256(word));              // bits 0-159
        uint32  origEnd = uint32(uint256(word) >> (160 + 64)); // bits 224-255

        uint64 cap      = uint64(origEnd) + ah.ANTI_SNIPE_MAX_EXTENSION();
        // Set endsAt 100 seconds before cap
        uint64 newEndsAt = cap - 100;

        bytes32 newWord = bytes32(
            uint256(coll) |
            (uint256(newEndsAt) << 160) |
            (uint256(origEnd)   << 224)
        );
        vm.store(address(ah), slot1, newWord);

        // Warp to 1 second before manipulated endsAt (inside snipe window)
        // proposed newEnd = (cap-101) + ANTI_SNIPE_WINDOW = cap - 101 + 300 = cap + 199 > cap → clamp
        vm.warp(uint256(newEndsAt) - 1);

        // Alice raises by 6% (above 5% min increment) as a compound raise
        vm.prank(alice);
        ah.bid{value: 0.06 ether}(id);

        (,,,,,, uint64 finalEnd,,,,,,) = ah.auctions(id);
        // Must be clamped to cap, not cap+199
        assertEq(finalEnd, cap);
    }

    // reclaimBid: winner recovers ETH after settle deadline if seller can't receive
    function test_reclaimBidAfterDeadline() public {
        (uint256 id,) = _createAuction();

        vm.prank(bob);
        ah.bid{value: 2 ether}(id);

        vm.warp(block.timestamp + 7 days + ah.SETTLE_DEADLINE() + 1);

        uint256 balBefore = bob.balance;
        vm.prank(bob);
        ah.reclaimBid(id);

        assertEq(bob.balance, balBefore + 2 ether);
    }

    // reclaimBid: reverts before settle deadline
    function test_reclaimBidTooEarlyReverts() public {
        (uint256 id,) = _createAuction();

        vm.prank(bob);
        ah.bid{value: 2 ether}(id);

        vm.warp(block.timestamp + 7 days + 1); // past endsAt but before settle deadline

        vm.prank(bob);
        vm.expectRevert();
        ah.reclaimBid(id);
    }

    // reclaimBid: non-winner cannot call
    function test_reclaimBidNonWinnerReverts() public {
        (uint256 id,) = _createAuction();

        vm.prank(bob);
        ah.bid{value: 2 ether}(id);

        vm.warp(block.timestamp + 7 days + ah.SETTLE_DEADLINE() + 1);

        vm.prank(alice);
        vm.expectRevert();
        ah.reclaimBid(id);
    }

    // settle after reclaimBid reverts (already settled flag set)
    function test_settleAfterReclaimReverts() public {
        (uint256 id,) = _createAuction();

        vm.prank(bob);
        ah.bid{value: 2 ether}(id);

        vm.warp(block.timestamp + 7 days + ah.SETTLE_DEADLINE() + 1);

        vm.prank(bob);
        ah.reclaimBid(id);

        vm.expectRevert();
        ah.settle(id);
    }
}
