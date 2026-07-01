// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Test, console2} from "forge-std/Test.sol";
import {MagicWebbNFT} from "../src/MagicWebbNFT.sol";
import {Ownable} from "@openzeppelin/contracts/access/Ownable.sol";

contract MagicWebbNFTTest is Test {
    MagicWebbNFT nft;
    address owner = makeAddr("owner");
    address alice = makeAddr("alice");
    address bob   = makeAddr("bob");

    function setUp() public {
        vm.prank(owner);
        nft = new MagicWebbNFT("Magic Webb Animi", "ANIMI", owner);
    }

    // ── Deployment ─────────────────────────────────────────────────────

    function test_deploy_setsNameAndSymbol() public view {
        assertEq(nft.name(), "Magic Webb Animi");
        assertEq(nft.symbol(), "ANIMI");
    }

    function test_deploy_ownerIsCorrect() public view {
        assertEq(nft.owner(), owner);
    }

    function test_deploy_totalSupplyZero() public view {
        assertEq(nft.totalSupply(), 0);
    }

    // ── Mint (onlyOwner) ───────────────────────────────────────────────

    function test_mint_createsToken() public {
        vm.prank(owner);
        uint256 tid = nft.mint(alice);
        assertEq(tid, 1);
        assertEq(nft.ownerOf(1), alice);
        assertEq(nft.totalSupply(), 1);
    }

    function test_mint_sequentialTokenIds() public {
        vm.startPrank(owner);
        assertEq(nft.mint(alice), 1);
        assertEq(nft.mint(bob), 2);
        assertEq(nft.mint(alice), 3);
        vm.stopPrank();

        assertEq(nft.ownerOf(1), alice);
        assertEq(nft.ownerOf(2), bob);
        assertEq(nft.ownerOf(3), alice);
        assertEq(nft.totalSupply(), 3);
    }

    function test_mint_revertsWhenNotOwner() public {
        vm.expectRevert(abi.encodeWithSelector(Ownable.OwnableUnauthorizedAccount.selector, alice));
        vm.prank(alice);
        nft.mint(alice);
    }

    // ── setTokenURI (inherited from ERC721URIStorage, onlyOwner) ──────

    function test_setTokenURI_setsURI() public {
        vm.startPrank(owner);
        nft.mint(alice);
        nft.setTokenURI(1, "ipfs://QmTest");
        vm.stopPrank();

        assertEq(nft.tokenURI(1), "ipfs://QmTest");
    }

    function test_setTokenURI_revertsWhenNotOwner() public {
        vm.startPrank(owner);
        nft.mint(alice);
        vm.stopPrank();

        vm.expectRevert(abi.encodeWithSelector(Ownable.OwnableUnauthorizedAccount.selector, bob));
        vm.prank(bob);
        nft.setTokenURI(1, "ipfs://QmHack");
    }

    // ── Transfer (standard ERC-721 semantics) ──────────────────────────

    function test_transferFrom_worksAfterMint() public {
        vm.prank(owner);
        nft.mint(alice);

        vm.prank(alice);
        nft.transferFrom(alice, bob, 1);
        assertEq(nft.ownerOf(1), bob);
    }

    function test_transferFrom_revertsWhenNotOwner() public {
        vm.prank(owner);
        nft.mint(alice);

        vm.expectRevert();
        vm.prank(bob);
        nft.transferFrom(alice, bob, 1);
    }

    // ── Approvals ──────────────────────────────────────────────────────

    function test_approve_allowsTransferFromApproved() public {
        vm.prank(owner);
        nft.mint(alice);

        vm.prank(alice);
        nft.approve(bob, 1);

        vm.prank(bob);
        nft.transferFrom(alice, bob, 1);
        assertEq(nft.ownerOf(1), bob);
    }

    function test_setApprovalForAll_allowsOperator() public {
        vm.prank(owner);
        nft.mint(alice);

        vm.prank(alice);
        nft.setApprovalForAll(bob, true);

        vm.prank(bob);
        nft.transferFrom(alice, bob, 1);
        assertEq(nft.ownerOf(1), bob);
    }

    // ── Ownership transfer ─────────────────────────────────────────────

    function test_transferOwnership_changesOwner() public {
        vm.prank(owner);
        nft.transferOwnership(alice);

        assertEq(nft.owner(), alice);

        // Old owner can no longer mint
        vm.expectRevert(abi.encodeWithSelector(Ownable.OwnableUnauthorizedAccount.selector, owner));
        vm.prank(owner);
        nft.mint(bob);
    }

    function test_transferOwnership_revertsWhenNotOwner() public {
        vm.expectRevert(abi.encodeWithSelector(Ownable.OwnableUnauthorizedAccount.selector, alice));
        vm.prank(alice);
        nft.transferOwnership(alice);
    }

    // ── tokenURI for nonexistent token ─────────────────────────────────

    function test_tokenURI_revertsForNonexistentToken() public {
        vm.expectRevert();
        nft.tokenURI(999);
    }
}
