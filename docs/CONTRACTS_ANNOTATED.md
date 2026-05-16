# MagicWebb Smart Contracts — Line-by-Line Annotation

**Scope:** `contracts/src/*.sol` — every line explained.
**Audience:** auditors, integrators, future maintainers.
**Solc:** `0.8.24` (pinned, not floating).
**Network:** Flare Coston2 testnet (EVM-equivalent).
**Auth model:** OpenZeppelin `AccessControl` + `Pausable` + `ReentrancyGuard`.

The four source files form a small hierarchy:

```
MarketplaceCore (abstract)
   ├── Marketplace        — fixed-price listings (ERC721 + ERC1155)
   ├── AuctionHouse       — English auctions     (ERC721 + ERC1155)
   └── OfferBook          — EIP-712 signed offers (ERC721 + ERC1155)
```

Platform fees route to an immutable address passed at construction. There is **no** separate `FeeVault.sol` in this repository.

Each child reuses fee handling, pausing, and the standard-aware `_transferToken` from `MarketplaceCore`. NFTs are **never custodied** by these contracts (except the auto-`ERC1155Holder` ack interface) — the seller retains custody until a buyer settles.

---

## 1. `MarketplaceCore.sol` — shared base

### Imports & errors

```solidity
1: // SPDX-License-Identifier: MIT
2: pragma solidity 0.8.24;
```
**Line 1** — MIT license SPDX tag (required by Solc warnings).
**Line 2** — Pinned compiler (post-audit decision; was `^0.8.24`). Pinning eliminates the risk of an unexpected compiler-bug regression on a future minor bump.

```solidity
4: import {ReentrancyGuard} from "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
5: import {AccessControl}    from "@openzeppelin/contracts/access/AccessControl.sol";
6: import {Pausable}         from "@openzeppelin/contracts/utils/Pausable.sol";
7: import {IERC721}          from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
8: import {IERC1155}         from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";
9: import {ERC1155Holder}    from "@openzeppelin/contracts/token/ERC1155/utils/ERC1155Holder.sol";
```
**Line 4** `ReentrancyGuard` — provides `nonReentrant` modifier. Used by every trade-completing function in derivatives (`buy`, `bid`, `withdrawRefund`, `settle`, `acceptOffer*`, `withdraw`). Cheaper than a full mutex via transient storage on Solc 0.8.24 + Cancun, but this codebase uses the standard storage variant for Coston2 compatibility.
**Line 5** `AccessControl` — role-based admin. Roles defined later: `DEFAULT_ADMIN_ROLE`, `FEE_ROLE`, `PAUSER_ROLE`.
**Line 6** `Pausable` — circuit breaker. Hooked into trade entry points via `whenNotPaused`. Withdrawals/cancels intentionally remain callable while paused (users must always be able to extract funds).
**Line 7** `IERC721` — token interface used for ownership/approval checks and `safeTransferFrom`.
**Line 8** `IERC1155` — token interface for balance/approval checks and `safeTransferFrom`.
**Line 9** `ERC1155Holder` — implements `onERC1155Received` and `onERC1155BatchReceived` returning the magic value `0xf23a6e61` / `0xbc197c81`. This is required by `safeTransferFrom` callers — without it, an ERC1155 sender that calls `safeTransferFrom(... to=address(this) ...)` would revert. We don't *need* the contract to ever custody tokens, but the implementation exists in case a workflow ever sends 1155s here (e.g. during testing or an admin migration).

```solidity
11: error TransferFailed();
12: error WithdrawFailed();
13: error InvalidFee();
14: error ZeroAddress();
```
**Lines 11–14** — Custom errors (cheaper than `require("msg")` strings). `WithdrawFailed` is re-exported via the named import from `AuctionHouse.sol` and `OfferBook.sol`.

```solidity
16: enum TokenStandard { ERC721, ERC1155 }
```
**Line 16** — Standard discriminator. Stored in 1 byte. Lets a single struct slot carry both ERC721 and ERC1155 metadata without using two contracts.

### Contract header & state

```solidity
21: abstract contract MarketplaceCore is ReentrancyGuard, AccessControl, Pausable, ERC1155Holder {
```
**Line 21** — `abstract` because derivatives provide a constructor and concrete entry points. Inheritance order is significant: Solidity walks parents right-to-left when resolving `supportsInterface` (see line 78).

```solidity
23:   uint16 public constant MAX_FEE_BPS = 1_000;
25:   bytes32 public constant FEE_ROLE    = keccak256("FEE_ROLE");
26:   bytes32 public constant PAUSER_ROLE = keccak256("PAUSER_ROLE");
```
**Line 23** — Hard cap on platform fee at 10%. Enforced both in constructor and `setFeeBps`. There is no escape hatch above this — protects users from an admin key that turns malicious.
**Lines 25–26** — Role identifiers. `keccak256` is computed at compile time. The admin granted in the constructor receives all three roles (default admin + fee + pauser); operationally these should be split across multisigs.

```solidity
29:   uint16  public feeBps;
31:   address public feeVault;
```
**Line 29** — Current fee in basis points. Public auto-getter so frontends can read without an ABI helper.
**Line 31** — Destination address for platform fee payments. The fee transfer uses a low-level call so the recipient can be either an EOA (≈2300 gas) or a contract (≈22000 gas). `setFeeVault` can update this to a smart-contract sink via `FEE_ROLE`.

```solidity
33:   event FeeUpdated(uint16 oldBps, uint16 newBps);
34:   event FeeVaultUpdated(address indexed oldVault, address indexed newVault);
```
**Line 33** — Emitted by `setFeeBps`. `uint16` doesn't benefit from indexing (Bloom-filter overhead would dominate for 2-byte values), so neither field is indexed.
**Line 34** — Both addresses indexed so off-chain consumers can `eth_getLogs`-filter by either side of the change. Indexing was added during the post-audit pass (slither flagged the unindexed addresses).

