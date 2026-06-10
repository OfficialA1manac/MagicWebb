# Deploying MagicWebb on Fly.io

One Go binary: HTTP server + HTMX UI + chain indexer + keeper, all in-process.
UI assets and DB migrations are embedded — the image is the binary, nothing
else. Repo ships `Dockerfile` + `fly.toml`; Fly builds remotely, no local
Docker needed.

Cost note: Fly has no permanent free tier — new accounts get trial credit,
then a single shared-cpu-1x/512MB machine runs ≈ $3–4/mo. DB stays on
Supabase free tier.

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
  POSTGRES_URL='<Supabase pooler DSN (port 6543, sslmode=require)>' \
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
