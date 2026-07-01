# MagicWebb v29 — Post-Launch Monitoring & Operations Runbook

> Companion to `contracts/AUDIT_REPORT.md` Phase 4d → Phase 6.
> Applies to Coston2 only.

## Event lore (chain-side monitoring)

| Event                | Severity tag     | What it means                           | Action                                                    |
|:---------------------|:-----------------|:----------------------------------------|:----------------------------------------------------------|
| `Listed` / `Bought`  | routine          | Marketplace activity                     | indexer handle                                            |
| `AuctionCreated`     | routine          | New auction live                         | indexer handle                                            |
| `BidPlaced`          | routine          | Cumulative bid landed                    | indexer handle                                            |
| `AuctionSettled`     | high             | Fund + NFT distribution                  | confirm indices in DB within 2 s                         |
| `AuctionReclaimed`   | **critical**     | 7-day safety-valve fired                 | **page on-call; audit the auction; this is the only path that can refund the winner's residual (no auto-resolve)** |
| `OfferMade` / `OfferAccepted` | routine | OfferBook activity               | indexer handle                                            |
| `PushFailed`         | **MEDIUM**       | A `.call{value: …}` reverted             | `runWithdrawalSweeper` will surface a banner via SSE; if PushFailed frequency >1/min investigate the receiver set |
| `AuctionStalled`     | **HIGH**         | Buyer-fault sandbox violation           | `settleUnstuck()` 7-day window opens; alert on-chain      |
| `EntriesPaused` / `EntriesUnpaused` | routine | Manager activity              | off-chain only (events are not stored on the cores)       |
| `AuditLog`           | informational    | Manager role + circuit-breaker writes   | audit only                                                |

## Backend health (Fly.io dashboard)

```bash
# Process / indexer liveness
curl -fsSL https://magicwebb.fly.dev/healthz | jq .status
# → {"status":"ok","watcher":"alive","uptime_s":3124,"last_block":4781209}

# SSE fan-out
curl -fsS -N --max-time 5 https://magicwebb.fly.dev/events | head -c 64
# → ": connected\n\n"
```

| Alert class | Signal                          | Auto-resolve? |
|:------------|:--------------------------------|:--------------|
| Indexer stall | `last_block` 60 s behind head | yes; backfill picks up on next tick |
| Watcher poll 5xx | RPC upstream errors             | yes; rpcpool failover + sticky retry |
| Keeper advisory-lock lost | `<CONTRACT_ADMIN>` rotated or `<KEEPER_BOT>` lost | **NO** — on-call must investigate |
| Postgres replication lag | Fly metrics `pg_lsn` drift      | yes; backfill re-runs idempotent |
| Fly.io region failover | 2xx rate < 99% for 5 min      | yes; DNS failover + warm spare |

## PushFailed investigation playbook

```bash
# 1. Get the past 24 h of PushFailed events.
cast logs --rpc-url "$RPC_URL" \
  --from-block $(($(cast block latest -n 86400 --rpc-url "$RPC_URL") - 1)) \
  --to-block latest \
  --address $MARKETPLACE_ADDR,$AUCTION_ADDR,$OFFERBOOK_ADDR \
  --event "PushFailed(address,uint256)"

# 2. Cross-reference each `to` address against on-chain
#    pendingReturns().
cast call $AUCTION_ADDR "pendingReturns(address)(uint256)" $TO_ADDR \
  --rpc-url "$RPC_URL"

# 3. If the receiver is a contract, simulate receive():
cast estimate $TO_ADDR --rpc-url …  # contract with empty value
```

If a receiver is a contract WITHOUT a payable `receive() / fallback()`
that has been holding escrow for >7 days, **page on-call**: this is
the only path for funds to be permanently trapped. A replacement
contract at a new address cannot claim the original address's
`pendingReturns` credit — the funds are unrecoverable unless the
receiver contract itself is upgradeable or has an explicit
`withdrawRefund()` path.

