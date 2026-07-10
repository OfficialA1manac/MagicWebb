// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test}       from "forge-std/Test.sol";
import {OfferBook}  from "../src/OfferBook.sol";
import {MockERC721} from "./MockERC721.sol";

/// @dev Drives random offer/reject/refund sequences and tracks a ghost sum of escrowed
///      principals. The OfferBook should hold exactly that much ETH at all times — fees
///      always leave immediately, and no principal is ever lost or stranded.
contract OfferHandler is Test {
    OfferBook  public ob;
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
        vm.stopPrank();
        // Enable offers on the mock collection so makeOffer succeeds.
        ob.setOfferEligible(address(nft), true);
    }

    function makeOffer(uint256 bSeed, uint256 tSeed, uint128 principal, uint256 ttl) external {
        address b = bidders[bSeed % 3];
        uint256 tid = tokenIds[tSeed % 3];
        principal = uint128(bound(principal, 0.01 ether, 100 ether));

        // M-01 fix: the new expiry must not reduce an existing position's expiry.
        (uint128 existingPrincipal,, uint64 existingExp,) = ob.positions(address(nft), tid, b);
        // Pick one of the 6 valid durations for the new position.
        // For top-ups, existing expiry is kept (the handler ensures >=).
        uint64[6] memory durations = [
            uint64(3 minutes), uint64(15 minutes), uint64(30 minutes),
            uint64(1 hours), uint64(4 hours), uint64(24 hours)
        ];
        uint64 exp = uint64(block.timestamp) + durations[ttl % 6];
        // If existing position exists, ensure new expiry >= existing expiry.
        // Top-up: do not change the expiry; timer continues from original.
        if (existingPrincipal > 0) {
            exp = existingExp;
        }

        if (uint256(existingPrincipal) + principal > type(uint128).max) return;

        vm.prank(b);
        // Fix: send exactly `principal` as msg.value (not principal + fee).
        // makeOffer checks msg.value == principal. The seller-pays-fee model
        // means offers are FREE — no fee at offer time.
        ob.makeOffer{value: uint256(principal)}(address(nft), tid, principal, exp);
        ghostEscrowed += principal;
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

    function refundExpired(uint256 bSeed, uint256 tSeed, uint256 warp) external {
        address b = bidders[bSeed % 3];
        uint256 tid = tokenIds[tSeed % 3];
        (uint128 p,, uint64 exp,) = ob.positions(address(nft), tid, b);
        if (p == 0) return;
        vm.warp(uint256(exp) + 1 + (warp % 1000));
        // Seller rejects the expired offer (refundExpiredOffer removed in v10).
        vm.prank(owner);
        ob.rejectOffer(address(nft), tid, b);
        ghostEscrowed -= p;
    }
}

contract OfferBookInvariantTest is Test {
    OfferBook    ob;
    MockERC721   nft;
    OfferHandler handler;
    address feeRecipient = address(0xFEE);

    function setUp() public {
        ob = new OfferBook(feeRecipient, address(0));
        nft = new MockERC721();
        handler = new OfferHandler(ob, nft);
        targetContract(address(handler));
    }

    /// The escrow holds exactly the sum of active principals — never more, never less.
    function invariant_escrowMatchesPrincipals() public view {
        assertEq(address(ob).balance, handler.ghostEscrowed());
    }
}
