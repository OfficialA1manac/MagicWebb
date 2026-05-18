// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}         from "forge-std/Test.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {MockERC721}   from "./MockERC721.sol";

contract AuctionHouseTest is Test {
    AuctionHouse ah;
    MockERC721   nft;
    address admin    = address(this);
    address feeVault = address(0x1111000000000000000000000000000000111100);
    uint16  feeBps   = 250;
    address seller   = address(0xBEEF);
    address alice    = address(0xA11CE);
    address bob      = address(0xB0B);

    function setUp() public {
        ah  = new AuctionHouse(feeVault, feeBps, admin);
        nft = new MockERC721();
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

        (,,,,,,,, , ,uint128 hi,,) = ah.auctions(id);
        assertEq(hi, 1.6 ether);
        assertEq(ah.pendingReturns(alice), 0);
        assertEq(address(ah).balance, 1.6 ether);
    }

    function test_feeOnlyOnSettleAfterTransfer() public {
        (uint256 id, uint256 tid) = _createAuction();

        _commitAndBid(id, bob, 2 ether, 2 ether);

        vm.warp(block.timestamp + 8 days);

        uint256 vaultBefore  = feeVault.balance;
        uint256 sellerBefore = seller.balance;

        uint256 fee          = (2 ether * uint256(feeBps)) / 10_000;
        uint256 sellerPayout = 2 ether - fee; // no royalty on MockERC721

        ah.settle(id);

        assertEq(nft.ownerOf(tid), bob);
        assertEq(feeVault.balance,  vaultBefore  + fee);
        assertEq(seller.balance,    sellerBefore + sellerPayout);
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

    // ── Anti-snipe ────────────────────────────────────────────────────────

    function test_antiSnipeExtends() public {
        (uint256 id,) = _createAuction();

        vm.warp(block.timestamp + 7 days - 2 minutes);

        _commitAndBid(id, alice, 1 ether, 1 ether);

        (,,,,,, uint64 endsAt,,,,,,) = ah.auctions(id);
        assertGt(endsAt, uint64(block.timestamp));
    }

    // Anti-snipe cap: extension clamped to originalEndsAt + ANTI_SNIPE_MAX_EXTENSION.
    function test_antiSnipeCapRespected() public {
        (uint256 id,) = _createAuction();

        // Place initial bid (needed so Alice can compound-raise later)
        _commitAndBid(id, alice, 1 ether, 1 ether);

        // Manipulate endsAt via vm.store to be near the absolute cap.
        // Storage layout (new MarketplaceCore adds Pausable._paused[slot0], AccessControl._roles[slot1],
        // royaltyRegistry[slot2]): nextAuctionId=slot3, auctions mapping=slot4.
        // Struct slot+1: collection(160) | endsAt(64) | originalEndsAt(32)
        bytes32 base = keccak256(abi.encode(uint256(id), uint256(4)));
        bytes32 slot1 = bytes32(uint256(base) + 1);
        bytes32 word  = vm.load(address(ah), slot1);

        uint160 coll    = uint160(uint256(word));
        uint32  origEnd = uint32(uint256(word) >> (160 + 64));

        uint64 cap       = uint64(origEnd) + ah.ANTI_SNIPE_MAX_EXTENSION();
        uint64 newEndsAt = cap - 100; // 100 seconds before cap

        bytes32 newWord = bytes32(
            uint256(coll) |
            (uint256(newEndsAt) << 160) |
            (uint256(origEnd)   << 224)
        );
        vm.store(address(ah), slot1, newWord);

        // Warp to inside snipe window; roll is independent of timestamp
        vm.warp(uint256(newEndsAt) - 1);

        // Alice compound-raises (fullAmount = 1.06 ether, increment = 0.06 ether)
        uint128 raised = 1.06 ether;
        bytes32 salt2  = keccak256("raise_salt");
        bytes32 c2     = keccak256(abi.encode(id, alice, raised, salt2));
        vm.prank(alice);
        ah.commitBid(id, c2);
        vm.roll(block.number + uint256(ah.COMMIT_DELAY_BLOCKS()) + 1);
        vm.prank(alice);
        ah.bid{value: 0.06 ether}(id, raised, salt2);

        (,,,,,, uint64 finalEnd,,,,,,) = ah.auctions(id);
        assertEq(finalEnd, cap);
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
}
