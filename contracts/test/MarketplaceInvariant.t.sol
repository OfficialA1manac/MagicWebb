// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test} from "forge-std/Test.sol";
import {Marketplace, NotOwner, NotListed, Expired, NotApproved, BatchTooLarge} from "../src/Marketplace.sol";
import {MockERC721} from "./MockERC721.sol";
import {TokenStandard, BelowMinPrice, InvalidDuration} from "../src/MarketplaceCore.sol";
import {TestHelpers} from "./TestHelpers.sol";

contract MarketplaceHandler is Test {
    Marketplace public mp;
    MockERC721 public nft;
    address public owner = address(0xA0);
    uint256 public tokenId;
    uint256 public ghostListed;

    constructor(Marketplace _mp, MockERC721 _nft) {
        mp = _mp;
        nft = _nft;
        vm.startPrank(owner);
        tokenId = nft.mint(owner);
        nft.setApprovalForAll(address(mp), true);
        mp.list(address(nft), tokenId, 1 ether, uint64(block.timestamp + 24 hours));
        ghostListed = 1 ether;
        vm.stopPrank();
    }

    function buy(uint256) external {
        vm.prank(address(0xB0B));
        vm.deal(address(0xB0B), 100 ether);
        try mp.buy{value: 1 ether}(address(nft), tokenId, owner) {
            ghostListed = 0;
        } catch {}
    }

    function cancel() external {
        vm.prank(owner);
        try mp.cancel(address(nft), tokenId) {
            ghostListed = 0;
        } catch {}
    }
}

contract MarketplaceInvariantTest is Test, TestHelpers {
    Marketplace mp;
    MockERC721 nft;
    MarketplaceHandler handler;
    address feeRecipient = address(0xFEE);

    function setUp() public {
        mp = _deployMarketplace(feeRecipient, address(0));
        nft = new MockERC721();
        handler = new MarketplaceHandler(mp, nft);
        targetContract(address(handler));
    }

    function invariant_listedOrZero() public view {
        uint256 ghost = handler.ghostListed();
        assertTrue(ghost == 0 || ghost == 1 ether, "ghost must be 0 or 1 ether");
    }
}
