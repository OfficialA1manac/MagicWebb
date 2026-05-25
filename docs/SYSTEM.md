# MagicWebb — Complete System Reference

**Version:** 2.0 — 2026-05-25  
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
│  • Auction inactivity sweeper (auto-cancels within 30 min)  │
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
- `feeRecipient` — immutable wallet address that receives all platform fees
- `_splitAndPay(seller, salePrice)` — fee calculation and payment dispatch
- `_transferToken(standard, coll, from, to, id, amount)` — ERC-721/1155 dispatch
- `ReentrancyGuard`, `ERC1155Holder`

Constructor: `constructor(address recipient)`

**Key invariants:**
- `feeRecipient` cannot be changed post-deploy
- `PLATFORM_FEE_BPS` is a Solidity `constant` — not a variable, not changeable by anyone
- No pause function. No admin role. Contracts are unstoppable once deployed.

### 2.2 Marketplace

`contracts/src/Marketplace.sol`

Fixed-price listings for ERC-721 and ERC-1155 tokens.

**State:**
```solidity
mapping(address => mapping(uint256 => Listing)) public listings;
```

**Functions:**

| Function | Caller | Fee | Description |
|----------|--------|-----|-------------|
| `list(coll, id, price, expiresAt)` | seller | 1.5% of price upfront | List ERC-721. Approval required. |
| `list1155(coll, id, amount, price, expiresAt)` | seller | 1.5% of price upfront | List ERC-1155. Approval required. |
| `batchList(items[])` | seller | 1.5% of each price, summed | List up to 50 ERC-721 tokens in one tx. |
| `cancel(coll, id)` | seller only | none | Remove listing. |
| `buy(coll, id)` | buyer | 1.5% of price deducted from seller proceeds | Buy at exact listing price. Atomic. |

**Events:** `Listed`, `Cancelled`, `Bought`

### 2.3 AuctionHouse

`contracts/src/AuctionHouse.sol`

English auctions with auto-settlement, instant push-refunds for outbid bidders, and no manual work required from participants.

**State:**
```solidity
mapping(uint256 => Auction) public auctions;
mapping(address => uint256) public pendingReturns; // emergency fallback only
```

**Constants:**
- `MAX_MIN_INCREMENT_BPS = 5000` (50% max bid increment cap)
- `NO_BID_CANCEL_WINDOW = 30 minutes` (auto-cancel deadline for zero-bid auctions)

**Auction flow:**
1. Seller calls `create` → `AuctionCreated` event. Auction starts immediately.
2. Bidder calls `bid(id, bidAmount)` with `msg.value = bidAmount + 1.5% fee`. Previous highest bidder is refunded their full payment automatically. `BidPlaced` event.
3. At expiry, keeper bot (or anyone) calls `settle(id)` → NFT → winner, fee → feeRecipient, full bid → seller. `AuctionSettled` event.

**Fee semantics for auctions:**
- Bidder pays 1.5% on top of their bid at bid time.
- If they win: the 1.5% goes to the platform.
- If they lose (outbid): full payment (bid + fee) is pushed back immediately — no fee taken.
- The seller always receives 100% of the winning bid amount.

**Functions:**

| Function | Caller | Fee | Description |
|----------|--------|-----|-------------|
| `create(coll, id, reserve, endsAt, minIncBps)` | seller | none | Create ERC-721 auction. Starts immediately. |
| `create1155(...)` | seller | none | Create ERC-1155 auction. |
| `bid(id, bidAmount)` | bidder | 1.5% of bidAmount paid upfront | Single-step bid. msg.value = bidAmount + fee. Outbid refund pushed automatically. |
| `settle(id)` | anyone | fee retained from winner's upfront payment | Auto-settle. Called by keeper bot after expiry. |
| `cancelIfInactive(id)` | anyone | none | Cancel zero-bid auction after 30-minute window. Keeper calls this. |
| `cancelEarly(id)` | seller only | none | Seller cancels early (manual approval). Refunds highest bidder if any. |
| `withdrawRefund()` | bidder | none | Emergency: reclaim ETH if automatic push failed. |

**Events:** `AuctionCreated`, `BidPlaced`, `AuctionSettled`, `AuctionCancelled`, `RefundPushed`

### 2.4 OfferBook

`contracts/src/OfferBook.sol`

On-chain NFT offer system. Owners explicitly mark tokens as eligible to receive offers.

