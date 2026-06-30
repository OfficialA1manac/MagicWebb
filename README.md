# MagicWebb

A fast, **unstoppable** NFT marketplace on the [Flare](https://flare.network) network.

MagicWebb is a fixed-price + auction + offer marketplace with a **seller-pays 1.5%** fee model and **zero-admin** smart contracts (no pause, no owner withdrawal, no upgrade proxy, immutable fee). Listings, auction creation, bidding, and making offers are free. On any successful sale, 1.5% is deducted from the seller's proceeds — the seller receives 98.5% of the sale price.

> Network: **Coston2 testnet** (chain `114`). This marketplace operates exclusively on Flare Coston2 testnet.

---

## Architecture

A **single Go binary** serves everything — REST API, server-rendered HTMX UI, real-time event stream, and the on-chain indexer — backed by [Neon Postgres](https://neon.tech) (serverless, free tier).

```
Browser (HTMX + Alpine + ethers.js)
        │  HTTP / WebSocket / SSE
        ▼
┌─────────────────────────────────────────────┐
│  Go Fiber binary (cmd/server)                │
│  • REST API              (internal/api)      │
│  • HTMX page handlers    (cmd/server/ui)     │
│  • Real-time SSE hub     (internal/sse)      │
│  • SIWE auth + JWT       (internal/auth)     │
│  • Chain indexer         (internal/indexer)  │── JSON-RPC ──▶ Flare/Coston2
│  • Offer/auction keeper                      │
│  • Image self-host store (internal/imagestore)│
└────────────────────┬────────────────────────┘
                     │ pgxpool (tuned for Neon free tier:
                     │   MaxConns=10, idle timeout <5 min)
                     ▼
              Neon Postgres (serverless)
              ┌──────────────────────┐
              │  NFT listings/tokens  │
              │  Auctions, bids, sales│
              │  Offers, profiles     │
              │  Image BYTEA blobs    │ ← Self-hosted, no IPFS/Pinata
              │  Real-time events     │
              │  Full-text search     │
              └──────────────────────┘
```

The browser talks to the **contracts directly** (wallet signs txs via ethers.js); the backend **observes** the chain through its indexer and projects state into Postgres for fast reads and live updates.

NFT images are **self-hosted** in Postgres BYTEA columns — no IPFS, no Pinata, no third-party gateways at render time. The indexer fetches metadata from upstream URIs on first discovery, hashes the bytes (SHA-256), stores them in `nft_image_blobs`, and rewrites `image_uri` to `/api/v1/img/<sha256>`. Every page load serves images from the local store.

## Tech stack

| Layer | Tech | Free tier |
|------|------|-----------|
| Contracts | Solidity 0.8.26, Foundry, OpenZeppelin v5 | Open source |
| Backend | Go 1.25, [Fiber](https://gofiber.io) v2, pgx v5, goose migrations, go-ethereum, zerolog | Open source |
| Frontend | HTMX 2, Alpine.js 3, ethers.js 6, Tailwind, WalletConnect v2 (self-hosted via `embed.FS`) | Open source |
| Database | [Neon Postgres](https://neon.tech) (serverless); `LISTEN/NOTIFY` as the real-time bus | Free tier: 0.5 GB storage, 100 CU-hours/mo, 10k pooled connections |
| Auth | Sign-In-with-Ethereum (EIP-191) → JWT | None needed |
| Chain RPC | Flare Coston2 public endpoints | Free public RPC |
| Image storage | Self-hosted in Postgres BYTEA (`nft_image_blobs`) | Included in DB storage |
| Hosting | [Fly.io](https://fly.io) (single Go binary) | ~$3-4/mo or trial credits |

## Repository layout

```
contracts/        Foundry project — Marketplace, AuctionHouse, OfferBook + tests + deploy scripts
backend/
  cmd/server/     Entry point: HTTP server + indexer wiring + HTMX page handlers
  internal/
    api/          REST handlers + router
    auth/         SIWE verification + JWT
    config/       Env-var configuration
    db/           pgx pool, queries, SQL migrations
    imagestore/   Content-addressed SHA-256 BYTEA blob store
    indexer/      Chain event watcher, handlers, keeper
    media/        IPFS/ar:// URI resolution + SSRF-safe fetcher
    sse/          Real-time event hub
    ui/           Embedded templates + static assets (HTMX/Alpine/ethers/wallet.js)
docs/             Project documentation (user guide, whitepaper, FAQ)
```

## Prerequisites

- [Go](https://go.dev/dl/) **1.25+**
- [Foundry](https://book.getfoundry.sh/getting-started/installation) (`forge`)
- A [Neon Postgres](https://neon.tech) project (free tier works)
- Optionally [slither](https://github.com/crytic/slither) for contract static analysis

## Neon Database Setup (Free Tier)

1. **Create a Neon account** at [neon.tech](https://neon.tech) (no credit card required).
2. **Create a project** — choose any region (us-east-2 recommended for Fly.io `iad`).
3. **Get your connection string** from the Neon dashboard → Connection Details → `psql` / `Postgres` tab.
   ```
   postgresql://user:password@ep-<project>-<pooler>.us-east-2.aws.neon.tech/magicwebb?sslmode=require
   ```
4. **Create the database** (the default `neondb` is fine, or create a named one):
   ```bash
   # Using the pooled connection string
   psql "<your-neon-connection-string>" -c "CREATE DATABASE magicwebb;"
   ```

**Free tier quotas:**
| Resource | Limit |
|----------|-------|
| Storage | 0.5 GB per project |
| Compute | 100 CU-hours/month (auto-pauses when idle) |
| Connections | Up to 10,000 pooled (built-in PgBouncer) |
| Projects | Up to 100 per account |
| Branching | Unlimited (copy-on-write, ideal for dev/CI) |

The app uses a pgxpool tuned for Neon (see `backend/internal/db/pool.go`):
- `MaxConns=10` — stays well within free tier
- `MaxConnIdleTime=4m` — closes connections before Neon's ~5 min idle timeout
- `MaxConnLifetime=30m` — rotates stale sockets
- `HealthCheckPeriod=30s` — detects dead connections quickly

> **Note:** Neon supports `LISTEN/NOTIFY` which the app uses for cross-instance real-time event fan-out. Use the **pooled** connection string (default port 5432) — the un-pooled direct port (6543) is only needed for long-running transactions or pg_dump.

## Configuration

The backend reads configuration from environment variables (see `.env.example`). Provide them via your shell, a `.env` file, or your host's secret manager.

**Required:** `RPC_URL`, `CHAIN_ID`, `MARKETPLACE_ADDR`, `AUCTION_ADDR`, `OFFERBOOK_ADDR`, `POSTGRES_URL`, `JWT_SECRET` (≥32 chars).

**All assets self-hosted:** No IPFS gateway JWT, no Pinata key, no third-party object storage needed. NFT images are stored directly in Postgres BYTEA columns and served from `/api/v1/img/<sha256>`.

## Run locally

```powershell
# Windows (PowerShell) — loads .env and starts the server with hot reload
./dev.ps1
```

```bash
# Or directly
cd backend
go run ./cmd/server      # serves on $HTTP_ADDR (default :8080)
```

Then open http://localhost:8080 — the server auto-runs DB migrations on first boot. `/healthz` reports liveness.

### Using Neon branching for local development

Neon's branching feature is ideal for testing schema migrations or indexer re-indexes without impacting production:

```bash
# Create a branch (via Neon dashboard or API)
# Get the branch's connection string
POSTGRES_URL=<branch-connection-string> go run ./cmd/server
```

Each branch is a copy-on-write clone — zero additional storage cost until data diverges.

## Contracts

```bash
cd contracts
forge build
forge test                       # full unit/scenario suite
forge script script/DeployCoston2.s.sol --rpc-url coston2 --broadcast   # deploy (needs funded key)
slither .                        # static analysis
```

Deployed Coston2 addresses are configured in `.env.example`.

## Deployment

The app compiles to a single self-contained Go binary (`make build` → `bin/magicwebb`) that serves the REST API, HTMX UI, SSE stream, and on-chain indexer. Run it anywhere a Go binary runs: set the env vars from `.env.example` (`POSTGRES_URL`, `RPC_URL`, `CHAIN_ID`, contract addresses, `JWT_SECRET`, optional `KEEPER_KEY`) and launch the binary. Health check is at `/healthz`.

See [`docs/DEPLOY_FLY.md`](docs/DEPLOY_FLY.md) for Fly.io deployment instructions.

### What makes it free to run

| Component | Cost |
|-----------|------|
| Database | [Neon free tier](https://neon.tech/pricing) — 0.5 GB, 100 CU-hours/mo, 10k connections |
| Hosting | [Fly.io](https://fly.io/pricing) — shared-cpu-1x/512MB ~$3-4/mo (trial credits available) |
| Smart contracts | Deployed immutably on Flare Coston2 — no admin gas overhead |
| Image storage | Self-hosted in Postgres BYTEA — no IPFS/Pinata/CDN costs |
| WalletConnect | Free project ID from [Reown Cloud](https://cloud.reown.com) |
| RPC | Free public Flare Coston2 endpoints |

**The entire application has zero paid dependencies** beyond the Fly.io hosting fee (~$3-4/mo).

## Documentation

See [`docs/`](docs/):
- **`USER_GUIDE.md`** — how to use the marketplace (list, bid, offer, buy)
- **`WHITEPAPER.md`** — economics & design philosophy
- **`WHITEPAPER_TECHNICAL.md`** — technical architecture deep-dive
- **`FAQ.md`** — frequently asked questions

## License

See repository.
