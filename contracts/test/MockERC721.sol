// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {ERC721} from "@openzeppelin/contracts/token/ERC721/ERC721.sol";

contract MockERC721 is ERC721 {
    uint256 public next;
    // ERC-173 owner: the deployer. setOfferEligible authorizes via owner()
    // now that the token-0 fallback is gone (CodeRabbit 2026-08-23).
    address public owner;

    constructor() ERC721("Mock", "MCK") {
        owner = msg.sender;
        _mint(msg.sender, 0); // token 0 for offerEligible gating (skip _safeMint — test contracts may lack onERC721Received)
        next = 0; // mint() uses ++next = 1, preserving existing behavior
    }
    function mint(address to) external returns (uint256 id) { id = ++next; _safeMint(to, id); }
}
