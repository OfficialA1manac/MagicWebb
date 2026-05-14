# MagicWebb

Non-custodial NFT marketplace on **Flare Coston2** testnet. Pure on-chain — wallet talks straight to the contracts via wagmi/viem. No backend, no database, no indexer.

- **Buy / sell** at fixed price (`Marketplace`)
- **English auctions** with reserve, increment, and **pull-pattern refunds** (`AuctionHouse`)
- **Off-chain signed offers** on listed *or unlisted* tokens (`OfferBook`, EIP-712)

Sellers keep custody until trade settles. Platform fee (default 2.5%) routes directly to the creator wallet.

## Stack

| Layer | Tech |
|---|---|
| Contracts | Solidity 0.8.24, Foundry, OpenZeppelin |
| Frontend  | Next.js 15 (App Router), React 19, wagmi v2, viem v2, TailwindCSS |
| Network   | Flare Coston2 (chain id 114), C2FLR |

`MarketplaceCore` is the shared base (fee config, NFT transfer, fee splitter). The three trade contracts inherit it.

**Who this is for** — See [`docs/PLATFORM.md`](docs/PLATFORM.md) for a full walkthrough: collectors, sellers/creators, developers, and community operators.

## Run from repository root (no `cd frontend`)

```bash
npm install --prefix frontend   # first time (or: npm run install:web)
npm start                       # same as: npm --prefix frontend run dev
npm run dev:clean               # wipe .next then dev (fixes stale webpack chunks)
npm run clean                   # wipe frontend/.next only (stop dev server first)
npm run build                   # production build (outputs standalone under frontend/.next)
```

On Windows, these `npm` scripts work in PowerShell. `make` targets still expect Git Bash / WSL.

## Prereqs

- `foundry` (forge, cast)
- `node` ≥ 20 + `npm`
- `jq` (for `make load-addrs`)

## Setup

```bash
cp frontend/.env.example frontend/.env.local
make install                  # node modules + forge libs
# Edit frontend/.env.local — set PRIVATE_KEY only if you intend to deploy.
make start                    # builds (if needed) and serves http://127.0.0.1:3000
```

**Single env file:** `frontend/.env.local` is the only file Next.js and the Makefile read. Copy from `frontend/.env.example` once, then edit. `NEXT_PUBLIC_*` vars are inlined at build time; `PRIVATE_KEY` / `ROUTESCAN_API_KEY` stay server-side and are used only by `make deploy` / verify.

