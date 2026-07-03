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

/// @dev NothingToWithdraw is inherited from MarketplaceCore — OfferBook
/// does not redeclare or shadow withdrawRefund(), so every caller across all
/// cores (Marketplace, AuctionHouse, OfferBook) sees the SAME custom error
/// from the same storage slot. Single selector = simpler frontend/indexer
/// code, fewer "if/else" branches per error-matching path.

/// @dev Maximum offer lifetime from the latest top-up.
uint64 constant MAX_OFFER_DURATION = 14 days;

/// @notice Sent when a collection's offer eligibility is toggled.
event OfferEligibilitySet(address indexed coll, bool indexed eligible);

error OffersNotEligible();

/// @title OfferBook
/// @notice On-chain NFT offers with stacked positions; the seller pays the fee on acceptance.
///
/// Fee model (seller-pays, Option-4 stacked positions):
///   - Making an offer is FREE, but only on collections marked as offer-eligible.
///     The collection owner (or admin via the MarketplaceManager) sets the flag.
///   - makeOffer is PAYABLE: send exactly `principal`. The full amount is escrowed; the
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

    /// @notice Toggle whether a collection accepts offers. Callable by the
    ///         collection's owner (ERC721 ownerOf(token 0)) or the
    ///         MarketplaceManager admin (DEFAULT_ADMIN_ROLE = bytes32(0)).
    ///         Non-ERC721 collections fall through to the admin-only path.
    /// @param coll     Collection address.
    /// @param eligible True to allow offers on this collection.
    function setOfferEligible(address coll, bool eligible) external nonReentrant {
        // Check if the caller is the ERC721 token-0 owner.
        bool authorized = false;
        (bool ok, bytes memory data) = coll.staticcall(
            abi.encodeWithSelector(IERC721.ownerOf.selector, 0)
        );
        if (ok && data.length == 32) {
            address tokenOwner = abi.decode(data, (address));
            if (msg.sender == tokenOwner) {
                authorized = true;
            }
        }
        // If not the collection owner, check if caller has DEFAULT_ADMIN_ROLE
        // via the MarketplaceManager. OpenZeppelin AccessControl defines
        // DEFAULT_ADMIN_ROLE as bytes32(0), NOT keccak256("DEFAULT_ADMIN_ROLE").
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
    /// @param expiresAt  Position expiry (now < expiresAt ≤ now + 14 days).
    function makeOffer(address coll, uint256 tokenId, uint128 principal, uint64 expiresAt) external payable nonReentrant entryGate {
        if (!offerEligible[coll]) revert OffersNotEligible();
        _makeOffer(TokenStandard.ERC721, coll, tokenId, principal, 1, expiresAt);
    }

    /// @notice Offer on ERC-1155 units. Send exactly `principal` as msg.value. FREE.
    ///         The target collection must be offer-eligible (setOfferEligible).
    /// @param units  Number of ERC-1155 units desired (latest top-up wins).
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
        if (expiresAt <= block.timestamp || expiresAt > block.timestamp + MAX_OFFER_DURATION) revert InvalidExpiry();
        // L-13 fix: reject zero-value top-ups. Without this, a caller with
        // an existing position >= MIN_PRICE could call makeOffer with
        // principal=0 to refresh the expiry or rewrite units without
        // adding escrow, breaking the "locked until accept/reject/expiry"
        // invariant — the offerer can effectively cancel and re-issue at
        // a shorter expiry or different unit count without the seller's
        // awareness, and a front-running seller who observed a top-up
        // with reduced units would accept fewer units than expected.
        if (principal == 0) revert InvalidAmount();

        // M-01 fix: when topping up an existing position, the new expiry must
        // not be less than the existing expiry. Without this, a bidder can
        // top up with MIN_PRICE and expiresAt=block.timestamp+1, effectively
        // expiring their own locked offer in the next block. This breaks the
        // "locked until expiry" invariant and lets a bidder front-run a
        // seller's acceptOffer to withdraw a large escrow.
        Position storage existing = positions[coll][tokenId][msg.sender];
        if (existing.principal > 0 && expiresAt < existing.expiresAt) revert InvalidExpiry();

        if (msg.value != uint256(principal)) revert WrongValue();

        Position storage p = existing;
        uint256 newPrincipal = uint256(p.principal) + principal;
        if (newPrincipal > type(uint128).max) revert InvalidAmount();
        // L-01 fix: apply MIN_PRICE to the total position, not the delta.
        // Previously, a user with a 10 ETH position couldn't add 0.005 ETH
        // because the check was on the increment. Now the check is on the
        // new total — micro-top-ups of large positions are allowed while
        // still preventing dust-sized initial offers.
        if (newPrincipal < MIN_PRICE) revert BelowMinPrice();
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

    /// @notice Reclaim an expired position's principal. Permissionless (keeper or bidder).
    ///         Full principal refunded — no fee was charged at offer time.
    function refundExpiredOffer(address coll, uint256 tokenId, address bidder) external nonReentrant {
        Position memory p = positions[coll][tokenId][bidder];
        if (p.principal == 0) revert NoOffer();
        if (block.timestamp <= p.expiresAt) revert OfferActive();

        delete positions[coll][tokenId][bidder];
        _pay(bidder, p.principal); // inherited pull-fallback + PushFailed event
        emit OfferRefunded(coll, tokenId, bidder, p.principal);
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
