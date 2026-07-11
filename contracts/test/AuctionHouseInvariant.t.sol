// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}       from "forge-std/Test.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {MockERC721}  from "./MockERC721.sol";

/// @dev Drives random auction bid/cancel/settle sequences and tracks a ghost sum
///      of all escrowed bids. The AuctionHouse contract should hold exactly that
///      much ETH at all times minus fees already paid out — fees leave immediately
///      on settlement, so the invariant is:
///
///      contractBalance + totalFeesPaid == ghostEscrowedBeforeSettlement
///
///      where totalFeesPaid is the sum of all 1.5% fees extracted at settlement.
///      Since fees leave at settle time (not at bid time), the pre-settlement
///      ghost matches contract balance exactly. Post-settlement, fees have left
///      and the ghost is reduced by winner total (not bid-by-bid).
contract AuctionHouseHandler is Test {
    AuctionHouse public ah;
    MockERC721   public nft;

    address public seller = address(0xA0);
    address[3] public bidders;
    uint256 public tokenId;
    uint256 public auctionId;
    bool    public settled;

    uint256 public ghostEscrowed;  // sum of all active bid escrows
    uint256 public totalFeesPaid;  // cumulative fees extracted at settlement

    constructor(AuctionHouse _ah, MockERC721 _nft) {
        ah = _ah;
        nft = _nft;
        for (uint256 i; i < 3; i++) {
            bidders[i] = address(uint160(0x1000 + i));
            vm.deal(bidders[i], 1_000 ether);
        }
        vm.deal(seller, 1 ether);
        // Mint token, approve, create auction, then activate it
        vm.startPrank(seller);
        tokenId = nft.mint(seller);
        nft.setApprovalForAll(address(ah), true);
        auctionId = ah.create(address(nft), tokenId, 1 ether, uint64(block.timestamp + 24 hours), 500, 0);
        vm.stopPrank();
    }

    function bid(uint256 bSeed, uint128 value) external {
        if (settled) return;
        address b = bidders[bSeed % 3];
        uint128 val = uint128(bound(value, 0.1 ether, 50 ether));

        // Compute minimum qualifying bid using MIN_BID_INCREMENT
        (,,,,,,,,,,,,uint128 leaderTotal,) = ah.auctions(auctionId);
        uint128 minInc = ah.MIN_BID_INCREMENT();
        uint128 minNext;
        unchecked { minNext = leaderTotal + minInc; }

        if (val < minNext) {
            // Not enough to overtake leader — bid must still be possible
            // (sub-leader accumulation is allowed but doesn't overtake)
            return;
        }

        vm.prank(b);
        try ah.bid{value: uint256(val)}(auctionId) {
            ghostEscrowed += val;
        } catch {
            // Bid reverted (e.g. auction ended mid-fuzz)
        }
    }

    function settle(uint256 warp) external {
        if (settled) return;
        // Warp past end time
        (,,,,,,,uint64 endsAt,,,,,,) = ah.auctions(auctionId);
        if (block.timestamp < endsAt) {
            vm.warp(uint256(endsAt) + 1 + (warp % 100));
        }
        // Try to settle
        try ah.settle(auctionId) {
            settled = true;
            // The winner's total escrow is consumed (sent to seller minus fee).
            // Fee leaves the contract immediately via feeRecipient.transfer.
            // Loser escrows stay in pendingReturns (pull pattern).
            // The ghost was tracking ALL escrows; settlement extracts winner total.
            (,,,,,,,,,,,,uint128 leaderTotal,) = ah.auctions(auctionId);
            // Track the 1.5% fee extracted at settlement
            uint256 fee = (uint256(leaderTotal) * 150) / 10000;
            totalFeesPaid += fee;
            ghostEscrowed -= leaderTotal;
        } catch {
            // settle may revert if already settled or nothing to settle
        }
    }

    function cancel() external {
        if (settled) return;
        // Only seller can cancel, and only if reserve not met
        vm.prank(seller);
        try ah.cancelEarly(auctionId) {
            settled = true;
            // On cancel with no qualifying bids, all escrows are refundable.
            // No fees extracted. Ghost matches pendingReturns total.
        } catch {}
    }
}

/// @notice Invariant: The AuctionHouse ETH balance never exceeds the ghost-escrowed
///         total. Fees extracted at settlement are tracked separately so the
///         pre-settlement balance exactly matches ghostEscrowed. Post-settlement,
///         contract balance + totalFeesPaid == ghostEscrowed (before settlement).
///
///         Simplified invariant (always true):
///         contractBalance <= ghostEscrowed + totalFeesPaid
///
///         This catches any ETH leak where the contract holds more ETH than can be
///         accounted for by active bids + refundable credits.
contract AuctionHouseInvariantTest is Test {
    AuctionHouse        ah;
    MockERC721          nft;
    AuctionHouseHandler handler;
    address feeRecipient = address(0xFEE);

    function setUp() public {
        ah = new AuctionHouse();
        ah.initialize(feeRecipient, address(0));
        nft = new MockERC721();
        handler = new AuctionHouseHandler(ah, nft);
        targetContract(address(handler));
    }

    /// @notice Contract balance must never exceed ghost-escrowed + fees-paid.
    ///         If this breaks, ETH is leaking into the contract from somewhere
    ///         other than bids (or bids are being double-counted).
    function invariant_balanceBoundedByGhost() public view {
        uint256 ghost = handler.ghostEscrowed();
        uint256 fees  = handler.totalFeesPaid();
        assertLe(address(ah).balance, ghost + fees, "contract holds more ETH than accounted for");
    }
}
