// SPDX-License-Identifier: MIT
pragma solidity 0.8.24;

import {MarketplaceCore, TokenStandard, WithdrawFailed} from "./MarketplaceCore.sol";
import {IERC721} from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155} from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";
import {EIP712} from "@openzeppelin/contracts/utils/cryptography/EIP712.sol";
import {ECDSA} from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";

error InvalidSig();
error OfferExpired();
error OfferUsed();
error NotOwner();
error WrongToken();
error InsufficientFunds();
error NotApproved();
error InvalidAmount();
error ZeroOffer();

/// @title OfferBook
/// @notice Off-chain signed EIP-712 offers redeemable on-chain.
/// @dev IMMUTABLE: no admin, no pause. Once an offer is accepted, the trade is FINAL — NFT to bidder,
///      fee → `feeVault`, remainder → seller. The bidder can pre-emptively burn a nonce via `cancelOffer`
///      to invalidate an unaccepted signed offer; this is a pre-trade action, not a reversal.
///      Domain `("MagicWebbOfferBook", "1")` is part of the signed digest — DO NOT change post-deploy.
contract OfferBook is MarketplaceCore, EIP712 {
    using ECDSA for bytes32;

    /// @notice Offer typed data.
    /// @dev `tokenId == 0` is the collection-wide sentinel — any token in `collection`
    ///      may be used to satisfy the offer (set `tokenIdActual` on accept).
    /// @dev WARNING: collections that mint a real `tokenId == 0` cannot receive a
    ///      single-token offer for that id via this struct (it will be treated as
    ///      collection-wide). Frontends MUST disable single-id offers for tokenId 0
    ///      and surface a collection-wide offer instead, or use an alternative path.
    struct Offer {
        address bidder;
        address collection;
        uint256 tokenId;     // 0 == collection-wide
        uint128 amount;
        uint64  expiresAt;
        uint64  nonce;
    }

    bytes32 private constant OFFER_TYPEHASH = keccak256(
        "Offer(address bidder,address collection,uint256 tokenId,uint128 amount,uint64 expiresAt,uint64 nonce)"
    );

    /// @notice ERC-1155 offer typed data. Separate typehash; coexists under the same (immutable) domain.
    struct Offer1155 {
        address bidder;
        address collection;
        uint256 tokenId;     // exact (collection-wide not supported for 1155 in MVP)
        uint128 units;       // number of 1155 units sought
        uint128 amount;      // total wei offered for `units`
        uint64  expiresAt;
        uint64  nonce;
    }

    bytes32 private constant OFFER1155_TYPEHASH = keccak256(
        "Offer1155(address bidder,address collection,uint256 tokenId,uint128 units,uint128 amount,uint64 expiresAt,uint64 nonce)"
    );

    /// @notice Burned nonces by bidder; either consumed by accept or pre-emptively cancelled.
    mapping(address => mapping(uint64 => bool)) public usedNonce;
    /// @notice Refundable deposit balance per bidder.
    mapping(address => uint256) public deposits;

    event Deposited(address indexed bidder, uint256 amount, uint256 newBalance);
    event Withdrawn(address indexed bidder, uint256 amount, uint256 newBalance);
    event OfferAccepted(address indexed coll, uint256 indexed tokenId, address indexed seller, address bidder, uint128 amount, uint256 fee, uint64 nonce);
    event Offer1155Accepted(address indexed coll, uint256 indexed tokenId, address indexed seller, address bidder, uint128 units, uint128 amount, uint256 fee, uint64 nonce);
    event OfferCancelled(address indexed bidder, uint64 indexed nonce);

    constructor(address vault, uint16 fee)
        MarketplaceCore(vault, fee)
        EIP712("MagicWebbOfferBook", "1")
    {}

    /// @notice Top up bidder deposit balance.
    function deposit() external payable {
        deposits[msg.sender] += msg.value;
        emit Deposited(msg.sender, msg.value, deposits[msg.sender]);
    }

    /// @notice Withdraw from deposit balance.
    function withdraw(uint256 amount) external nonReentrant {
        uint256 bal = deposits[msg.sender];
        if (amount > bal) revert InsufficientFunds();
        unchecked { deposits[msg.sender] = bal - amount; }
        (bool ok,) = msg.sender.call{value: amount}("");
        if (!ok) revert WithdrawFailed();
        emit Withdrawn(msg.sender, amount, deposits[msg.sender]);
    }

    /// @notice Burn a nonce so any signed offer with that nonce becomes unredeemable. Pre-trade only.
    function cancelOffer(uint64 nonce) external {
        usedNonce[msg.sender][nonce] = true;
        emit OfferCancelled(msg.sender, nonce);
    }

    /// @notice EIP-712 digest for an offer. Used by both signers and `acceptOffer`.
    function hashOffer(Offer calldata o) public view returns (bytes32) {
        return _hashTypedDataV4(keccak256(abi.encode(
            OFFER_TYPEHASH, o.bidder, o.collection, o.tokenId, o.amount, o.expiresAt, o.nonce
        )));
    }

    /// @notice Token owner accepts a signed offer. FINAL on success — `tokenIdActual` must equal
    ///         `o.tokenId` unless `o.tokenId == 0`. Fee → `feeVault`, remainder → seller.
    function acceptOffer(Offer calldata o, bytes calldata sig, uint256 tokenIdActual)
        external nonReentrant
    {
        if (o.amount == 0) revert ZeroOffer();
        if (block.timestamp > o.expiresAt) revert OfferExpired();
        if (usedNonce[o.bidder][o.nonce]) revert OfferUsed();
        if (o.tokenId != 0 && o.tokenId != tokenIdActual) revert WrongToken();

        bytes32 digest = hashOffer(o);
        address signer = digest.recover(sig);
        if (signer != o.bidder) revert InvalidSig();

        if (IERC721(o.collection).ownerOf(tokenIdActual) != msg.sender) revert NotOwner();
        if (!IERC721(o.collection).isApprovedForAll(msg.sender, address(this))
            && IERC721(o.collection).getApproved(tokenIdActual) != address(this)) revert NotApproved();

        if (deposits[o.bidder] < o.amount) revert InsufficientFunds();

        usedNonce[o.bidder][o.nonce] = true;
        unchecked { deposits[o.bidder] -= o.amount; }

        _transferToken(TokenStandard.ERC721, o.collection, msg.sender, o.bidder, tokenIdActual, 1);
        (uint256 fee,) = _splitAndPay(msg.sender, o.amount);

        emit OfferAccepted(o.collection, tokenIdActual, msg.sender, o.bidder, o.amount, fee, o.nonce);
    }

    /// @notice EIP-712 digest for an ERC-1155 offer.
    function hashOffer1155(Offer1155 calldata o) public view returns (bytes32) {
        return _hashTypedDataV4(keccak256(abi.encode(
            OFFER1155_TYPEHASH, o.bidder, o.collection, o.tokenId, o.units, o.amount, o.expiresAt, o.nonce
        )));
    }

    /// @notice Seller accepts a signed ERC-1155 offer for `o.units` units of `o.tokenId`. FINAL on success.
    function acceptOffer1155(Offer1155 calldata o, bytes calldata sig)
        external nonReentrant
    {
        if (o.units == 0) revert InvalidAmount();
        if (o.amount == 0) revert ZeroOffer();
        if (block.timestamp > o.expiresAt) revert OfferExpired();
        if (usedNonce[o.bidder][o.nonce]) revert OfferUsed();

        bytes32 digest = hashOffer1155(o);
        address signer = digest.recover(sig);
        if (signer != o.bidder) revert InvalidSig();

        if (IERC1155(o.collection).balanceOf(msg.sender, o.tokenId) < o.units) revert NotOwner();
        if (!IERC1155(o.collection).isApprovedForAll(msg.sender, address(this))) revert NotApproved();

        if (deposits[o.bidder] < o.amount) revert InsufficientFunds();

        usedNonce[o.bidder][o.nonce] = true;
        unchecked { deposits[o.bidder] -= o.amount; }

        _transferToken(TokenStandard.ERC1155, o.collection, msg.sender, o.bidder, o.tokenId, o.units);
        (uint256 fee,) = _splitAndPay(msg.sender, o.amount);

        emit Offer1155Accepted(o.collection, o.tokenId, msg.sender, o.bidder, o.units, o.amount, fee, o.nonce);
    }
}
