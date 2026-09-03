# Deploying MagicWebb to Fly.io — one app per network

MagicWebb runs **one independent stack per blockchain network**. Each stack is its own
Fly app, its own Neon Postgres project, its own (optional) Redis, its own `/ws` and its
own indexer. The binary validates exactly one `CHAIN_ID` per process
(`backend/internal/config/config.go`), so "multi-network" is three deployments of one
image, never one process serving three chains.

| Network | Chain ID | Fly app | Template | Status |
|---|---|---|---|---|
| Coston2 (testnet) | 114 | `magicwebb` | `fly.coston2.toml.example` | live |
| Songbird (canary) | 19 | `magicwebb-songbird` | `fly.songbird.toml.example` | contracts not deployed |
| Flare (mainnet) | 14 | `magicwebb-flare` | `fly.flare.toml.example` | contracts not deployed |

Contract addresses: **`deployments/<network>.json` is the only source of truth.**
See `deployments/README.md`.

## How CI deploys (push to `main`)

`.github/workflows/deploy.yml`:

1. `test` — Zig libs + `go test -tags zigmedia -race`.
2. `frontend-lint` — `astro check`, vitest, test-file guard.
3. `deploy` — checks repository variables against `deployments/<NETWORK>.json`
   (`tools/check-deployments.sh`), generates `fly.toml` from
   `fly.<NETWORK>.toml.example` by substituting `CHANGE_ME_*`, fails on any leftover
   placeholder, then `fly deploy --remote-only --strategy rolling`, then smoke-tests
   `/healthz` and `/readyz` and runs `tools/check-fly-sync.sh`.
4. `verify` (tags `v*` only) — `forge verify-contract` for Marketplace, AuctionHouse,
   OfferBook, MarketplaceManager on the network's explorer, addresses from
   `deployments/<NETWORK>.json`.

Repository **variables** (Settings → Secrets and variables → Actions → Variables):
`NETWORK`, `MARKETPLACE_ADDR`, `AUCTION_ADDR`, `OFFERBOOK_ADDR`, `NFT_ADDR`,
`MARKETPLACE_MANAGER_ADDR`, `TRACKED_COLLECTIONS`, `INDEX_FROM_BLOCK`.

Repository **secrets**: `FLY_API_TOKEN`, `REOWN_PROJECT_ID`, `WC_PROJECT_ID`,
`EXPLORER_API_KEY`.

Runtime secrets are set on the Fly app, never in the repo:

```bash
fly secrets set -a magicwebb \
  POSTGRES_URL='postgresql://…neon…/neondb?sslmode=require' \
  JWT_SECRET="$(openssl rand -hex 32)" \
  KEEPER_KEY='0x…' \
  WC_PROJECT_ID='…'
# optional: READ_POOL_URL, REDIS_URL, RPC_URL/RPC_URLS (private provider),
#           SAFE_ADDR, PERSONAL_WALLET_ADDR, DISCORD_WEBHOOK_URL, SENTRY_DSN
```

## Manual deploy (same thing CI does)

```bash
cp fly.coston2.toml.example fly.toml              # pick the network
# replace every CHANGE_ME_* from deployments/coston2.json
fly deploy --remote-only --strategy rolling --build-arg REOWN_PROJECT_ID=…
curl -fsS https://magicwebb.fly.dev/healthz && curl -fsS https://magicwebb.fly.dev/readyz
```

`fly.toml` is gitignored on purpose; the committed `*.toml.example` files are the
templates.

## Bringing up a new network (Songbird / Flare)

Nothing in the app has to change. The whole procedure is:

1. Deploy contracts: `contracts/script/DeployV34.s.sol` UNSEALED with
   `PRIVATE_KEY`, `ADMIN_ADDR` (that network's saved admin wallet -- every
   network stays instantly upgradeable until the owner's explicit
   go-immutable `renounceAdmin()`), `FEE_RECIPIENT_ADDR`, `KEEPER_ADDR`.
   See `docs/DEPLOY_CHECKLIST.md`.
2. Record the result in `deployments/<network>.json` (`status: "deployed"`, addresses,
   `indexFromBlock` = deploy block).
3. Create the Neon project for that network (never share a database between networks).
   Optionally a Redis instance.
4. `fly apps create magicwebb-<network>`; `fly secrets set …` as above.
5. Set the GitHub repository variables for that network and push / re-run the workflow.
6. Add the new origin to `NETWORK_URLS` on the **other** apps so their network switcher
   links to it (format `114=https://magicwebb.fly.dev,19=https://magicwebb-songbird.fly.dev`).

Until step 1 happens the switcher shows the network as unavailable — deliberately.

## Things that bite

- The binary runs goose migrations at boot and **fatals** if `POSTGRES_URL` is wrong.
  Verify DB connectivity before deploying.
- The startup guard compares `deployment_config` (migration 020) with the env addresses.
  Changing contract addresses on an existing database requires
  `go run ./cmd/chainwipe` first — the indexer cannot mix two contract sets.
- `auto_stop_machines` must stay `"off"`: indexer and keeper run in-process.
- Neon branches auto-archive when idle; the first request after a long idle can take a
  few seconds while the endpoint wakes.
