# Deploying MagicWebb on Fly.io

One Go binary: HTTP server + HTMX UI + chain indexer + keeper, all in-process.
UI assets and DB migrations are embedded — the image is the binary, nothing
else. Repo ships `Dockerfile` + `fly.toml`; Fly builds remotely, no local
Docker needed.

Cost note: Fly has no permanent free tier — new accounts get trial credit,
then a single shared-cpu-1x/512MB machine runs ≈ $3–4/mo. DB stays on
Neon Postgres free tier.

## 1. One-time setup

```bash
# install CLI (Windows PowerShell)
pwsh -Command "iwr https://fly.io/install.ps1 -useb | iex"

fly auth login
fly apps create magicwebb        # pick another name if taken — then update
                                 # app/SIWE_DOMAIN/FRONTEND_URL in fly.toml
```

## 2. Secrets

Everything non-secret already lives in `fly.toml` `[env]` (v2 contract
addresses, RPC, `INDEX_FROM_BLOCK`). Set the rest:

```bash
fly secrets set \
  POSTGRES_URL='<Neon Postgres DSN (port 5432, sslmode=require)>' \
  JWT_SECRET="$(openssl rand -hex 32)" \
  KEEPER_KEY='<keeper private key from backend/.env.keeper>'
# optional:
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
