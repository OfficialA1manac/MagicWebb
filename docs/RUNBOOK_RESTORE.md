# Restore drill — Neon snapshot → staging machine → verify

One Neon project per network (org A1manac, all Singapore):

| Network  | Neon project id            | Fly app              |
|----------|----------------------------|----------------------|
| Coston2  | `still-mountain-83246431`  | `magicwebb`          |
| Songbird | `bitter-pond-73256956`     | `magicwebb-songbird` |
| Flare    | `royal-paper-82216877`     | `magicwebb-flare`    |

Everything in the database is **re-derivable from chain** except profiles,
saved searches, notifications, API keys and the governance/gas trail. A
restore is therefore about speed (a re-index from `INDEX_FROM_BLOCK` takes
hours on Coston2 and would take days on a busy mainnet), not about the only
copy of the truth.

## Snapshot schedule (owner-gated — changes billing)

Proposed for every project: **daily, retain 7**. Neon charges for snapshot
storage, so this is switched on by the owner, not by CI:

```
# Neon MCP (claude.ai connector) or the Neon console → Project → Backups
set_snapshot_schedule(project_id="still-mountain-83246431", schedule={ "frequency": "daily", "retention_days": 7 })
set_snapshot_schedule(project_id="bitter-pond-73256956",    schedule={ "frequency": "daily", "retention_days": 7 })
set_snapshot_schedule(project_id="royal-paper-82216877",    schedule={ "frequency": "daily", "retention_days": 7 })
```

Check it stuck: `get_snapshot_schedule(project_id=…)` and `list_snapshots(project_id=…)`
should show a snapshot per day after 24 h.

## The drill (run it once per quarter; ~20 minutes)

Pick the network first; everything below derives from it:

```bash
NET=coston2                      # coston2 | songbird | flare
APP=$(jq -r .app.flyApp deployments/$NET.json)          # magicwebb | magicwebb-songbird | magicwebb-flare
CHAIN_ID=$(jq -r .chainId deployments/$NET.json)
PROJECT=<Neon project id from the table above>
RESTORE_APP=$APP-restore
```

1. **Pick the snapshot.** `list_snapshots(project_id=$PROJECT)` → note the id
   and its timestamp. Prefer the newest one taken *before* the incident.
2. **Branch from it, never restore in place.** `restore_snapshot(project_id,
   snapshot_id, target_branch_name="restore-YYYYMMDD")` creates a new branch
   with its own compute. The production branch is untouched until step 6.
3. **Get its connection string.** `get_connection_string(project_id,
   branch_id=<restore branch>)` (pooled URL).
4. **Point a staging Fly machine at it.** Never the production app:

   ```bash
   fly apps create "$RESTORE_APP" --org personal   # once per network
   fly secrets set -a "$RESTORE_APP" \
     POSTGRES_URL='<restore branch pooled url>' \
     JWT_SECRET="$(openssl rand -hex 32)" \
     CHAIN_ID="$CHAIN_ID" \
     MARKETPLACE_ADDR="$(jq -r .contracts.marketplace deployments/$NET.json)" \
     AUCTION_ADDR="$(jq -r .contracts.auctionHouse deployments/$NET.json)" \
     OFFERBOOK_ADDR="$(jq -r .contracts.offerBook deployments/$NET.json)"
   # No KEEPER_KEY on the staging machine: it must never broadcast.
   fly deploy -a "$RESTORE_APP" --image "$(fly image show -a "$APP" --json | jq -r '.Registry + "/" + .Repository + ":" + .Tag')"
   ```

   Migrations run at boot (`MIGRATE_TIMEOUT` 5 m). `RESET_ON_ADDRESS_CHANGE`
   stays unset (on mainnets the process refuses it anyway).
5. **Verify against the snapshot's own baseline, not today's production.**
   Row counts move both ways (cancelled listings and expired offers are
   deleted), so "restore ≤ production" proves nothing. Instead:

   ```sql
   SELECT (SELECT count(*) FROM listings)       AS listings,
          (SELECT count(*) FROM auctions)       AS auctions,
          (SELECT count(*) FROM offers)         AS offers,
          (SELECT count(*) FROM profiles)       AS profiles,
          (SELECT max(block_number) FROM indexer_state) AS last_block;
   ```

   - `last_block` on the restore branch must be **at or just below** the chain
     block at the snapshot's timestamp (explorer → block by time); a value far
     below it means the snapshot predates the incident by more than you thought.
   - `profiles` (never re-derivable) must equal the count in the daily
     `gas_alerts`/ops log line nearest the snapshot, or at least not be lower
     than the previous drill's figure recorded below.
   - Then hit the staging app: `/readyz` 200, `/api/v1/listings?limit=3`
     returns rows, `/api/v1/indexer/slo` shows `head_lag_blocks` shrinking as
     the watcher replays from the restored `last_block` — the chain-derived
     tables converge on production by themselves.
6. **Promote only if production is actually lost.** Swap `POSTGRES_URL` on the
   production app to the restore branch's URL (`fly secrets set -a "$APP"
   POSTGRES_URL=…`), watch `/readyz`, then in Neon make the restore branch the
   default (`set_default_branch`). The indexer catches up from `last_block`;
   the keeper resumes settlements automatically (single-flight gate).
7. **Clean up.** `fly apps destroy "$RESTORE_APP"` (or keep it stopped) and
   `delete_branch` for drill branches older than the drill.

Drill log (append a line per drill): `date · network · snapshot id · profiles count · minutes`.

## What is NOT in the snapshot

- Fly secrets (`KEEPER_KEY`, `JWT_SECRET`, `WC_PROJECT_ID`) — 1Password / owner.
- The S3 bucket when `IMG_STORE_BACKEND=s3` — blob metadata is in Postgres,
  bodies are in the bucket; snapshot both or accept re-ingest.
- Redis (optional caches) — safe to lose.

## HA note

One Fly machine per app, by design: the REST rate limiter and SIWE nonces
are Postgres-backed, but the in-memory caches and the WebSocket fan-out are
per-process. Scaling to two machines is safe for reads (the gRPC mesh fans
out SSE) and the keeper election (`internal/keeper`) guarantees a single
broadcaster; it is not needed at today's traffic and doubles the Neon
connection count.