### Constructor

```solidity
36:   constructor(address admin, address vault, uint16 fee) {
37:     if (admin == address(0) || vault == address(0)) revert ZeroAddress();
38:     if (fee > MAX_FEE_BPS) revert InvalidFee();
39:     _grantRole(DEFAULT_ADMIN_ROLE, admin);
40:     _grantRole(FEE_ROLE, admin);
41:     _grantRole(PAUSER_ROLE, admin);
42:     feeVault = vault;
43:     feeBps   = fee;
44:   }
```
**Lines 36–38** — Validate admin and vault are non-zero, fee within cap. Reverts use custom errors so the function selector is the only thing emitted (small calldata).
**Lines 39–41** — Grant all three roles to `admin`. Admin can later split them out via `grantRole`/`revokeRole`. Note `DEFAULT_ADMIN_ROLE` controls who can grant/revoke other roles, so it must be granted last in a real multisig migration.
**Lines 42–43** — Persist vault and initial fee. No event emitted because constructor execution is observable from creation tx.

### Admin functions

```solidity
47:   function setFeeBps(uint16 newBps) external onlyRole(FEE_ROLE) {
48:     if (newBps > MAX_FEE_BPS) revert InvalidFee();
49:     emit FeeUpdated(feeBps, newBps);
50:     feeBps = newBps;
51:   }
```
**Line 47** — `external` only because no internal caller. `onlyRole(FEE_ROLE)` reverts with the OZ-standard `AccessControlUnauthorizedAccount` error.
**Line 48** — Re-check cap. Defence in depth — if admin key is compromised an attacker cannot raise above 10%.
**Lines 49–50** — Emit before state change so the event carries the old value. Cheap pattern in Solc 0.8 (event params are encoded from stack, no SSTORE dependency).

```solidity
54:   function setFeeVault(address newVault) external onlyRole(FEE_ROLE) {
55:     if (newVault == address(0)) revert ZeroAddress();
56:     emit FeeVaultUpdated(feeVault, newVault);
57:     feeVault = newVault;
58:   }
```
**Lines 54–58** — Same pattern. Zero-address guard prevents accidentally bricking fee payouts.

```solidity
61:   function pause()   external onlyRole(PAUSER_ROLE) { _pause(); }
63:   function unpause() external onlyRole(PAUSER_ROLE) { _unpause(); }
```
**Lines 61, 63** — Thin wrappers around OZ `Pausable`. Pausing blocks `buy`, `list`, `create`, `bid`, `acceptOffer*` (anything tagged `whenNotPaused`). Does **not** block `cancel`, `withdrawRefund`, `withdraw`, or `cancelOffer` — users always exit.

### Payment splitter

```solidity
66:   function _splitAndPay(address seller, uint256 amount) internal returns (uint256 fee, uint256 sellerAmt) {
67:     fee = (amount * feeBps) / 10_000;
68:     unchecked { sellerAmt = amount - fee; } // fee ≤ amount via /10_000
69:     if (fee > 0) {
70:       (bool ok,) = feeVault.call{value: fee}("");
71:       if (!ok) revert TransferFailed();
72:     }
73:     (bool ok2,) = seller.call{value: sellerAmt}("");
74:     if (!ok2) revert TransferFailed();
75:   }
```
**Line 67** — Fee = `amount * feeBps / 10_000`. With `feeBps ≤ 1_000` and `amount ≤ uint128.max` (enforced by caller types), `amount * feeBps` cannot overflow uint256.
**Line 68** — `unchecked` is safe: `fee = (amount * x) / 10000` is bounded by `amount`, so `amount - fee` cannot underflow. Saves ~30 gas.
**Lines 69–72** — Skip fee CALL if fee is zero (small auctions or zero-fee config). Use low-level `call` so the recipient can be either an EOA (2300 gas) or a contract that needs more than `transfer`'s stipend.
**Lines 73–74** — Pay seller. Reverts the whole tx if seller is a contract that rejects ETH. This is acceptable: buyer doesn't lose funds (tx reverts atomically), but griefing-sellers can DOS themselves. Documented trade-off.

**Slither flags** this as `arbitrary-send-eth`. Triage: the destinations are bounded — `feeVault` is admin-controlled (and validated non-zero), and `seller` is an authenticated party who created the listing/offer. Not a vulnerability; documented in `SECURITY.md`.

### Interface resolution

```solidity
78:   function supportsInterface(bytes4 interfaceId)
79:     public
80:     view
81:     virtual
82:     override(AccessControl, ERC1155Holder)
83:     returns (bool)
84:   {
85:     return super.supportsInterface(interfaceId);
86:   }
```
**Lines 78–86** — Diamond-inheritance resolution. Both `AccessControl` and `ERC1155Holder` define `supportsInterface`. Solidity requires an explicit override naming all parent definitions. `super` walks the linearised MRO and returns true if any parent supports the queried interface. Without this, the contract would not compile.

### Standard-aware transfer dispatch

```solidity
89:   function _transferToken(
90:     TokenStandard standard,
91:     address coll,
92:     address from,
93:     address to,
94:     uint256 id,
95:     uint256 amount
96:   ) internal {
97:     if (standard == TokenStandard.ERC721) {
98:       IERC721(coll).safeTransferFrom(from, to, id);
99:     } else {
100:      IERC1155(coll).safeTransferFrom(from, to, id, amount, "");
101:    }
102:  }
```
**Lines 89–101** — Single dispatch point for all NFT transfers. ERC721 calls take 3 args (no amount); ERC1155 takes 5 (id + amount + empty data). Both invoke `safeTransferFrom` so the receiver contract gets its `onERC*Received` callback — required by ERC1155 (transfers to non-aware contracts revert), strongly recommended by ERC721.