**State:**
```solidity
mapping(address => mapping(uint256 => address)) public eligible;      // ERC-721 eligibility
mapping(address => mapping(uint256 => address)) public eligible1155;  // ERC-1155 eligibility
mapping(address => mapping(uint256 => mapping(address => uint256))) public offers;       // ERC-721 offers
mapping(address => mapping(uint256 => mapping(address => Offer1155))) public offers1155; // ERC-1155 offers
```

**Functions:**

| Function | Caller | Fee | Description |
|----------|--------|-----|-------------|
| `markEligible(coll, tokenId)` | token owner | none | Mark ERC-721 as eligible to receive offers. |
| `removeEligible(coll, tokenId)` | owner who marked | none | Stop accepting new offers. Existing offers persist. |
| `makeOffer(coll, tokenId)` | anyone | none | Deposit ETH offer for an eligible ERC-721. Accumulates on repeat calls. |
| `withdrawOffer(coll, tokenId)` | offeror | none | Reclaim full ETH — no fee on unaccepted offers. |
| `acceptOffer(coll, tokenId, bidder)` | token owner | 1.5% of offer amount from seller proceeds | Accept offer. NFT → bidder. Eligibility auto-cleared. |
| `markEligible1155(coll, tokenId)` | holder | none | Mark ERC-1155 as eligible. |
| `removeEligible1155(coll, tokenId)` | owner who marked | none | Stop accepting ERC-1155 offers. |
| `makeOffer1155(coll, tokenId, units)` | anyone | none | One offer per bidder per token. Withdraw to update. |
| `withdrawOffer1155(coll, tokenId)` | offeror | none | Reclaim full ETH. |
| `acceptOffer1155(coll, tokenId, bidder)` | holder | 1.5% of offer amount from seller proceeds | Accept ERC-1155 offer. |

**Events:** `EligibilityMarked`, `EligibilityRemoved`, `Eligibility1155Marked`, `Eligibility1155Removed`, `OfferMade`, `OfferWithdrawn`, `OfferAccepted`, `Offer1155Made`, `Offer1155Withdrawn`, `Offer1155Accepted`

---

## 3. Platform Fee System

### 3.1 The single rule

**One fee. 1.5%. Applied everywhere.**

```solidity
uint16 public constant PLATFORM_FEE_BPS = 150;
```

This is a Solidity `constant` — compiled into the bytecode. No admin key, environment variable, multisig vote, or upgrade can change it.

### 3.2 Fee recipient

```solidity
address public immutable feeRecipient;
```

Set once at deployment via `CREATOR_ADDR` env var. ETH is transferred directly to this address via `.call{value: fee}("")`. No vault contract, no accumulator, no intermediary.

### 3.3 Fee application by operation

| Operation | When fee is taken | Who pays | Amount |
|-----------|------------------|----------|--------|
| `list` / `list1155` | At listing time (upfront) | Seller | 1.5% of listing price |
| `batchList` | At listing time (upfront, summed) | Seller | 1.5% × each item's price |
| `buy` | At buy time | Deducted from seller proceeds | 1.5% of sale price |
| `bid` (auction) | At bid time (upfront, on top of bid) | Bidder | 1.5% of bid amount |
| `settle` (auction) | At settlement | Retained from winner's upfront payment | 1.5% of winning bid |
| `acceptOffer` / `acceptOffer1155` | At acceptance | Deducted from seller proceeds | 1.5% of offer amount |

**Operations with NO fee:** create auction, outbid refund (pushed automatically), unaccepted offer withdrawal, listing cancellation, zero-bid auction cancellation, marking/removing offer eligibility.

### 3.4 Auction fee detail

Bidder bids 1 ETH: sends `1.015 ETH` (1 ETH bid + 0.015 ETH fee).

- If they win: platform gets 0.015 ETH, seller gets 1 ETH.
- If outbid: they receive 1.015 ETH back in full — no fee kept.

### 3.5 Fee flow diagram

```
LISTING:
  Seller calls list(price=1 ETH)
  Seller sends msg.value = 0.015 ETH (1.5% of price)
  Contract: feeRecipient.call{value: 0.015 ETH}("")  ← immediate

BUY:
  Buyer calls buy() with msg.value = 1 ETH
  Contract: NFT transferred seller → buyer
  Contract: _splitAndPay(seller, 1 ETH)
    └── feeRecipient.call{value: 0.015 ETH}("")
    └── seller.call{value: 0.985 ETH}("")

AUCTION BID:
  Bidder calls bid(id, 1 ETH) with msg.value = 1.015 ETH
  If outbid later: previous bidder receives 1.015 ETH automatically

AUCTION SETTLE:
  Anyone calls settle(id)
  Contract: NFT → winner
  Contract: feeRecipient.call{value: 0.015 ETH}("")  ← exact premium paid by winner
  Contract: seller.call{value: 1 ETH}("")             ← full bid amount

OFFER ACCEPT:
  Owner calls acceptOffer(coll, tokenId, bidder)
  Contract: NFT transferred owner → bidder
  Contract: _splitAndPay(owner, offer.amount)
    └── feeRecipient.call{value: fee}("")
    └── owner.call{value: offer.amount - fee}("")
```

