# MagicWebb — System Overview

MagicWebb is a **non-custodial** NFT marketplace on Flare (Coston2 testnet, chain `114`;
Flare mainnet, chain `14`, gated behind a readiness review). NFTs stay in the seller's
wallet until a transaction settles on-chain — no deposits, no wrapping, no escrow of the
token itself.

## Architecture

Everything ships as a **single Go binary**. The browser talks to the contracts directly
(the wallet signs every transaction); the backend only *observes* the chain and projects
state into Postgres for fast reads and live updates.

```
┌──────────────────────────────────────────────────────────┐
│                     Browser / Wallet                      │
│   HTMX 2  •  Alpine.js 3  •  ethers.js 6  •  WalletConnect │
└───────────────────────────┬──────────────────────────────┘
                            │ HTTP (HTMX partials) + SSE (live events)
┌───────────────────────────▼──────────────────────────────┐
│              Go Fiber binary  (cmd/server, :8080)         │
│  • REST API + HTMX page handlers   (internal/api, ui)     │
│  • SIWE auth → JWT                  (internal/auth)        │
│  • Real-time hub (in-memory SSE)    (internal/sse)        │
│  • Chain indexer + auction keeper   (internal/indexer)    │
└─────────┬─────────────────────────────────┬──────────────┘
          │ pgx                             │ JSON-RPC (eth_getLogs / sendRawTx)
┌─────────▼──────────────┐      ┌───────────▼──────────────────────────────┐
│  PostgreSQL (Supabase) │      │  Flare Coston2 (114) / Flare (14)         │
│  projected read model  │      │  Marketplace • AuctionHouse • OfferBook   │
└────────────────────────┘      └───────────────────────────────────────────┘
```

There is **no Redis, no separate frontend service, and no Docker Compose** — the SSE hub is
in-process and the UI is server-rendered from the same binary via `embed.FS`.

## Smart contracts

| Contract | Purpose |
|----------|---------|
| `Marketplace` | Fixed-price ERC-721/1155 listings. Free to list. `list`, `list1155`, `batchList` (up to 50 ERC-721), `cancel`, `buy`. Non-custodial: the NFT stays with the seller until `buy` settles. |
| `AuctionHouse` | English auctions. Single-step bidding; the previous high bidder is refunded automatically (with a pull-pattern fallback). Anti-snipe time extension. Settled by an off-chain keeper after the end time. |
| `OfferBook` | On-chain escrowed ETH offers. A bidder locks ETH (`makeOffer`); the owner accepts (`acceptOffer`) or rejects (`rejectOffer`); expired offers are refundable by anyone (`refundExpiredOffer`). No off-chain signatures. |

**Fee model: taker-pays 1.5%.** `PLATFORM_FEE_BPS = 150` is a hardcoded constant. Listings are
free; the buyer / bidder / offerer pays 1.5% on top, so the seller always nets 100% of their ask.
The fee goes directly to the immutable `feeRecipient` set at deploy time. **No royalties.**

## Key design decisions

| Decision | Rationale |
|----------|-----------|
| Unstoppable contracts | No pause, no admin, no owner withdrawal, no upgrade proxy. A key compromise cannot freeze or drain the market. |
| Non-custodial | The token never leaves the seller's wallet pre-settlement — no escrow risk. |
| Taker-pays fee | Sellers receive their full ask; the 1.5% is added on top for the buyer/bidder/offerer. |
| Immutable fee + recipient | `feeRecipient` and the 150 bps rate are fixed at deploy; changing them means deploying new contracts. |
| On-chain offers | `OfferBook` escrows real ETH on-chain (no EIP-712 signatures / nonces) — an accepted offer always has funds behind it. |
| Keeper-settled auctions | An off-chain keeper calls `settle()` after the end time so winners don't have to. (Operational dependency — see `READINESS.md`.) |
| Batch listing | Up to 50 ERC-721 tokens listed in a single `batchList()` transaction. |

See `SYSTEM.md` and `CONTRACTS_ANNOTATED.md` for contract-level detail, `WALKTHROUGH.md` for
end-to-end user/developer flows, and `TECH_STACK.md` for the component inventory.
