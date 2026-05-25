# MagicWebb Platform Guide

## Prerequisites
| Tool | Purpose |
|------|---------|
| Docker + Docker Compose 24+ | Run all services |
| Foundry (`forge`, `cast`) | Contract build/deploy |
| Node.js 20+ | Frontend build |
| `jq` | ABI regen + address sync |
| Go 1.22+ | Backend build (optional — Docker handles it) |

## First-time setup
```bash
git clone https://github.com/OfficialA1manac/MagicWebb && cd MagicWebb
make install
cp backend/.env.example .env          # backend/indexer env
cp frontend/.env.example frontend/.env.local  # frontend env
# Fill in: POSTGRES_URL, PRIVATE_KEY, CREATOR_ADDR, KEEPER_KEY, JWT_SECRET
```

## Full deploy (one command)
```bash
make fresh-deploy
# builds contracts → tests → deploys to Coston2 → regenerates ABIs
# → updates .env + frontend/.env.local + render.yaml → starts Docker
```

## Daily operations
```bash
make up      # start all services (restart:always — survives reboots + crashes)
make down    # stop all services
make logs    # tail all logs (Ctrl-C to stop)
make health  # HTTP health check
make status  # process/port/chain status
```

## Services
| Service | Port | Description |
|---------|------|-------------|
| `api` | 8080 | REST API + SSE events |
| `indexer` | — | Chain watcher + keeper bot + sweepers |
| `redis` | 6379 | Event pub/sub + rate limiting |
| `frontend` | 3000 | Next.js SSR app |

All services have `restart: always` — they restart automatically on crash or Docker daemon restart. `make up` once = runs forever.

## Auction keeper bot
Enabled by setting `KEEPER_KEY` in `.env`. The keeper runs two sweeps every 30 seconds:

**Auto-settle:** Calls `settle(auctionId)` on expired auctions that have at least one bid.
- Contract: NFT → winner, fee (upfront from bidder) → feeRecipient, full bid → seller.

**Auto-cancel:** Calls `cancelIfInactive(auctionId)` on auctions with zero bids past the 30-minute window.

Fund the keeper wallet with small amounts of FLR for gas.

## ABI regeneration
After redeploying contracts, regenerate TypeScript ABIs:
```bash
make contracts-build   # compile
make regen-abi         # writes frontend/lib/abi/*.ts from compiled JSON
```

## Environment variables
### Root `.env` (backend + indexer)
| Key | Required | Description |
|-----|----------|-------------|
| `POSTGRES_URL` | yes | Supabase connection string |
| `REDIS_URL` | yes | Redis (default: `redis://redis:6379` in Docker) |
| `RPC_URL` | yes | Flare/Coston2 JSON-RPC |
| `CHAIN_ID` | yes | `114` (Coston2) or `14` (mainnet) |
| `MARKETPLACE_ADDR` | yes | Deployed Marketplace address |
| `AUCTION_ADDR` | yes | Deployed AuctionHouse address |
| `OFFERBOOK_ADDR` | yes | Deployed OfferBook address |
| `JWT_SECRET` | yes | 32+ byte hex for SIWE JWT signing |
| `FRONTEND_URL` | yes | CORS origin |
| `KEEPER_KEY` | yes | Keeper wallet private key (auto-settles/cancels auctions) |
| `INDEX_FROM_BLOCK` | opt | Start block (auto-set by `make deploy`) |

### `frontend/.env.local`
| Key | Description |
|-----|-------------|
| `NEXT_PUBLIC_API_URL` | Backend URL |
| `NEXT_PUBLIC_CHAIN_ID` | `114` / `14` |
| `NEXT_PUBLIC_RPC_URL` | Chain RPC for wagmi |
| `NEXT_PUBLIC_MARKETPLACE_ADDR` | Marketplace address |
| `NEXT_PUBLIC_AUCTION_ADDR` | AuctionHouse address |
| `NEXT_PUBLIC_OFFER_ADDR` | OfferBook address |

## Contract addresses are permanent
Once deployed, contract addresses and `feeRecipient` cannot change. The 1.5% platform fee is hardcoded as `PLATFORM_FEE_BPS = 150` — not overridable by any env var or admin key. Contracts have no pause function and no admin role — they run forever. To change the fee recipient or fee rate: deploy new contracts and run `make fresh-deploy`.

No royalties are supported or enforced by any contract.