## Keeper advisory-lock health

The keeper is single-flight via Postgres advisory-lock(`SELECT pg_try_advisory_lock(<KEEPER_KEY_HASH>)`). If two replicas
simultaneously hold the lock (rare split-brain via network
partition), `WaitKeeperLock` rejects both, and the keeper logs
`keeper gate: acquisition failed`. 

```bash
# Check who currently holds the advisory lock.
psql "$DATABASE_URL" -c "SELECT * FROM pg_locks WHERE locktype='advisory' LIMIT 5;"

# Force-release if stuck (atomic; only if you're SURE no real keeper is running):
psql "$DATABASE_URL" -c "SELECT pg_advisory_unlock(<KEEPER_KEY_HASH>);"
```

## FTSO / State-Connector status

MagicWebb does NOT consume FTSO or State Connector feeds. The
contracts are pure escrow + auction/offer logic; the chain-truth
source is block timestamps (≥ 12 s granularity on Flare).

If a future protocol addition uses FTSO:

- Verify FTSO deliverer addresses against
  `https://gitlab.com/flarenetwork/flare-smart-contracts/-/tree/master/contract`
- Read feed prices via `FtsoV2Interface.getFeedById` only — NEVER
  inline a feed address; that was the FTSO-spoof attack class in
  FIP-12 era.
- For State Connector attestation (cross-chain NFT bridge use cases):
  validate finality window (≥ 60 s on Flare) before acting on a
  proof.

## MEV sandwich detection

AuctionHouse applies `MIN_BID_INCREMENT` (0.5% / 5% floor configurable
per auction) as the leading-bid floor. Front-running attackers must
outbid AND be outbid, costing capital on every failed attempt.

```sql
-- Detect potential sandwich patterns on a single auction:
SELECT block_number, encoded_bidder, tx_hash, wei
FROM auction_bids
WHERE auction_id = $AUCTION_ID
ORDER BY block_number, log_index;
-- Pattern: same leading bidder address → outbid → re-leader in <2 blocks
-- is a sandwich if a MEV-bot tx sits between; surface for human review.
```

## Runbook: per-alert class

| Alert                   | Who pages         | First action                                                |
|:------------------------|:------------------|:-------------------------------------------------------------|
| `AuctionReclaimed`      | on-call (P0)     | Check the auction was actually stalled — false positives if a seller pre-emptively `settleUnstuck()` triggers the 7-day window |
| `PushFailed` rate >1/min| indexer operator | Enumerate receivers, identify non-payable contracts, cohort by deployer |
| Indexer 60 s behind     | indexer (auto)    | Wait one tick; if persistent, rotate `RPC_URLS` priority    |
| Keeper lock lost        | on-call (P1)     | Confirm the rotate was intentional (`<CONTRACT_ADMIN>` grantRole trace) |
| Postgres trailing       | DBA (P2)         | Check `pg_stat_replication`; if application-side pool saturated, deploy a fresh instance |
| Image quota exhausted   | indexer operator | See F-09 runbook below; check `nft_image_blobs` total bytes + per-collection counts; raise cap or offload to object storage |

## === v29-Specific Monitoring ===

### F-01 SIWE chain-id mismatch spike

If the v29 chain-id substring check in `verifyHandler` increases
rejection rate above ~0.1% of total verifications:

- Investigate whether the `WALLET_MISCONFIG` herd is using a sticker
  wallet that auto-flips `chainId`.
- Or whether `CHAIN_ID` env changed mid-flight without coordinated
  Redis cache clear (the page would otherwise re-render with stale
  chain metadata).
- Or whether a phishing ring is now replaying testnet-signed
  payloads.

### F-02 transfers-chunk abort

If `runWatcher` logs `watcher: range failed, will retry` more than
once every 30 s, an RPC is stalling on header lookups; rotate
`RPC_URLS` priority.

### F-03 keeper gas cap clamp

