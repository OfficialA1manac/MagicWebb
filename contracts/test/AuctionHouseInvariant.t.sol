// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {MockERC721} from "./MockERC721.sol";
import {TestHelpers} from "./TestHelpers.sol";

/// @notice Fuzz driver for the AuctionHouse escrow invariants.
/// @dev Reads auction state through `getAuction()` — AuctionHouse.sol:118-124
///      mandates it over the positional `auctions(id)` tuple, which silently
///      misreads if a field is ever inserted before `leader`.
contract AuctionHouseHandler is Test {
    AuctionHouse public ah;
    MockERC721 public nft;
    address public seller = address(0xA0);
    address[3] public bidders;
    uint256 public tokenId;
    uint256 public auctionId;

    /// @notice Every wei the handler has ever sent into the AuctionHouse.
    uint256 public ghostDeposited;


    constructor(AuctionHouse _ah, MockERC721 _nft) {
        ah = _ah;
        nft = _nft;
        for (uint256 i; i < 3; i++) {
            bidders[i] = address(uint160(0x1000 + i));
            vm.deal(bidders[i], 1_000 ether);
        }
        vm.deal(seller, 1 ether);
        vm.startPrank(seller);
        tokenId = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        auctionId = ah.create(address(nft), tokenId, 1 ether, uint64(24 hours));
        vm.stopPrank();
    }

    // ── Views the invariants assert against ───────────────────────────────

    /// @notice Everything the AuctionHouse still owes: live per-bidder escrow
    ///         plus unclaimed pull-refund credits.
    function outstandingClaims() external view returns (uint256 total) {
        for (uint256 i; i < 3; i++) {
            total += uint256(ah.cumulative(auctionId, bidders[i]));
            total += ah.pendingReturns(bidders[i]);
        }
        total += ah.pendingReturns(seller);
        total += ah.pendingReturns(ah.feeRecipient());
    }

    function isSettled() public view returns (bool) {
        return ah.getAuction(auctionId).settled;
    }

    // ── Actions ───────────────────────────────────────────────────────────

    /// @notice Place a bid that is actually large enough to take the lead.
    /// @dev The old ladder compared a flat `bound(value, 0.1, 50 ether)` against
    ///      `leaderTotal + MIN_BID_INCREMENT`, ignoring both the auction's
    ///      `minIncrementBps` (5%) and the bidder's existing cumulative escrow.
    ///      Once `leaderTotal` passed ~48 ether every draw fell under the
    ///      threshold and the action degenerated into a no-op. The requirement
    ///      is now computed from the auction and the bid is sized relative to
    ///      it, so the ladder keeps climbing for the whole run.
    function bid(uint256 bSeed, uint128 value) external {
        AuctionHouse.Auction memory a = ah.getAuction(auctionId);
        if (a.settled || block.timestamp >= a.endsAt) return;

        address b = bidders[bSeed % 3];
        uint256 prevCum = uint256(ah.cumulative(auctionId, b));

        // Cumulative total this bidder must reach for bid() to accept the call.
        uint256 minTotal;
        if (a.leader == b) {
            minTotal = prevCum + 1; // leader topping up: any non-zero value works
        } else if (a.leaderTotal == 0) {
            minTotal = uint256(a.reserve); // no leader yet: must clear the reserve
        } else {
            // v3.3: flat marketplace-wide increment — leader + 1 native token.
            minTotal = uint256(a.leaderTotal) + uint256(ah.MIN_BID_INCREMENT());
        }

        uint256 need = minTotal > prevCum ? minTotal - prevCum : 1;
        uint256 val = bound(uint256(value), need, need + 50 ether);
        if (val > type(uint128).max) return;

        // Fund the bidder for exactly this call: the ladder compounds at 5% and
        // would otherwise outrun a fixed starting balance mid-run, turning every
        // later bid into a silent out-of-funds no-op.
        vm.deal(b, val);
        vm.prank(b);
        try ah.bid{value: val}(auctionId) {
            ghostDeposited += val;
        } catch {}
    }

    function settle(uint256 warp) external {
        AuctionHouse.Auction memory a = ah.getAuction(auctionId);
        if (a.settled) return;
        if (block.timestamp < a.endsAt) {
            // Warp on ~1 draw in 8 rather than every call: an unconditional jump
            // to endsAt ended the auction on the first settle() the fuzzer drew,
            // so the bid ladder never got more than a couple of rungs deep.
            if (warp % 8 != 0) return;
            vm.warp(uint256(a.endsAt) + 1 + (warp % 100));
        }
        // v3: settle is keeper/seller/winner-only. Prank the seller so the
        // handler keeps real settle coverage — a bare call would revert
        // NotAuthorized and the try/catch would silently swallow it.
        vm.prank(seller);
        try ah.settle(auctionId) {} catch {}
    }

    function cancel() external {
        vm.prank(seller);
        try ah.cancelEarly(auctionId) {} catch {}
    }

    /// @notice Escrow-recovery backstop: permissionless once the auction is
    ///         settled/cancelled. Previously unfuzzed.
    function refundLosers(uint256 seed) external {
        address[] memory batch = new address[](3);
        uint256 base = seed % 3; // reduce first: seed + i can overflow
        for (uint256 i; i < 3; i++) {
            batch[i] = bidders[(base + i) % 3];
        }
        // Permissionless by design — call it from a random address.
        vm.prank(address(uint160(0xDEAD0000 + (seed % 16))));
        try ah.refundLosers(auctionId, batch) {} catch {}
    }

    /// @notice Early loser exit before settlement. Previously unfuzzed.
    function withdrawLoserFunds(uint256 bSeed) external {
        address b = bidders[bSeed % 3];
        vm.prank(b);
        try ah.withdrawLoserFunds(auctionId) {} catch {}
    }

    /// @notice Permissionless safety valve at endsAt + SELLER_DEFAULT_WINDOW.
    ///         Previously unfuzzed, despite being the promise that escrow can
    ///         never be trapped by a defaulting seller.
    function forceCancel(uint256 warp) external {
        AuctionHouse.Auction memory a = ah.getAuction(auctionId);
        if (a.settled) return;
        uint256 unlock = uint256(a.endsAt) + ah.SELLER_DEFAULT_WINDOW();
        if (block.timestamp < unlock) {
            // Same rationale as settle(): jumping 3 days forward on every draw
            // would terminate the auction almost immediately.
            if (warp % 8 != 0) return;
            vm.warp(unlock + (warp % 100));
        }
        vm.prank(address(0xCAFE));
        try ah.forceCancel(auctionId) {} catch {}
    }

    /// @notice Pull the fallback credit, if any push payment ever failed.
    function withdrawRefund(uint256 bSeed) external {
        address b = bidders[bSeed % 3];
        vm.prank(b);
        try ah.withdrawRefund() {} catch {}
    }
}

