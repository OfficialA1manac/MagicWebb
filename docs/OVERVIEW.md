# MagicWebb — System Overview

MagicWebb is a non-custodial NFT marketplace on Flare (Coston2 testnet / Flare mainnet).
NFTs stay in the seller's wallet until a transaction settles on-chain. No deposits, no wrapping.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     Browser / Wallet                    │
│   Next.js 14 (App Router)  wagmi v2  viem               │
└────────────────────┬────────────────────────────────────┘
                     │ REST / SSE
┌────────────────────▼────────────────────────────────────┐
│                 Go API Server (:8080)                    │
│  REST handlers  •  Redis pub/sub SSE  •  gRPC services  │
└────┬────────────────────────────────────────────────────┘
     │ PostgreSQL (Supabase)        │ Redis
┌────▼────────────────────────────────────────────────────┐
│  Go Indexer (background worker)                         │
│  • Chain watcher (polls every 2 s)                      │
│  • Auction keeper (polls every 30 s — auto-settles)     │
│  • Offer expiry sweeper (every 5 min)                   │
│  • Trending score worker (every 60 s)                   │
└────────────────────┬────────────────────────────────────┘
                     │ eth_getLogs / eth_sendRawTransaction
┌────────────────────▼────────────────────────────────────┐
│            Flare Coston2 (chain 114)                     │
│   Marketplace  •  AuctionHouse  •  OfferBook             │
└─────────────────────────────────────────────────────────┘
```

## Smart contracts

| Contract | Purpose |
|----------|---------|
| `Marketplace` | Fixed-price ERC-721/1155 listings. `list`, `batchList` (up to 50 tokens), `cancel`, `buy`. |
| `AuctionHouse` | English auctions. Fixed end time (never extended). Commit-reveal MEV protection. Auto-settled by keeper. |
| `OfferBook` | EIP-712 signed offers. Deposited ETH held until accepted or expired. |

Platform fee: **1.5%** (`FEE_BPS=150`). No royalties. Fee goes to immutable `feeVault` set at deploy time.

## Key design decisions

| Decision | Rationale |
|----------|-----------|
| Non-custodial | Tokens stay with seller; no escrow risk. Approval revocation mid-auction is safe — winner can reclaim after 7 days. |
| Fixed auction time | `endsAt` is immutable after creation. |
| Commit-reveal bidding | 2-block delay between commit and reveal prevents MEV/front-running on bids. |
| Pull refunds | Outbid ETH accumulates in `pendingReturns`; bidder calls `withdrawRefund()` — contract never pushes ETH. |
| Keeper bot | Background goroutine in the indexer polls every 30 s and calls `settle()` on expired auctions. NFT goes to winner, ETH goes to seller, automatically. |
| Batch listing | Up to 50 ERC-721 tokens from any collections listed in one `batchList()` transaction. |
| `restart: always` | All Docker services auto-restart on crash or reboot. `make up` once = runs forever. |
