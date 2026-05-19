# WebbPlace

Non-custodial NFT marketplace on Flare — buy, sell, bid, and make offers on ERC-721 tokens without giving up custody of your assets or your keys.

---

## What It Is

WebbPlace is a marketplace, not a creation platform. It lets anyone trade NFTs that already exist on the Flare network (or Coston2 testnet). The smart contracts are non-custodial: your NFT stays in your wallet until the moment a sale settles.

**What you can do:**
- Browse listings and collections
- Buy NFTs at a fixed price
- List your own NFTs for sale
- Create and bid in timed auctions
- Make off-chain EIP-712 signed offers (no gas until acceptance)
- Accept incoming offers on NFTs you own

**What this platform does not do:**
- Mint new NFTs
- Create or deploy collections
- Bridge assets cross-chain
- Accept fiat payment

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Frontend | Next.js 15, React 19, wagmi v2, Tailwind CSS |
| Backend | Go 1.23, Fiber, gRPC (port 9090), REST + SSE (port 8080) |
| Smart Contracts | Solidity 0.8.26, Forge test suite |
| Database | Supabase Postgres (pgx + sqlc), Goose migrations |
| Cache / Pub-Sub | Redis 7 |
| Blockchain | Flare Network / Coston2 testnet (chain ID 114) |

---

## Architecture

```
Browser (Next.js)
     |
  REST API + SSE (/api/v1/*, /events)
     |
Go Backend (port 8080 HTTP | 9090 gRPC)
  ├── MarketplaceService
  ├── AuctionService
  ├── OffersService
  └── IndexerService
       |
  Blockchain Indexer ──── Flare RPC
       |
 Supabase Postgres ← Goose Migrations
       |
     Redis (pub/sub + SIWE nonces)
```

The frontend can also read directly from the chain via RPC for latency-sensitive operations (wallet holdings, live auction state). The Go backend provides indexed history, search, trending scores, and offer aggregation.

---

## Smart Contracts (Coston2 Testnet)

| Contract | Address |
|----------|---------|
| Marketplace | `0x767F7fF7c66673488a30053C025C153E13b6BfAa` |
| AuctionHouse | `0x6016688AfFAF5427E1f8100160A6378Da2B1476a` |
| OfferBook | `0x0C7112Ec22262d1E423132e35bC87E33abF64a22` |
| RoyaltyRegistry | deployed on Coston2 |
| TreasuryVault | deployed on Coston2 |