---

## 4. User Flows — Step by Step

### 4.1 Buy a listed token

1. User opens listing page → sees price, expiry, seller.
2. User clicks **Buy** → frontend calls `buy(coll, id)` with `msg.value = listingPrice`.
3. Wallet popup: confirm exact price.
4. On-chain (atomic): listing validated → NFT transferred → 1.5% fee to feeRecipient → remainder to seller → `Bought` event.
5. Frontend detects event → UI updates.

### 4.2 List a single token (fixed price)

1. User selects token → enters price and duration.
2. Frontend checks approval; if missing, sends `setApprovalForAll(marketplace, true)` first.
3. Frontend calculates listing fee: `price * 150 / 10000`.
4. User calls `list(coll, id, price, expiresAt)` with `msg.value = listingFee`.
5. On-chain: fee → feeRecipient → listing stored → `Listed` event.

### 4.3 Batch list (up to 50 tokens)

1. User selects up to 50 tokens → sets price and duration per token.
2. Frontend calculates total fee: `sum(price_i * 150 / 10000)`.
3. One `batchList(items[])` call with `msg.value = totalFee`.
4. On-chain: total fee → feeRecipient → each item listed.

### 4.4 Create an auction

1. User opens token → clicks **Auction** → sets reserve, end time, min increment (bps).
2. Frontend checks approval for AuctionHouse → sends `setApprovalForAll` if needed.
3. User calls `create(coll, id, reserve, endsAt, minIncBps)` — no fee at creation.
4. `AuctionCreated` event emitted. Auction starts immediately and accepts bids.

### 4.5 Bid on an auction

1. User enters bid amount → frontend computes required `msg.value = bidAmount + floor(bidAmount * 150 / 10000)`.
2. User clicks **Bid** → `bid(id, bidAmount)` called with exact msg.value.
3. On-chain: bid recorded; if previous bidder exists, their full payment is pushed back to them immediately. `BidPlaced` event.
4. If outbid later: full payment (bid + fee) is automatically pushed back — no action needed.

### 4.6 Auction settlement (automatic)

- **Automatic (normal path):** Keeper bot polls every 30 s, calls `settle(id)` on expired auctions with bids.
- **Manual fallback:** Anyone may call `settle(id)` after `endsAt`.
- On-chain: NFT → winner. Fee (premium paid by winner) → feeRecipient. Full bid amount → seller. `AuctionSettled` emitted.

### 4.7 Auction inactivity cancel (automatic)

- Keeper bot also sweeps for auctions with zero bids past the 30-minute window.
- Calls `cancelIfInactive(id)` — permissionless, anyone can trigger.

### 4.8 Auction early cancel (seller)

1. Seller clicks **Cancel Auction** on their active auction.
2. Wallet prompt requires manual approval (the user must sign the transaction).
3. `cancelEarly(id)` called — if a highest bidder exists, their full ETH is pushed back immediately.

### 4.9 Make an offer on an NFT

1. Owner marks token eligible: `markEligible(coll, tokenId)`.
2. Bidder navigates to token → clicks **Make Offer** → enters offer amount.
3. `makeOffer(coll, tokenId)` called with `msg.value = offerAmount`. ETH held on-chain.
4. Owner sees offer in their dashboard.
5. Owner accepts: `acceptOffer(coll, tokenId, bidder)` → NFT → bidder, 1.5% fee → feeRecipient, remainder → owner. Eligibility auto-cleared.
6. Bidder may `withdrawOffer(coll, tokenId)` at any time to get full ETH back — no fee taken.

### 4.10 Remove offer eligibility

Owner calls `removeEligible(coll, tokenId)` — stops new offers from being made.
Existing offers persist; bidders call `withdrawOffer` to reclaim their ETH.

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
cd contracts && forge script script/DeployCoston2.s.sol \
  --rpc-url $RPC_URL --broadcast

# Mainnet (Flare, chain 14):
forge script script/DeployFlare.s.sol \
  --rpc-url $RPC_URL --broadcast
