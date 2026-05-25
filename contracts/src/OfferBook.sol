// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {MarketplaceCore, TokenStandard, WithdrawFailed} from "./MarketplaceCore.sol";
import {IERC721}  from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155} from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";

error NotOwner();
error NotEligible();
error NoOffer();
error NotApproved();
error ZeroOffer();
error InvalidAmount();
error OfferExists();

/// @title OfferBook
/// @notice On-chain NFT offer system. Owners opt-in tokens to receive offers.
///         Bidders deposit ETH; owners accept the offer they choose.
///
/// Flow (ERC-721):
///   1. Owner calls markEligible(coll, tokenId) — signals willingness to receive offers.
///   2. Bidder calls makeOffer(coll, tokenId) with msg.value = offer amount.
///   3. Owner calls acceptOffer(coll, tokenId, bidder) — NFT → bidder,
///      1.5% fee → feeRecipient, remainder → owner. Eligibility cleared automatically.
///   4. Bidder may call withdrawOffer(coll, tokenId) at any time to reclaim ETH in full.
///      No fee is taken on unaccepted offers.
///   5. Owner calls removeEligible(coll, tokenId) to stop receiving new offers.
///      Existing offers remain live (bidders withdraw them).
///
/// No royalties. No off-chain signatures. No pause. Unstoppable once deployed.
contract OfferBook is MarketplaceCore {
    // ── ERC-721 state ──────────────────────────────────────────────────────

    /// @notice eligible[coll][tokenId] = address that marked it eligible (address(0) = not eligible).
    mapping(address => mapping(uint256 => address)) public eligible;

    /// @notice offers[coll][tokenId][bidder] = ETH offered (0 = no offer).
    mapping(address => mapping(uint256 => mapping(address => uint256))) public offers;

    // ── ERC-1155 state ─────────────────────────────────────────────────────

    /// @notice eligible1155[coll][tokenId] = address that marked it eligible.
    mapping(address => mapping(uint256 => address)) public eligible1155;

    struct Offer1155 {
        uint128 amount; // total ETH offered
        uint128 units;  // number of ERC-1155 units desired
    }

    /// @notice offers1155[coll][tokenId][bidder] = offer details.
    mapping(address => mapping(uint256 => mapping(address => Offer1155))) public offers1155;

    // ── Events ─────────────────────────────────────────────────────────────

    event EligibilityMarked(address indexed coll, uint256 indexed tokenId, address indexed owner);
    event EligibilityRemoved(address indexed coll, uint256 indexed tokenId, address indexed owner);
    event Eligibility1155Marked(address indexed coll, uint256 indexed tokenId, address indexed owner);
    event Eligibility1155Removed(address indexed coll, uint256 indexed tokenId, address indexed owner);
    event OfferMade(address indexed coll, uint256 indexed tokenId, address indexed bidder, uint256 amount);
    event OfferWithdrawn(address indexed coll, uint256 indexed tokenId, address indexed bidder, uint256 amount);
    event OfferAccepted(address indexed coll, uint256 indexed tokenId, address indexed seller, address bidder, uint256 amount, uint256 fee);
    event Offer1155Made(address indexed coll, uint256 indexed tokenId, address indexed bidder, uint128 units, uint128 amount);
    event Offer1155Withdrawn(address indexed coll, uint256 indexed tokenId, address indexed bidder, uint128 amount);
    event Offer1155Accepted(address indexed coll, uint256 indexed tokenId, address indexed seller, address bidder, uint128 units, uint128 amount, uint256 fee);

    constructor(address recipient)
        MarketplaceCore(recipient)
    {}

    // ── ERC-721 eligibility ───────────────────────────────────────────────

    /// @notice Mark an ERC-721 token as eligible to receive offers.
    ///         Caller must be current owner. Can be called again after NFT transfer to refresh.
    function markEligible(address coll, uint256 tokenId) external {
        if (IERC721(coll).ownerOf(tokenId) != msg.sender) revert NotOwner();
        eligible[coll][tokenId] = msg.sender;
        emit EligibilityMarked(coll, tokenId, msg.sender);
    }

    /// @notice Remove ERC-721 token from offer eligibility. Caller must be the address that marked it.
    ///         Existing offers from bidders persist — they must withdrawOffer to reclaim ETH.
    function removeEligible(address coll, uint256 tokenId) external {
        if (eligible[coll][tokenId] != msg.sender) revert NotOwner();
        delete eligible[coll][tokenId];
        emit EligibilityRemoved(coll, tokenId, msg.sender);
    }

    // ── ERC-721 offers ─────────────────────────────────────────────────────

    /// @notice Submit an offer for an eligible ERC-721 token. msg.value = offer amount.
    ///         Multiple calls accumulate — total stored is your current offer.
    ///         To reduce your offer: call withdrawOffer then makeOffer with the new amount.
    function makeOffer(address coll, uint256 tokenId) external payable {
        if (eligible[coll][tokenId] == address(0)) revert NotEligible();
        if (msg.value == 0) revert ZeroOffer();
        offers[coll][tokenId][msg.sender] += msg.value;
        emit OfferMade(coll, tokenId, msg.sender, offers[coll][tokenId][msg.sender]);
    }

    /// @notice Withdraw your entire offer for a token. Full ETH returned — no fee taken.
    function withdrawOffer(address coll, uint256 tokenId) external nonReentrant {
        uint256 amt = offers[coll][tokenId][msg.sender];
        if (amt == 0) revert NoOffer();
        delete offers[coll][tokenId][msg.sender];
        (bool ok,) = msg.sender.call{value: amt}("");
        if (!ok) revert WithdrawFailed();
        emit OfferWithdrawn(coll, tokenId, msg.sender, amt);
    }

    /// @notice Accept a specific bidder's offer. Caller must be current NFT owner.
    ///         NFT → bidder. 1.5% platform fee → feeRecipient. Remainder → seller.
    ///         Eligibility is automatically cleared on acceptance.
    function acceptOffer(address coll, uint256 tokenId, address bidder) external nonReentrant {
        if (IERC721(coll).ownerOf(tokenId) != msg.sender) revert NotOwner();
        if (!IERC721(coll).isApprovedForAll(msg.sender, address(this))
            && IERC721(coll).getApproved(tokenId) != address(this)) revert NotApproved();

        uint256 amt = offers[coll][tokenId][bidder];
        if (amt == 0) revert NoOffer();

        delete offers[coll][tokenId][bidder];
        delete eligible[coll][tokenId];

        _transferToken(TokenStandard.ERC721, coll, msg.sender, bidder, tokenId, 1);
        uint256 fee = _splitAndPay(msg.sender, amt);

        emit OfferAccepted(coll, tokenId, msg.sender, bidder, amt, fee);
    }

    // ── ERC-1155 eligibility ──────────────────────────────────────────────

    /// @notice Mark an ERC-1155 token as eligible to receive offers.
    ///         Caller must hold at least 1 unit of the token.
    function markEligible1155(address coll, uint256 tokenId) external {
        if (IERC1155(coll).balanceOf(msg.sender, tokenId) == 0) revert NotOwner();
        eligible1155[coll][tokenId] = msg.sender;
        emit Eligibility1155Marked(coll, tokenId, msg.sender);
    }

    /// @notice Remove ERC-1155 token from offer eligibility.
    function removeEligible1155(address coll, uint256 tokenId) external {
        if (eligible1155[coll][tokenId] != msg.sender) revert NotOwner();
        delete eligible1155[coll][tokenId];
        emit Eligibility1155Removed(coll, tokenId, msg.sender);
    }

    // ── ERC-1155 offers ───────────────────────────────────────────────────

    /// @notice Submit an offer for eligible ERC-1155 tokens.
    ///         One active offer per (coll, tokenId, bidder). Call withdrawOffer1155 first to update.
    /// @param units  Number of ERC-1155 units you want.
    function makeOffer1155(address coll, uint256 tokenId, uint128 units) external payable {
        if (eligible1155[coll][tokenId] == address(0)) revert NotEligible();
        if (msg.value == 0) revert ZeroOffer();
        if (units == 0) revert InvalidAmount();
        if (uint256(msg.value) > type(uint128).max) revert InvalidAmount();
        if (offers1155[coll][tokenId][msg.sender].amount > 0) revert OfferExists();

        offers1155[coll][tokenId][msg.sender] = Offer1155({
            amount: uint128(msg.value),
            units:  units
        });
        emit Offer1155Made(coll, tokenId, msg.sender, units, uint128(msg.value));
    }

    /// @notice Withdraw your ERC-1155 offer. Full ETH returned — no fee taken.
    function withdrawOffer1155(address coll, uint256 tokenId) external nonReentrant {
        Offer1155 storage o = offers1155[coll][tokenId][msg.sender];
        uint128 amt = o.amount;
        if (amt == 0) revert NoOffer();
        delete offers1155[coll][tokenId][msg.sender];
        (bool ok,) = msg.sender.call{value: amt}("");
        if (!ok) revert WithdrawFailed();
        emit Offer1155Withdrawn(coll, tokenId, msg.sender, amt);
    }

    /// @notice Accept a specific bidder's ERC-1155 offer. Caller must be current holder.
    ///         `units` tokens → bidder. 1.5% platform fee → feeRecipient. Remainder → seller.
    ///         Eligibility is automatically cleared on acceptance.
    function acceptOffer1155(address coll, uint256 tokenId, address bidder) external nonReentrant {
        Offer1155 storage o = offers1155[coll][tokenId][bidder];
        if (o.amount == 0) revert NoOffer();

        uint128 amt   = o.amount;
        uint128 units = o.units;

        if (IERC1155(coll).balanceOf(msg.sender, tokenId) < units) revert NotOwner();
        if (!IERC1155(coll).isApprovedForAll(msg.sender, address(this))) revert NotApproved();

        delete offers1155[coll][tokenId][bidder];
        delete eligible1155[coll][tokenId];

        _transferToken(TokenStandard.ERC1155, coll, msg.sender, bidder, tokenId, units);
        uint256 fee = _splitAndPay(msg.sender, amt);

        emit Offer1155Accepted(coll, tokenId, msg.sender, bidder, units, amt, fee);
    }
}
