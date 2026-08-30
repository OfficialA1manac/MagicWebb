// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {OfferBook} from "../src/OfferBook.sol";
import {MockERC721} from "./MockERC721.sol";
import {TestHelpers} from "./TestHelpers.sol";

contract OfferHandler is Test {
    OfferBook public ob;
    MockERC721 public nft;
    address public owner = address(0xA0);
    address[3] public bidders;
    uint256[3] public tokenIds;
    uint256 public ghostEscrowed;

    constructor(OfferBook _ob, MockERC721 _nft) {
        ob = _ob;
        nft = _nft;
        for (uint256 i; i < 3; i++) {
            bidders[i] = address(uint160(0x1000 + i));
            vm.deal(bidders[i], 1_000 ether);
        }
        vm.startPrank(owner);
        for (uint256 i; i < 3; i++) {
            tokenIds[i] = nft.mint(owner);
        }
        nft.setApprovalForAll(address(ob), true);
        // setOfferEligible is called in the test's setUp() (as token-0 owner)
        // before OfferHandler is deployed; skip it here.
        vm.stopPrank();
    }

    function makeOffer(uint256 bSeed, uint256 tSeed, uint128 principal, uint256 ttl) external {
        address b = bidders[bSeed % 3];
        uint256 tid = tokenIds[tSeed % 3];
        // Floor at MIN_PRICE: _makeOffer reverts BelowMinPrice below 1 ether, so
        // the old 0.01-ether floor made ~99% of fuzz draws revert-and-discard,
        // leaving the suite almost no real coverage.
        principal = uint128(bound(principal, ob.MIN_PRICE(), 100 ether));
        (uint128 existingPrincipal,,,) = ob.positions(address(nft), tid, b);
        uint64[15] memory durations = [
            uint64(1 minutes), uint64(3 minutes), uint64(5 minutes),
            uint64(10 minutes), uint64(15 minutes), uint64(30 minutes),
            uint64(45 minutes), uint64(1 hours), uint64(2 hours),
            uint64(4 hours), uint64(8 hours), uint64(12 hours),
            uint64(16 hours), uint64(20 hours), uint64(24 hours)
        ];
        vm.prank(b);
        ob.makeOffer{value: uint256(principal)}(address(nft), tid, principal, durations[ttl % 15]);
        ghostEscrowed = ghostEscrowed + principal - uint256(existingPrincipal);
    }

    function rejectOffer(uint256 bSeed, uint256 tSeed) external {
        address b = bidders[bSeed % 3];
        uint256 tid = tokenIds[tSeed % 3];
        (uint128 p,,,) = ob.positions(address(nft), tid, b);
        if (p == 0) return;
        vm.prank(owner);
        ob.rejectOffer(address(nft), tid, b);
        ghostEscrowed -= p;
    }

    /// @notice Exercise the unstoppable-exit path: refundExpiredOffer().
    /// @dev This used to call rejectOffer(), a copy-paste bug that left
    ///      refundExpiredOffer with ZERO invariant coverage even though
    ///      .github/workflows/audit.yml gates releases on this file.
    ///      The bidder is pranked because a bidder reclaiming their own expired
    ///      escrow is always allowed, whatever the manager configuration.
    function refundExpired(uint256 bSeed, uint256 tSeed, uint256 warp) external {
        address b = bidders[bSeed % 3];
        uint256 tid = tokenIds[tSeed % 3];
        (uint128 p,, uint64 exp,) = ob.positions(address(nft), tid, b);
        if (p == 0) return;
        vm.warp(uint256(exp) + 1 + (warp % 1000));
        vm.prank(b);
        ob.refundExpiredOffer(address(nft), tid, b);
        ghostEscrowed -= p;
    }

    /// @notice Bidder-initiated exit before expiry.
    function cancelOffer(uint256 bSeed, uint256 tSeed) external {
        address b = bidders[bSeed % 3];
        uint256 tid = tokenIds[tSeed % 3];
        (uint128 p,, uint64 exp,) = ob.positions(address(nft), tid, b);
        if (p == 0 || block.timestamp >= exp) return;
        vm.prank(b);
        ob.cancelOffer(address(nft), tid);
        ghostEscrowed -= p;
    }

    /// @notice Sum of every live position's principal: the escrow the book owes.
    function outstandingPrincipal() external view returns (uint256 total) {
        for (uint256 i; i < 3; i++) {
            for (uint256 j; j < 3; j++) {
                (uint128 p,,,) = ob.positions(address(nft), tokenIds[i], bidders[j]);
                total += uint256(p);
            }
        }
    }

    /// @notice Sum of every pull-refund credit the book still owes.
    function outstandingPending() external view returns (uint256 total) {
        for (uint256 j; j < 3; j++) {
            total += ob.pendingReturns(bidders[j]);
        }
        total += ob.pendingReturns(owner);
        total += ob.pendingReturns(ob.feeRecipient());
    }
}

contract OfferBookInvariantTest is Test, TestHelpers {
    OfferBook ob;
    MockERC721 nft;
    OfferHandler handler;
    address feeRecipient = address(0xFEE);

    function setUp() public {
        ob = _deployOfferBook(feeRecipient, address(0));
        nft = new MockERC721();
        // Token 0 was minted to this test contract by MockERC721's constructor.
        // Enable offers here (as the token-0 owner) so OfferHandler doesn't
        // need to call setOfferEligible itself.
        ob.setOfferEligible(address(nft), true);
        handler = new OfferHandler(ob, nft);
        targetContract(address(handler));
    }

    /// @notice Ghost accounting: every wei in escrow was deposited by a handler
    ///         action, and every exit decremented the ghost by exactly the
    ///         principal it released.
    function invariant_escrowMatchesGhost() public view {
        assertEq(address(ob).balance, handler.ghostEscrowed(), "balance != ghost escrow");
    }

    /// @notice Solvency derived from contract state rather than the ghost: the
    ///         ETH the book holds is exactly what it still owes as live offer
    ///         principals plus unclaimed pull-refund credits. Catches a delete
    ///         that forgets to pay, and a pay that forgets to delete.
    function invariant_escrowMatchesPositions() public view {
        assertEq(
            address(ob).balance,
            handler.outstandingPrincipal() + handler.outstandingPending(),
            "balance != live principals + pending refunds"
        );
    }
}
