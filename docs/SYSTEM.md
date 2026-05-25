# MagicWebb — Complete System Reference

**Version:** 1.0 — 2026-05-25  
**Network:** Flare Coston2 (testnet) / Flare mainnet  
**License:** MIT

---

## Table of Contents

1. [System Architecture](#1-system-architecture)
2. [Smart Contracts](#2-smart-contracts)
3. [Platform Fee System](#3-platform-fee-system)
4. [User Flows — Step by Step](#4-user-flows--step-by-step)
5. [Developer Flows](#5-developer-flows)
6. [Internal Data Flow](#6-internal-data-flow)
7. [Security Model](#7-security-model)
8. [Environment Variables Reference](#8-environment-variables-reference)
9. [Operations Reference](#9-operations-reference)

---

## 1. System Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Browser / Wallet                          │
│  Next.js 14 (App Router)  •  wagmi v2  •  viem              │
└──────────────────────┬──────────────────────────────────────┘
                       │ REST + SSE
┌──────────────────────▼──────────────────────────────────────┐
│                 Go API Server (:8080)                         │
│  REST handlers  •  Redis pub/sub SSE  •  SIWE auth           │
└──────┬──────────────────────────────────────────────────────┘
       │ PostgreSQL (Supabase)         │ Redis
┌──────▼──────────────────────────────────────────────────────┐
│  Go Indexer (background worker)                              │
│  • Chain watcher (polls every 2 s)                          │
│  • Auction keeper (polls every 30 s — auto-settles)         │
│  • Offer expiry sweeper (every 5 min)                       │
│  • Trending score worker (every 60 s)                       │
└──────────────────────┬──────────────────────────────────────┘
                       │ eth_getLogs / eth_sendRawTransaction
┌──────────────────────▼──────────────────────────────────────┐
│            Flare Coston2 (chain 114) / Flare (chain 14)      │
│   Marketplace  •  AuctionHouse  •  OfferBook                 │
│   (inherit MarketplaceCore)                                  │
└─────────────────────────────────────────────────────────────┘
```

### Layer summary

| Layer | Technology | Purpose |
|-------|-----------|---------|
| Frontend | Next.js 14, wagmi v2, viem | UI, wallet connection, transaction submission |
| API | Go, Gin, Redis | REST endpoints, SSE real-time events, SIWE auth |
| Indexer | Go, eth_getLogs | Chain event watcher, keeper bot, DB population |
| Database | PostgreSQL (Supabase) | Persistent storage: listings, sales, auctions, collections |
| Cache | Redis | SSE pub/sub, rate limiting |
| Contracts | Solidity 0.8.26, Foundry | On-chain settlement — the only source of truth |

---

## 2. Smart Contracts

### 2.1 MarketplaceCore (abstract base)

`contracts/src/MarketplaceCore.sol`

Shared base inherited by all three marketplace contracts. Provides:

- `PLATFORM_FEE_BPS = 150` — the single hardcoded 1.5% fee constant
- `feeRecipient` — immutable wallet address that receives all fees
- `_splitAndPay(seller, salePrice)` — fee calculation and payment dispatch
- `_transferToken(standard, coll, from, to, id, amount)` — ERC-721/1155 dispatch
- `PAUSER_ROLE` — pause/unpause via AccessControl
- `ReentrancyGuard`, `Pausable`, `ERC1155Holder`

Constructor: `constructor(address recipient, address admin)`

**Key invariants:**
- `feeRecipient` cannot be changed post-deploy
- `PLATFORM_FEE_BPS` is a Solidity `constant` — not a variable, not changeable by anyone
- `_splitAndPay` always sends to `feeRecipient` first, then to seller — atomically in one function

### 2.2 Marketplace

`contracts/src/Marketplace.sol`

Fixed-price listings for ERC-721 and ERC-1155 tokens.

**State:**
```solidity
mapping(address => mapping(uint256 => Listing)) public listings;
```

**Listing struct (2 storage slots):**
```
slot 0: seller(20) + expiresAt(8) + standard(1) + [3 bytes padding]
slot 1: price(16)  + amount(16)
```

**Functions:**

| Function | Caller | Fee | Description |
|----------|--------|-----|-------------|
| `list(coll, id, price, expiresAt)` | seller | 1.5% of price upfront | List ERC-721. Approval required. |
| `list1155(coll, id, amount, price, expiresAt)` | seller | 1.5% of price upfront | List ERC-1155. Approval required. |
| `batchList(items[])` | seller | 1.5% of each price, summed | List up to 50 ERC-721 tokens in one tx. |
| `cancel(coll, id)` | seller only | none | Remove listing. Works while paused. |
| `buy(coll, id)` | buyer | 1.5% of price deducted from seller | Buy at exact listing price. Atomic. |

**Events:** `Listed`, `Cancelled`, `Bought`

### 2.3 AuctionHouse

`contracts/src/AuctionHouse.sol`

English auctions with fixed end time, commit-reveal MEV protection, and pull-pattern refunds.

**State:**
```solidity
mapping(uint256 => Auction) public auctions;
mapping(address => uint256) public pendingReturns;
mapping(uint256 => mapping(address => bytes32)) public commitments;
mapping(uint256 => mapping(address => uint256)) public commitBlock;
```

**Constants:**
- `MAX_MIN_INCREMENT_BPS = 5000` (50% max bid increment cap)
- `SETTLE_DEADLINE = 7 days` (after `endsAt`, winner may reclaim if unsettled)
- `COMMIT_DELAY_BLOCKS = 2` (MEV protection: 2-block minimum between commit and reveal)

**Functions:**

| Function | Caller | Fee | Description |
|----------|--------|-----|-------------|
| `create(coll, id, reserve, startsAt, endsAt, minIncBps)` | seller | none | Create ERC-721 auction. |
| `create1155(...)` | seller | none | Create ERC-1155 auction. |
| `commitBid(id, commitment)` | bidder | none | Phase 1: store bid hash. |
| `bid(id, fullAmount, salt)` | bidder | none | Phase 2: reveal bid, send ETH. |
| `settle(id)` | anyone | 1.5% of winning bid from seller proceeds | Finalize auction. NFT → winner, fee → feeRecipient, remainder → seller. |
| `cancel(id)` | seller | none | Cancel zero-bid auction. |
| `withdrawRefund()` | outbid bidder | none | Claim `pendingReturns` balance. |
| `reclaimBid(id)` | winner | none | Reclaim ETH if unsettled after 7 days. |

**Events:** `AuctionCreated`, `BidCommitted`, `BidPlaced`, `AuctionSettled`, `AuctionCancelled`, `RefundWithdrawn`, `BidReclaimed`

### 2.4 OfferBook

`contracts/src/OfferBook.sol`

EIP-712 signed off-chain offers with on-chain ETH escrow.

**State:**
```solidity
mapping(address => mapping(uint64 => bool)) public usedNonce;
mapping(address => uint256) public deposits;
```

**EIP-712 domain:** `"MagicWebbOfferBook"`, version `"1"`

**Functions:**

| Function | Caller | Fee | Description |
|----------|--------|-----|-------------|
| `deposit()` | offeror | none | Add ETH to bidder escrow balance. |
| `withdraw(amount)` | offeror | none | Remove ETH from escrow. Works while paused. |
| `cancelOffer(nonce)` | offeror | none | Burn nonce pre-emptively. Works while paused. |
| `acceptOffer(offer, sig, tokenIdActual)` | token owner | 1.5% of offer amount from seller proceeds | Accept ERC-721 offer. Verifies sig, transfers NFT, pays. |
| `acceptOffer1155(offer, sig)` | token owner | 1.5% of offer amount from seller proceeds | Accept ERC-1155 offer. |

**Events:** `Deposited`, `Withdrawn`, `OfferAccepted`, `Offer1155Accepted`, `OfferCancelled`

### 2.5 RoyaltyRegistry

`contracts/src/RoyaltyRegistry.sol`

Registry for ERC-2981 royalty info. Currently informational only — royalties are NOT deducted from settlement proceeds in the current version. Reserved for Phase 2.

---

## 3. Platform Fee System

### 3.1 The single rule

**One fee. 1.5%. Applied everywhere.**

```solidity
uint16 public constant PLATFORM_FEE_BPS = 150;
```

This is a Solidity `constant` — compiled into the bytecode. No admin key, environment variable, multisig vote, or upgrade can change it. To change the fee rate, new contracts must be deployed (new addresses = verifiable by users).

### 3.2 Fee recipient

```solidity
address public immutable feeRecipient;
```

Set once at deployment via `CREATOR_ADDR` env var. ETH is transferred directly to this address via `.call{value: fee}("")`. No vault contract, no accumulator, no intermediary. Fee lands in the wallet immediately on each transaction.

### 3.3 Fee application by operation

| Operation | When fee is taken | Who pays | Amount |
|-----------|------------------|----------|--------|
| `list` / `list1155` | At listing time (upfront) | Seller | 1.5% of listing price |
| `batchList` | At listing time (upfront, summed) | Seller | 1.5% × each item's price |
| `buy` | At buy time | Deducted from seller proceeds | 1.5% of sale price |
| `settle` (auction) | At settlement | Deducted from seller proceeds | 1.5% of winning bid |
| `acceptOffer` / `acceptOffer1155` | At acceptance | Deducted from seller proceeds | 1.5% of offer amount |

**Operations with NO fee:** create auction, commit/reveal bid, outbid refund withdrawal, offer deposit/withdrawal, offer cancellation, listing cancellation, zero-bid auction cancellation.

### 3.4 Fee calculation

```
fee = (amount * 150) / 10_000
sellerReceives = amount - fee
```

For a 1 ETH sale:
- Fee: `1e18 * 150 / 10000 = 0.015 ETH` → feeRecipient
- Seller receives: `1e18 - 0.015e18 = 0.985 ETH`

For a 1 ETH listing:
- Upfront listing fee: `1e18 * 150 / 10000 = 0.015 ETH` → feeRecipient (paid at list time)
- At buy: additional 1.5% of 1 ETH = 0.015 ETH → feeRecipient; seller gets 0.985 ETH

### 3.5 Fee flow diagram

```
LISTING:
  Seller calls list(price=1 ETH)
  Seller sends msg.value = 0.015 ETH (1.5% of price)
  Contract: feeRecipient.call{value: 0.015 ETH}("")  ← immediate direct transfer

BUY:
  Buyer calls buy() with msg.value = 1 ETH (listing price)
  Contract: NFT transferred seller → buyer
  Contract: _splitAndPay(seller, 1 ETH)
    └── feeRecipient.call{value: 0.015 ETH}("")  ← fee direct to wallet
    └── seller.call{value: 0.985 ETH}("")        ← remainder to seller

AUCTION SETTLE:
  Anyone calls settle(id)
  Contract: NFT transferred seller → winner
  Contract: _splitAndPay(seller, winningBid)
    └── feeRecipient.call{value: fee}("")
    └── seller.call{value: winningBid - fee}("")

OFFER ACCEPT:
  Owner calls acceptOffer(offer, sig, tokenId)
  Contract: debits bidder's deposit
  Contract: NFT transferred owner → bidder
  Contract: _splitAndPay(owner, offer.amount)
    └── feeRecipient.call{value: fee}("")
    └── owner.call{value: offer.amount - fee}("")
```

---

## 4. User Flows — Step by Step

### 4.1 Buy a listed token

1. User opens listing page → sees price, expiry, seller
2. User clicks **Buy** → frontend calls `buy(coll, id)` with `msg.value = listingPrice`
3. Wallet popup: user confirms exact price
4. On-chain (atomic): listing validated → NFT transferred → 1.5% fee to feeRecipient → remainder to seller → `Bought` event emitted
5. Frontend detects `Bought` event (SSE or tx receipt) → UI updates: NFT shows in buyer's profile

**Revert conditions:** price wrong, listing expired, listing doesn't exist, NFT transfer fails

### 4.2 List a single token (fixed price)

1. User goes to **List an NFT** → selects token → enters price and duration
2. Frontend checks approval: if not approved, sends `setApprovalForAll(marketplace, true)` first (wallet confirm)
3. Frontend calculates listing fee: `price * 150 / 10000`
4. User clicks **List** → frontend calls `list(coll, id, price, expiresAt)` with `msg.value = listingFee`
5. Wallet popup: user confirms listing fee amount
6. On-chain: fee sent to feeRecipient → listing stored → `Listed` event emitted
7. Frontend shows listing live

**Revert conditions:** msg.value ≠ fee (WrongPrice), price = 0, expiry invalid, not owner, not approved, already listed by different seller

### 4.3 Batch list (up to 50 tokens)

1. User goes to **Batch list** → selects up to 50 tokens → sets shared price and duration
2. Frontend calculates total fee: `sum(price_i * 150 / 10000)` for all selected
3. User clicks **List N tokens** → one `batchList(items[])` call with `msg.value = totalFee`
4. Wallet popup: single confirm for all tokens
5. On-chain: total fee → feeRecipient → each item listed → `Listed` events emitted

### 4.4 Create an auction

1. User opens token → clicks **Auction** → sets reserve, start/end times, min increment (bps)
2. Frontend checks approval for AuctionHouse → sends `setApprovalForAll` if needed
3. User clicks **Create auction** → `create(coll, id, reserve, startsAt, endsAt, minIncBps)` called
4. No fee at creation. `AuctionCreated` event emitted.
5. Auction is live when `block.timestamp >= startsAt`

### 4.5 Bid on an auction (2-step commit-reveal)

**Step 1 — Commit (no ETH required):**
1. User enters bid amount → frontend computes commitment hash:
   `keccak256(abi.encode(id, bidder, fullBidAmount, salt))`
2. User clicks **Commit bid** → `commitBid(id, commitment)` — no msg.value
3. Commitment saved to browser storage + on-chain

**Step 2 — Reveal (after 2+ blocks, ~4 seconds):**
4. After 2 blocks, **Reveal bid** button becomes active
5. User clicks **Reveal bid** → `bid(id, fullBidAmount, salt)` with `msg.value = fullBidAmount` (or increment if re-bidding)
6. On-chain: commitment verified, delay enforced, bid recorded, previous high bidder credited in `pendingReturns`

**If outbid:** ETH sits in `pendingReturns[bidder]` — claim via **Profile → Refunds → Withdraw**

### 4.6 Settle an auction

- **Automatic:** Keeper bot polls every 30s, calls `settle(id)` on expired auctions with bids
- **Manual:** Anyone can call `settle(id)` after `endsAt` — permissionless
- On-chain: NFT → winner, 1.5% of bid → feeRecipient, remainder → seller, `AuctionSettled` emitted

### 4.7 Make an offer

1. User opens token → clicks **Make offer**
2. If first offer: deposit ETH into OfferBook → `deposit()` with `msg.value` (reusable for all future offers)
3. User sets amount and expiry → frontend constructs EIP-712 `Offer` struct → requests wallet signature (no transaction, no gas)
4. Signed offer stored off-chain (backend) — visible to NFT owner

### 4.8 Accept an offer

1. Owner sees offer in **Profile → Offers → Received**
2. Owner clicks **Accept** → frontend calls `acceptOffer(offer, sig, tokenIdActual)`
3. On-chain: sig verified, nonce burned, NFT transferred, 1.5% fee → feeRecipient, remainder → owner, `OfferAccepted` emitted

### 4.9 Withdraw refund / deposit

- **Outbid refund:** Profile → Refunds → Withdraw → `withdrawRefund()` sends `pendingReturns[user]` to wallet
- **OfferBook deposit:** Profile → Deposit → Withdraw → `withdraw(amount)` returns deposited ETH
- Both work while contracts are paused

---

## 5. Developer Flows

### 5.1 First-time setup

```bash
git clone https://github.com/OfficialA1manac/MagicWebb && cd MagicWebb
make install
cp backend/.env.example .env
cp frontend/.env.example frontend/.env.local
# Edit .env: POSTGRES_URL, REDIS_URL, RPC_URL, JWT_SECRET, KEEPER_KEY
# Edit frontend/.env.local: CREATOR_ADDR, PRIVATE_KEY
```

### 5.2 Deploy contracts

```bash
# Testnet (Coston2, chain 114):
cd frontend && forge script script/DeployCoston2.s.sol \
  --rpc-url $RPC_URL --broadcast

# Mainnet (Flare, chain 14):
forge script script/DeployFlare.s.sol \
  --rpc-url $RPC_URL --broadcast
```

Required env vars: `PRIVATE_KEY`, `CREATOR_ADDR`
- `CREATOR_ADDR` becomes both `feeRecipient` and the initial `DEFAULT_ADMIN_ROLE`
- Fee rate is `PLATFORM_FEE_BPS = 150` — hardcoded, not configurable

After deploy, copy output addresses into `backend/.env` and `frontend/.env.local`.

### 5.3 Regenerate ABIs

After any contract change + redeploy:
```bash
make contracts-build   # forge build
make regen-abi         # writes frontend/lib/abi/*.ts
```

### 5.4 Full fresh deploy (all-in-one)

```bash
make fresh-deploy
# Runs: build → test → deploy → regen-abi → update env → start Docker
```

### 5.5 Start / stop services

```bash
make up       # start all Docker services (restart:always — survives reboots)
make down     # stop all services
make logs     # tail logs
make health   # HTTP health check
make status   # process/port/chain status
```

### 5.6 Run tests

```bash
cd contracts && forge test          # all tests
forge test -vvv                     # verbose output
forge test --match-test test_listAndBuy  # single test
```

---

## 6. Internal Data Flow

### 6.1 Event emission → indexer → database → API → frontend

```
1. User submits tx (e.g. buy)
2. Contract emits event (e.g. Bought)
3. Indexer polls eth_getLogs every 2s, detects event
4. Handler: onBought() → inserts row into sales table (price, fee, seller, buyer, tx_hash)
5. Indexer publishes to Redis pub/sub channel (e.g. "marketplace:events")
6. API SSE handler reads Redis → pushes event to connected browser clients
7. Frontend receives SSE event → invalidates React Query cache → UI updates
```

### 6.2 Database schema (key tables)

```sql
listings      -- active fixed-price listings
sales         -- completed trades (price_wei, fee_wei, royalty_wei, tx_hash)
auctions      -- auction records (reserve, highest_bid, settled)
bids          -- bid history per auction
offers        -- signed off-chain offers (pending/accepted/cancelled)
collections   -- NFT collection metadata
tokens        -- per-token metadata cache
```

The `fee_wei` column in `sales` records the exact 1.5% fee taken on every settled trade, available for analytics and metrics.

### 6.3 Keeper bot (auction auto-settlement)

```
Keeper goroutine wakes every 30 seconds:
  1. Queries DB: SELECT auctions WHERE endsAt < now AND settled = false AND highest_bid > 0
  2. For each: calls settle(auctionId) via KEEPER_KEY wallet
  3. Contract: NFT → winner, fee → feeRecipient, remainder → seller
  4. Indexer detects AuctionSettled event → updates DB
```

Keeper wallet needs small FLR balance for gas. Fund it separately from `CREATOR_ADDR`.

---

## 7. Security Model

### 7.1 Smart contract security

| Vector | Mitigation |
|--------|-----------|
| Reentrancy | `nonReentrant` on all payable settlement functions (`buy`, `bid`, `settle`, `acceptOffer`, `withdrawRefund`) |
| Fee rate manipulation | `PLATFORM_FEE_BPS` is a compile-time `constant` — zero admin surface |
| Fee recipient change | `feeRecipient` is `immutable` — set once at deploy, frozen forever |
| Signature replay (offers) | EIP-712 domain includes `chainId` + `verifyingContract`; nonce burn map |
| Listing overwrite | `AlreadyListed` check prevents third-party overwrites |
| NFT transfer failure | Fee sent AFTER NFT transfer for `buy`/`settle`/`accept`; if transfer reverts, whole tx reverts, no fee taken |
| Pull-payment DOS | Outbid refunds use pull pattern (`pendingReturns`) — no push that can be griefed |
| Bid front-running (MEV) | 2-block commit-reveal delay on all auction bids |
| Stuck auction (no settle) | `reclaimBid` allows winner to reclaim ETH after `SETTLE_DEADLINE = 7 days` |
| Admin key compromise | Admin can only: pause contracts, grant/revoke roles — cannot change fee, recipient, or drain funds |

### 7.2 Immutability guarantees

These values are set once at deploy and can never be changed without deploying new contracts at new addresses:
- `feeRecipient` — who receives fees
- `PLATFORM_FEE_BPS` — how much the fee is (a constant, not even stored as state)
- EIP-712 domain (OfferBook) — signature domain binding

Users can verify these by reading the deployed bytecode.

---

## 8. Environment Variables Reference

### Root `.env` (backend + indexer)

| Key | Required | Description |
|-----|----------|-------------|
| `POSTGRES_URL` | yes | Supabase/PostgreSQL connection string |
| `REDIS_URL` | yes | Redis URL (default: `redis://redis:6379`) |
| `RPC_URL` | yes | Flare/Coston2 JSON-RPC URL |
| `CHAIN_ID` | yes | `114` (Coston2) or `14` (Flare mainnet) |
| `MARKETPLACE_ADDR` | yes | Deployed Marketplace contract address |
| `AUCTION_ADDR` | yes | Deployed AuctionHouse contract address |
| `OFFERBOOK_ADDR` | yes | Deployed OfferBook contract address |
| `JWT_SECRET` | yes | 32+ byte hex for SIWE JWT signing |
| `FRONTEND_URL` | yes | CORS origin (e.g. `http://localhost:3000`) |
| `KEEPER_KEY` | optional | Keeper wallet private key (auto-settles auctions) |
| `INDEX_FROM_BLOCK` | optional | Start block for log scanning (auto-set by `make deploy`) |

### `frontend/.env.local`

| Key | Required | Description |
|-----|----------|-------------|
| `NEXT_PUBLIC_CHAIN_ID` | yes | `114` (Coston2) or `14` (mainnet) |
| `NEXT_PUBLIC_RPC_URL` | yes | Chain RPC URL |
| `NEXT_PUBLIC_MARKETPLACE_ADDR` | yes | Marketplace contract address |
| `NEXT_PUBLIC_AUCTION_ADDR` | yes | AuctionHouse contract address |
| `NEXT_PUBLIC_OFFER_ADDR` | yes | OfferBook contract address |
| `NEXT_PUBLIC_API_URL` | yes | Backend API URL (e.g. `http://localhost:8080`) |
| `NEXT_PUBLIC_EXPLORER_URL` | yes | Chain explorer URL |
| `NEXT_PUBLIC_CURRENCY_SYMBOL` | yes | `C2FLR` or `FLR` |
| `PRIVATE_KEY` | deploy only | Deployer private key (never commit) |
| `CREATOR_ADDR` | deploy only | Fee recipient + admin wallet address |

**Note:** `FEE_BPS` is not an env var. The 1.5% fee is hardcoded as `PLATFORM_FEE_BPS = 150` in the contracts.

---

## 9. Operations Reference

### Service ports
| Service | Port | Notes |
|---------|------|-------|
| Frontend | 3000 | Next.js SSR |
| API | 8080 | REST + SSE |
| Redis | 6379 | Internal only |
| PostgreSQL | 5432 | Supabase-hosted (not local) |

### Make targets
```bash
make up           # start all Docker services
make down         # stop all services
make logs         # tail all logs
make health       # HTTP health check
make status       # process/port/chain status
make fresh-deploy # full redeploy pipeline
make regen-abi    # regenerate TypeScript ABIs from compiled JSON
make contracts-build  # forge build
```

### Contract interaction via CLI (no frontend)
```bash
# Read a listing
cast call $MARKETPLACE_ADDR "listings(address,uint256)" $COLL_ADDR $TOKEN_ID

# Check feeRecipient
cast call $MARKETPLACE_ADDR "feeRecipient()"

# Check platform fee
cast call $MARKETPLACE_ADDR "PLATFORM_FEE_BPS()"
# Returns: 0x0000...0096 (= 150 decimal)

# Settle an auction directly
cast send $AUCTION_ADDR "settle(uint256)" $AUCTION_ID --private-key $PRIVATE_KEY
```

### Verifying fee rate on deployed contract
Because `PLATFORM_FEE_BPS` is a public constant, it is readable via any ABI-compatible call:
```bash
cast call $MARKETPLACE_ADDR "PLATFORM_FEE_BPS()" --rpc-url $RPC_URL
# Must return 150 (0x96) — if it returns anything else, you are talking to a different contract
```
