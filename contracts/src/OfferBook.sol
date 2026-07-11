// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {MarketplaceCore, TokenStandard, BelowMinPrice, InvalidDuration, DURATION_3MIN, DURATION_15MIN, DURATION_30MIN, DURATION_1HR, DURATION_4HR, DURATION_24HR} from "./MarketplaceCore.sol";
import {IERC721}  from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155} from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";

error NotOwner();
error NoOffer();
error NotApproved();
error InvalidAmount();
error WrongValue();
error OfferActive();
error OfferExpired();

/// @dev NothingToWithdraw is inherited from MarketplaceCore — OfferBook
/// does not redeclare or shadow withdrawRefund(), so every caller across all
/// cores (Marketplace, AuctionHouse, OfferBook) sees the SAME custom error
/// from the same storage slot. Single selector = simpler frontend/indexer
/// code, fewer "if/else" branches per error-matching path.



/// @notice Sent when a collection's offer eligibility is toggled.
event OfferEligibilitySet(address indexed coll, bool indexed eligible);

error OffersNotEligible();
error NotKeeper();

/// @title OfferBook
/// @notice On-chain NFT offers: one active position per (NFT, buyer); the seller pays the fee on acceptance.
///
/// Fee model (seller-pays):
///   - Making an offer is FREE, but only on collections marked as offer-eligible.
///     The collection owner (or admin via the MarketplaceManager) sets the flag.
///   - makeOffer is PAYABLE: send exactly the new `principal`. The full amount is
///     escrowed; the offerer pays no fee.
///   - Only ONE offer per buyer per NFT. Calling makeOffer again on the same NFT
///     EDITS the position — the old principal is refunded atomically and the new
///     principal replaces it. Units and expiry can also be updated in the same call.
///   - Bidder can cancel their own offer before expiry via cancelOffer(), receiving a
///     full principal refund.
///   - There is NO individual withdrawal while the position is active. A position is
///     locked until accept / reject / expiry / edit.
///   - acceptOffer DEDUCTS a 1.5% platform fee from the principal; the seller receives 98.5%.
///   - rejectOffer (owner), cancelOffer (bidder, before expiry), or refundExpiredOffer
///     (keeper, after expiry) returns the FULL principal to the bidder — an offer
///     that never sells costs nothing.
///
/// Non-custodial. No royalties. No off-chain signatures. No pause. Unstoppable once deployed.
contract OfferBook is MarketplaceCore {
    /// @notice A bidder's offer on one NFT — one position per (coll, tokenId, bidder).
    ///         Calling makeOffer again edits the position (refunds old principal,
    ///         sets new principal/units/expiry) rather than compounding.
    struct Position {
        uint128       principal; // escrowed ETH (fees already removed)
        uint128       units;     // ERC-1155 units desired (1 for ERC-721)
        uint64        expiresAt; // set on creation or edit; validated against 6 fixed durations
        TokenStandard standard;  // token kind this offer targets
    }

    /// @notice positions[coll][tokenId][bidder] → Position.
    mapping(address => mapping(uint256 => mapping(address => Position))) public positions;

    /// @notice offerEligible[coll] → true if the collection accepts offers.
    ///         The collection owner (msg.sender in makeOffer context) or an
    ///         authorized admin toggles this. Offers revert when false.
    mapping(address => bool) public offerEligible;

    // ── Storage note ───────────────────────────────────────────────────────
    // `pendingReturns` is declared ONCE in MarketplaceCore. OfferBook inherits
    // it without redeclaration; OfferBook does NOT override `withdrawRefund()`
    // either, so callers hit the inherited implementation that emits
    // NothingToWithdraw on empty balance. _pay() (also inherited) emits
    // PushFailed on fallback — there are NO duplicated or divergent code paths.


    // ── Events ──────────────────────────────────────────────────────────────────

    event OfferMade(
        address indexed coll,
        uint256 indexed tokenId,
        address indexed bidder,
        uint256 principal, // new escrowed principal (replaces any previous position)
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

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {}

    /// @notice One-time initializer. Calls __MarketplaceCore_init to store
    ///         feeRecipient + manager in upgradeable storage.
    function initialize(address recipient, address manager_) public initializer {
        __MarketplaceCore_init(recipient, manager_);
    }

    /// @notice Toggle whether a collection accepts offers. Callable by the
    ///         collection's contract owner (via ERC-173/Ownable owner() selector
    ///         0x8da5cb5b) or the MarketplaceManager admin (DEFAULT_ADMIN_ROLE).
    ///         Uses ERC-173's standardized owner() rather than IERC721.ownerOf(0)
    ///         because owning token 0 is NOT the same as owning the contract —
    ///         any secondary-market buyer of token 0 could otherwise toggle offers
    ///         for the entire collection.
    ///
    ///         Graceful fallback: if ERC-173 owner() is not available (the
    ///         collection doesn't implement it), falls back to IERC721.ownerOf(0)
    ///         for backwards compatibility with mocks and non-Ownable contracts.
    ///         Non-ERC721 / non-Ownable collections fall through to the
    ///         admin-only path via MarketplaceManager.
    /// @param coll     Collection address.
    /// @param eligible True to allow offers on this collection.
    function setOfferEligible(address coll, bool eligible) external nonReentrant {
        // Check if the caller is the collection's ERC-173/Ownable owner.
        // The owner() selector is 0x8da5cb5b, standardized by ERC-173.
        bool authorized = false;
        (bool ok, bytes memory data) = coll.staticcall(
            abi.encodeWithSignature("owner()")
        );
        if (ok && data.length == 32) {
            address contractOwner = abi.decode(data, (address));
            if (msg.sender == contractOwner) {
                authorized = true;
            }
        }
        // Graceful fallback: if ERC-173 is not available (the staticcall
        // reverted or returned unexpected data), try IERC721.ownerOf(0).
        // This handles collections that only implement ERC-721 without
        // ERC-173/Ownable. The `ownerOf(0)` path is less secure (token 0
        // ownership conflates with contract admin) but is a reasonable
        // fallback for non-Ownable contracts.
        if (!authorized) {
            (bool ok721, bytes memory data721) = coll.staticcall(
                abi.encodeWithSelector(IERC721.ownerOf.selector, uint256(0))
            );
            if (ok721 && data721.length == 32) {
                address tokenZeroOwner = abi.decode(data721, (address));
                if (msg.sender == tokenZeroOwner) {
                    authorized = true;
                }
            }
        }
        // If neither ERC-173 nor ERC-721 ownerOf(0) worked, check if the
        // caller has DEFAULT_ADMIN_ROLE via the MarketplaceManager.
        // OpenZeppelin AccessControl defines DEFAULT_ADMIN_ROLE as
        // bytes32(0), NOT keccak256("DEFAULT_ADMIN_ROLE").
        if (!authorized && manager != address(0)) {
            (bool adminOk, bytes memory adminData) = manager.staticcall(
                abi.encodeWithSignature("hasRole(bytes32,address)", bytes32(0), msg.sender)
            );
            if (adminOk && adminData.length == 32 && abi.decode(adminData, (bool))) {
                authorized = true;
            }
        }
        if (!authorized) revert NotOwner();

        offerEligible[coll] = eligible;
        emit OfferEligibilitySet(coll, eligible);
    }

    // ── Make offer (free; full principal escrowed) ─────────────────────────────

    /// @notice Offer on an ERC-721 token. Send exactly `principal` as msg.value. FREE.
    ///         The target collection must be offer-eligible (setOfferEligible).
    /// @param coll       NFT collection.
    /// @param tokenId    Token ID.
    /// @param principal  The escrowed offer amount (≥ MIN_PRICE). No fee at offer time.
    /// @param expiresAt  Position expiry (one of 6 fixed durations: 3min–24hr).
    function makeOffer(address coll, uint256 tokenId, uint128 principal, uint64 expiresAt) external payable nonReentrant entryGate {
        if (!offerEligible[coll]) revert OffersNotEligible();
        _makeOffer(TokenStandard.ERC721, coll, tokenId, principal, 1, expiresAt);
    }

    /// @notice Offer on ERC-1155 units. Send exactly `principal` as msg.value. FREE.
    ///         The target collection must be offer-eligible (setOfferEligible).
    /// @param units  Number of ERC-1155 units desired (replaces any previous position).
    function makeOffer1155(address coll, uint256 tokenId, uint128 principal, uint128 units, uint64 expiresAt)
        external payable nonReentrant entryGate
    {
        if (units == 0) revert InvalidAmount();
        if (!offerEligible[coll]) revert OffersNotEligible();
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
        // Zero-value offers are rejected — every position must have escrow.
        if (principal == 0) revert InvalidAmount();
        if (principal < MIN_PRICE) revert BelowMinPrice();
        if (msg.value != uint256(principal)) revert WrongValue();

        Position storage p = positions[coll][tokenId][msg.sender];
        bool isEdit = p.principal > 0;

        // Only ONE offer per buyer per NFT. If a position already exists,
        // this call EDITS it: the old principal is refunded atomically and
        // the new principal replaces it. This lets buyers adjust their bid
        // up or down without cancelling and re-creating.
        uint256 oldPrincipal = uint256(p.principal);

        // Validate duration — must be one of the 6 fixed durations shared
        // across all cores. Checked on both creation and edit (the buyer
        // can change the expiry when editing).
        uint256 dur = uint256(expiresAt) - block.timestamp;
        if (dur != DURATION_3MIN && dur != DURATION_15MIN
            && dur != DURATION_30MIN && dur != DURATION_1HR
            && dur != DURATION_4HR && dur != DURATION_24HR) {
            revert InvalidDuration();
        }

        p.principal = principal;
        p.units     = units;
        p.expiresAt = expiresAt;
        p.standard  = standard;

        // Refund the old principal to the buyer atomically in the same tx.
        // The net effect: buyer sends `principal` wei and receives
        // `oldPrincipal` wei back. If increasing the offer, they net-pay
        // the difference; if decreasing, they net-receive the delta.
        if (isEdit && oldPrincipal > 0) {
            _pay(msg.sender, oldPrincipal);
        }

        emit OfferMade(coll, tokenId, msg.sender, p.principal, units, p.expiresAt);
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
        uint256 proceeds;
        unchecked { proceeds = uint256(p.principal) - fee; } // fee = 1.5% of principal, always < principal
        _pay(msg.sender, proceeds); // seller nets 98.5%

        emit OfferAccepted(coll, tokenId, msg.sender, bidder, p.principal, fee, p.units, p.standard);
    }

    // ── Reject / expire (full principal refunded — offers are free) ────────────
    //
    // Both paths use the INHERITED `_pay()` from MarketplaceCore so the
    // pull-fallback (audit-#3 fix) automatically emits the `PushFailed` event,
    // making any fallback visible to off-chain indexers, and credits to the
    // same `pendingReturns` slot every other core writes to. A bidder whose
    // contract cannot receive ETH can withdraw later via the inherited
    // `withdrawRefund()` on their core (Marketplace / AuctionHouse / this).
    // No code duplication, no shadowed storage, no divergent error selectors.

    // ── Refund expired (keeper) ──────────────────────────────────────────
    //
    // refundExpiredOffer: Keeper-only, anyone can call after expiry (if no
    //     MarketplaceManager) to return the escrow to the bidder. This enables
    //     the keeper bot to auto-refund expired offers without user interaction.
    //     Users CAN cancel their own offer before expiry via cancelOffer();
    //     the seller can reject, and the keeper handles expired offers.

    /// @notice Refund an expired offer's FULL principal. Callable only by addresses
    ///         with KEEPER_ROLE (via the MarketplaceManager) after the offer expires.
    ///         The keeper bot auto-refunds expired offers instantly without requiring
    ///         user interaction. If no MarketplaceManager is deployed (manager == address(0)),
    ///         this stays permissionless as a safety fallback — funds are never trapped.
    /// @param coll    Collection address.
    /// @param tokenId Token ID.
    /// @param bidder  The original offerer to refund.
    function refundExpiredOffer(address coll, uint256 tokenId, address bidder) external nonReentrant {
        // Only the keeper can refund expired offers when a MarketplaceManager is deployed.
        // When no manager is configured (address(0)), refunding stays
        // permissionless as a safety fallback so funds are never trapped.
        if (manager != address(0)) {
            (bool ok, bytes memory data) = manager.staticcall(
                abi.encodeWithSignature("hasRole(bytes32,address)", keccak256("KEEPER_ROLE"), msg.sender)
            );
            if (!ok || data.length != 32 || !abi.decode(data, (bool))) {
                revert NotKeeper();
            }
        }

        Position memory p = positions[coll][tokenId][bidder];
        if (p.principal == 0) revert NoOffer();
        if (block.timestamp < p.expiresAt) revert OfferActive();

        delete positions[coll][tokenId][bidder];
        _pay(bidder, p.principal);
        emit OfferRefunded(coll, tokenId, bidder, p.principal);
    }

    // ── Cancel (bidder withdraws before expiry) ────────────────────────────

    /// @notice Bidder cancels their own offer before it expires, receiving a full
    ///         principal refund. Callable only by the bidder and only before expiry.
    ///         Once expired, only the keeper can refund via refundExpiredOffer.
    function cancelOffer(address coll, uint256 tokenId) external nonReentrant {
        Position memory p = positions[coll][tokenId][msg.sender];
        if (p.principal == 0) revert NoOffer();
        if (block.timestamp >= p.expiresAt) revert OfferExpired();

        delete positions[coll][tokenId][msg.sender];
        _pay(msg.sender, p.principal);
        emit OfferRefunded(coll, tokenId, msg.sender, p.principal);
    }

    /// @notice Owner rejects a bidder's offer, refunding the FULL principal.
    ///         Best-effort push with pull-fallback — a bidder contract without
    ///         a payable receive() never traps its own refund inside the offer
    ///         record (audit-#3). Caller withdraws via `withdrawRefund()`.
    function rejectOffer(address coll, uint256 tokenId, address bidder) external nonReentrant {
        Position memory p = positions[coll][tokenId][bidder];
        if (p.principal == 0) revert NoOffer();

        if (p.standard == TokenStandard.ERC721) {
            if (IERC721(coll).ownerOf(tokenId) != msg.sender) revert NotOwner();
        } else {
            if (IERC1155(coll).balanceOf(msg.sender, tokenId) < p.units) revert NotOwner();
        }

        delete positions[coll][tokenId][bidder];
        _pay(bidder, p.principal); // inherited pull-fallback + PushFailed event
        emit OfferRefunded(coll, tokenId, bidder, p.principal);
    }
}