contract AuctionHouseInvariantTest is Test, TestHelpers {
    AuctionHouse ah;
    MockERC721 nft;
    AuctionHouseHandler handler;
    address feeRecipient = address(0xFEE);

    function setUp() public {
        ah = _deployAuctionHouse(feeRecipient, address(0));
        nft = new MockERC721();
        handler = new AuctionHouseHandler(ah, nft);
        targetContract(address(handler));
    }

    /// @notice TWO-SIDED escrow solvency. The previous assertion was
    ///         `assertLe(balance, ghost + fees)`, so a contract holding LESS
    ///         than it owed — the failure mode that actually loses user funds —
    ///         passed silently. The AuctionHouse must hold exactly the sum of
    ///         live per-bidder escrow plus unclaimed pull-refund credits: not a
    ///         wei more (phantom ETH / double-count) and not a wei less
    ///         (insolvent escrow / double-payout).
    function invariant_escrowExactlyMatchesClaims() public view {
        assertEq(
            address(ah).balance,
            handler.outstandingClaims(),
            "AuctionHouse balance != live escrow + pending refunds"
        );
    }

    /// @notice Nothing can be owed that was never deposited.
    function invariant_claimsNeverExceedDeposits() public view {
        assertLe(
            handler.outstandingClaims(),
            handler.ghostDeposited(),
            "outstanding claims exceed everything ever deposited"
        );
    }

    /// @notice The leader always holds the maximum cumulative escrow, and a
    ///         non-zero leaderTotal always matches the leader's own cumulative.
    ///         This is the property `bid()` and `settle()` both rely on when
    ///         deciding who is paid — if it ever breaks, settlement pays the
    ///         wrong bidder's escrow to the seller.
    function invariant_leaderHoldsMaxCumulative() public view {
        AuctionHouse.Auction memory a = ah.getAuction(handler.auctionId());
        if (a.leader == address(0)) {
            assertEq(a.leaderTotal, 0, "no leader but leaderTotal is set");
            return;
        }
        if (a.settled) return; // settle()/refundLosers() zero escrow by design
        uint256 id = handler.auctionId();
        assertEq(
            uint256(ah.cumulative(id, a.leader)),
            uint256(a.leaderTotal),
            "leaderTotal diverged from the leader's cumulative escrow"
        );
        for (uint256 i; i < 3; i++) {
            address b = handler.bidders(i);
            if (b == a.leader) continue;
            assertLe(
                uint256(ah.cumulative(id, b)),
                uint256(a.leaderTotal),
                "a non-leader out-escrows the leader"
            );
        }
    }
}