`from` is always the seller — these contracts never escrow. The transfer uses the seller's *prior* approval to this contract; if the seller revokes between listing and settlement, the call reverts and the buyer's funds stay with the buyer (tx atomicity).

**Communicates with:** ERC721/ERC1155 collection at `coll`. Reverts propagate through the entire trade tx.

---

## 2. `Marketplace.sol` — fixed-price listings

```solidity
1: // SPDX-License-Identifier: MIT
2: pragma solidity 0.8.24;
4: import {MarketplaceCore, TokenStandard} from "./MarketplaceCore.sol";
5: import {IERC721}  from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
6: import {IERC1155} from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";
```
**Lines 1–6** — Imports `TokenStandard` enum and the abstract base. Token interfaces are re-imported because we make direct ownership/approval queries here (not just in `_transferToken`).

```solidity
8:  error NotOwner();        // seller doesn't own the token
9:  error NotListed();       // listing slot empty
10: error WrongPrice();      // msg.value != listing.price (or price == 0)
11: error Expired();         // block.timestamp > listing.expiresAt
12: error NotApproved();     // marketplace lacks transfer approval
13: error InvalidExpiry();   // expiresAt <= block.timestamp
14: error InvalidAmount();   // ERC1155 amount == 0
15: error AlreadyListed();   // another seller has the (coll,id) slot
```
**Lines 8–15** — Error palette. `AlreadyListed` was added during production-readiness: prevents seller A's listing from being silently overwritten by seller B if both list the same `(coll,id)` (this can happen for ERC1155 where multiple owners exist).

### Listing struct (packed storage)

```solidity
22: struct Listing {
23:   address       seller;     // slot 0 lower 20 bytes
24:   uint64        expiresAt;  // slot 0 next 8 bytes
25:   TokenStandard standard;   // slot 0 next 1 byte
26:   uint128       price;      // slot 1 lower 16 bytes
27:   uint128       amount;     // slot 1 upper 16 bytes
28: }
```
**Lines 22–28** — Two storage slots per listing. Slot 0 packs `seller(20) + expiresAt(8) + standard(1) = 29 bytes` (fits in 32). Slot 1 packs `price(16) + amount(16) = 32` exactly. The compiler does this packing automatically because the field order follows slot rules. Reordering would inflate to 3 slots.

```solidity
31: mapping(address => mapping(uint256 => Listing)) public listings;
```
**Line 31** — `listings[collection][tokenId]`. One listing per `(coll, id)` pair. For ERC1155, where multiple owners can exist for the same id, the first seller occupies the slot until they cancel/sell or it expires. Documented limitation; matches OpenSea Seaport's per-listing key model.

```solidity
33: event Listed(...);
42: event Cancelled(...);
43: event Bought(...);
```
**Lines 33–53** — Events indexed by `(coll, id, seller/buyer)` so frontends (and any future indexer) can filter by collection or user.

### Constructor

```solidity
54: constructor(address admin, address vault, uint16 fee) MarketplaceCore(admin, vault, fee) {}
```
**Line 54** — Pure forwarding constructor. Marketplace adds no construction-time state beyond what `MarketplaceCore` initialises.

### List entry points

```solidity
57: function list(address coll, uint256 id, uint128 price, uint64 expiresAt) external whenNotPaused {
58:   _list(TokenStandard.ERC721, coll, id, 1, price, expiresAt);
59: }
```
**Lines 57–59** — ERC721 path. Amount is hard-coded to 1 (ERC721 NFTs are unique). `whenNotPaused` is the only gate on listing creation — listings during pause are blocked but cancellation is not.

```solidity
62: function list1155(address coll, uint256 id, uint128 amount, uint128 price, uint64 expiresAt) external whenNotPaused {
63:   if (amount == 0) revert InvalidAmount();
64:   _list(TokenStandard.ERC1155, coll, id, amount, price, expiresAt);
65: }
```
**Lines 62–65** — ERC1155 path. Zero-amount guard prevents free-NFT exploits on the buy side (defensive — the price check would catch it too).

### `_list` shared logic

```solidity
75:   if (price == 0) revert WrongPrice();
76:   if (expiresAt <= block.timestamp) revert InvalidExpiry();
```
**Line 75** — Zero-price listing forbidden. Stops accidental "free for taker" listings (which a frontend bug could otherwise create).
**Line 76** — Past expiry forbidden. `<=` so a listing expiring this same block is rejected.

```solidity
80:   address curSeller = listings[coll][id].seller;
81:   if (curSeller != address(0) && curSeller != msg.sender) revert AlreadyListed();
```
**Lines 80–81** — **Production-readiness patch.** Before this fix, anyone could overwrite anyone else's active listing simply by calling `list` on the same `(coll,id)`. The original seller can still overwrite their own listing (relisting at new price).

```solidity
83:   if (standard == TokenStandard.ERC721) {
84:     if (IERC721(coll).ownerOf(id) != msg.sender) revert NotOwner();
85:     if (!IERC721(coll).isApprovedForAll(msg.sender, address(this))
86:         && IERC721(coll).getApproved(id) != address(this)) revert NotApproved();
87:   } else {
88:     if (IERC1155(coll).balanceOf(msg.sender, id) < amount) revert NotOwner();
89:     if (!IERC1155(coll).isApprovedForAll(msg.sender, address(this))) revert NotApproved();
90:   }
```
**Lines 83–86** — ERC721 ownership + approval. Two approval paths: per-token (`approve`) or operator (`setApprovalForAll`). Short-circuit `||` is correct here — only one needs to be true.
**Lines 87–90** — ERC1155 balance + approval. Note ERC1155 has no per-token approval (only operator-wide), so a single check.

