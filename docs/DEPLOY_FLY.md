# Deploying MagicWebb on Fly.io

One Go binary: HTTP server + HTMX UI + chain indexer + keeper, all in-process.
UI assets and DB migrations are embedded — the image is the binary, nothing
else. Repo ships `Dockerfile` + `fly.toml`; Fly builds remotely, no local
Docker needed.

**Cost:** Fly has no permanent free tier — new accounts get trial credit,
then a single shared-cpu-1x/512MB machine runs ≈ $3–4/mo.

**Database:** [Neon Postgres](https://neon.tech) free tier (0.5 GB storage,
100 CU-hours/mo, up to 10k pooled connections).

**Images:** All NFT assets are self-hosted in Postgres BYTEA — no IPFS,
no Pinata, no external CDN costs.

## 1. One-time setup

### 1a. Create a Neon Postgres project

1. Sign up at [neon.tech](https://neon.tech) (no credit card).
2. Create a project in `us-east-2` (same region as Fly.io `iad` for low latency).
3. Copy the **pooled connection string** from the dashboard:
   ```
   postgresql://user:password@ep-<project>-<pooler>.us-east-2.aws.neon.tech/neondb?sslmode=require
   ```
4. Optionally create a named database:
   ```bash
   psql "<connection-string>" -c "CREATE DATABASE magicwebb;"
   ```

> Neon's built-in PgBouncer handles connection pooling. Use the pooled
> endpoint (default port 5432). The direct un-pooled port (6543) is only
> needed for `pg_dump` or long-running transactions.

### 1b. Create the Fly.io app

```bash
# install CLI (Windows PowerShell)
pwsh -Command "iwr https://fly.io/install.ps1 -useb | iex"

fly auth login
fly apps create magicwebb        # pick another name if taken — then update
                                 # app/SIWE_DOMAIN/FRONTEND_URL in fly.toml
```

## 2. Secrets

Everything non-secret already lives in `fly.toml` `[env]` (v2 contract
addresses, RPC, `INDEX_FROM_BLOCK`). Set secrets via `fly secrets set`:

```bash
# PostgreSQL — your Neon pooled connection string
# No IPFS/Pinata keys needed — all images self-hosted in Postgres BYTEA
fly secrets set \
  POSTGRES_URL='postgresql://user:password@ep-<project>-<pooler>.us-east-2.aws.neon.tech/magicwebb?sslmode=require' \
  JWT_SECRET="$(openssl rand -hex 32)" \
  KEEPER_KEY='<keeper private key from backend/.env.keeper>'

# Optional:
# fly secrets set WC_PROJECT_ID='...' SERVICE_TOKEN='...' ADMIN_ALLOWLIST='0x...'
```

## 3. Deploy

```bash
fly deploy            # remote build from Dockerfile, then rolling release
fly logs              # watch: migrations apply, indexer catches up from 31610228
fly status            # machine should be `started`, health check passing
```

`fly.toml` pins `auto_stop_machines = "off"` + `min_machines_running = 1` —
required: the indexer and keeper run inside the web process and must not
scale to zero. Keep it at exactly 1 machine; the keeper assumes a single
instance (single-flight guards exist, but 1 machine is the supported shape).

## 4. Custom domain (optional)

```bash
fly certs add yourdomain.example   # then add the shown A/AAAA records
```

Update `SIWE_DOMAIN` and `FRONTEND_URL` in `fly.toml` to the new domain and
`fly deploy` again — SIWE signatures bind to the domain, logins break if it
doesn't match.

## 5. Update procedure

```bash
git pull && fly deploy
```

Migrations auto-apply on boot; the indexer resumes from the last indexed
block stored in Postgres.

## 6. Automated deploys via GitHub Actions

Every push to `main` automatically rebuilds and re-deploys via
`.github/workflows/deploy.yml`:

1. **test** job — `make test` (Go race detector). Gates the deploy.
2. **deploy** job — `fly deploy --remote-only --strategy rolling`. The
   Docker image is built on Fly's infrastructure; the workflow checks
   out the source but does **not** build the binary locally.

### One-time secret setup

```bash
# Create a scoped deploy token (NOT your personal auth token — this one
# is checked into GitHub Actions and has deploy-only scope).
fly tokens create deploy

# GitHub repo → Settings → Secrets and variables → Actions
# → New repository secret:
#   Name:  FLY_API_TOKEN
#   Value: <paste the token from `fly tokens create deploy`>
```

### Behaviour

- Pushes to `main` queue sequentially — at most one rolling release
  in flight per branch. Concurrent pushes wait for the in-progress
  one to complete rather than cancel it.
- PR branches do **not** trigger deploys (the workflow is bounded to
  `push` of `main`).
- A `make test` failure aborts the deploy; the previous release stays
  live.
- The `make test` target covers the full Go race-detector run
  (`cd backend && go test ./... -race -count=1 -timeout 120s`).
  Foundry contracts tests live separately in `ci.yml` and run
  concurrently from the same push without blocking the deploy gate.

If you ever need to revert an automated release:

```bash
fly releases              # list recent releases
fly releases rollback <id> # atomic revert to a prior release
```
