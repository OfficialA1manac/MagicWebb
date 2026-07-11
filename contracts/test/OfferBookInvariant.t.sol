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
        principal = uint128(bound(principal, 0.01 ether, 100 ether));
        (uint128 existingPrincipal,,,) = ob.positions(address(nft), tid, b);
        uint64[6] memory durations = [
            uint64(3 minutes), uint64(15 minutes), uint64(30 minutes),
            uint64(1 hours), uint64(4 hours), uint64(24 hours)
        ];
        uint64 exp = uint64(block.timestamp) + durations[ttl % 6];
        if (uint256(existingPrincipal) > type(uint128).max) return;
        vm.prank(b);
        ob.makeOffer{value: uint256(principal)}(address(nft), tid, principal, exp);
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

    function refundExpired(uint256 bSeed, uint256 tSeed, uint256 warp) external {
        address b = bidders[bSeed % 3];
        uint256 tid = tokenIds[tSeed % 3];
        (uint128 p,, uint64 exp,) = ob.positions(address(nft), tid, b);
        if (p == 0) return;
        vm.warp(uint256(exp) + 1 + (warp % 1000));
        vm.prank(owner);
        ob.rejectOffer(address(nft), tid, b);
        ghostEscrowed -= p;
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

    function invariant_escrowMatchesPrincipals() public view {
        assertEq(address(ob).balance, handler.ghostEscrowed());
    }
}