```solidity
92:   listings[coll][id] = Listing({
93:     seller: msg.sender,
94:     expiresAt: expiresAt,
95:     standard: standard,
96:     price: price,
97:     amount: amount
98:   });
99:   emit Listed(coll, id, msg.sender, standard, amount, price, expiresAt);
```
**Lines 92–99** — Write listing then emit. Two storage slots written. Event indexed for downstream consumers.

### Cancel

```solidity
103: function cancel(address coll, uint256 id) external {
104:   Listing memory l = listings[coll][id];
105:   if (l.seller != msg.sender) revert NotOwner();
106:   delete listings[coll][id];
107:   emit Cancelled(coll, id, msg.sender);
108: }
```
**Line 103** — No `whenNotPaused`. Users can always cancel.
**Line 104** — Load into memory (one SLOAD per slot = 2 SLOADs). For just an access check we could SLOAD only `seller`, but `delete` would re-load anyway, so memory copy is fine.
**Lines 105–107** — Authorisation, zero out storage, emit. `delete` clears both slots (refunds gas pre-EIP-3529; reduced refund post).

### Buy

```solidity
111: function buy(address coll, uint256 id) external payable nonReentrant whenNotPaused {
112:   Listing memory l = listings[coll][id];
113:   if (l.seller == address(0)) revert NotListed();
114:   if (block.timestamp > l.expiresAt) revert Expired();
115:   if (msg.value != l.price) revert WrongPrice();
116:
117:   delete listings[coll][id];
118:
119:   _transferToken(l.standard, coll, l.seller, msg.sender, id, l.amount);
120:   (uint256 fee,) = _splitAndPay(l.seller, msg.value);
121:
122:   emit Bought(coll, id, msg.sender, l.seller, l.standard, l.amount, l.price, fee);
123: }
```
**Line 111** — `payable` to receive purchase value. `nonReentrant` to prevent a recursive call from a malicious seller contract during `_splitAndPay`. `whenNotPaused` for circuit breaker.
**Line 112–115** — Load listing, check exists, check non-expired, check exact-price (no partial fills, no overpay).
**Line 117** — **CEI ordering:** `delete` storage first to prevent a reentrancy from re-buying.
**Line 119** — Transfer NFT from seller to buyer. Reverts the whole tx if seller revoked approval or transferred away — buyer's ETH stays put due to atomicity.
**Line 120** — Split `msg.value` between fee recipient and seller. `fee` returned for event.
**Line 122** — Emit. Indexed by `(coll, id, buyer)`; seller carried in non-indexed for compact ABI.

**Communicates with:**
- ERC721/ERC1155 collection at `coll` (read approval + transfer).
- `feeVault` and `seller` via `_splitAndPay`.
- Indexer & frontend via `Bought` event.

---

## 3. `AuctionHouse.sol` — English auctions

```solidity
1:  // SPDX-License-Identifier: MIT
2:  pragma solidity 0.8.24;
4:  import {MarketplaceCore, TokenStandard, WithdrawFailed} from "./MarketplaceCore.sol";
5:  import {IERC721}  from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
6:  import {IERC1155} from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";
```
**Line 4** — Note the named import of `WithdrawFailed`. Solidity 0.8 requires errors used across files to be imported when not defined inline; reuse of the base contract's error keeps gas / bytecode predictable.

```solidity
8:  error NotSeller();          // not the auction creator
9:  error InvalidAmount();      // 1155 amount == 0
10: error NotActive();          // settled or non-existent
11: error AuctionEnded();       // bid after endsAt
12: error AuctionLive();        // settle before endsAt / cancel with bids
13: error BidTooLow();          // amount < minNext
14: error NoBids();             // settle with highestBidder == 0
15: error InvalidWindow();      // bad startsAt/endsAt
16: error NotApproved();
17: error NothingToWithdraw();  // pull-refund of zero
18: error BidOverflow();        // msg.value > uint128.max
19: error BadIncrement();       // minIncBps > cap
```
**Lines 8–19** — Error palette. `BidOverflow` and `BadIncrement` added in audit pass.

### Audit-pass constants

```solidity
27: uint16 public constant MAX_MIN_INCREMENT_BPS = 5_000;
30: uint64 public constant ANTI_SNIPE_WINDOW    = 5 minutes;
```
**Line 27** — Cap on bid-increment percentage at 50%. Without this, a malicious seller could create an auction with `minIncBps = 65_535` (≈655%), making the auction un-outbiddable beyond the reserve. Documented self-grief, but capped for sanity.
**Line 30** — Window for anti-snipe extension. Solidity time literal `5 minutes` = 300 seconds. Constant — set at compile time, no SLOAD cost.

### Auction struct

```solidity
34: struct Auction {
35:   address       seller;          // slot 0
36:   uint64        startsAt;        // slot 0
37:   uint16        minIncrementBps; // slot 0
38:   bool          settled;         // slot 0
39:   TokenStandard standard;        // slot 0
40:   address       collection;      // slot 1
41:   uint64        endsAt;          // slot 1
42:   uint256       tokenId;         // slot 2
43:   uint128       reserve;         // slot 3
44:   uint128       highestBid;      // slot 3
45:   address       highestBidder;   // slot 4
46:   uint128       amount;          // slot 5
47: }
```
**Lines 34–47** — 6 slots. Slot 0 packs seller(20) + startsAt(8) + minIncBps(2) + settled(1) + standard(1) = 32. Slot 1 packs collection(20) + endsAt(8) = 28 (4 bytes free, used in future packing). Other slots dominated by uint256/uint128.

