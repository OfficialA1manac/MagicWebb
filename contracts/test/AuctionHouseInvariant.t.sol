// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {AuctionHouse} from "../src/AuctionHouse.sol";
import {MockERC721} from "./MockERC721.sol";
import {TestHelpers} from "./TestHelpers.sol";

contract AuctionHouseHandler is Test {
    AuctionHouse public ah;
    MockERC721 public nft;
    address public seller = address(0xA0);
    address[3] public bidders;
    uint256 public tokenId;
    uint256 public auctionId;
    bool public settled;
    uint256 public ghostEscrowed;
    uint256 public totalFeesPaid;

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
        auctionId = ah.create(address(nft), tokenId, 1 ether, uint64(block.timestamp + 24 hours), 500, 0);
        vm.stopPrank();
    }

    function bid(uint256 bSeed, uint128 value) external {
        if (settled) return;
        address b = bidders[bSeed % 3];
        uint128 val = uint128(bound(value, 0.1 ether, 50 ether));
        (,,,,,,,,,,,,uint128 leaderTotal,) = ah.auctions(auctionId);
        uint128 minInc = ah.MIN_BID_INCREMENT();
        uint128 minNext;
        unchecked { minNext = leaderTotal + minInc; }
        if (val < minNext) {
            // Not enough to overtake leader — skip (sub-leader bids revert in the new model)
            return;
        }
        vm.prank(b);
        try ah.bid{value: uint256(val)}(auctionId) {
            ghostEscrowed += val;
        } catch {}
    }

    function settle(uint256 warp) external {
        if (settled) return;
        (,,,,,,,uint64 endsAt,,,,,,) = ah.auctions(auctionId);
        if (block.timestamp < endsAt) {
            vm.warp(uint256(endsAt) + 1 + (warp % 100));
        }
        try ah.settle(auctionId) {
            settled = true;
            (,,,,,,,,,,,,uint128 leaderTotal,) = ah.auctions(auctionId);
            uint256 fee = (uint256(leaderTotal) * 150) / 10000;
            totalFeesPaid += fee;
            ghostEscrowed -= leaderTotal;
        } catch {}
    }

    function cancel() external {
        if (settled) return;
        vm.prank(seller);
        try ah.cancelEarly(auctionId) {
            settled = true;
        } catch {}
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

    function invariant_balanceBoundedByGhost() public view {
        uint256 ghost = handler.ghostEscrowed();
        uint256 fees = handler.totalFeesPaid();
        assertLe(address(ah).balance, ghost + fees, "contract holds more ETH than accounted for");
    }
}
