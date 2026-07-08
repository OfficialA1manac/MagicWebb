// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}         from "forge-std/Test.sol";
import {Marketplace}  from "../src/Marketplace.sol";
import {MockERC721}   from "./MockERC721.sol";

/// @dev Drives random list/buy/cancel sequences on the Marketplace and tracks
///      the ghost fee total. The Marketplace should extract exactly 1.5% (150 bps)
///      from every sale — never more, never less. Fees are sent to feeRecipient
///      immediately on buy(), so the invariant is:
///
///      feeRecipient.balance == ghostTotalFees
///
///      This catches:
///        - Fee miscalculation (wrong bps)
///        - Fee double-extraction
///        - ETH leaking to feeRecipient outside of buy()
contract MarketplaceHandler is Test {
    Marketplace public mp;
    MockERC721  public nft;

    address public seller = address(0xA0);
    address public buyer  = address(0xB0);
    address public feeRecipient = address(0xFEE);

    uint256 public ghostTotalFees;  // sum of all expected fees from sales
    uint256 public nextToken;

    constructor(Marketplace _mp, MockERC721 _nft) {
        mp = _mp;
        nft = _nft;
        vm.deal(seller, 1 ether);
        vm.deal(buyer, 100 ether);
        // Mint several tokens for the seller
        vm.startPrank(seller);
        for (uint256 i; i < 5; i++) {
            nft.mint(seller);
        }
        nft.setApprovalForAll(address(mp), true);
        vm.stopPrank();
        nextToken = 1;
    }

    function listAndBuy(uint128 price) external {
        price = uint128(bound(price, 1 ether, 10 ether));

        uint256 tid;
        unchecked { tid = (nextToken % 5) + 1; nextToken++; }

        uint64 expires = uint64(block.timestamp + 24 hours);

        // List
        vm.prank(seller);
        try mp.list(address(nft), tid, price, expires) {
            // Buy
            vm.prank(buyer);
            try mp.buy{value: uint256(price)}(address(nft), tid, seller) {
                // Fee = price * 150 / 10000
                uint256 expectedFee = (uint256(price) * 150) / 10000;
                ghostTotalFees += expectedFee;
            } catch {
                // buy may revert (e.g. token not listed, expired)
            }
        } catch {
            // list may revert (e.g. already listed by another seller)
        }
    }

    function listAndCancel(uint128 price) external {
        price = uint128(bound(price, 1 ether, 10 ether));

        uint256 tid;
        unchecked { tid = (nextToken % 5) + 1; nextToken++; }

        uint64 expires = uint64(block.timestamp + 24 hours);

        vm.prank(seller);
        try mp.list(address(nft), tid, price, expires) {
            // No fee on cancel
            vm.prank(seller);
            mp.cancel(address(nft), tid);
        } catch {}
    }
}

/// @notice Invariant: feeRecipient ETH balance must exactly match the ghost
///         total of expected fees (150 bps on every sale). The Marketplace
///         fee is hardcoded at 150/10000 and immutable.
///
///         Any deviation indicates:
///           - Fee calculation bug (wrong bps)
///           - ETH leaking to feeRecipient from a non-buy path
///           - Fee double-extraction
contract MarketplaceInvariantTest is Test {
    Marketplace        mp;
    MockERC721         nft;
    MarketplaceHandler handler;
    address feeRecipient = address(0xFEE);

    function setUp() public {
        mp = new Marketplace(feeRecipient, address(0));
        nft = new MockERC721();
        handler = new MarketplaceHandler(mp, nft);
        targetContract(address(handler));
    }

    /// @notice feeRecipient balance MUST equal ghost total fees from all sales.
    function invariant_feeRecipientMatchesFees() public view {
        assertEq(feeRecipient.balance, handler.ghostTotalFees(), "feeRecipient balance mismatch");
    }
}
