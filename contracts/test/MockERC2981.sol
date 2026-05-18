// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {ERC721}   from "@openzeppelin/contracts/token/ERC721/ERC721.sol";
import {IERC2981} from "@openzeppelin/contracts/interfaces/IERC2981.sol";
import {IERC165}  from "@openzeppelin/contracts/utils/introspection/IERC165.sol";

/// @dev ERC-721 that returns a fixed royalty on every royaltyInfo() call.
contract MockERC2981 is ERC721, IERC2981 {
    uint256 public next;
    address public royaltyReceiver;
    uint16  public royaltyBps;

    constructor(address receiver, uint16 bps) ERC721("MockRoyalty", "MRY") {
        royaltyReceiver = receiver;
        royaltyBps      = bps;
    }

    function mint(address to) external returns (uint256 id) {
        id = ++next;
        _safeMint(to, id);
    }

    function royaltyInfo(uint256, uint256 salePrice)
        external view override returns (address receiver, uint256 royaltyAmount)
    {
        receiver      = royaltyReceiver;
        royaltyAmount = (salePrice * royaltyBps) / 10_000;
    }

    function supportsInterface(bytes4 interfaceId)
        public view override(ERC721, IERC165) returns (bool)
    {
        return interfaceId == type(IERC2981).interfaceId || super.supportsInterface(interfaceId);
    }
}