### State

```solidity
50: uint256 public nextAuctionId;
52: mapping(uint256 => Auction) public auctions;
55: mapping(address => uint256) public pendingReturns;
```
**Line 50** — Auto-incrementing id. First valid id is 1 (slot starts at 0, increment-then-use).
**Line 52** — Auction storage.
**Line 55** — Refund balances for outbid bidders. **Pull pattern critical to the design**: without it, an attacker contract could `revert` on `receive()` to make every outbid fail, locking the auction at their bid price. See `test_maliciousBidderCannotBlockOutbid`.

### Events

```solidity
57: event AuctionCreated(...);  // 3 indexed: id, coll, tokenId
68: event BidPlaced(...);       // 2 indexed: id, bidder
69: event AuctionSettled(...);  // 3 indexed: id, winner, seller
70: event AuctionCancelled(uint256 indexed id);
71: event RefundWithdrawn(address indexed bidder, uint256 amount);
72: event AuctionExtended(uint256 indexed id, uint64 newEndsAt);
```
**Lines 57–72** — `AuctionExtended` emitted whenever anti-snipe extends `endsAt`. Frontend uses this to re-render the countdown.

### Constructor

```solidity
74: constructor(address admin, address vault, uint16 fee) MarketplaceCore(admin, vault, fee) {}
```
**Line 74** — Forwards to base.

### Create

```solidity
77: function create(address coll, uint256 tokenId, uint128 reserve, uint64 startsAt, uint64 endsAt, uint16 minIncBps)
78:   external whenNotPaused returns (uint256 id) { return _create(TokenStandard.ERC721, coll, tokenId, 1, reserve, startsAt, endsAt, minIncBps); }
83: function create1155(address coll, uint256 tokenId, uint128 amount, uint128 reserve, uint64 startsAt, uint64 endsAt, uint16 minIncBps)
84:   external whenNotPaused returns (uint256 id) {
85:     if (amount == 0) revert InvalidAmount();
86:     return _create(TokenStandard.ERC1155, coll, tokenId, amount, reserve, startsAt, endsAt, minIncBps);
87:   }
```
**Lines 77–87** — Thin wrappers; differ only in `TokenStandard` and amount default.

```solidity
107: if (endsAt <= startsAt || startsAt < block.timestamp) revert InvalidWindow();
108: if (minIncBps > MAX_MIN_INCREMENT_BPS) revert BadIncrement();
```
**Line 107** — Reject end-before-start and start-in-past. Allows `startsAt == block.timestamp` (auction live immediately).
**Line 108** — Audit-patch: cap minimum increment.

```solidity
110: if (standard == TokenStandard.ERC721) { ... } else { ... }
```
**Lines 110–117** — Ownership + approval gate, mirrors `Marketplace._list`. Note ownership is checked at `create`, not at `settle` — if the seller transfers the NFT mid-auction, `settle` will revert (the NFT must transfer from the original seller). Documented non-custodial trade-off.

```solidity
119: id = ++nextAuctionId;
120: auctions[id] = Auction({ ... });
123:   minIncrementBps: minIncBps == 0 ? 500 : minIncBps,
134: emit AuctionCreated(...);
```
**Line 119** — Pre-increment so first id is 1 (id 0 used as sentinel for "no auction").
**Line 123** — Sane default of 5% (`500` bps) if seller passes 0. Caller-friendly.

### Bid

```solidity
139: function bid(uint256 id) external payable nonReentrant whenNotPaused {
140:   Auction storage a = auctions[id];
141:   if (a.seller == address(0) || a.settled) revert NotActive();
142:   if (block.timestamp < a.startsAt || block.timestamp >= a.endsAt) revert AuctionEnded();
```
**Line 139** — Payable + reentrancy guard + pause gate.
**Line 140** — `storage` (not memory) so we can write back without a full copy.
**Lines 141–142** — Active checks. Note `>= endsAt` blocks bids exactly at end timestamp; `settle` uses the matching `< endsAt` reject. No edge.

```solidity
144: if (msg.value > type(uint128).max) revert BidOverflow();
145: uint128 amount = uint128(msg.value);
```
**Line 144** — **Audit-patch:** explicit guard before silent truncation. Practically impossible on Coston2 (total supply << 2^128) but defensive.

```solidity
148: uint256 incRaw = uint256(a.highestBid) * a.minIncrementBps / 10_000;
149: uint128 minNext;
150: if (a.highestBid == 0) {
151:   minNext = a.reserve == 0 ? 1 : a.reserve;
152: } else {
153:   uint256 next = uint256(a.highestBid) + (incRaw == 0 ? 1 : incRaw);
154:   if (next > type(uint128).max) revert BidOverflow();
155:   minNext = uint128(next);
156: }
157: if (amount < minNext) revert BidTooLow();
```
**Line 148** — Increment in wei. Promotes to uint256 to avoid intermediate overflow (uint128 * uint16 = max 2^144, fits in uint256).
**Lines 150–151** — First bid path: must meet reserve (or 1 wei if reserve is 0).
**Lines 152–155** — Subsequent bid path. **Audit-patch:** when `incRaw == 0` (small `highestBid`, small `minIncBps`), force a `+1` wei minimum so an equal-value bid cannot displace the prior bidder by recency. Without this, two bidders at the same wei amount would let the second bidder steal the highest-bidder slot. Overflow check on `next` prevents the same uint128 issue as line 144.
**Line 157** — Strict-less-than check.