All contracts are verified on [Coston2 Explorer](https://coston2-explorer.flare.network).

---

## Prerequisites

- A wallet: MetaMask, or any WalletConnect-compatible wallet
- Coston2 FLR for gas — get free testnet tokens at the [Flare Faucet](https://faucet.flare.network)
- **Frontend only:** Node 22+
- **Backend (optional):** Go 1.23+, Redis, Supabase project

---

## Quick Start (Local Dev)

### Frontend only (no backend required)

The frontend reads directly from the chain and needs no running backend.

```bash
git clone https://github.com/your-org/webbplace
cd webbplace/frontend
cp .env.example .env.local
# Fill in .env.local — chain vars and contract addresses are already populated for Coston2
npm install
npm run dev
```

Open http://localhost:3000.

### Full stack

```bash
# 1. Clone
git clone https://github.com/your-org/webbplace
cd webbplace

# 2. Backend env
cp backend/.env.example backend/.env
# Edit backend/.env — set POSTGRES_URL, REDIS_URL, JWT_SECRET, contract addresses

# 3. Frontend env
cp frontend/.env.example frontend/.env.local
# Edit frontend/.env.local — set contract addresses and WalletConnect project ID

# 4. Start Redis (and optionally Postgres via Docker)
docker compose up redis -d

# 5. Run the API server
cd backend && go run ./cmd/api

# 6. Run the indexer (separate terminal)
cd backend && go run ./cmd/indexer

# 7. Start the frontend
cd frontend && npm install && npm run dev
```

---

## Production Deploy

```bash
docker compose up --build
```

This builds and starts: `api` (port 8080/9090), `indexer`, `redis`, and `frontend` (port 3000). The `api` and `indexer` services read from the root `.env` file. The `frontend` service reads from `frontend/.env.local`.

---

## Environment Variables

### Backend (`.env` or `backend/.env`)

| Variable | Required | Description |
|----------|----------|-------------|
| `POSTGRES_URL` | Yes | Supabase pooler DSN — port 6543, `pool_mode=transaction` |
| `REDIS_URL` | Yes | Redis connection URL |
| `RPC_URL` | Yes | Flare/Coston2 JSON-RPC endpoint |
| `CHAIN_ID` | Yes | `114` for Coston2, `14` for Flare mainnet |
| `MARKETPLACE_ADDR` | Yes | Deployed Marketplace contract address |
| `AUCTION_ADDR` | Yes | Deployed AuctionHouse contract address |
| `OFFERBOOK_ADDR` | Yes | Deployed OfferBook contract address |
| `JWT_SECRET` | Yes | 32+ hex bytes — generate with `openssl rand -hex 32` |
| `FRONTEND_URL` | Yes | Allowed CORS origin (e.g. `http://localhost:3000`) |
| `ROYALTY_ADDR` | No | RoyaltyRegistry contract address |
| `KEEPER_KEY` | No | Hex-encoded private key for auction auto-settlement bot |
| `SERVICE_TOKEN` | No | Admin token for `IndexerService.Reindex` endpoint |
| `SENTRY_DSN` | No | Sentry error tracking DSN |
| `INDEX_FROM_BLOCK` | No | Starting block for indexer (default `0`) |
| `GETLOGS_CHUNK` | No | Blocks per `eth_getLogs` request (default `30`) |
| `GETLOGS_BLOCK_CAP` | No | Hard cap per range; `0` = unlimited (default `0`) |

### Frontend (`frontend/.env.local`)

| Variable | Required | Description |
|----------|----------|-------------|
| `NEXT_PUBLIC_CHAIN_ID` | Yes | `114` for Coston2 |
| `NEXT_PUBLIC_RPC_URL` | Yes | Flare/Coston2 JSON-RPC endpoint |
| `NEXT_PUBLIC_MARKETPLACE_ADDR` | Yes | Marketplace contract address |
| `NEXT_PUBLIC_AUCTION_ADDR` | Yes | AuctionHouse contract address |
| `NEXT_PUBLIC_OFFER_ADDR` | Yes | OfferBook contract address |
| `NEXT_PUBLIC_EXPLORER_URL` | Yes | Block explorer base URL |
| `NEXT_PUBLIC_CURRENCY_SYMBOL` | Yes | Native currency symbol (`FLR` or `C2FLR`) |
| `NEXT_PUBLIC_CHAIN_NAME` | Yes | Human-readable chain name |
| `NEXT_PUBLIC_WALLETCONNECT_PROJECT_ID` | No | Reown/WalletConnect project ID (enables mobile wallets) |
| `NEXT_PUBLIC_APP_URL` | No | App origin shown in WalletConnect modal |
| `NEXT_PUBLIC_API_URL` | No | Backend API base URL (enables indexed data) |
| `NEXT_PUBLIC_INDEX_FROM_BLOCK` | No | First block to scan for listings |
| `NEXT_PUBLIC_INDEX_CHUNK_BLOCKS` | No | Block range per `eth_getLogs` chunk (default `30`) |

> The frontend throws `"Refusing to launch"` at startup if any of the six required `NEXT_PUBLIC_*` vars are missing or empty. This is intentional — a misconfigured build is caught immediately.

---

## Switching to Flare Mainnet

Only `frontend/.env.local` (and `backend/.env`) need to change. No code changes are required.

1. Deploy contracts to Flare mainnet.
2. Update `NEXT_PUBLIC_CHAIN_ID=14`, RPC URL, explorer URL, currency symbol, chain name, and all three contract addresses in `frontend/.env.local`.
3. Update backend `.env` with mainnet RPC, chain ID, and contract addresses.
4. Rebuild and redeploy.

---

## License

MIT
