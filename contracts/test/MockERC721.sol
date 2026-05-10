// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {ERC721} from "@openzeppelin/contracts/token/ERC721/ERC721.sol";

contract MockERC721 is ERC721 {
    uint256 public next;
    constructor() ERC721("Mock", "MCK") {}
    function mint(address to) external returns (uint256 id) { id = ++next; _safeMint(to, id); }
}