```solidity
159: address prev    = a.highestBidder;
160: uint128 prevAmt = a.highestBid;
162: a.highestBid    = amount;
163: a.highestBidder = msg.sender;
```
**Lines 159–163** — Cache prior bidder, then update. State change happens before any external interaction (CEI for the refund credit; no external call yet).

```solidity
168: uint64 endsAt = a.endsAt;
169: if (endsAt - block.timestamp < ANTI_SNIPE_WINDOW) {
170:   uint64 newEnd = uint64(block.timestamp) + ANTI_SNIPE_WINDOW;
171:   a.endsAt = newEnd;
172:   emit AuctionExtended(id, newEnd);
173: }
```
**Lines 168–173** — **Audit-patch (anti-snipe):** if a winning bid arrives within the last 5 minutes, extend `endsAt` to `now + 5 minutes`. Each subsequent qualifying bid extends again. Subtraction is safe — we already checked `block.timestamp < endsAt` on line 142, so `endsAt - block.timestamp > 0`.

```solidity
175: if (prev != address(0)) {
176:   pendingReturns[prev] += prevAmt;
177: }
178: emit BidPlaced(id, msg.sender, amount);
```
**Lines 175–177** — Credit prior bidder. Skipped on first bid where `prev == address(0)`.
**Line 178** — Indexer consumes this for live-feed bids.

### Withdraw refund (pull pattern)

```solidity
182: function withdrawRefund() external nonReentrant {
183:   uint256 amt = pendingReturns[msg.sender];
184:   if (amt == 0) revert NothingToWithdraw();
185:   pendingReturns[msg.sender] = 0;
186:   (bool ok,) = msg.sender.call{value: amt}("");
187:   if (!ok) revert WithdrawFailed();
188:   emit RefundWithdrawn(msg.sender, amt);
189: }
```
**Lines 182–189** — Classic CEI. Read → check → zero → external call → emit. `nonReentrant` is belt-and-braces.

### Settle

```solidity
192: function settle(uint256 id) external nonReentrant {
193:   Auction storage a = auctions[id];
194:   if (a.seller == address(0) || a.settled) revert NotActive();
195:   if (block.timestamp < a.endsAt) revert AuctionLive();
196:   if (a.highestBidder == address(0)) revert NoBids();
198:   a.settled = true;
200:   _transferToken(a.standard, a.collection, a.seller, a.highestBidder, a.tokenId, a.amount);
201:   (uint256 fee,) = _splitAndPay(a.seller, a.highestBid);
203:   emit AuctionSettled(id, a.highestBidder, a.seller, a.highestBid, fee);
204: }
```
**Line 192** — No pause check on `settle` — never block a closed auction from settling, otherwise funds stuck. Anyone can call.
**Line 198** — Set settled before external interactions (CEI).
**Line 200** — NFT seller → winner.
**Line 201** — Fee + sale split.

### Cancel

```solidity
207: function cancel(uint256 id) external {
208:   Auction storage a = auctions[id];
209:   if (a.seller != msg.sender) revert NotSeller();
210:   if (a.highestBidder != address(0)) revert AuctionLive();
211:   a.settled = true;
212:   emit AuctionCancelled(id);
213: }
```
**Lines 207–213** — Only seller, only with no bids. Reuses `settled` flag as a "dead" marker. A user observing events should treat both `AuctionSettled` and `AuctionCancelled` as terminal.

---

## 4. `OfferBook.sol` — EIP-712 signed offers

```solidity
1: // SPDX-License-Identifier: MIT
2: pragma solidity 0.8.24;
4: import {MarketplaceCore, TokenStandard, WithdrawFailed} from "./MarketplaceCore.sol";
5: import {IERC721}  from "@openzeppelin/contracts/token/ERC721/IERC721.sol";
6: import {IERC1155} from "@openzeppelin/contracts/token/ERC1155/IERC1155.sol";
7: import {EIP712}   from "@openzeppelin/contracts/utils/cryptography/EIP712.sol";
8: import {ECDSA}    from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";
```
**Line 7** — `EIP712` provides `_hashTypedDataV4` (computes the full digest including domain separator).
**Line 8** — `ECDSA.recover` extracts signer from a `(digest, sig)` pair. Reverts on malformed signature (length ≠ 65 or low-s violation).

```solidity
10: error InvalidSig();      // signer != bidder
11: error OfferExpired();    // block.timestamp > expiresAt
12: error OfferUsed();       // nonce already burned
13: error NotOwner();
14: error WrongToken();      // tokenIdActual != offer.tokenId (when not collection-wide)
15: error InsufficientFunds(); // deposit < offer.amount
16: error NotApproved();
17: error InvalidAmount();   // 1155 units == 0
18: error ZeroOffer();       // amount == 0 (audit-patch)
```

### Contract header

```solidity
25: contract OfferBook is MarketplaceCore, EIP712 {
26:   using ECDSA for bytes32;
```
**Line 25** — Multi-inherit. `EIP712` constructor sets domain separator (name + version, see line 76).
**Line 26** — Attaches `recover(bytes)` to `bytes32` via using-for.

### Offer struct + typehash

```solidity
35: struct Offer {
36:   address bidder;
37:   address collection;
38:   uint256 tokenId;     // 0 == collection-wide
39:   uint128 amount;
40:   uint64  expiresAt;
41:   uint64  nonce;
42: }
44: bytes32 private constant OFFER_TYPEHASH = keccak256(
45:   "Offer(address bidder,address collection,uint256 tokenId,uint128 amount,uint64 expiresAt,uint64 nonce)"
46: );
```
**Lines 35–42** — Signed-message schema for ERC721 offers. `tokenId == 0` is the collection-wide sentinel (see warning comment 31–34 in source).
**Lines 44–46** — Typehash = keccak of the canonical EIP-712 type string. **Must match the literal struct field order and types exactly** — any change requires a new typehash.