If `log.Warn("keeper: feeCap above max; clamping")` fires more than
~3 times per day, network fees might be spiking. Either:
- Reconsider `KEEPER_MAX_FEE_CAP_GWEI` (raise if economically OK).
- Hold the keeper off-air until fees normalize.
- Or accept that the keeper queue is backed up and gas spend is
  capped.

### F-04 stalled auction dashboard

An admin dashboard for inspecting auctions that require manual attention
is served at two endpoints:

**Admin page (HTMX/Alpine):** `GET /admin/stalled`
- Renders an Alpine.js dashboard with count cards, a breakdown bar chart,
  and a detail table of stalled auction rows.
- Fetches data client-side from the API endpoint below.
- Auto-refreshes every 30 seconds.
- Requires a connected wallet with admin privileges (401/403 errors are
  surfaced inline).

**API (JSON):** `GET /api/v1/admin/auctions/stalled?limit=N`
- JWT + `IsAdmin` gated. Returns both summary counts and the full row list.
- Default limit 100, max 200.

Response shape:
```json
{
  "counts": {
    "expiredUnsettled": 5,
    "inactiveNoBids": 2,
    "unrefunded": 12
  },
  "rows": [
    {
      "auction_id": 142,
      "collection": "0xabc…def",
      "token_id": "12345",
      "seller": "0x…",
      "status": "active",
      "stall_reason": "expired_unsettled",
      "ends_at": "2026-06-28T12:00:00Z",
      "starts_at": "2026-06-27T12:00:00Z",
      "highest_bid_wei": "5000000000000000000",
      "highest_bidder": "0x…",
      "reserve_price_wei": "1000000000000000000",
      "create_tx": "0x…"
    }
  ]
}
```

