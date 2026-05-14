# MagicWebb — Technical Whitepaper

**Version 1.3 — 2026-05-14**  
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

### 3.2 Platform fee (immutable per deployment)

On each child contract (`Marketplace`, `AuctionHouse`, `OfferBook`), **`feeVault` and `feeBps` are constructor arguments only**—there is no setter, upgrade path, or owner key that can redirect or rewrite them after deploy.

**Canonical production parameters (documented target):**

- **Platform fee amount:** `150` bps (1.5% of the trade `msg.value` / accepted bid / offer amount).  
- **Platform fee address (`feeVault`):** `0x78993B71051de91C2D2595BC3475F07748927dc0`

Deploy scripts read `CREATOR_ADDR` as the fee vault and `FEE_BPS` for basis points; operators must set these to the intended immutable values **before** broadcasting, because they cannot be corrected post-deploy without new contracts.

### 3.3 Fees, refunds, and failed transfers (all surfaces)

**Where the immutable bps applies.** The same `feeBps` / `feeVault` applies to every **successful, final settlement** that moves an NFT and pays the seller from trade proceeds: `Marketplace.buy`, `AuctionHouse.settle` (winning bid only), and `OfferBook.acceptOffer` / `acceptOffer1155`. There is **no** separate listing fee, auction-creation fee, or offer-signature fee—**listing, relisting, cancelling an unsold listing, cancelling a zero-bid auction, and offer cancellation (nonce burn) do not call `_splitAndPay`**, so the platform fee is never assessed on those actions.

**Auction bids (no fee on bids).** Losing bidders are credited **100%** of their superseded high bid in `pendingReturns` (no skim). They reclaim funds via `withdrawRefund`. The **current high bidder** may **raise their own bid** by sending only the **increment** as `msg.value`; it is **compounded** onto their existing high bid without routing the prior amount through `pendingReturns`. A **new** bidder still sends the **full** new winning amount as `msg.value`. The contract holds one active high bid plus aggregate pull-refund liabilities—no per-bid siloed “deposit accounts.”

**When the fee runs (after NFT transfer).** On every settlement path, the implementation performs the standard-aware **NFT transfer first**, then `_splitAndPay` to `feeVault` and seller. If the transfer reverts, the whole transaction reverts: **no fee is taken** and no sale state is finalized.

**Expired or cancelled listings / unsold auctions.** If nothing sells, **no trade proceeds exist** and **no platform fee is due**—there is nothing held as a “fee escrow” to release back to sellers. `buy` on an expired listing simply reverts (`Expired`). A seller-cancelled listing is deleted with no payment flow.

## 4. Data flow

### 4.1 Fixed-price purchase

1. Seller lists an NFT on `Marketplace` (on-chain listing acts as the seller’s **offer to sell** for the listed price until expiry).
2. Buyer calls `buy` with **exact** `msg.value` equal to the list price (buyer **accepts** that listing; the buyer becomes the NFT recipient and the listed amount is taken from the buyer’s wallet in the same transaction).
3. Contract validates the listing, transfers the NFT, then splits payment (fee → `feeVault`, remainder → seller) in the same atomic transaction, emits events.
4. Frontend refreshes chain-backed reads after transaction receipt.

### 4.2 Signed offer acceptance

1. Bidder **deposits** native token into `OfferBook` and signs an EIP-712 `Offer` / `Offer1155` off-chain.
2. Owner receives `{offer, signature}` and calls `acceptOffer` / `acceptOffer1155`.
3. Contract validates signature, nonce, expiry, ownership/approval, debits deposit, transfers the NFT, then settles payment (fee + seller) in the same atomic transaction.

### 4.3 Auction settlement

1. Seller creates an auction with reserve and end time (and optional minimum bid increment / anti-snipe behavior per contract rules).
2. Bidders place bids. Outbid losers receive **full** prior-bid balances in `pendingReturns` (no fee on bids). The leader may **compound** a higher bid by sending only the **increment**; other bidders send the **full** new high amount. Losers withdraw via `withdrawRefund`.
3. After `endsAt`, **anyone** may call `settle` once (permissionless finalizer). That transaction transfers the NFT to the highest bidder, **then** applies the immutable platform fee to the winning `highestBid` and pays the seller—**fee is charged only on this winning settlement**, not on intermediate bids. **Product UX** should treat settlement as automatic (e.g. prompt immediately after expiry or use a relayer); **on-chain**, it remains one atomic transaction, not a cron inside the contract.

## 5. Security model

| Vector | Mitigation |
|---|---|
| Reentrancy on payable flows | `ReentrancyGuard` + checks-effects-interactions |
| Auction griefing via refund callback | Pull-pattern refunds (`withdrawRefund`) |
| Signature replay | EIP-712 domain includes `chainId` and `verifyingContract`; nonce burn map |
| Fee abuse | `MAX_FEE_BPS` (10%) enforced at **constructor**; `feeBps` and `feeVault` are **immutable** (no admin fee changes) |
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
