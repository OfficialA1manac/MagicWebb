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
│  REST handlers  •  Redis pub/sub SSE  •  SIWE auth      │
└────┬────────────────────────────────────────────────────┘
     │ PostgreSQL (Supabase)        │ Redis
┌────▼────────────────────────────────────────────────────┐
│  Go Indexer (background worker)                         │
│  • Chain watcher (polls every 2 s)                      │
│  • Auction keeper (polls every 30 s — auto-settles)     │
│  • Inactivity sweeper (auto-cancels 0-bid auctions)     │
│  • Trending score worker (every 60 s)                   │
└────────────────────┬────────────────────────────────────┘
                     │ eth_getLogs / eth_sendRawTransaction
┌────────────────────▼────────────────────────────────────┐
│            Flare Coston2 (chain 114) / Flare (chain 14)  │
│   Marketplace  •  AuctionHouse  •  OfferBook             │
└─────────────────────────────────────────────────────────┘
```

## Smart contracts

| Contract | Purpose |
|----------|---------|
| `Marketplace` | Fixed-price ERC-721/1155 listings. `list`, `batchList` (up to 50 tokens), `cancel`, `buy`. |
| `AuctionHouse` | English auctions. Single-step bidding. Auto-settled by keeper. Push-refunds for outbid bidders. |
| `OfferBook` | On-chain ETH offers. Owners mark tokens eligible; bidders deposit ETH; owners accept. |

Platform fee: **1.5%** (`PLATFORM_FEE_BPS = 150`, hardcoded constant). Applied to all settlement operations. Fee sent directly to the immutable `feeRecipient` wallet set at deploy time. No vault, no intermediary.

No royalties. The platform does not route or enforce any royalty payments.

## Key design decisions

| Decision | Rationale |
|----------|-----------|
| Unstoppable contracts | No pause, no admin. Once deployed, contracts run forever. Cannot be frozen by any key compromise. |
| Non-custodial | Tokens stay with seller; no escrow risk. |
| Fixed auction time | `endsAt` set at creation, immutable after. |
| Single-step bidding | No commit-reveal. Bidder sends bid + 1.5% fee in one tx. Simple, automatic. |
| Push-refunds | Outbid bidder receives full payment back (including fee) automatically in the same tx as the new bid. No manual reclaim. |
| Bidder-pays auction fee | 1.5% on top of bid at bid time. Losing bidders get it back. Platform keeps it only from the winner. |
| Auto-settle | Keeper bot calls `settle()` on expired auctions. No user action needed. |
| Auto-cancel | 30-minute window: zero-bid auctions are cancelled automatically if no one bids. |
| On-chain offer eligibility | Owners explicitly mark NFTs eligible for offers. No off-chain signatures. |
| Batch listing | Up to 50 ERC-721 tokens from any collections listed in one `batchList()` transaction. |
| `restart: always` | All Docker services auto-restart on crash or reboot. |