`stall_reason` is one of:
- `expired_unsettled` — status='active' and ends_at < now() (keeper hasn't settled)
- `inactive_no_bids` — status='active', no leader, started >30 min ago (should be cancelled)
- `unrefunded` — settled/cancelled, losers_refunded=false, not attempted in last 2 min

**Covering indexes (migration 019):**
- `idx_auctions_inactive_no_bids` on `auctions(starts_at)` WHERE `status='active' AND highest_bidder IS NULL`
- The expired_unsettled query is covered by the existing `idx_auctions_active (ends_at) WHERE status='active'` from migration 002.
- The unrefunded branch uses the existing `idx_auctions_refund_pending (auction_id) WHERE status IN ('settled','cancelled') AND NOT losers_refunded` from migration 010.

### F-05 GraphQL query endpoint

A read-only GraphQL endpoint exposes the full data model (collections, listings,
auctions, offers, tokens, profiles, activity, metrics, trending, search) through
a single `POST /graphql` query endpoint. The schema is auto-loaded at startup
from `backend/internal/graphql/schema.graphql`.

**Endpoint:** `POST /graphql`
- Accepts `{"query": "...", "variables": {...}}` JSON body.
- Read-only (only `query` operations accepted; mutations/subscriptions rejected).
- No authentication required — access control mirrors the REST API (public data).
- Playground at `GET /graphiql` (serves the GraphiQL IDE for interactive exploration).
- Schema documentation at `GET /graphql` (returns a rendered docs page).

**Available top-level queries (26 total):**

| Category | Queries |
|----------|---------|
| Collections | `collection(address)`, `collections(limit)`, `collectionStats(collection)`, `traitValues(collection)`, `countCollections` |
| Listings | `listing(collection, tokenID)`, `listings(collection, seller, sort, limit, minPrice, maxPrice, traits)`, `countActiveListings` |
| Auctions | `auction(id)`, `auctions(collection, seller, status, limit, minPrice, maxPrice)`, `countActiveAuctions` |
| Offers | `offers(collection, tokenID, bidder, owner, status, limit)`, `offerPositions(collection, tokenID)` |
| Tokens | `tokenMeta(collection, tokenID)`, `tokenFullMetadata(collection, tokenID)`, `tokenAttributes(collection, tokenID)`, `tokenActivity(collection, tokenID, limit)` |
| Activity | `activity(limit, address, collection, tokenID)` |
| Profiles | `profile(address)`, `notifications(address, limit)`, `savedSearches(address, page, limit)` |
| Wallet | `walletNFTs(owner)` |
| Search | `search(query, limit)` |
| Metrics | `metrics`, `trending(window, limit)`, `totalVolume24h` |

**Nested sub-queries (computed):**
- `Collection.stats`, `Collection.floorPrice`, `Collection.volume24h`, `Collection.listedCount` — resolved via `GetCollectionStats`
- `Collection.listings(limit, sort)` — resolved via `ListActiveListings`
- `Collection.auctions(limit, status)` — resolved via `ListAuctions`
- `Auction.bids` — resolved via `GetBidsForAuction`
- `Auction.effectiveBids` — resolved via `GetEffectiveBids`

**Example query:**
```graphql
{
  collection(address: "0xabc…") {
    name
    stats { floorPriceWei listedCount }
    listings(limit: 5, sort: "price_asc") {
      tokenID priceWei imageURI
    }
  }
  metrics {
    totalActiveListings totalSales grossVolumeWei
  }
}
```

**Monitoring considerations:**
- The executor walks selection sets dynamically — deep or recursive queries are
  bounded by the schema (no cycles in `Collection → listings → collection`).
- No query cost analysis is applied; a single query that requests all nested
  sub-fields on a large result set may trigger N+1 DB queries (e.g.
  `auctions { bids { ... } }` on a listing page). Monitor slow query
  log for unexpected patterns.
- The GraphiQL playground (`GET /graphiql`) is served from `backend/internal/graphql/handler.go`
  and renders the GraphiQL HTML.

### F-06 WebSocket bidirectional endpoint

The SSE `/events` endpoint is now complemented by a bidirectional WebSocket
endpoint at `/ws` that supports client-to-server actions, channel-based
subscriptions, and server-to-client push over the same connection.

**Endpoint:** `wss://magicwebb.fly.dev/ws` (or `ws://localhost:8080/ws` locally)
- Upgrades HTTP to WebSocket via `fasthttp/websocket`.
- Authenticated via JWT cookie (`mw_s_<addr>` prefix) or `Authorization: Bearer <jwt>` header.
- Unauthenticated connections are accepted but limited to public data actions.

**Message types (client → server):**

| Type | Direction | Payload | Description |
|------|-----------|---------|-------------|
| `ping` | client → server | `{}` | Keepalive; server responds with `pong` |
| `action` | client → server | `{"action":"get_listing","params":{"collection":"0x…","token_id":"123"}}` | Lightweight state query (no wallet signature needed) |
| `subscribe` | client → server | `{"channels":["token:0xabc:123","collection:0xabc"]}` | Subscribe to event channels for targeted push filtering |
| `unsubscribe` | client → server | `{"channels":["token:0xabc:123"]}` | Unsubscribe from event channels |

**Message types (server → client):**

| Type | Direction | Payload | Description |
|------|-----------|---------|-------------|
| `ack` | server → client | `{"status":"ok","message":"connected"}` | Confirmation of connection or action receipt |
| `pong` | server → client | `{"server_time_ms": …}` | Response to client ping |
| `error` | server → client | `{"status":"error","message":"…"}` | Error response (malformed message, unknown action, DB error) |
| `state` | server → client | `{collection, token_id, name, …}` | Result of an `action` query |
| `subscribed` / `unsubscribed` | server → client | `{"channels":[…]}` | Confirmation of subscription changes |
| Event types (`listing-updated`, `auction-updated`, `offer-updated`, `notification`, `activity`) | server → client | Event-specific JSON | SSE events bridged to WS; clients without active subscriptions receive all events |

**Available actions (`action` → `state`):**
- `get_listing` — params: `{collection, token_id}` → returns `ListingRow`
- `get_auction` — params: `{auction_id}` → returns `AuctionRow`
- `get_offer` — params: `{offer_id}` → returns `OfferRow`
- `get_token` — params: `{collection, token_id}` → returns full token metadata + attributes

**Connection limits:**
- Per-IP: 20 concurrent connections (returns 429 when exceeded)
- Global: 5,000 concurrent connections (returns 503 when exceeded)
- Read limit: 4 KB per message
- Read deadline: 60 s (extended by pong handler)
- Write deadline: 10 s
- Send buffer: 64 messages (slow clients have messages dropped)

**Monitoring:**
- Active connection count via `ws.Handler.ActiveConns()` — consider exposing as a
  Prometheus gauge or logging on tick if >80% of the 5,000 cap is reached.
- Per-IP connection churn: if a single IP rapidly opens/closes connections it
  may indicate a misconfigured client or a DoS attempt.
- SSE bridge filter drops: when a client subscribes to specific channels and the
  SSE bridge goroutine filters out an event, no log is emitted — the client
  simply doesn't receive it. If expected events aren't arriving, verify the
  subscription channel names match the SSE event types.

### F-07 Self-hosted image store (imagestore) and quota enforcement

NFT images and metadata JSON are self-hosted in Postgres BYTEA columns
(catalogued in `nft_image_blobs`) keyed by SHA-256 hash, so the frontend
serves all media from the same origin (`/api/v1/img/<sha256>`) instead of
proxying external IPFS/HTTP gateways on every page load.

**Key constants (hard-coded in `backend/internal/imagestore/imagestore.go`):**

| Constant | Value | Purpose |
|----------|-------|---------|
| `MaxBlobBytes` | 8 MiB | Rejects individual blobs larger than this before INSERT |
| `MaxBlobCountPerCollection` | 1,000 | Caps distinct blobs per collection (dedup hits bypass) |
| `MaxTotalBlobBytes` | 256 MiB | Caps cumulative blob volume across all collections |

When a quota cap is exceeded, `Put()` returns `Skipped=true` and the caller falls
back to proxying the upstream URL (the frontend still renders the image, just not
from the local store). The image retry worker retries periodically.

**Endpoints:**
- `GET /api/v1/img/<sha256>` — serves a blob by hash (Content-Type from sniff, `Cache-Control: immutable`)
- `GET /api/v1/media?url=…` — proxy fallback for pre-ingest / un-stored URIs
- `POST /api/v1/img/retry?coll=0x…&id=<token_id>` — user-triggered retry (rate-limited: 10 req/min per IP)

**Image retry workers:**
- **Slow-path worker:** runs every 60 min, retries up to 50 tokens per tick where
  `image_uri` is still an upstream URL. Found in `indexer/runImageRetryWorker()`.
- **Metadata worker:** runs every 30 s, fetches missing token metadata + images
  with semaphore-bounded parallelism (`METADATA_CONCURRENCY` env var, default 3).

**Relevant migrations:**
- `013_image_blobs.sql` — creates `nft_image_blobs` table (BYTEA + mime + refcount)
- `014_image_retry_backoff.sql` — adds retry columns to `nft_metadata`
- `015_image_retry_recompute.sql` — computes initial retry candidates
- `017_listings_price_expr_index.sql` — expression index for listings price queries
- `018_image_blobs_collection.sql` — adds `collection` column for per-collection quota

**Monitoring: quota-exceeded log signatures:**
```
log.Warn().Bool("skipped",true).Msg("metadata: self-host meta body rejected or quota exceeded")
log.Warn().Msg("image-retry: quota exceeded, will retry later")
log.Warn().Int("count",n).Msg("image-retry: attempting self-host for upstream image URIs")
```

If these logs appear in production:
1. Check total blob volume: `SELECT sum(byte_length) FROM nft_image_blobs`
2. Check per-collection blob counts: `SELECT collection, count(*) FROM nft_image_blobs GROUP BY collection ORDER BY count DESC`
3. If `MaxTotalBlobBytes` (256 MiB) is regularly hit, raise the cap in `imagestore.go`
   and deploy. The cap exists to protect the Postgres free-tier limit — for
   paid/self-hosted Postgres it can safely be increased.

### F-08 Astro frontend app (SSR + client-side hydration)

The Astro frontend (`app/`) is a separate build target that produces static HTML
+ client-side JS bundles served from `app/dist/`. It coexists with the Go HTMX
backend via the `mountAstro()` middleware in `cmd/server/ui.go`.

**Architecture:**
- Astro pages are built with `astro build` → output to `app/dist/`
- Go middleware serves Astro-built pages at their URL paths (e.g. `/listings`)
  and falls through to Go HTMX handlers when no Astro page matches
- Paths like `/api/`, `/auth/`, `/static/`, `/events`, `/healthz` bypass the
  Astro middleware entirely
- Dynamic routes (`/token/:addr/:id`, `/auction/:id`, `/collection/:addr`,
  `/profile/:addr`) use catch-all index.html pages where client-side JS parses
  params from `window.location.pathname`

**Key files:**
- `app/astro.config.mjs` — Astro config with Svelte integration
- `app/src/layouts/BaseLayout.astro` — shared page layout
- `app/src/pages/` — Astro page components per route
- `app/src/components/` — Svelte components (NFT cards, wallet connect)
- `app/src/appkit-bridge.js` — Reown AppKit bridge for WalletConnect
- `app/vite.bridge.config.mjs` — Vite config for bundling the AppKit bridge

**Data store:**
- `app/.astro/data-store.json` — Astro content layer data store (auto-generated)
- `app/.astro/settings.json` — Astro content layer settings

**Monitoring considerations:**
- The Astro middleware uses `os.Stat` on every request to check for file
  existence. Under high load, this can generate significant filesystem I/O.
  If latency spikes are observed, consider a startup-time in-memory map of
  known page paths to reduce per-request stat calls.
- Cache headers: HTML pages get 5-min `max-age=300`; hashed Vite assets
  (JS/CSS) get 1-year `max-age=31536000, immutable`.
- The `ASTRO_DIST_DIR` env var (default `../app/dist`) must point to a valid
  build output directory. A missing or empty dist dir silently passes all
  requests through to Go HTMX handlers — no 500, just missing Astro pages.
- Svelte components use client-side hydration; if the JS bundle fails to load
  (CSP block, network error), the Svelte components render as empty divs with
  no fallback. Monitor browser console error rates for `appkit-bridge.js` and
  Svelte chunk load failures.
- The Astro dev server (port 4321) proxies to Go on :8080 in development.
  Production uses pre-built static files only — no Node process required.

### F-09 Runbook: image quota exceeded alerts

When imagestore quota caps are hit, images fall back to proxying from upstream
URLs — the frontend still works but without the latency/privacy benefits of
self-hosting. Persistent quota exhaustion means the blob store needs attention.

**Alert signal:** Log patterns matching:
- `log.Warn().Str("coll",…).Str("token",…).Msg("image-retry: quota exceeded, will retry later")`
- `log.Warn().Bool("skipped",true).Msg("metadata: self-host meta body rejected or quota exceeded")`

**Investigation steps:**
1. Check per-collection blob counts:
   ```sql
   SELECT collection, count(*) AS blobs, sum(byte_length) AS total_bytes
   FROM nft_image_blobs
   GROUP BY collection
   ORDER BY blobs DESC;
   ```
2. Check total blob volume:
   ```sql
   SELECT count(*) AS total_blobs, sum(byte_length) AS total_bytes FROM nft_image_blobs;
   ```
3. Identify the collection(s) hitting `MaxBlobCountPerCollection` (1,000):
   any collection with `count(*) >= 1000` is throttled.
4. Check if `MaxTotalBlobBytes` (256 MiB) is the bottleneck:
   `sum(byte_length) > 256 * 1024 * 1024` means the global cap is hit.

**Resolution options:**
- **Short-term:** Increase `MaxTotalBlobBytes` in `imagestore/imagestore.go` and
  redeploy. For paid/self-hosted Postgres instances, 1 GiB or more is safe.
- **Short-term:** Increase `MaxBlobCountPerCollection` (currently 1,000). Most
  collections have far fewer distinct image hashes; if a single collection has
  genuinely exceeded 1,000 unique images, the cap may be too conservative.
- **Long-term:** Consider offloading blob storage to object storage (S3/R2) with
  Postgres as the metadata catalog. The current BYTEA-based store is designed
  for the Postgres free tier (512 MB); at scale, a dedicated blob store with
  CDN fronting is more cost-effective.
- **Monitoring:** Add a periodic health check that queries `count(*)` and
  `sum(byte_length)` from `nft_image_blobs` and emits a warning metric when
  either approaches 80% of its cap.

## === Keepalive: tests + audits replay nightly ===

A nightly cron in Fly-sidekick runs (advised):

```bash
0 4 * * * cd /app && go test ./internal/... 2>&1 | tee nightly-test.log
```

And a weekly auditor-side rerun (per external tester):

```bash
cd contracts && forge test --match-path test/AuditFuzz.t.sol -vv
slither . --filter-paths 'lib/|test/'
```

Any regression fails the cron -> emails on-call.

---

## Appendix — Full event signature cheat-sheet

```text
// Marketplace (fixed-price listings)
Listed(    address indexed coll, uint256 indexed id, address indexed seller, TokenStandard standard, uint128 amount, uint128 price, uint64 expiresAt)
Bought(   address indexed coll, uint256 indexed id, address indexed buyer, address seller, TokenStandard standard, uint128 amount, uint128 price, uint256 fee)
Cancelled(address indexed coll, uint256 indexed id, address indexed seller)

// AuctionHouse (English auctions)
AuctionCreated(       uint256 indexed id, address indexed coll, uint256 indexed tokenId, address seller, TokenStandard standard, uint128 amount, uint128 reserve, uint64 startsAt, uint64 endsAt)
BidPlaced(            uint256 indexed id, address indexed bidder, uint256 amount, uint256 newTotal)
OutbidNotification(   uint256 indexed id, address indexed outbid, uint256 newLeaderTotal)
AuctionExtended(      uint256 indexed id, uint64 newEndsAt)
AuctionSettled(       uint256 indexed id, address indexed winner, address indexed seller, uint128 winningBid, uint256 fee)
LoserRefunded(        uint256 indexed id, address indexed bidder, uint256 amount)
AuctionCancelled(     uint256 indexed id)
RefundPushed(         address indexed bidder, uint256 amount)
AuctionStalled(       uint256 indexed id, address indexed winner, address indexed seller)
AuctionReclaimed(     uint256 indexed id, address indexed winner, uint256 refundAmount)

// OfferBook (stacked offers)
OfferMade(     address indexed coll, uint256 indexed tokenId, address indexed bidder, uint256 principal, uint128 units, uint64 expiresAt)
OfferAccepted( address indexed coll, uint256 indexed tokenId, address indexed seller, address bidder, uint256 principal, uint256 fee, uint128 units, TokenStandard standard)
OfferRefunded( address indexed coll, uint256 indexed tokenId, address indexed bidder, uint256 principal)

// MarketplaceCore (shared base)
PushFailed(address indexed to, uint256 amount)

// MarketplaceManager (role registry, circuit breaker)
EntriesPaused(   address indexed by)
EntriesUnpaused( address indexed by)
ModuleSet(       bytes32 indexed slot, address indexed addr)
AuditLog(        bytes32 indexed action, address indexed actor, address indexed subject, bytes32 extra)
```

Indices are designed so an off-chain indexer can join cheaply with
Postgres WITHOUT recomputing ABI fields — see
`backend/internal/db/migrations/002_indexes.sql`.

