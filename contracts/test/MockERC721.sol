// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {ERC721} from "@openzeppelin/contracts/token/ERC721/ERC721.sol";

contract MockERC721 is ERC721 {
    uint256 public next;
    constructor() ERC721("Mock", "MCK") {
        _mint(msg.sender, 0); // token 0 for offerEligible gating (skip _safeMint — test contracts may lack onERC721Received)
        next = 0; // mint() uses ++next = 1, preserving existing behavior
    }
    function mint(address to) external returns (uint256 id) { id = ++next; _safeMint(to, id); }
}
