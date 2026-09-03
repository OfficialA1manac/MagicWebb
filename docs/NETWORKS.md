# Networks — provisioning one stack per chain

Every network is fully independent: its own Fly app, Neon project, optional Redis, socket
and indexer. Nothing is shared except the Docker image and the code.

| | Coston2 | Songbird | Flare |
|---|---|---|---|
| Chain id | 114 | 19 | 14 |
| Role | testnet | canary mainnet | mainnet |
| Fly app | `magicwebb` | `magicwebb-songbird` | `magicwebb-flare` |
| Neon project | `still-mountain-83246431` | `snowy-mountain-21008952` | `falling-dust-05670744` |
| Record | `deployments/coston2.json` | `deployments/songbird.json` | `deployments/flare.json` |
| CI flag | `COSTON2_ENABLED` (default true) | `SONGBIRD_ENABLED` | `FLARE_ENABLED` |
| Profile | `internal/chain/profile` 114 | 19 | 14 |

## Per-network resources

1. **Neon Postgres** — one project per network. Pooled URL for `POSTGRES_URL`; optionally a
   read-only branch for `READ_POOL_URL`. Never point two networks at one database: the
   indexer's `deployment_config` guard and `chain_id` columns assume one chain.
2. **Redis (optional)** — Upstash or Fly Redis in the app's region. Set `REDIS_URL` as a Fly
   secret. Shares the read caches across machines *of that network*: trending (30s),
   activity (10s) and the merged wallet inventory (30s) — see `internal/cache`. Rate-limit
   counters and SIWE nonces are **Postgres-backed** (`internal/ratelimit`, `internal/nonce`)
   and never touch Redis. Unset = per-instance memory caches (fine for one machine).
3. **Fly secrets** — `POSTGRES_URL`, `JWT_SECRET`, `KEEPER_KEY`, `WC_PROJECT_ID`; optional
   `REDIS_URL`, `READ_POOL_URL`, `RPC_URL`/`RPC_URLS` (private provider), `SAFE_ADDR`,
   `PERSONAL_WALLET_ADDR`, `DISCORD_WEBHOOK_URL`, `SENTRY_DSN`.
4. **Keeper wallet** — the `KEEPER_KEY` address needs native balance
   (`KEEPER_MIN_BALANCE_WEI`); the profile caps its gas (`MaxFeeCapGwei`).
5. **Contracts** — `deployments/<network>.json`. Status lifecycle:

   `not-deployed → read-only → deployed`

   - `not-deployed`: no app exists. The network renders as "Unavailable" in switchers.
   - `read-only`: the Fly app + Neon DB exist, contracts are null. The backend boots in
     read-only network mode (UI, API, wallet, profile; indexer/keepers/verifier idle),
     the frontend shows the read-only banner + empty states, and the origin appears in
     every sibling's switcher via NETWORK_URLS.
   - `deployed`: contracts live, full stack (indexer, keepers, verifier, trading).

## Bringing a network up read-only (no contracts yet)

```text
1. Create the Neon project; note the pooled POSTGRES_URL (migrations run at boot)
2. fly apps create magicwebb-<network>
3. fly secrets set -a magicwebb-<network> POSTGRES_URL=… JWT_SECRET=… [REDIS_URL=…]
   (no KEEPER_KEY — keepers are idle in read-only mode)
4. GitHub → Variables → <NETWORK>_ENABLED = true
5. edit deployments/<network>.json: status "read-only" (contracts stay null)
6. push to main (or run the Deploy workflow manually) → CI deploys every enabled
   network and regenerates every app's NETWORK_URLS to include the new origin
```

## Enabling trading on a network

```text
1. forge script contracts/script/DeployV34.s.sol (unsealed) … --broadcast      (see DEPLOY_CHECKLIST.md)
2. edit deployments/<network>.json: status deployed, addresses, indexFromBlock
3. fly secrets set -a magicwebb-<network> KEEPER_KEY=…
4. push to main → CI redeploys; indexer/keepers/verifier start on the next boot
```

## Scaling a network to 2+ machines

Set `GRPC_PORT=9090` and `GRPC_PEERS=<fly 6PN addresses>` so the instances form the event
mesh (every WebSocket client sees every event) and elect a single keeper. Set `REDIS_URL`
so the read caches are shared (rate limits and nonces are already shared via Postgres).
Keep `auto_stop_machines = "off"`.

## What must never vary between networks

The code, the Docker image, the migration set, the ABI. If something needs to differ, it is
a profile field (`internal/chain/profile`) or an env var — never a branch of the code.
