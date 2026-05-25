// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}         from "forge-std/Test.sol";
import {AuctionHouse, BidTooLow} from "../src/AuctionHouse.sol";
import {MockERC721}   from "./MockERC721.sol";
import {MockERC1155}  from "./MockERC1155.sol";

contract AuctionHouseTest is Test {
    AuctionHouse ah;
    MockERC721   nft;
    MockERC1155  multi;
    address admin        = address(this);
    address feeRecipient = address(0x1111000000000000000000000000000000111100);
    address seller       = address(0xBEEF);
    address alice        = address(0xA11CE);
    address bob          = address(0xB0B);

    function setUp() public {
        ah    = new AuctionHouse(feeRecipient, admin);
        nft   = new MockERC721();
        multi = new MockERC1155();
        vm.deal(alice, 100 ether);
        vm.deal(bob,   100 ether);
    }

    // ── Helpers ───────────────────────────────────────────────────────────

    function _createAuction() internal returns (uint256 id, uint256 tid) {
        vm.startPrank(seller);
        tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint64 start = uint64(block.timestamp);
        uint64 end   = uint64(block.timestamp + 7 days);
        id = ah.create(address(nft), tid, 1 ether, start, end, 500);
        vm.stopPrank();
    }

    /// @dev Commit + advance blocks + reveal. For new bidders: msgValue == fullAmount.
    ///      For compound raises: msgValue == increment (fullAmount - prevBid).
    function _commitAndBid(
        uint256 id,
        address bidder,
        uint128 fullAmount,
        uint256 msgValue
    ) internal {
        bytes32 salt = keccak256(abi.encode(id, bidder, fullAmount, block.number));
        bytes32 c    = keccak256(abi.encode(id, bidder, fullAmount, salt));
        vm.prank(bidder);
        ah.commitBid(id, c);
        vm.roll(block.number + uint256(ah.COMMIT_DELAY_BLOCKS()) + 1);
        vm.prank(bidder);
        ah.bid{value: msgValue}(id, fullAmount, salt);
    }

    // ── Core bid flow ─────────────────────────────────────────────────────

    function test_outbidLoserGetsFullRefundViaPendingReturns() public {
        (uint256 id,) = _createAuction();

        _commitAndBid(id, alice, 1 ether, 1 ether);
        _commitAndBid(id, bob,   2 ether, 2 ether);

        assertEq(ah.pendingReturns(alice), 1 ether);
        assertEq(address(ah).balance,      3 ether);

        uint256 balBefore = alice.balance;
        vm.prank(alice);
        ah.withdrawRefund();
        assertEq(alice.balance, balBefore + 1 ether);
        assertEq(ah.pendingReturns(alice), 0);
    }

    function test_leaderCompoundsBidWithIncrementOnly() public {
        (uint256 id,) = _createAuction();

        // Initial bid
        _commitAndBid(id, alice, 1 ether, 1 ether);
        // Compound: full = 1.6 ether, increment = 0.6 ether (> 5% of 1 ether)
        _commitAndBid(id, alice, 1.6 ether, 0.6 ether);

        (,,,,,,,,, uint128 hi,,) = ah.auctions(id);
        assertEq(hi, 1.6 ether);
        assertEq(ah.pendingReturns(alice), 0);
        assertEq(address(ah).balance, 1.6 ether);
    }

    function test_feeOnlyOnSettleAfterTransfer() public {
        (uint256 id, uint256 tid) = _createAuction();

        _commitAndBid(id, bob, 2 ether, 2 ether);

        vm.warp(block.timestamp + 8 days);

        uint256 vaultBefore  = feeRecipient.balance;
        uint256 sellerBefore = seller.balance;

        uint256 fee          = (2 ether * 150) / 10_000;
        uint256 sellerPayout = 2 ether - fee;

        ah.settle(id);

        assertEq(nft.ownerOf(tid), bob);
        assertEq(feeRecipient.balance, vaultBefore  + fee);
        assertEq(seller.balance,       sellerBefore + sellerPayout);
    }

    // ── Commit-reveal MEV protection ──────────────────────────────────────

    function test_bidWithoutCommitReverts() public {
        (uint256 id,) = _createAuction();
        vm.prank(alice);
        vm.expectRevert();
        ah.bid{value: 1 ether}(id, 1 ether, bytes32(0));
    }

    function test_bidWithWrongSaltReverts() public {
        (uint256 id,) = _createAuction();
        bytes32 salt = keccak256("right_salt");
        bytes32 c    = keccak256(abi.encode(id, alice, uint128(1 ether), salt));
        vm.prank(alice);
        ah.commitBid(id, c);
        vm.roll(block.number + 3);
        vm.prank(alice);
        vm.expectRevert();
        ah.bid{value: 1 ether}(id, 1 ether, keccak256("wrong_salt"));
    }

    function test_bidTooSoonAfterCommitReverts() public {
        (uint256 id,) = _createAuction();
        uint128 amt   = 1 ether;
        bytes32 salt  = keccak256("s");
        bytes32 c     = keccak256(abi.encode(id, alice, amt, salt));
        vm.prank(alice);
        ah.commitBid(id, c);
        // Only advance 1 block (need 2 + 1 = 3)
        vm.roll(block.number + 1);
        vm.prank(alice);
        vm.expectRevert();
        ah.bid{value: uint256(amt)}(id, amt, salt);
    }

    // ── reclaimBid safety valve ───────────────────────────────────────────

    function test_reclaimBidAfterDeadline() public {
        (uint256 id,) = _createAuction();

        _commitAndBid(id, bob, 2 ether, 2 ether);

        vm.warp(block.timestamp + 7 days + ah.SETTLE_DEADLINE() + 1);

        uint256 balBefore = bob.balance;
        vm.prank(bob);
        ah.reclaimBid(id);
        assertEq(bob.balance, balBefore + 2 ether);
    }

    function test_reclaimBidTooEarlyReverts() public {
        (uint256 id,) = _createAuction();

        _commitAndBid(id, bob, 2 ether, 2 ether);

        vm.warp(block.timestamp + 7 days + 1); // past endsAt, before settle deadline
        vm.prank(bob);
        vm.expectRevert();
        ah.reclaimBid(id);
    }

    function test_reclaimBidNonWinnerReverts() public {
        (uint256 id,) = _createAuction();

        _commitAndBid(id, bob, 2 ether, 2 ether);

        vm.warp(block.timestamp + 7 days + ah.SETTLE_DEADLINE() + 1);
        vm.prank(alice);
        vm.expectRevert();
        ah.reclaimBid(id);
    }

    function test_settleAfterReclaimReverts() public {
        (uint256 id,) = _createAuction();

        _commitAndBid(id, bob, 2 ether, 2 ether);

        vm.warp(block.timestamp + 7 days + ah.SETTLE_DEADLINE() + 1);
        vm.prank(bob);
        ah.reclaimBid(id);

        vm.expectRevert();
        ah.settle(id);
    }

    // ── Pause ─────────────────────────────────────────────────────────────

    function test_pauseBlocksCreate() public {
        ah.pause();
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        vm.expectRevert();
        ah.create(address(nft), tid, 1 ether, uint64(block.timestamp), uint64(block.timestamp + 1 days), 500);
        vm.stopPrank();
    }

    function test_pauseBlocksCommit() public {
        (uint256 id,) = _createAuction();
        ah.pause();
        vm.prank(alice);
        vm.expectRevert();
        ah.commitBid(id, bytes32(uint256(1)));
    }

    function test_withdrawRefundWorksWhilePaused() public {
        (uint256 id,) = _createAuction();
        _commitAndBid(id, alice, 1 ether, 1 ether);
        _commitAndBid(id, bob,   2 ether, 2 ether);

        ah.pause();
        // Alice was outbid, should still be able to withdraw
        uint256 balBefore = alice.balance;
        vm.prank(alice);
        ah.withdrawRefund();
        assertEq(alice.balance, balBefore + 1 ether);
    }

    function test_unpauseRestoresCreate() public {
        ah.pause();
        ah.unpause();
        // Should succeed after unpause
        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint256 id = ah.create(address(nft), tid, 1 ether,
            uint64(block.timestamp), uint64(block.timestamp + 1 days), 500);
        vm.stopPrank();
        assertGt(id, 0);
    }

    // ── ERC-1155 auction ──────────────────────────────────────────────────

    function test_create1155AndSettleTransfersAmount() public {
        vm.startPrank(seller);
        multi.mint(seller, 99, 5);
        multi.setApprovalForAll(address(ah), true);
        uint64 start = uint64(block.timestamp);
        uint64 end   = uint64(block.timestamp + 7 days);
        uint256 id   = ah.create1155(address(multi), 99, 5, 1 ether, start, end, 500);
        vm.stopPrank();

        _commitAndBid(id, alice, 1 ether, 1 ether);
        vm.warp(block.timestamp + 8 days);

        uint256 vaultBefore = feeRecipient.balance;
        ah.settle(id);

        assertEq(multi.balanceOf(alice,  99), 5);
        assertEq(multi.balanceOf(seller, 99), 0);
        assertGt(feeRecipient.balance, vaultBefore);
    }

    function test_settleFailsIfApprovalRevoked() public {
        (uint256 id,) = _createAuction();
        _commitAndBid(id, alice, 1 ether, 1 ether);

        // Seller revokes approval mid-auction
        vm.prank(seller);
        nft.setApprovalForAll(address(ah), false);

        vm.warp(block.timestamp + 8 days);
        vm.expectRevert();
        ah.settle(id);

        // Alice can reclaim after SETTLE_DEADLINE
        vm.warp(block.timestamp + ah.SETTLE_DEADLINE() + 1);
        uint256 balBefore = alice.balance;
        vm.prank(alice);
        ah.reclaimBid(id);
        assertEq(alice.balance, balBefore + 1 ether);
    }

    function testFuzz_bidIncrementEnforced(uint128 reserve, uint16 incBps) public {
        reserve = uint128(bound(reserve, 0.001 ether, 10 ether));
        incBps  = uint16(bound(incBps, 100, ah.MAX_MIN_INCREMENT_BPS()));

        vm.deal(alice, reserve + 10 ether);
        vm.deal(bob,   reserve + 10 ether);

        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint256 id = ah.create(address(nft), tid, reserve,
            uint64(block.timestamp), uint64(block.timestamp + 7 days), incBps);
        vm.stopPrank();

        _commitAndBid(id, alice, reserve, reserve);

        // Bob bids same amount — below required increment — must revert
        bytes32 salt = keccak256(abi.encode(id, bob, reserve, block.number));
        bytes32 c    = keccak256(abi.encode(id, bob, reserve, salt));
        vm.prank(bob);
        ah.commitBid(id, c);
        vm.roll(block.number + uint256(ah.COMMIT_DELAY_BLOCKS()) + 1);
        vm.expectRevert(BidTooLow.selector);
        vm.prank(bob);
        ah.bid{value: reserve}(id, reserve, salt);

        // Bob bids above minimum — must succeed
        uint256 inc      = uint256(reserve) * incBps / 10_000;
        uint128 validBid = uint128(uint256(reserve) + (inc == 0 ? 1 : inc) + 1);
        _commitAndBid(id, bob, validBid, validBid);
        (,,,,,,,,, uint128 hi,,) = ah.auctions(id);
        assertEq(hi, validBid);
    }

    function test_pendingReturnsNeverExceedBalance() public {
        (uint256 id,) = _createAuction();
        _commitAndBid(id, alice, 1 ether, 1 ether);
        _commitAndBid(id, bob,   2 ether, 2 ether);

        assertLe(
            ah.pendingReturns(alice) + ah.pendingReturns(bob),
            address(ah).balance
        );
    }

    // ── Fuzz tests ────────────────────────────────────────────────────────

    /// @dev Any reserve in [0.001e18, 100e18] with valid incrBps: first bid at reserve succeeds.
    ///      Verified by confirming a tie bid (same amount) fails with BidTooLow after alice bids.
    function testFuzz_bidAtReserveAlwaysSucceeds(uint128 reserve, uint16 incrBps) public {
        reserve = uint128(bound(reserve, 0.001 ether, 100 ether));
        incrBps = uint16(bound(incrBps, 0, ah.MAX_MIN_INCREMENT_BPS()));

        vm.deal(alice, reserve + 1 ether);
        vm.deal(bob,   reserve + 1 ether);

        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint256 id = ah.create(address(nft), tid, reserve,
            uint64(block.timestamp), uint64(block.timestamp + 7 days), incrBps);
        vm.stopPrank();

        _commitAndBid(id, alice, reserve, reserve);

        // Bob cannot match alice's bid — proves alice is recorded as highest bidder
        bytes32 salt = keccak256(abi.encode(id, bob, reserve, block.number));
        bytes32 c    = keccak256(abi.encode(id, bob, reserve, salt));
        vm.prank(bob);
        ah.commitBid(id, c);
        vm.roll(block.number + uint256(ah.COMMIT_DELAY_BLOCKS()) + 1);
        vm.expectRevert(BidTooLow.selector);
        vm.prank(bob);
        ah.bid{value: reserve}(id, reserve, salt);
    }

    /// @dev Outbid bidder's pending return must equal their full bid — no dust lost.
    ///      minIncrementBps defaults to 500 (5%) when 0 is passed, so second bid must clear that.
    function testFuzz_outbidRefundExact(uint128 firstBid, uint128 secondBid) public {
        // Keep firstBid in a safe range so 5% increment + 50 ether doesn't overflow uint128
        firstBid = uint128(bound(firstBid, 1 ether, 10 ether));

        // minNext = firstBid + max(firstBid * 500 / 10000, 1)
        uint256 inc     = uint256(firstBid) * 500 / 10_000;
        uint256 minNext = uint256(firstBid) + (inc == 0 ? 1 : inc);
        secondBid = uint128(bound(secondBid, minNext, minNext + 50 ether));

        vm.deal(alice, uint256(firstBid)  + 1 ether);
        vm.deal(bob,   uint256(secondBid) + 1 ether);

        vm.startPrank(seller);
        uint256 tid = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        uint256 id = ah.create(address(nft), tid, firstBid,
            uint64(block.timestamp), uint64(block.timestamp + 7 days), 0);
        vm.stopPrank();

        _commitAndBid(id, alice, firstBid,  firstBid);
        _commitAndBid(id, bob,   secondBid, secondBid);

        assertEq(ah.pendingReturns(alice), firstBid);
    }
}