```

Required env vars: `PRIVATE_KEY`, `CREATOR_ADDR`
- `CREATOR_ADDR` becomes the `feeRecipient` for all contracts (immutable post-deploy)
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
forge test --match-test test_settleAfterExpiry  # single test
```

---

## 6. Internal Data Flow

### 6.1 Event emission → indexer → database → API → frontend

```
1. User submits tx (e.g. bid)
2. Contract emits event (e.g. BidPlaced)
3. Indexer polls eth_getLogs every 2s, detects event
4. Handler: onBidPlaced() → inserts row into bids table
5. Indexer publishes to Redis pub/sub
6. API SSE handler reads Redis → pushes event to connected browsers
7. Frontend receives SSE → invalidates React Query cache → UI updates
```

### 6.2 Database schema (key tables)

```sql
listings      -- active fixed-price listings
sales         -- completed trades (price_wei, fee_wei, tx_hash)
auctions      -- auction records (reserve, highest_bid, settled)
bids          -- bid history per auction
offers        -- on-chain ETH offers per (collection, tokenId, bidder)
eligibility   -- tokens marked as eligible for offers
collections   -- NFT collection metadata
tokens        -- per-token metadata cache
```

### 6.3 Keeper bot (auction auto-settlement and auto-cancel)

**Auto-settle goroutine (every 30 s):**
```
1. Queries DB: SELECT auctions WHERE endsAt < now AND settled = false AND highest_bid > 0
2. For each: calls settle(auctionId) via KEEPER_KEY wallet
3. On-chain: NFT → winner, fee → feeRecipient, full bid → seller
4. Indexer detects AuctionSettled → updates DB
```

**Inactivity cancel goroutine (every 30 s):**
```
1. Queries DB: SELECT auctions WHERE created_at + 30min < now AND highest_bidder IS NULL AND settled = false
2. For each: calls cancelIfInactive(auctionId)
3. Indexer detects AuctionCancelled → updates DB
```

Keeper wallet needs a small FLR balance for gas. Fund it separately from `CREATOR_ADDR`.

---

## 7. Security Model

### 7.1 Smart contract security

| Vector | Mitigation |
|--------|-----------|
| Reentrancy | `nonReentrant` on all payable settlement functions (`buy`, `bid`, `settle`, `cancelEarly`, `acceptOffer`, `withdrawOffer`, `withdrawRefund`) |
| Fee rate manipulation | `PLATFORM_FEE_BPS` is a compile-time `constant` — zero admin surface |
| Fee recipient change | `feeRecipient` is `immutable` — set once at deploy, frozen forever |
| Listing overwrite | `AlreadyListed` check prevents third-party overwrites |
| NFT transfer failure | NFT transferred before payment in `buy`/`settle`/`acceptOffer`; if transfer reverts, whole tx reverts, no payment taken |
| Push refund griefing | Outbid refund push uses silent fallback: if push fails, stores in `pendingReturns` without blocking the incoming bid |
| Bid DOS via non-receiving contract | 2300 gas limit on push + fallback to `pendingReturns` |
| No pause attack surface | Contracts have no pause, no admin — cannot be frozen by any key compromise |
| Auction fee double-charge | Fee taken from bidder upfront; losing bidders get full refund including fee — never double-charged |

### 7.2 Immutability guarantees

These values are set once at deploy and can never be changed without deploying new contracts at new addresses:
- `feeRecipient` — who receives fees
- `PLATFORM_FEE_BPS` — how much the fee is (a constant, not even stored as state)

Users can verify these by reading the deployed bytecode/state.

### 7.3 No royalties

The platform does not conduct, route, or enforce royalties of any kind. No royalty registry, no ERC-2981 lookups, no royalty splits in any settlement function.

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
| `KEEPER_KEY` | yes | Keeper wallet private key (auto-settles/cancels auctions) |
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
| `CREATOR_ADDR` | deploy only | Fee recipient wallet address |

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

# Settle an auction (keeper does this automatically)
cast send $AUCTION_ADDR "settle(uint256)" $AUCTION_ID --private-key $KEEPER_KEY

# Cancel inactive auction (keeper does this automatically)
cast send $AUCTION_ADDR "cancelIfInactive(uint256)" $AUCTION_ID --private-key $KEEPER_KEY
```

### Verifying fee rate on deployed contract

```bash
cast call $MARKETPLACE_ADDR "PLATFORM_FEE_BPS()" --rpc-url $RPC_URL
# Must return 150 (0x96)
```
