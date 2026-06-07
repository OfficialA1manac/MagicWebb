# MagicWebb — Technical Whitepaper

**Version 1.4 — 2026-05-14**  
**Network:** Flare Coston2 (testnet), mainnet-ready architecture  
**License:** MIT

## 1. Abstract

MagicWebb is a non-custodial NFT marketplace built as a single application with a direct contract model: **Next.js frontend + on-chain contracts**. It supports fixed-price listings, English auctions, and EIP-712 signed offers across ERC-721 and ERC-1155 assets.

## 2. Design goals

| Goal | Rationale |
|---|---|
| Non-custodial | NFTs remain in seller custody until atomic settlement. |
| Minimal architecture | Remove service sprawl and runtime drift by keeping only frontend + contracts. |
| Hybrid 721/1155 | One marketplace surface for both token standards. |
| Off-chain signatures, on-chain settlement | Offers are signed off-chain and accepted atomically on-chain. |
| Predictable operations | Single env file (`frontend/.env.local`) and one Makefile lifecycle (`start`, `stop`, `restart`, `status`, `health`). |
| Wallet-only surface area | The app submits transactions when chain state requires it; users **connect and confirm** (or reject) in the wallet — no separate manual “claim” or “settle” hunting except on failure retry. |

## 3. System architecture

```
Wallet <-> Frontend (Next.js + wagmi/viem) <-> Flare RPC <-> Contracts
```

### 3.1 Contract roles

| Contract | Responsibility |
|---|---|
| `Marketplace` | **All** fixed-price, native-token listings. Listings are time-bounded (expiry); price and terms are set on-chain by the seller. |
| `AuctionHouse` | **All** English auctions (ERC-721 and ERC-1155). Auction copy, timers, and bid semantics should be **internationalized in the app** so every locale can follow reserve, increments, anti-snipe, and settlement without relying on English-only chain data. |
| `OfferBook` | **All** offer lifecycle: bidder **deposits**, EIP-712 **signed** offers off-chain, **acceptance** or **cancellation** on-chain. Nonces, deposits, and events give a clear **open vs. closed** picture; the contract is the source of truth for what can still execute. |
| `MarketplaceCore` | Shared **immutable** fee routing, `_splitAndPay`, standard-aware NFT transfer helpers, and `ReentrancyGuard`. No admin roles, no fee mutability, no pause switch—so listings, auctions, and offers **cannot** silently change fee economics or clash on shared settlement primitives. |

### 3.2 Platform fee (single, unified, immutable)

MagicWebb charges a single **1.5% platform fee** (`PLATFORM_FEE_BPS = 150`), applied **only on a successful sale** and **deducted from the seller's proceeds**. Listing, auction creation, bidding, and making offers are all free. One constant governs every settlement path.

The fee is a `constant` in `MarketplaceCore.sol`, not a constructor argument or mutable storage variable. It cannot be changed by any admin key, environment variable, or upgrade path. Changing it requires deploying new contracts.

**Fee recipient:** `feeRecipient` — an immutable wallet address set once at deploy time. Fees are sent directly via `.call{value: fee}("")` to this address. No intermediary contract, no vault, no accumulator.

Deploy scripts require only `CREATOR_ADDR` (the fee recipient + admin wallet). No `FEE_BPS` variable exists — the rate is fixed in code.

### 3.3 Fees applied, refunds, and failed transfers (all surfaces)

**Where the 1.5% fee applies.** The platform fee is charged only when a sale settles, deducted from the seller's proceeds:
- **Buy:** 1.5% of sale price, deducted from seller proceeds (`_payFee` + `_pay`) in the same atomic transaction as the NFT transfer. The buyer sends exactly the price.
- **Auction settlement:** 1.5% of the winning bid, deducted from seller proceeds when `settle()` is called.
- **Offer acceptance:** 1.5% of the offer amount, deducted from seller proceeds when `acceptOffer()` / `acceptOffer1155()` is called.

**What is NOT charged:** Listing, auction creation, bid placement, outbid refunds, making/topping-up offers, offer rejection/expiry refunds, and listing cancellation — none of these deduct any fee. Bids and offer principals are fully refundable.

**Auction bids (no fee applied on bids).** Losing bidders are credited **100%** of their superseded high bid in `pendingReturns` (no skim). They reclaim funds via `withdrawRefund`. The **current high bidder** may **raise their own bid** by sending only the **increment** as `msg.value`; it is **compounded** onto their existing high bid without routing the prior amount through `pendingReturns`. A **new** bidder still sends the **full** new winning amount as `msg.value`. The contract holds one active high bid plus aggregate pull-refund liabilities—no per-bid siloed “deposit accounts.”

**When the fee is applied (after NFT transfer).** On every settlement path, the implementation performs the standard-aware **NFT transfer first**, then `_splitAndPay` so the platform fee is **applied** to the seller’s proceeds. If the transfer reverts, the whole transaction reverts: **the fee is not applied** and no sale state is finalized.

