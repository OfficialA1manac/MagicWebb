// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {MarketplaceCore, TokenStandard, WithdrawFailed} from "./MarketplaceCore.sol";
import {IERC721}  from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155} from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";

error NotOwner();
error NoPosition();
error NotApproved();
error InvalidAmount();
error InvalidDuration();
error PositionExpired();
error PositionLive();
error PositionExists();

/// @title OfferBook
/// @notice Stacked, time-locked NFT offers (Option-4).
///
/// Flow (ERC-721):
///   1. (optional) Owner calls markOfferEligible — purely informational.
///   2. Bidder calls makeOffer(coll, id, offerAmount, duration) with
///      msg.value = offerAmount + 1.5% fee. First call sets expiresAt; subsequent
///      calls compound into one position WITHOUT extending expiry. Fee component
///      is forwarded to feeRecipient immediately on every call (non-refundable).
///   3. Owner calls acceptOffer(coll, id, bidder). Owner pays no fee — receives
///      the entire totalOffer. NFT → bidder. Position cleared.
///   4. After expiry, anyone calls refundExpired(coll, id, bidder) → totalOffer
///      principal sent back to bidder, position deleted. Fees stay with platform.
///
/// No individual / partial withdrawal exists. Position is locked until accept,
/// expire, or seller "reject" by simply ignoring it past expiry.
///
/// Same NFT may carry offers from many bidders simultaneously and live in other
/// markets at the same time — first settle wins.
contract OfferBook is MarketplaceCore {
    uint64 public constant DEFAULT_DURATION = 3 days;
    uint64 public constant MAX_DURATION     = 14 days;

    // ── ERC-721 ────────────────────────────────────────────────────────────

    /// @notice Informational opt-in. `eligible[coll][id] != address(0)` signals
    ///         the owner is open to offers. Not required to make an offer.
    mapping(address => mapping(uint256 => address)) public eligible;

    /// @notice Aggregated offer position from a single bidder on a single token.
    ///         Multiple deposits compound into one position; expiresAt is set on
    ///         first deposit and never extends.
    struct OfferPosition {
        uint128 totalOffer;
        uint128 totalFeePaid;
        uint64  firstAt;
        uint64  expiresAt;
    }

    /// @notice positions[coll][tokenId][bidder]
    mapping(address => mapping(uint256 => mapping(address => OfferPosition))) public positions;

    // ── ERC-1155 ──────────────────────────────────────────────────────────

    mapping(address => mapping(uint256 => address)) public eligible1155;

    struct OfferPosition1155 {
        uint128 totalOffer;
        uint128 totalFeePaid;
        uint128 units;
        uint64  firstAt;
        uint64  expiresAt;
    }

    mapping(address => mapping(uint256 => mapping(address => OfferPosition1155))) public positions1155;

    // ── Events ─────────────────────────────────────────────────────────────

    event EligibilityMarked(address indexed coll, uint256 indexed tokenId, address indexed owner);
    event EligibilityRemoved(address indexed coll, uint256 indexed tokenId, address indexed owner);
    event Eligibility1155Marked(address indexed coll, uint256 indexed tokenId, address indexed owner);
    event Eligibility1155Removed(address indexed coll, uint256 indexed tokenId, address indexed owner);

    /// @notice Emitted on every (compounding) offer deposit and on accept/expire.
    event OfferPositionUpdated(
        address indexed coll,
        uint256 indexed tokenId,
        address indexed bidder,
        uint128 totalOffer,
        uint128 totalFeePaid,
        uint64  expiresAt
    );
    event OfferRefunded(address indexed coll, uint256 indexed tokenId, address indexed bidder, uint128 amount);
    event OfferAccepted(
        address indexed coll,
        uint256 indexed tokenId,
        address indexed seller,
        address bidder,
        uint128 amount,
        uint256 fee
    );
    event Offer1155PositionUpdated(
        address indexed coll,
        uint256 indexed tokenId,
        address indexed bidder,
        uint128 totalOffer,
        uint128 totalFeePaid,
        uint128 units,
        uint64  expiresAt
    );
    event Offer1155Refunded(address indexed coll, uint256 indexed tokenId, address indexed bidder, uint128 amount);
    event Offer1155Accepted(
        address indexed coll,
        uint256 indexed tokenId,
        address indexed seller,
        address bidder,
        uint128 units,
        uint128 amount,
        uint256 fee
    );

    constructor(address recipient)
        MarketplaceCore(recipient)
    {}

    // ── ERC-721 eligibility ───────────────────────────────────────────────

    function markOfferEligible(address coll, uint256 tokenId) external {
        if (IERC721(coll).ownerOf(tokenId) != msg.sender) revert NotOwner();
        eligible[coll][tokenId] = msg.sender;
        emit EligibilityMarked(coll, tokenId, msg.sender);
    }

    function removeOfferEligible(address coll, uint256 tokenId) external {
        if (eligible[coll][tokenId] != msg.sender) revert NotOwner();
        delete eligible[coll][tokenId];
        emit EligibilityRemoved(coll, tokenId, msg.sender);
    }

    // ── ERC-721 offers (stacked) ───────────────────────────────────────────

    /// @notice Deposit ETH into your stacked offer position on a token.
    ///         msg.value MUST equal offerAmount + 1.5% fee. Fee is forwarded
    ///         to feeRecipient immediately and is non-refundable.
    ///         On the first call, `duration` is honored (capped at MAX_DURATION,
    ///         defaulted to DEFAULT_DURATION if 0). Subsequent calls do NOT
    ///         extend expiresAt — they only add principal.
    function makeOffer(address coll, uint256 tokenId, uint128 offerAmount, uint64 duration)
        external payable nonReentrant
    {
        _checkMin(offerAmount);
        uint256 fee = _feeOnTop(offerAmount);
        if (msg.value != uint256(offerAmount) + fee) revert InvalidAmount();

        OfferPosition storage p = positions[coll][tokenId][msg.sender];

        if (p.totalOffer == 0) {
            // New position
            uint64 dur = duration == 0 ? DEFAULT_DURATION : duration;
            if (dur > MAX_DURATION) revert InvalidDuration();
            p.firstAt   = uint64(block.timestamp);
            p.expiresAt = uint64(block.timestamp) + dur;
        } else {
            // Compounding deposit
            if (block.timestamp >= p.expiresAt) revert PositionExpired();
        }

        p.totalOffer   += offerAmount;
        p.totalFeePaid += uint128(fee);

        // Forward fee immediately (non-refundable on expiry/reject)
        if (fee > 0) {
            (bool ok,) = feeRecipient.call{value: fee}("");
            if (!ok) revert WithdrawFailed();
        }

        emit OfferPositionUpdated(coll, tokenId, msg.sender, p.totalOffer, p.totalFeePaid, p.expiresAt);
    }

    /// @notice Refund an expired offer position. Anyone may call.
    function refundExpired(address coll, uint256 tokenId, address bidder) external nonReentrant {
        OfferPosition storage p = positions[coll][tokenId][bidder];
        if (p.totalOffer == 0) revert NoPosition();
        if (block.timestamp < p.expiresAt) revert PositionLive();

        uint128 amt = p.totalOffer;
        delete positions[coll][tokenId][bidder];

        (bool ok,) = bidder.call{value: amt}("");
        if (!ok) revert WithdrawFailed();

        emit OfferRefunded(coll, tokenId, bidder, amt);
        emit OfferPositionUpdated(coll, tokenId, bidder, 0, 0, 0);
    }

    /// @notice Accept a stacked offer position. Caller must be current NFT owner.
    ///         No fee on the seller side — fees were paid up-front by the bidder.
    function acceptOffer(address coll, uint256 tokenId, address bidder) external nonReentrant {
        if (IERC721(coll).ownerOf(tokenId) != msg.sender) revert NotOwner();
        if (!IERC721(coll).isApprovedForAll(msg.sender, address(this))
            && IERC721(coll).getApproved(tokenId) != address(this)) revert NotApproved();

        OfferPosition storage p = positions[coll][tokenId][bidder];
        if (p.totalOffer == 0) revert NoPosition();
        if (block.timestamp >= p.expiresAt) revert PositionExpired();

        uint128 amt = p.totalOffer;
        uint128 fee = p.totalFeePaid;

        delete positions[coll][tokenId][bidder];
        delete eligible[coll][tokenId];

        _transferToken(TokenStandard.ERC721, coll, msg.sender, bidder, tokenId, 1);

        (bool ok,) = msg.sender.call{value: amt}("");
        if (!ok) revert WithdrawFailed();

        emit OfferAccepted(coll, tokenId, msg.sender, bidder, amt, fee);
        emit OfferPositionUpdated(coll, tokenId, bidder, 0, 0, 0);
    }

    // ── ERC-1155 eligibility ──────────────────────────────────────────────

    function markOfferEligible1155(address coll, uint256 tokenId) external {
        if (IERC1155(coll).balanceOf(msg.sender, tokenId) == 0) revert NotOwner();
        eligible1155[coll][tokenId] = msg.sender;
        emit Eligibility1155Marked(coll, tokenId, msg.sender);
    }

    function removeOfferEligible1155(address coll, uint256 tokenId) external {
        if (eligible1155[coll][tokenId] != msg.sender) revert NotOwner();
        delete eligible1155[coll][tokenId];
        emit Eligibility1155Removed(coll, tokenId, msg.sender);
    }

    // ── ERC-1155 offers (stacked) ─────────────────────────────────────────

    function makeOffer1155(
        address coll,
        uint256 tokenId,
        uint128 offerAmount,
        uint128 units,
        uint64  duration
    ) external payable nonReentrant {
        _checkMin(offerAmount);
        if (units == 0) revert InvalidAmount();
        uint256 fee = _feeOnTop(offerAmount);
        if (msg.value != uint256(offerAmount) + fee) revert InvalidAmount();

        OfferPosition1155 storage p = positions1155[coll][tokenId][msg.sender];

        if (p.totalOffer == 0) {
            uint64 dur = duration == 0 ? DEFAULT_DURATION : duration;
            if (dur > MAX_DURATION) revert InvalidDuration();
            p.firstAt   = uint64(block.timestamp);
            p.expiresAt = uint64(block.timestamp) + dur;
            p.units     = units;
        } else {
            if (block.timestamp >= p.expiresAt) revert PositionExpired();
            // Locked unit count — subsequent deposits must match the original count
            if (p.units != units) revert InvalidAmount();
        }

        p.totalOffer   += offerAmount;
        p.totalFeePaid += uint128(fee);

        if (fee > 0) {
            (bool ok,) = feeRecipient.call{value: fee}("");
            if (!ok) revert WithdrawFailed();
        }

        emit Offer1155PositionUpdated(coll, tokenId, msg.sender, p.totalOffer, p.totalFeePaid, p.units, p.expiresAt);
    }

    function refundExpired1155(address coll, uint256 tokenId, address bidder) external nonReentrant {
        OfferPosition1155 storage p = positions1155[coll][tokenId][bidder];
        if (p.totalOffer == 0) revert NoPosition();
        if (block.timestamp < p.expiresAt) revert PositionLive();

        uint128 amt = p.totalOffer;
        delete positions1155[coll][tokenId][bidder];

        (bool ok,) = bidder.call{value: amt}("");
        if (!ok) revert WithdrawFailed();

        emit Offer1155Refunded(coll, tokenId, bidder, amt);
        emit Offer1155PositionUpdated(coll, tokenId, bidder, 0, 0, 0, 0);
    }

    function acceptOffer1155(address coll, uint256 tokenId, address bidder) external nonReentrant {
        OfferPosition1155 storage p = positions1155[coll][tokenId][bidder];
        if (p.totalOffer == 0) revert NoPosition();
        if (block.timestamp >= p.expiresAt) revert PositionExpired();

        uint128 amt   = p.totalOffer;
        uint128 fee   = p.totalFeePaid;
        uint128 units = p.units;

        if (IERC1155(coll).balanceOf(msg.sender, tokenId) < units) revert NotOwner();
        if (!IERC1155(coll).isApprovedForAll(msg.sender, address(this))) revert NotApproved();

        delete positions1155[coll][tokenId][bidder];
        delete eligible1155[coll][tokenId];

        _transferToken(TokenStandard.ERC1155, coll, msg.sender, bidder, tokenId, units);

        (bool ok,) = msg.sender.call{value: amt}("");
        if (!ok) revert WithdrawFailed();

        emit Offer1155Accepted(coll, tokenId, msg.sender, bidder, units, amt, fee);
        emit Offer1155PositionUpdated(coll, tokenId, bidder, 0, 0, 0, 0);
    }
}
