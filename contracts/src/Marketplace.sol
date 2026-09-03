// SPDX-License-Identifier: MIT
pragma solidity 0.8.26;

import {MarketplaceCore, TokenStandard, BelowMinPrice, InvalidDuration} from "./MarketplaceCore.sol";
import {IERC721}  from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
import {IERC1155} from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";

error NotOwner();
error NotListed();
error WrongPrice();
error Expired();
error NotApproved();
error InvalidExpiry();
error InvalidAmount();
error BatchTooLarge();

/// @title Marketplace
/// @notice Fixed-price, time-bound listings for ERC-721 and ERC-1155 tokens.
/// @dev Non-custodial: tokens stay with seller until buyer settles. Approval required.
///      Listing is FREE. The buyer pays exactly the asking price; a 2% platform fee (1.5% platform + 0.5% keeper) is
///      deducted from the seller's proceeds (the seller receives 98%).
///      Listings are keyed by (collection, tokenId, seller): ERC-1155 holders each keep
///      their own stacked listing; for ERC-721 only the true owner's listing is
///      settle-able (a stale listing from a prior owner simply reverts on `buy`).
///      No exclusivity: the same NFT may also sit in the AuctionHouse / OfferBook —
///      first settle wins, the rest revert when the token has moved.
///      Once `buy` settles the trade is FINAL — no reverse, refund, or admin override.
///      Nothing is pausable: no entry or exit path can ever be halted — listings,
///      buys, cancels and withdrawals always run.
contract Marketplace is MarketplaceCore {
    /// @notice Listing record. Two storage slots:
    ///   slot 0: seller(20) + expiresAt(8) + standard(1) [3 bytes padding]
    ///   slot 1: price(16)  + amount(16)
    struct Listing {
        address       seller;    // slot 0
        uint64        expiresAt; // slot 0
        TokenStandard standard;  // slot 0
        uint128       price;     // slot 1
        uint128       amount;    // slot 1 (always 1 for ERC-721)
    }

    /// @notice listings[collection][tokenId][seller] → Listing.
    mapping(address => mapping(uint256 => mapping(address => Listing))) public listings;


    event Listed(
        address indexed coll,
        uint256 indexed id,
        address indexed seller,
        TokenStandard standard,
        uint128 amount,
        uint128 price,
        uint64  expiresAt
    );
    event Cancelled(address indexed coll, uint256 indexed id, address indexed seller);
    event Bought(
        address indexed coll,
        uint256 indexed id,
        address indexed buyer,
        address seller,
        TokenStandard standard,
        uint128 amount,
        uint128 price,
        uint256 fee
    );

    /// @notice v3.4: feeRecipient + manager are baked into the implementation
    ///         as immutables (validated in MarketplaceCore's constructor).
    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor(address recipient, address manager_)
        MarketplaceCore(recipient, manager_)
    { _disableInitializers(); }

    /// @notice One-time proxy initializer — no arguments in v3.4 (immutables
    ///         live in the implementation). Still initializer-gated.
    function initialize() public initializer {
        __MarketplaceCore_init();
    }

    // ── List (free) ───────────────────────────────────────────────────────────

    /// @notice List an ERC-721 token at a fixed price. FREE — no listing fee.
    /// @dev Defense-in-depth: nonReentrant added per L-09 invariant
    ///      ("every state-changing external on the cores is nonReentrant").
    ///      While a single-item list() reentrancy cannot front-run loop state
    ///      (unlike batchList), a malicious ERC-721 collection whose
    ///      isApprovedForAll or getApproved includes a reentrant hook could
    ///      still cause unexpected state reads mid-call. The modifier costs
    ///      ~2.3k gas on the first call and zero on re-entry (revert); the
    ///      invariant is cheap insurance.
    /// @param duration One of the fifteen shared durations (1m–24h); expiry is computed on-chain.
    function list(address coll, uint256 id, uint128 price, uint64 duration) external nonReentrant {
        _list(TokenStandard.ERC721, coll, id, 1, price, _expiryFor(duration));
    }

    /// @notice List ERC-1155 units at a fixed price. FREE — no listing fee.
    /// @dev Defense-in-depth: nonReentrant added per L-09 invariant.
    ///      Same rationale as list() — a malicious ERC-1155 collection could
    ///      re-enter during the balanceOf or isApprovedForAll probes.
    function list1155(address coll, uint256 id, uint128 amount, uint128 price, uint64 duration) external nonReentrant {
        if (amount == 0) revert InvalidAmount();
        _list(TokenStandard.ERC1155, coll, id, amount, price, _expiryFor(duration));
    }

    // ── Batch List ────────────────────────────────────────────────────────────

    struct BatchItem {
        address coll;
        uint256 id;
        uint128 price;
        uint64  duration; // one of the fifteen shared durations
    }

    /// @notice List up to 50 ERC-721 tokens in one transaction. FREE.
    ///         Caller must have approved this contract on each collection.
    /// @dev L-09 fix (v28 - Round 3): `nonReentrant` was previously missing
    ///      here despite every other state-changing entry path on this
    ///      contract using it (`list`, `list1155`, `buy` all carry it).
    ///      The for-loop calls `_list()` which in turn performs three
    ///      external reads per item (`ownerOf`, `isApprovedForAll`,
    ///      `getApproved`) and one external write (the storage struct
    ///      assignment; storage itself is not an external call but the
    ///      surrounding read calls are). A malicious collection contract
    ///      whose `isApprovedForAll` or `getApproved` includes a hook
    ///      (e.g. via a proxy delegatecall into an attacker-controlled
    ///      implementation) could re-enter this function mid-loop on the
    ///      first item, see partial state (some listings already written,
    ///      others not), and front-run a later item with an unexpected
    ///      approval-state read. Even though each `_list` is strictly
    ///      checks-effects-interactions (storage write happens AFTER all
    ///      external reads, BEFORE the next iteration), the missing
    ///      nonReentrant was a defense-in-depth gap that broke the
    ///      invariant "every state-changing external on the cores is
    ///      nonReentrant". Cheap, mechanical, conservative. Added.
    function batchList(BatchItem[] calldata items) external nonReentrant {
        // v3.4: length hoisted + unchecked increment (−1.5k on a full batch).
        // Safe: calldata length is constant and bounded to 50 above.
        uint256 len = items.length;
        if (len == 0 || len > 50) revert BatchTooLarge();
        for (uint256 i; i < len; ) {
            _list(TokenStandard.ERC721, items[i].coll, items[i].id, 1, items[i].price, _expiryFor(items[i].duration));
            unchecked { ++i; }
        }
    }

    function _list(
        TokenStandard standard,
        address coll,
        uint256 id,
        uint128 amount,
        uint128 price,
        uint64  expiresAt
    ) internal {
        if (price < MIN_PRICE) revert BelowMinPrice();
        // Prevent the same NFT from being listed at a different price by the same seller.
        // If an existing listing is active at a different price, revert.
        Listing memory existing = listings[coll][id][msg.sender];
        if (existing.seller != address(0) && block.timestamp <= existing.expiresAt && existing.price != price) {
            revert NotOwner(); // seller must cancel first to relist at a different price
        }
        // expiresAt was produced by _expiryFor(): strictly in the future and one
        // of the fifteen shared durations away. Kept as a cheap invariant check.
        if (expiresAt <= block.timestamp) revert InvalidExpiry();

        if (standard == TokenStandard.ERC721) {
            if (IERC721(coll).ownerOf(id) != msg.sender) revert NotOwner();
            if (!IERC721(coll).isApprovedForAll(msg.sender, address(this))
                && IERC721(coll).getApproved(id) != address(this)) revert NotApproved();
        } else {
            if (IERC1155(coll).balanceOf(msg.sender, id) < amount) revert NotOwner();
            if (!IERC1155(coll).isApprovedForAll(msg.sender, address(this))) revert NotApproved();
        }

        listings[coll][id][msg.sender] = Listing({
            seller:    msg.sender,
            expiresAt: expiresAt,
            standard:  standard,
            price:     price,
            amount:    amount
        });
        emit Listed(coll, id, msg.sender, standard, amount, price, expiresAt);
    }

    // ── Cancel ────────────────────────────────────────────────────────────────

    /// @notice Cancel an unsold listing. Seller only.
    /// @dev L-09 fix (v28 — Round 3): every state-changing external on the
    ///      cores is nonReentrant. While this function has no external calls
    ///      (only a storage delete + event), adding the modifier enforces
    ///      the invariant consistently. ~2.3k gas overhead on cold entry,
    ///      zero on re-entry (revert).
    function cancel(address coll, uint256 id) external nonReentrant {
        // v3.4: storage pointer instead of a memory copy — the guard reads
        // only slot 0 (seller) instead of loading the whole struct (−2.1k).
        Listing storage l = listings[coll][id][msg.sender];
        if (l.seller != msg.sender) revert NotOwner(); // seller == address(0) → not listed
        delete listings[coll][id][msg.sender];
        emit Cancelled(coll, id, msg.sender);
    }

    // ── Edit Price ─────────────────────────────────────────────────────────

    /// @notice Change the price of an active listing. Seller only. The listing
    ///         must not have expired and the new price must be ≥ MIN_PRICE.
    ///         Emits a Listed event with the updated price so off-chain
    ///         indexers refresh without a full re-scan.
    function editPrice(address coll, uint256 id, uint128 newPrice) external nonReentrant {
        Listing storage l = listings[coll][id][msg.sender];
        if (l.seller != msg.sender) revert NotOwner();
        if (block.timestamp > l.expiresAt) revert Expired();
        if (newPrice < MIN_PRICE) revert BelowMinPrice();
        l.price = newPrice;
        emit Listed(coll, id, msg.sender, l.standard, l.amount, newPrice, l.expiresAt);
    }

    // ── Expire ────────────────────────────────────────────────────────────────

    /// @notice Clean up an expired listing that had no buyer. Callable only by
    ///         addresses with KEEPER_ROLE (via the MarketplaceManager). Expired
    ///         listings are auto-cleaned by the keeper bot; the seller can also
    ///         remove their own listing at any time via cancel(), which does not
    ///         check expiry. Emits Cancelled event.
    ///         If no MarketplaceManager is deployed (manager == address(0)),
    ///         cleanup stays permissionless as a safety fallback.
    /// @dev The listing must be expired (block.timestamp > expiresAt). No price
    ///      check, no seller authorization — the listing is dead and can be
    ///      safely deleted. The seller must relist the NFT to offer it for sale
    ///      again.
    function cleanExpired(address coll, uint256 id, address seller_) external nonReentrant {
        if (manager != address(0)) {
            (bool ok, bytes memory data) = manager.staticcall(
                abi.encodeWithSignature("hasRole(bytes32,address)", keccak256("KEEPER_ROLE"), msg.sender)
            );
            if (!ok || data.length != 32 || !abi.decode(data, (bool))) {
                revert NotOwner();
            }
        }
        Listing memory l = listings[coll][id][seller_];
        if (l.seller == address(0)) revert NotListed();
        if (block.timestamp <= l.expiresAt) revert NotOwner(); // only seller can cancel active listings
        delete listings[coll][id][seller_];
        emit Cancelled(coll, id, seller_);
    }

    // ── Buy (seller pays 2% on the sale: 1.5% platform + 0.5% keeper) ────────────────────────────────────

    /// @notice Buy a listed token. Send exactly `price` as msg.value.
    /// @dev FINAL on success. NFT → buyer, 2% fee (1.5% platform + 0.5% keeper), price − fee → seller.
    ///      The `seller` arg selects which listing to buy (listings are seller-keyed).
    ///      Entire tx reverts if the NFT transfer fails (seller no longer owns/approves) —
    ///      no fee is taken, the listing remains. This is how first-settle-wins works.
    // The only state written after the payout calls is `pendingReturns[to] += amount`
    // inside _pay's failure branch. Those calls carry a 50_000 gas stipend, which
    // cannot fund a reentrant call, and this function is nonReentrant — the shared
    // ReentrancyGuard status also blocks re-entering withdrawRefund mid-buy, so the
    // cross-function path slither reports is not reachable.
    // slither-disable-next-line reentrancy-eth
    function buy(address coll, uint256 id, address seller) external payable nonReentrant {
        Listing memory l = listings[coll][id][seller];
        if (l.seller == address(0)) revert NotListed();
        if (block.timestamp > l.expiresAt) revert Expired();

        if (msg.value != uint256(l.price)) revert WrongPrice();
        uint256 fee = _feeOf(l.price);

        delete listings[coll][id][seller];

        // v3.4: ERC-721 uses transferFrom, not safeTransferFrom (−2.6k). The
        // buyer IS msg.sender here, so the onERC721Received probe only ever
        // protected the buyer from their own purchase — a contract buyer that
        // cannot handle ERC-721 loses only its own convenience. This mirrors
        // (inverted) the settle() rationale in AuctionHouse: there the winner
        // is NOT the caller, so removing the hook removes a veto; here the
        // caller is consenting by construction. NOTE the deliberate opposite
        // choice in acceptOffer's _transferToken: its recipient (the offerer)
        // is not the caller, so it keeps safeTransferFrom. ERC-1155 has no
        // unsafe variant — that branch keeps its callback.
        if (l.standard == TokenStandard.ERC721) {
            IERC721(coll).transferFrom(l.seller, msg.sender, id);
        } else {
            IERC1155(coll).safeTransferFrom(l.seller, msg.sender, id, l.amount, "");
        }
        _payFee(fee);
        uint256 proceeds;
        unchecked { proceeds = uint256(l.price) - fee; } // fee = 2% of price, always < price
        _pay(l.seller, proceeds);

        emit Bought(coll, id, msg.sender, l.seller, l.standard, l.amount, l.price, fee);
    }
}
