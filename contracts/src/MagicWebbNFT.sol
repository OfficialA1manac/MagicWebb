// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {ERC721} from "@openzeppelin/contracts/token/ERC721/ERC721.sol";
import {ERC721URIStorage} from "@openzeppelin/contracts/token/ERC721/extensions/ERC721URIStorage.sol";
import {Ownable} from "@openzeppelin/contracts/access/Ownable.sol";

/// @title MagicWebbNFT
/// @notice Minimal, auditable ERC-721 contract for MagicWebb marketplace NFTs.
///         Sequential mint, per-token URI storage, owner-only minting.
///         No upgradeability, no burn, no batch mint — deliberately minimal
///         surface area for auditability and mainnet safety.
///
///         C-03 audit fix: replaces the previous out-of-repo NFT contract at
///         0x0E513BfE29E00E160ADE7516AD9363F070a101bF (Coston2) with a
///         source-controlled, tested, production-grade contract.
contract MagicWebbNFT is ERC721, ERC721URIStorage, Ownable {
    uint256 private _nextTokenId;

    /// @param name   ERC-721 token name (e.g. "Magic Webb Animi")
    /// @param symbol ERC-721 token symbol (e.g. "ANIMI")
    /// @param owner  Initial owner — holds mint + setTokenURI authority.
    ///               Should be the deployer's wallet or a multisig.
    constructor(string memory name, string memory symbol, address owner)
        ERC721(name, symbol)
        Ownable(owner)
    {}

    /// @notice Mint a new token to `to`. Token ID is sequential starting at 1.
    ///         Only callable by the current owner.
    /// @param to Recipient address for the newly minted token.
    /// @return tokenId The ID of the minted token.
    function mint(address to) external onlyOwner returns (uint256 tokenId) {
        tokenId = ++_nextTokenId;
        _safeMint(to, tokenId);
    }

    /// @notice Set the token URI for a given token ID. Overwrites any existing URI.
    ///         Only callable by the current owner (typically set at mint time).
    /// @param tokenId The token to set the URI for.
    /// @param uri     The new token URI (e.g. ipfs://, https://, ar://).
    function setTokenURI(uint256 tokenId, string calldata uri) external onlyOwner {
        _setTokenURI(tokenId, uri);
    }

    /// @notice Total number of tokens minted so far. Replaces the derived
    ///         totalSupply() since we track the counter explicitly.
    function totalSupply() external view returns (uint256) {
        return _nextTokenId;
    }

    // ── Required overrides ──────────────────────────────────────────────────

    /// @inheritdoc ERC721
    function tokenURI(uint256 tokenId)
        public
        view
        override(ERC721, ERC721URIStorage)
        returns (string memory)
    {
        return super.tokenURI(tokenId);
    }

    /// @inheritdoc ERC721
    function supportsInterface(bytes4 interfaceId)
        public
        view
        override(ERC721, ERC721URIStorage)
        returns (bool)
    {
        return super.supportsInterface(interfaceId);
    }
}
