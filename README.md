# Magic Webb

Non-custodial NFT marketplace on Flare Coston2 — buy, sell, bid, and make offers on ERC-721 / ERC-1155 tokens without giving up custody of your assets or your keys.

---

## What It Is

Magic Webb is a marketplace, not a creation platform. It lets anyone trade NFTs that already exist on Coston2. The smart contracts are non-custodial: your NFT stays in your wallet until the moment a sale settles.

**What you can do:**
- Browse listings and collections
- Buy NFTs at a fixed price (buyer pays the 1.5% taker fee on top)
- List your NFTs for sale (free — no listing fee)
- Create timed English auctions with anti-snipe extension
- Make stacked, time-locked offers (Option-4 positions)
- Accept incoming offers on NFTs you own (no fee — fees were collected up-front)

**What this platform does not do:**
- Mint new NFTs or deploy collections
- Bridge assets cross-chain
- Accept fiat payment
- Pay royalties (royalty registry was dropped in the rework)
- Run on Flare mainnet (Coston2 testnet only for now)

---

## Fee Model (unified 1.5% taker)

| Action | Who pays | What they pay |
|--------|----------|---------------|
| List / batch-list / create auction / mark-offer-eligible | seller | nothing |
| Buy | buyer | `price + 1.5%` (seller receives `price` in full) |
| Auction bid | bidder | `bid + 1.5%` (1.5% is non-refundable — outbid refunds bid only) |
| Auction winning settle | winner already paid | seller receives the full bid, no extra fee |
| Auction reserve-unmet / seller cancel | leading bidder | refunded `bid` only; fee retained |
| Offer (stacked deposit) | bidder | `offer + 1.5%` (fee forwarded immediately, non-refundable) |
| Offer expiry refund | platform | bidder receives total principal back; fee retained |
| Accept offer | seller | nothing (receives full position principal) |

Floor: every priced input (`list`, `reserve`, `offer`) must be ≥ `MIN_PRICE = 0.01 FLR`.

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Frontend | HTMX + Alpine.js + ethers v6, embedded in Go templates |
| Backend | Go 1.23, Fiber, pgx (Postgres), Server-Sent Events |
| Smart Contracts | Solidity 0.8.26, Foundry test suite |
| Database | Postgres + Goose migrations |
| Wallets | MetaMask + WalletConnect v2 |
| Blockchain | Flare Coston2 testnet (chain ID 114) |

---

## Architecture

```
Browser (HTMX + Alpine + ethers v6)
     |
  REST API + SSE (/api/v1/*, /events)
     |
Go server (single binary, port 8080)
  ├── REST handlers (listings, auctions, offers, wallet, profile, notifications, reports, admin)
  ├── HTMX page handlers + embedded templates
  ├── Indexer goroutines
  │     ├── Market event watcher (Marketplace + AuctionHouse + OfferBook)
  │     ├── Transfer watcher (ERC-721/1155 ownership tracking + orphan flagging)
  │     ├── Metadata worker (tokenURI / uri fetch + attribute parse)
  │     └── Keeper (settle expired auctions + refundExpired offer positions)
  └── SSE broadcaster
         |
   Postgres ← Goose migrations (incl. 006_rework.sql)
         |
   Coston2 RPC
```

---

## Smart Contracts

Deploy fresh to Coston2 with `forge script script/DeployCoston2.s.sol`. The deploy script prints the three addresses; paste them into `.env`.

| Contract | Notes |
|----------|-------|
| Marketplace | Free `list / list1155 / batchList`. `buy(coll, id, seller)` payable: msg.value = price + 1.5%. |
| AuctionHouse | English with anti-snipe (3-min extension), default 3d / max 7d, min-increment max(5%, sellerFlatMinFLR). Outbid refunds bid only. |
| OfferBook | Stacked positions per (NFT, bidder). 1.5% fee forwarded on every deposit, non-refundable. `acceptOffer` is free. |

All three contracts are unstoppable: `feeRecipient` is immutable; there is no pause, no admin, no upgrade path.

---

## Quick Start

```bash
git clone https://github.com/OfficialA1manac/MagicWebb
cd MagicWebb
cp backend/.env.example backend/.env
# Edit backend/.env — set POSTGRES_URL, RPC_URL, JWT_SECRET, contract addresses, ADMIN_ADDRS
cd backend && go run ./cmd/server
# → http://localhost:8080
```

For contract development:

```bash
cd contracts
forge build
forge test -vvv
forge script script/DeployCoston2.s.sol \
  --rpc-url https://coston2-api.flare.network/ext/C/rpc \
  --broadcast --private-key $PRIVATE_KEY
```

---

## Environment Variables

Backend (`backend/.env`):

| Variable | Required | Description |
|----------|----------|-------------|
| `POSTGRES_URL` | Yes | Postgres connection URL |
| `RPC_URL` | Yes | Coston2 JSON-RPC endpoint |
| `CHAIN_ID` | Yes | `114` for Coston2 |
| `MARKETPLACE_ADDR` | Yes | Deployed Marketplace address |
| `AUCTION_ADDR` | Yes | Deployed AuctionHouse address |
| `OFFERBOOK_ADDR` | Yes | Deployed OfferBook address |
| `JWT_SECRET` | Yes | 32+ hex bytes — `openssl rand -hex 32` |
| `FRONTEND_URL` | Yes | Allowed CORS origin |
| `ADMIN_ADDRS` | No | Comma list of EOAs allowed to call `/api/v1/admin/*` (SIWE JWT also required) |
| `WC_PROJECT_ID` | No | WalletConnect projectId surfaced to the browser |
| `KEEPER_KEY` | No | Hex private key for the auction-settlement + offer-refund keeper |
| `KEEPER_POLL_SECONDS` | No | Keeper poll interval, default 30 |
| `INDEX_FROM_BLOCK` | No | Indexer start block (set to deploy block on rework) |
| `GETLOGS_CHUNK` | No | Blocks per `eth_getLogs` request, default 30 |

---

## License

MIT