**Expired or cancelled listings / unsold auctions.** If nothing sells, **no trade proceeds exist** and **the platform fee is not applied**—there is no separate “fee escrow” to unwind. `buy` on an expired listing simply reverts (`Expired`). A seller-cancelled listing is deleted with no payment flow.

### 3.4 Wallet confirmations only (product automation)

On-chain rules still require transactions for settlement and pull refunds, but the **MagicWebb app** is responsible for **submitting** those transactions when reads show they are needed, so users normally only **connect the wallet** and **approve or reject** the prompts—no hunting for extra buttons like “settle” or “claim refund” unless a submission fails and a one-tap **retry** is shown.

Concrete behavior (reference implementation):

- **Auction end:** When an auction has bids and has passed `endsAt`, opening the auction page with a connected wallet triggers `settle` automatically (wallet confirmation).
- **Outbid refunds:** Opening your profile when `pendingReturns` is positive triggers `withdrawRefund` automatically (wallet confirmation). After a successful refund, state resets so a future outbid can prompt again.
- **OfferBook deposit:** “Withdraw all” withdraws the full on-chain deposit with one confirmation; partial amounts remain an optional path.

Optional operations hardening (not required by contracts): a small **relayer** balance can call `settle` on users’ behalf so winners receive the NFT with **zero** signatures from them; that is a deployment choice and does not change fee **application** rules.

## 4. Data flow

### 4.1 Fixed-price purchase

1. Seller lists an NFT on `Marketplace` (on-chain listing acts as the seller’s **offer to sell** for the listed price until expiry).
2. Buyer calls `buy` with **exact** `msg.value` equal to the list price (buyer **accepts** that listing; the buyer becomes the NFT recipient and the listed amount is taken from the buyer’s wallet in the same transaction).
3. Contract validates the listing, transfers the NFT, then splits payment (platform fee collected, remainder → seller) in the same atomic transaction, emits events.
4. Frontend refreshes chain-backed reads after transaction receipt.

### 4.2 Signed offer acceptance

1. Bidder **deposits** native token into `OfferBook` and signs an EIP-712 `Offer` / `Offer1155` off-chain.
2. Owner receives `{offer, signature}` and calls `acceptOffer` / `acceptOffer1155`.
3. Contract validates signature, nonce, expiry, ownership/approval, debits deposit, transfers the NFT, then settles payment (fee + seller) in the same atomic transaction.

### 4.3 Auction settlement

1. Seller creates an auction with reserve and end time (and optional minimum bid increment / anti-snipe behavior per contract rules).
2. Bidders place bids. Outbid losers receive **full** prior-bid balances in `pendingReturns` (no fee **applied** on bids). The leader may **compound** a higher bid by sending only the **increment**; other bidders send the **full** new high amount. The app prompts `withdrawRefund` when appropriate (see §3.4).
3. After `endsAt`, **anyone** may call `settle` once (permissionless finalizer). That transaction transfers the NFT to the highest bidder, **then** **applies** the immutable platform fee to the winning `highestBid` and pays the seller—**fee is applied only on this winning settlement**, not on intermediate bids. The app prompts `settle` when you view the auction (see §3.4); **on-chain**, it remains one atomic transaction, not a cron inside the contract.

## 5. Security model

| Vector | Mitigation |
|---|---|
| Reentrancy on payable flows | `ReentrancyGuard` + checks-effects-interactions |
| Auction griefing via refund callback | Pull-pattern refunds (`withdrawRefund`) |
| Signature replay | EIP-712 domain includes `chainId` and `verifyingContract`; nonce burn map |
| Fee abuse | `PLATFORM_FEE_BPS` is a hardcoded `constant` (1.5%); no admin key, env var, or upgrade path can change it |
| Listing overwrite by third party | Seller collision checks (`AlreadyListed`) |

Residual accepted risks:

- Timestamp-based expiries inherit normal block timestamp variance.
- `tokenId == 0` collection-wide sentinel behavior is documented and UI-constrained.

## 6. Operations

Canonical runtime controls:

- `make start`
- `make stop`
- `make restart`
- `make status`
- `make health`

Production uses the same targets: build with `make build`, run with `make start` (see `README.md`).

Configuration source of truth:

- committed template: `frontend/.env.example`
- local / production runtime: `frontend/.env.local` (copy the template, then edit — the only env file)

## 7. Roadmap

| Phase | Scope |
|---|---|
| Phase 1 | Coston2 with fixed-price, auctions, and signed offers. |
| Phase 2 | Mainnet rollout and multisig operational hardening. |
| Phase 3 | UX/performance iteration and broader collection coverage. |

## 8. Appendix — core event families

- `Marketplace`: `Listed`, `Cancelled`, `Bought`
- `AuctionHouse`: `AuctionCreated`, `BidPlaced`, `AuctionSettled`, `AuctionCancelled`, `RefundWithdrawn`, `AuctionExtended`
- `OfferBook`: `OfferAccepted`, `Offer1155Accepted`, `OfferCancelled`, `Deposited`, `Withdrawn`