**WalletConnect (Reown):** create a project at [cloud.reown.com](https://cloud.reown.com), then set `NEXT_PUBLIC_WALLETCONNECT_PROJECT_ID` in `frontend/.env.local`. Set `NEXT_PUBLIC_APP_URL` to the URL users open (e.g. `https://yourdomain.com` in production). Restart `npm run dev` after changing env.

## Lifecycle

```bash
make start      # start the marketplace
make stop       # stop the marketplace
make restart    # stop + start
make status     # pid / port + RPC reachability
make health     # HTTP 200 on / + RPC (needs curl)
make clean      # remove build artifacts
```

## Production (same Coston2 contracts, one env file)

Ship the same `frontend/.env.local` to the server (keep chain id **114** and your Coston2 contract addresses until you redeploy elsewhere). Then:

```bash
cd frontend
npm ci
npm run build
npm run start -- -p 3000
```

The app is built with `output: "standalone"` plus baseline security headers (frame, MIME sniffing, referrer). Add **HSTS** only at your HTTPS reverse proxy (e.g. nginx, Cloudflare), not over plain HTTP.

**Standalone output:** after `npm run build`, run `node .next/standalone/frontend/server.js` from the `frontend` directory (or copy `frontend/.next/static` → `frontend/.next/standalone/frontend/.next/static` and `frontend/public` → `frontend/.next/standalone/frontend/public` per [Next.js standalone](https://nextjs.org/docs/app/api-reference/config/next-config-js/output)). `outputFileTracingRoot` is set to the repo root so tracing does not pick a parent-folder lockfile by mistake.

**Windows:** use Git Bash or WSL for `make start` / `make deploy`; for local dev, `npm start` from repo root or `cd frontend && npm run dev` in PowerShell.

## Deploy contracts (optional)

```bash
make contracts-build
make contracts-test
make deploy        # broadcasts via NEXT_PUBLIC_RPC_URL (Coston2 by default), then load-addrs → .env.local
```

After `make deploy`, the new contract addresses are written back into `frontend/.env.local` automatically; the next `make restart` picks them up.

## Coston2 reference

| Field | Value |
|---|---|
| Chain ID | 114 |
| Native   | C2FLR |
| RPC      | `https://coston2-api.flare.network/ext/C/rpc` |
| Explorer | `https://coston2-explorer.flare.network` |
| Faucet   | `https://faucet.flare.network` (select Coston2) |

## Live deployment (chain id 114)

| Contract | Address |
|---|---|
| Marketplace  | `0x767F7fF7c66673488a30053C025C153E13b6BfAa` |
| AuctionHouse | `0x6016688AfFAF5427E1f8100160A6378Da2B1476a` |
| OfferBook    | `0x0C7112Ec22262d1E423132e35bC87E33abF64a22` |

Fee recipient (`feeVault` in each contract) is **immutable** at deploy time — read it on [Coston2 explorer](https://coston2-explorer.flare.network) from the contract you interact with; do not trust a copy-pasted EOA from this README alone. Default platform fee is **250 bps (2.5%)** on-chain (`feeBps()`). All three contracts are verified on Routescan; **49/49** `forge test` pass.

## User flows

### Buy listed token
1. Connect wallet → switch to Coston2 if prompted.
2. Search by collection → token detail page.
3. Click **Buy now**. Tx submits with `msg.value == price`.
4. Seller receives `price * (1 - feeBps/10000)`; creator wallet gets the fee.

### Auction
1. Seller `setApprovalForAll(AuctionHouse, true)` and calls `create(...)`.
2. Bidders call `bid` on `/auction/:id`. Each outbid credits the prior bidder via `pendingReturns`.
3. Outbid bidders claim their refund on **Profile → Withdraw refund** (`withdrawRefund()`).
4. After `endsAt`, anyone calls **Settle**. NFT moves; fee routes to creator.

### Signed offer (works on unlisted tokens)
1. Bidder funds deposit (`OfferBook.deposit` payable).
2. Bidder fills the offer modal → wallet signs EIP-712 typed data → `{offer, sig}` JSON copied to clipboard.
3. Bidder sends JSON to the token owner off-chain.
4. Owner approves `OfferBook` for the token, then submits `acceptOffer(offer, sig, tokenIdActual)`.
5. NFT transfers; deposit is debited; fee routes to creator. Bidder can pre-emptively `cancelOffer(nonce)`.

## Gas notes

- `feeVault` is the creator EOA — `buy / settle / acceptOffer` push the fee via one `.call{value:}`. No FeeVault contract hop.
- **Pull-pattern refunds** on outbids — one storage write, re-entrancy-safe.
- **EIP-712 signed offers off-chain** — chain spend is `O(matches)`, not `O(offers)`.
- Solidity 0.8.24 / Cancun target — uses `MCOPY` and `PUSH0`.

## Troubleshooting

See **[`docs/TROUBLESHOOTING.md`](docs/TROUBLESHOOTING.md)** for full detail (webpack chunks, chain **114** / Coston2 RPC, `NotApproved`, `Expired`, `OfferUsed`, auction refunds). Quick reminders:

- **Chunks / `./NNNN.js`:** `npm run dev:clean` from repo root, or `cd frontend && npm run clean && npm run dev`. Do not mix `next dev` and `next start` on the same `.next` without rebuilding; exclude `frontend/.next` from OneDrive sync.
- **Wrong network:** use the in-app banner — **Switch** or **Add Coston2** (chain id **114**).
- **NotApproved:** `setApprovalForAll` on the NFT for **Marketplace**, **AuctionHouse**, or **OfferBook** depending on the action (see troubleshooting doc).
- **Expired listing / offer:** seller re-lists or bidder re-signs with a new expiry.
- **OfferUsed:** new nonce + new signature.
- **Auction refund:** **Profile → Withdraw refund** (`withdrawRefund` on `AuctionHouse`).

## License

MIT.
