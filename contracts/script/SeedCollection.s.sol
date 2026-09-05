// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {Script, console2} from "forge-std/Script.sol";
import {ERC721} from "@openzeppelin/contracts/token/ERC721/ERC721.sol";
import {Base64} from "@openzeppelin/contracts/utils/Base64.sol";
import {Strings} from "@openzeppelin/contracts/utils/Strings.sol";

/// @title MagicWebb Genesis — open-mint TESTNET seed collection
/// @notice Fully self-contained: metadata and art live on-chain as data:
///         URIs, so the indexer's metadata worker can resolve every token
///         without IPFS and the verifier can mark the collection Verified
///         (ERC-165 ok + metadata resolved) and Authentic (ERC-173 owner).
///         Anyone may mint (testnet only — gives live testers something to
///         list, auction and offer on). Max supply keeps it tidy.
contract MagicWebbGenesis is ERC721 {
    using Strings for uint256;

    /// @notice ERC-173 owner — the deployer. Read by the verifier sweep
    ///         (creator badge) and by OfferBook.setOfferEligible.
    address public owner;
    uint256 public next;
    uint256 public constant MAX_SUPPLY = 1000;

    constructor() ERC721("MagicWebb Genesis", "GENESIS") {
        owner = msg.sender;
    }

    /// @notice Open mint, testnet only. One call, one token.
    function mint() external returns (uint256 id) {
        require(next < MAX_SUPPLY, "sold out");
        id = ++next;
        _safeMint(msg.sender, id);
    }

    /// @notice Batch mint for seeding (still open — testnet).
    function mintMany(uint256 n) external {
        require(n > 0 && n <= 20, "1-20");
        require(next + n <= MAX_SUPPLY, "sold out");
        for (uint256 i; i < n; ++i) {
            _safeMint(msg.sender, ++next);
        }
    }

    function tokenURI(uint256 id) public view override returns (string memory) {
        require(_exists(id), "no token");
        // Deterministic three-colour palette per token.
        uint256 h = uint256(keccak256(abi.encodePacked(id)));
        string memory hue1 = ((h % 360)).toString();
        string memory hue2 = (((h >> 16) % 360)).toString();
        string memory svg = string.concat(
            '<svg xmlns="http://www.w3.org/2000/svg" width="512" height="512">',
            '<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">',
            '<stop offset="0" stop-color="hsl(', hue1, ',70%,55%)"/>',
            '<stop offset="1" stop-color="hsl(', hue2, ',70%,25%)"/>',
            "</linearGradient></defs>",
            '<rect width="512" height="512" fill="url(#g)"/>',
            '<circle cx="256" cy="230" r="', ((h >> 32) % 90 + 60).toString(), '" fill="hsl(', hue2, ',80%,70%)" opacity="0.85"/>',
            '<text x="256" y="440" font-family="monospace" font-size="42" fill="white" text-anchor="middle">GENESIS #', id.toString(),
            "</text></svg>"
        );
        string memory json = string.concat(
            '{"name":"Genesis #', id.toString(),
            '","description":"MagicWebb Genesis - on-chain testnet seed collection. Free to mint, made for trying listings, auctions and offers.",',
            '"image":"data:image/svg+xml;base64,', Base64.encode(bytes(svg)), '"}'
        );
        return string.concat("data:application/json;base64,", Base64.encode(bytes(json)));
    }
}

/// Deploys the seed collection and mints 6 tokens to the broadcaster.
contract SeedCollection is Script {
    function run() external {
        uint256 pk = vm.envUint("PRIVATE_KEY");
        vm.startBroadcast(pk);
        MagicWebbGenesis nft = new MagicWebbGenesis();
        nft.mintMany(6);
        vm.stopBroadcast();
        console2.log("GENESIS_ADDR=", address(nft));
        console2.log("minted 1-6 to", vm.addr(pk));
    }
}
