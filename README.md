# MagicWebb

A fast, **unstoppable** NFT marketplace on the [Flare](https://flare.network) network.

MagicWebb is a fixed-price + auction + offer marketplace with a **taker-pays 1.5%** fee model and **zero-admin** smart contracts (no pause, no owner withdrawal, no upgrade proxy, immutable fee). Listings are free; the buyer/bidder/offerer pays the 1.5% on top, and the seller always receives 100% of their ask.

> Network: **Coston2 testnet** (chain `114`). Flare mainnet (chain `14`) deploy is gated behind a readiness review.

---

## Architecture

A **single Go binary** serves everything — REST API, server-rendered HTMX UI, real-time event stream, and the on-chain indexer — backed by Postgres.

```
Browser (HTMX + Alpine + ethers.js)
        │  HTTP / WebSocket / SSE
        ▼
┌─────────────────────────────────────────┐
│  Go Fiber binary (cmd/server)            │
│  • REST API            (internal/api)    │
│  • HTMX page handlers  (cmd/server/ui)   │
│  • Real-time hub       (internal/sse)    │
│  • SIWE auth + JWT     (internal/auth)   │
│  • Chain indexer       (internal/indexer)│  ── JSON-RPC ──▶ Flare/Coston2 node
│  • Offer/auction keeper                  │
└───────────────┬─────────────────────────┘
                │ pgx
                ▼
        Postgres (Supabase)
```

The browser talks to the **contracts directly** (wallet signs txs via ethers.js); the backend **observes** the chain through its indexer and projects state into Postgres for fast reads and live updates.

## Tech stack

| Layer | Tech |
|------|------|
| Contracts | Solidity 0.8.26, Foundry, OpenZeppelin v5 |
| Backend | Go 1.25, [Fiber](https://gofiber.io) v2, pgx v5, goose migrations, go-ethereum, zerolog |
| Frontend | HTMX 2, Alpine.js 3, ethers.js 6, Tailwind, WalletConnect v2 (served from the Go binary via `embed.FS`) |
| Data | PostgreSQL (Supabase); `LISTEN/NOTIFY` as the real-time bus |
| Auth | Sign-In-with-Ethereum (EIP-191) → JWT |

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
    indexer/      Chain event watcher, handlers, keeper
    sse/          Real-time event hub
    ui/           Embedded templates + static assets (HTMX/Alpine/ethers/wallet.js)
docs/             Project documentation (user guide, whitepaper, FAQ)
```

## Prerequisites

- [Go](https://go.dev/dl/) **1.25+**
- [Foundry](https://book.getfoundry.sh/getting-started/installation) (`forge`)
- A PostgreSQL database (e.g. a free [Supabase](https://supabase.com) project)
- Optionally [slither](https://github.com/crytic/slither) for contract static analysis

## Configuration

The backend reads configuration from environment variables (see `.env.example`). Provide them via your shell, a `.env` file loaded by `dev.ps1`, or your host's secret manager. Required: `RPC_URL`, `CHAIN_ID`, `MARKETPLACE_ADDR`, `AUCTION_ADDR`, `OFFERBOOK_ADDR`, `POSTGRES_URL`, `JWT_SECRET` (≥32 chars).

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

Then open http://localhost:8080 and `/healthz` for liveness.

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

## Documentation

See [`docs/`](docs/):
- **`USER_GUIDE.md`** — how to use the marketplace (list, bid, offer, buy)
- **`WHITEPAPER.md`** — economics & design philosophy
- **`WHITEPAPER_TECHNICAL.md`** — technical architecture deep-dive
- **`FAQ.md`** — frequently asked questions

## License

See repository.