### ERC1155 offer struct

```solidity
49: struct Offer1155 {
50:   address bidder;
51:   address collection;
52:   uint256 tokenId;
53:   uint128 units;
54:   uint128 amount;
55:   uint64  expiresAt;
56:   uint64  nonce;
57: }
59: bytes32 private constant OFFER1155_TYPEHASH = keccak256("Offer1155(...)");
```
**Lines 49–61** — Same shape with `units` added. Collection-wide is **not** supported for 1155 in MVP — a 1155 offer must name an exact tokenId.

### State + events

```solidity
64: mapping(address => mapping(uint64 => bool)) public usedNonce;
66: mapping(address => uint256) public deposits;
68: event Deposited(...);
69: event Withdrawn(...);
70: event OfferAccepted(...);
71: event Offer1155Accepted(...);
72: event OfferCancelled(...);
```
**Line 64** — Nonce burn map. Either consumed at accept-time or pre-emptively via `cancelOffer`. Nonces are per-bidder, so two bidders can use the same numeric nonce.
**Line 66** — Bidder ETH deposits. Required because an off-chain signed offer alone cannot pull ETH from a wallet — the bidder must escrow first.

### Constructor

```solidity
74: constructor(address admin, address vault, uint16 fee)
75:   MarketplaceCore(admin, vault, fee)
76:   EIP712("MagicWebbOfferBook", "1")
77: {}
```
**Line 76** — Domain `(name, version)` participates in every digest. **Changing either breaks every previously-signed offer.** The on-chain string remains **`MagicWebbOfferBook`** / version **`1`** so existing signatures stay valid; do not change post-deploy.

### Deposit / withdraw

```solidity
80: function deposit() external payable {
81:   deposits[msg.sender] += msg.value;
82:   emit Deposited(msg.sender, msg.value, deposits[msg.sender]);
83: }
```
**Lines 80–83** — Add `msg.value` to bidder balance. No reentrancy concern: no external call.

```solidity
86: function withdraw(uint256 amount) external nonReentrant {
87:   uint256 bal = deposits[msg.sender];
88:   if (amount > bal) revert InsufficientFunds();
89:   unchecked { deposits[msg.sender] = bal - amount; }
90:   (bool ok,) = msg.sender.call{value: amount}("");
91:   if (!ok) revert WithdrawFailed();
92:   emit Withdrawn(msg.sender, amount, deposits[msg.sender]);
93: }
```
**Lines 86–93** — CEI: read, check, write, external call, emit. `unchecked` safe because `amount ≤ bal` on line 88.

### Cancel nonce

```solidity
96: function cancelOffer(uint64 nonce) external {
97:   usedNonce[msg.sender][nonce] = true;
98:   emit OfferCancelled(msg.sender, nonce);
99: }
```
**Lines 96–99** — Bidder can burn a nonce pre-emptively if they want to invalidate an in-flight offer (e.g. they shared the signature publicly). Idempotent — re-cancelling a used nonce just no-ops.

### Hash + accept (ERC721)

```solidity
102: function hashOffer(Offer calldata o) public view returns (bytes32) {
103:   return _hashTypedDataV4(keccak256(abi.encode(
104:     OFFER_TYPEHASH, o.bidder, o.collection, o.tokenId, o.amount, o.expiresAt, o.nonce
105:   )));
106: }
```
**Lines 102–106** — Compute EIP-712 v4 digest:
1. `keccak256(typehash || abi.encode(fields))` — message hash.
2. `_hashTypedDataV4` prepends `\x19\x01 || domainSeparator || messageHash` and hashes.

Public so frontends can recompute the digest for debug; deterministic.

```solidity
109: function acceptOffer(Offer calldata o, bytes calldata sig, uint256 tokenIdActual)
110:   external nonReentrant whenNotPaused
111: {
112:   if (o.amount == 0) revert ZeroOffer();
113:   if (block.timestamp > o.expiresAt) revert OfferExpired();
114:   if (usedNonce[o.bidder][o.nonce]) revert OfferUsed();
115:   if (o.tokenId != 0 && o.tokenId != tokenIdActual) revert WrongToken();
```
**Line 112** — **Audit-patch:** reject zero-amount offers. Without this, a bidder could sign an offer with `amount=0` and a seller accepting would transfer the NFT for free (perhaps via a frontend bug or social engineering). Belt-and-braces.
**Lines 113–115** — Expiry, nonce-burn, tokenId-match (or collection-wide sentinel).

```solidity
117: bytes32 digest = hashOffer(o);
118: address signer = digest.recover(sig);
119: if (signer != o.bidder) revert InvalidSig();
```
**Lines 117–119** — Verify signature recovers to the offer's claimed bidder.

```solidity
121: if (IERC721(o.collection).ownerOf(tokenIdActual) != msg.sender) revert NotOwner();
122: if (!IERC721(o.collection).isApprovedForAll(msg.sender, address(this))
123:     && IERC721(o.collection).getApproved(tokenIdActual) != address(this)) revert NotApproved();
125: if (deposits[o.bidder] < o.amount) revert InsufficientFunds();
```
**Lines 121–125** — Caller must own the token, OfferBook must be approved, bidder must still have deposit covering the offer.

```solidity
127: usedNonce[o.bidder][o.nonce] = true;
128: unchecked { deposits[o.bidder] -= o.amount; }
130: _transferToken(TokenStandard.ERC721, o.collection, msg.sender, o.bidder, tokenIdActual, 1);
131: (uint256 fee,) = _splitAndPay(msg.sender, o.amount);
133: emit OfferAccepted(o.collection, tokenIdActual, msg.sender, o.bidder, o.amount, fee, o.nonce);
```
**Lines 127–128** — Burn nonce and debit deposit before external interactions (CEI).
**Line 130** — NFT seller (`msg.sender`) → bidder.
**Line 131** — Pay fee + seller from the debited deposit. Note `seller` to `_splitAndPay` is `msg.sender` (the offer-accepter, current NFT owner).

