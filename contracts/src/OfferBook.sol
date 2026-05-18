// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {MarketplaceCore, TokenStandard, WithdrawFailed} from "./MarketplaceCore.sol";
import {IERC721}  from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155} from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";
import {EIP712}   from "@openzeppelin/contracts/utils/cryptography/EIP712.sol";
import {ECDSA}    from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";

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
/// @dev IMMUTABLE fee config + EIP-712 domain ("MagicWebbOfferBook", "1") — DO NOT change post-deploy.
///      Pausable via `pause()` (PAUSER_ROLE). `withdrawRefund`, `cancelOffer`, and `withdraw` work
///      while paused so bidders can always reclaim their funds.
contract OfferBook is MarketplaceCore, EIP712 {
    using ECDSA for bytes32;

    /// @notice ERC-721 offer typed data.
    /// @dev `tokenId == 0` is the collection-wide sentinel (any token in `collection` may satisfy).
    ///      WARNING: collections that mint real tokenId 0 cannot receive a single-id offer for id 0.
    ///      Frontends MUST surface a collection-wide offer for tokenId 0 instead.
    struct Offer {
        address bidder;
        address collection;
        uint256 tokenId;   // 0 = collection-wide
        uint128 amount;
        uint64  expiresAt;
        uint64  nonce;
    }

    bytes32 private constant OFFER_TYPEHASH = keccak256(
        "Offer(address bidder,address collection,uint256 tokenId,uint128 amount,uint64 expiresAt,uint64 nonce)"
    );

    /// @notice ERC-1155 offer typed data.
    struct Offer1155 {
        address bidder;
        address collection;
        uint256 tokenId;
        uint128 units;   // number of 1155 units sought
        uint128 amount;  // total wei offered for `units`
        uint64  expiresAt;
        uint64  nonce;
    }

    bytes32 private constant OFFER1155_TYPEHASH = keccak256(
        "Offer1155(address bidder,address collection,uint256 tokenId,uint128 units,uint128 amount,uint64 expiresAt,uint64 nonce)"
    );

    /// @notice Burned nonces — consumed by accept or pre-emptively cancelled.
    mapping(address => mapping(uint64 => bool)) public usedNonce;
    /// @notice Refundable deposit balance per bidder.
    mapping(address => uint256) public deposits;

    event Deposited(address indexed bidder, uint256 amount, uint256 newBalance);
    event Withdrawn(address indexed bidder, uint256 amount, uint256 newBalance);
    event OfferAccepted(
        address indexed coll, uint256 indexed tokenId, address indexed seller,
        address bidder, uint128 amount, uint256 fee, uint256 royalty, uint64 nonce
    );
    event Offer1155Accepted(
        address indexed coll, uint256 indexed tokenId, address indexed seller,
        address bidder, uint128 units, uint128 amount, uint256 fee, uint256 royalty, uint64 nonce
    );
    event OfferCancelled(address indexed bidder, uint64 indexed nonce);

    constructor(address vault, uint16 fee, address admin)
        MarketplaceCore(vault, fee, admin)
        EIP712("MagicWebbOfferBook", "1")
    {}

    // ── Deposit / Withdraw ────────────────────────────────────────────────

    function deposit() external payable whenNotPaused {
        deposits[msg.sender] += msg.value;
        emit Deposited(msg.sender, msg.value, deposits[msg.sender]);
    }

    /// @notice Withdraw from deposit balance. Works while paused.
    function withdraw(uint256 amount) external nonReentrant {
        uint256 bal = deposits[msg.sender];
        if (amount > bal) revert InsufficientFunds();
        unchecked { deposits[msg.sender] = bal - amount; }
        (bool ok,) = msg.sender.call{value: amount}("");
        if (!ok) revert WithdrawFailed();
        emit Withdrawn(msg.sender, amount, deposits[msg.sender]);
    }

    // ── Offer management ─────────────────────────────────────────────────

    /// @notice Cancel a nonce pre-emptively. Works while paused.
    function cancelOffer(uint64 nonce) external {
        usedNonce[msg.sender][nonce] = true;
        emit OfferCancelled(msg.sender, nonce);
    }

    // ── EIP-712 helpers ───────────────────────────────────────────────────

    function hashOffer(Offer calldata o) public view returns (bytes32) {
        return _hashTypedDataV4(keccak256(abi.encode(
            OFFER_TYPEHASH, o.bidder, o.collection, o.tokenId, o.amount, o.expiresAt, o.nonce
        )));
    }

    function hashOffer1155(Offer1155 calldata o) public view returns (bytes32) {
        return _hashTypedDataV4(keccak256(abi.encode(
            OFFER1155_TYPEHASH, o.bidder, o.collection, o.tokenId, o.units, o.amount, o.expiresAt, o.nonce
        )));
    }

    // ── Accept ────────────────────────────────────────────────────────────

    /// @notice Token owner accepts a signed ERC-721 offer. FINAL on success.
    ///         `tokenIdActual` must equal `o.tokenId` unless `o.tokenId == 0`.
    function acceptOffer(Offer calldata o, bytes calldata sig, uint256 tokenIdActual)
        external nonReentrant whenNotPaused
    {
        if (o.collection == address(0) || o.bidder == address(0)) revert ZeroOffer();
        if (o.amount == 0) revert ZeroOffer();
        if (block.timestamp > o.expiresAt) revert OfferExpired();
        if (usedNonce[o.bidder][o.nonce]) revert OfferUsed();
        if (o.tokenId != 0 && o.tokenId != tokenIdActual) revert WrongToken();

        if (hashOffer(o).recover(sig) != o.bidder) revert InvalidSig();

        if (IERC721(o.collection).ownerOf(tokenIdActual) != msg.sender) revert NotOwner();
        if (!IERC721(o.collection).isApprovedForAll(msg.sender, address(this))
            && IERC721(o.collection).getApproved(tokenIdActual) != address(this)) revert NotApproved();

        if (deposits[o.bidder] < o.amount) revert InsufficientFunds();

        usedNonce[o.bidder][o.nonce] = true;
        unchecked { deposits[o.bidder] -= o.amount; }

        _transferToken(TokenStandard.ERC721, o.collection, msg.sender, o.bidder, tokenIdActual, 1);
        (uint256 fee, uint256 royalty,) = _splitAndPay(msg.sender, o.amount, o.collection, tokenIdActual);

        emit OfferAccepted(o.collection, tokenIdActual, msg.sender, o.bidder, o.amount, fee, royalty, o.nonce);
    }

    /// @notice Seller accepts a signed ERC-1155 offer. FINAL on success.
    function acceptOffer1155(Offer1155 calldata o, bytes calldata sig)
        external nonReentrant whenNotPaused
    {
        if (o.units == 0) revert InvalidAmount();
        if (o.amount == 0) revert ZeroOffer();
        if (block.timestamp > o.expiresAt) revert OfferExpired();
        if (usedNonce[o.bidder][o.nonce]) revert OfferUsed();

        if (hashOffer1155(o).recover(sig) != o.bidder) revert InvalidSig();

        if (IERC1155(o.collection).balanceOf(msg.sender, o.tokenId) < o.units) revert NotOwner();
        if (!IERC1155(o.collection).isApprovedForAll(msg.sender, address(this))) revert NotApproved();

        if (deposits[o.bidder] < o.amount) revert InsufficientFunds();

        usedNonce[o.bidder][o.nonce] = true;
        unchecked { deposits[o.bidder] -= o.amount; }

        _transferToken(TokenStandard.ERC1155, o.collection, msg.sender, o.bidder, o.tokenId, o.units);
        (uint256 fee, uint256 royalty,) = _splitAndPay(msg.sender, o.amount, o.collection, o.tokenId);

        emit Offer1155Accepted(o.collection, o.tokenId, msg.sender, o.bidder, o.units, o.amount, fee, royalty, o.nonce);
    }
}
