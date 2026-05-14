# MagicWebb — Technical Whitepaper

**Version 1.1 — 2026-05-13**  
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

Contracts:
- `Marketplace` for fixed-price listings
- `AuctionHouse` for English auctions
- `OfferBook` for signed offers
- `MarketplaceCore` shared access control, fee model, pause controls, transfer helpers

## 4. Data flow

### 4.1 Fixed-price purchase
1. Seller lists NFT on `Marketplace`.
2. Buyer submits `buy` with exact value.
3. Contract validates listing, performs transfer, splits payment, emits event.
4. Frontend refreshes chain-backed reads after transaction receipt.

### 4.2 Signed offer acceptance
1. Bidder deposits funds and signs EIP-712 offer off-chain.
2. Owner receives `{offer, signature}` and calls `acceptOffer`.
3. Contract validates signature/nonce/expiry/ownership, transfers NFT, settles payment.

### 4.3 Auction settlement
1. Seller creates auction with reserve and end time.
2. Bidders place bids; outbid funds are tracked in `pendingReturns`.
3. Anyone can settle after expiry; winner receives NFT; seller and fee recipient are paid.

## 5. Security model

| Vector | Mitigation |
|---|---|
| Reentrancy on payable flows | `ReentrancyGuard` + checks-effects-interactions |
| Auction griefing via refund callback | Pull-pattern refunds (`withdrawRefund`) |
| Signature replay | EIP-712 domain includes `chainId` and `verifyingContract`; nonce burn map |
| Fee abuse | Hard `MAX_FEE_BPS` cap enforced in constructor/setter |
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