### Hash + accept (ERC1155)

```solidity
137: function hashOffer1155(Offer1155 calldata o) public view returns (bytes32) {
138:   return _hashTypedDataV4(keccak256(abi.encode(
139:     OFFER1155_TYPEHASH, o.bidder, o.collection, o.tokenId, o.units, o.amount, o.expiresAt, o.nonce
140:   )));
141: }
```
**Lines 137–141** — Same shape with `units`.

```solidity
144: function acceptOffer1155(Offer1155 calldata o, bytes calldata sig)
145:   external nonReentrant whenNotPaused
146: {
147:   if (o.units == 0) revert InvalidAmount();
148:   if (o.amount == 0) revert ZeroOffer();
149:   if (block.timestamp > o.expiresAt) revert OfferExpired();
150:   if (usedNonce[o.bidder][o.nonce]) revert OfferUsed();
152:   bytes32 digest = hashOffer1155(o);
153:   address signer = digest.recover(sig);
154:   if (signer != o.bidder) revert InvalidSig();
156:   if (IERC1155(o.collection).balanceOf(msg.sender, o.tokenId) < o.units) revert NotOwner();
157:   if (!IERC1155(o.collection).isApprovedForAll(msg.sender, address(this))) revert NotApproved();
159:   if (deposits[o.bidder] < o.amount) revert InsufficientFunds();
161:   usedNonce[o.bidder][o.nonce] = true;
162:   unchecked { deposits[o.bidder] -= o.amount; }
164:   _transferToken(TokenStandard.ERC1155, o.collection, msg.sender, o.bidder, o.tokenId, o.units);
165:   (uint256 fee,) = _splitAndPay(msg.sender, o.amount);
167:   emit Offer1155Accepted(o.collection, o.tokenId, msg.sender, o.bidder, o.units, o.amount, fee, o.nonce);
168: }
```
**Lines 144–168** — Mirror of ERC721 path with `units` parameter for 1155 transfer.

---

## 5. `FeeVault.sol` (removed from tree)

An optional standalone fee sink contract existed in earlier iterations; it is **not** in `contracts/src/` today. Each trade pays the platform fee with one low-level call to the configured `feeVault` address. A dedicated vault contract can be wired later by deploying one separately and calling `setFeeVault` (guarded by `FEE_ROLE`) — the line-by-line walkthrough of the old file has been dropped to avoid doc drift.

---

## Inter-contract data flow

```
Off-chain signer ──signs Offer──> Frontend
                                    │
                                    ▼
Bidder ──deposit ETH──> OfferBook ──(stores deposit)
                                    │
                                    │ Seller submits (offer, sig)
                                    ▼
        OfferBook.acceptOffer ─verifies sig─> ECDSA.recover
                                    │
                                    ├──(deposit -= amount)
                                    ├──(usedNonce = true)
                                    ├──(transferToken seller→bidder via _transferToken)
                                    └──(splitAndPay → feeVault + seller)


Seller ──list──> Marketplace ──(stores Listing)
                                    │
Buyer ──buy w/ ETH──>               │
                                    ├──(delete listing)
                                    ├──(transferToken seller→buyer)
                                    └──(splitAndPay → feeVault + buyerSentETH)


Seller ──create──> AuctionHouse ──(stores Auction)
Bidder ──bid w/ ETH──> (pendingReturns += prevBid, replace highestBidder)
Bidder ──withdrawRefund──> ETH out
Anyone ──settle (after endsAt)──> transferToken + splitAndPay
```

All three trade contracts:
- read approval/ownership from the **collection contract** (external, untrusted).
- write events any off-chain indexer would consume (topic layouts are stable for log filtering).
- pay the **feeVault** (EOA in prod) and the **seller**.

---

## Audit summary (post-patch)

| Finding | Severity | Fix |
|---|---|---|
| F1 — zero-amount offer | High | `ZeroOffer` revert in `acceptOffer*` |
| F2 — silent uint128 cast on `msg.value` | Med | `BidOverflow` check |
| F3 — unbounded `minIncrementBps` | Med | `BadIncrement` cap at 5000 bps |
| F4 — optional vault event ordering | Low | N/A — standalone `FeeVault` removed from repo |
| F5 — unindexed `FeeVaultUpdated` | Info | N/A — see `MarketplaceCore` events in-tree |
| F6 — floating pragma | Info | Pinned to 0.8.24 |
| F7 — auction sniping | Med | Anti-snipe extension |
| F8 — `tokenId == 0` collection sentinel collision | Info | Documented frontend constraint |
| F9 — equal-amount bid displacement when increment rounds to 0 | Low | Force `+1` wei when `incRaw == 0` |

**Residual (accepted) risks:**
- Non-custodial: seller can revoke approval mid-auction (refund-via-pull mitigates).
- Lazy listings (ERC1155 amount larger than current balance) — transfer reverts atomically at buy time; no fund loss.
- Block-timestamp dependency (~15s manipulation window cannot break expiry checks at minute-or-longer granularity).
- Native-ETH-only payments (no ERC20 path by design).

**Test coverage:** 49/49 forge tests pass post-patch, including dedicated regressions for F1, F3, F7, F9.

**Slither (residual):** 3 categories — all design choices: `arbitrary-send-eth` (paying authenticated seller), `timestamp` (expiry checks), `low-level-calls` (ETH transfer to EOA or contract).
