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
    }

    function makeOffer(uint256 bSeed, uint256 tSeed, uint128 principal, uint256 ttl) external {
        address b = bidders[bSeed % 3];
        uint256 tid = tokenIds[tSeed % 3];
        principal = uint128(bound(principal, 0.01 ether, 100 ether));
        uint64 exp = uint64(block.timestamp + bound(ttl, 1, 14 days));

        (uint128 existing,,,) = ob.positions(address(nft), tid, b);
        if (uint256(existing) + principal > type(uint128).max) return;

        uint256 fee = uint256(principal) * 150 / 10_000;
        vm.prank(b);
        ob.makeOffer{value: uint256(principal) + fee}(address(nft), tid, principal, exp);
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
        ob.refundExpiredOffer(address(nft), tid, b);
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
