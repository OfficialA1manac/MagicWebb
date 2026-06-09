// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {MarketplaceCore, TokenStandard, BelowMinPrice} from "./MarketplaceCore.sol";
import {IERC721}  from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155} from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";

error NotOwner();
error NoOffer();
error NotApproved();
error InvalidExpiry();
error InvalidAmount();
error WrongValue();
error OfferActive();

/// @dev Maximum offer lifetime from the latest top-up.
uint64 constant MAX_OFFER_DURATION = 14 days;

/// @title OfferBook
/// @notice On-chain NFT offers with stacked positions; the seller pays the fee on acceptance.
///
/// Fee model (seller-pays, Option-4 stacked positions):
///   - Anyone may offer on any NFT — no eligibility gate. Making an offer is FREE.
///   - makeOffer is PAYABLE: send exactly `principal`. The full amount is escrowed; the
///     offerer pays no fee.
///   - Multiple offers from the same bidder on the same NFT COMPOUND into one position;
///     each top-up refreshes the position's expiry.
///   - There is NO individual withdrawal. A position is locked until accept / reject / expiry.
///   - acceptOffer DEDUCTS a 1.5% platform fee from the principal; the seller receives 98.5%.
///   - rejectOffer (owner) or refundExpiredOffer (anyone, after expiry) returns the FULL
///     principal to the bidder — an offer that never sells costs nothing.
///
/// Non-custodial. No royalties. No off-chain signatures. No pause. Unstoppable once deployed.
contract OfferBook is MarketplaceCore {
    /// @notice A bidder's compounded offer on one NFT.
    struct Position {
        uint128       principal; // escrowed ETH (fees already removed)
        uint128       units;     // ERC-1155 units desired (1 for ERC-721)
        uint64        expiresAt; // refreshed on each top-up
        TokenStandard standard;  // token kind this offer targets
    }

    /// @notice positions[coll][tokenId][bidder] → Position.
    mapping(address => mapping(uint256 => mapping(address => Position))) public positions;

    // ── Events ──────────────────────────────────────────────────────────────────

    event OfferMade(
        address indexed coll,
        uint256 indexed tokenId,
        address indexed bidder,
        uint256 principal, // cumulative escrowed principal after this top-up
        uint128 units,
        uint64  expiresAt
    );
    event OfferAccepted(
        address indexed coll,
        uint256 indexed tokenId,
        address indexed seller,
        address bidder,
        uint256 principal, // gross accepted principal
        uint256 fee,       // 1.5% platform fee deducted from the seller
        uint128 units,
        TokenStandard standard
    );
    event OfferRefunded(address indexed coll, uint256 indexed tokenId, address indexed bidder, uint256 principal);

    constructor(address recipient, address manager_)
        MarketplaceCore(recipient, manager_)
    {}

    // ── Make offer (free; full principal escrowed) ─────────────────────────────

    /// @notice Offer on an ERC-721 token. Send exactly `principal` as msg.value. FREE.
    /// @param coll       NFT collection.
    /// @param tokenId    Token ID.
    /// @param principal  The escrowed offer amount (≥ MIN_PRICE). No fee at offer time.
    /// @param expiresAt  Position expiry (now < expiresAt ≤ now + 14 days).
    function makeOffer(address coll, uint256 tokenId, uint128 principal, uint64 expiresAt) external payable nonReentrant entryGate {
        _makeOffer(TokenStandard.ERC721, coll, tokenId, principal, 1, expiresAt);
    }

    /// @notice Offer on ERC-1155 units. Send exactly `principal` as msg.value. FREE.
    /// @param units  Number of ERC-1155 units desired (latest top-up wins).
    function makeOffer1155(address coll, uint256 tokenId, uint128 principal, uint128 units, uint64 expiresAt)
        external payable nonReentrant entryGate
    {
        if (units == 0) revert InvalidAmount();
        _makeOffer(TokenStandard.ERC1155, coll, tokenId, principal, units, expiresAt);
    }

    function _makeOffer(
        TokenStandard standard,
        address coll,
        uint256 tokenId,
        uint128 principal,
        uint128 units,
        uint64  expiresAt
    ) internal {
        if (principal < MIN_PRICE) revert BelowMinPrice();
        if (expiresAt <= block.timestamp || expiresAt > block.timestamp + MAX_OFFER_DURATION) revert InvalidExpiry();

        if (msg.value != uint256(principal)) revert WrongValue();

        Position storage p = positions[coll][tokenId][msg.sender];
        uint256 newPrincipal = uint256(p.principal) + principal;
        if (newPrincipal > type(uint128).max) revert InvalidAmount();
        p.principal = uint128(newPrincipal);
        p.units     = units;
        p.expiresAt = expiresAt;
        p.standard  = standard;

        emit OfferMade(coll, tokenId, msg.sender, p.principal, units, expiresAt);
    }

    // ── Accept (seller pays 1.5%; seller nets 98.5% of principal) ──────────────

    /// @notice Accept a bidder's full position. Caller must currently own/hold the NFT.
    ///         NFT → bidder, 1.5% fee → feeRecipient, principal − fee → seller.
    function acceptOffer(address coll, uint256 tokenId, address bidder) external nonReentrant entryGate {
        Position memory p = positions[coll][tokenId][bidder];
        if (p.principal == 0) revert NoOffer();

        if (p.standard == TokenStandard.ERC721) {
            if (IERC721(coll).ownerOf(tokenId) != msg.sender) revert NotOwner();
            if (!IERC721(coll).isApprovedForAll(msg.sender, address(this))
                && IERC721(coll).getApproved(tokenId) != address(this)) revert NotApproved();
        } else {
            if (IERC1155(coll).balanceOf(msg.sender, tokenId) < p.units) revert NotOwner();
            if (!IERC1155(coll).isApprovedForAll(msg.sender, address(this))) revert NotApproved();
        }

        delete positions[coll][tokenId][bidder];

        uint256 fee = _feeOf(p.principal);
        uint256 moveAmount = p.standard == TokenStandard.ERC721 ? 1 : p.units;
        _transferToken(p.standard, coll, msg.sender, bidder, tokenId, moveAmount);
        _payFee(fee);
        _pay(msg.sender, uint256(p.principal) - fee); // seller nets 98.5%

        emit OfferAccepted(coll, tokenId, msg.sender, bidder, p.principal, fee, p.units, p.standard);
    }

    // ── Reject / expire (full principal refunded — offers are free) ────────────

    /// @notice Owner rejects a bidder's offer, refunding the full principal.
    function rejectOffer(address coll, uint256 tokenId, address bidder) external nonReentrant {
        Position memory p = positions[coll][tokenId][bidder];
        if (p.principal == 0) revert NoOffer();

        if (p.standard == TokenStandard.ERC721) {
            if (IERC721(coll).ownerOf(tokenId) != msg.sender) revert NotOwner();
        } else {
            if (IERC1155(coll).balanceOf(msg.sender, tokenId) == 0) revert NotOwner();
        }

        delete positions[coll][tokenId][bidder];
        _pay(bidder, p.principal); // full refund — no fee was charged at offer time
        emit OfferRefunded(coll, tokenId, bidder, p.principal);
    }

    /// @notice Reclaim an expired position's principal. Permissionless (keeper or bidder).
    ///         Full principal refunded — no fee was charged at offer time.
    function refundExpiredOffer(address coll, uint256 tokenId, address bidder) external nonReentrant {
        Position memory p = positions[coll][tokenId][bidder];
        if (p.principal == 0) revert NoOffer();
        if (block.timestamp <= p.expiresAt) revert OfferActive();

        delete positions[coll][tokenId][bidder];
        _pay(bidder, p.principal);
        emit OfferRefunded(coll, tokenId, bidder, p.principal);
    }
}
