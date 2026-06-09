# Performance & Security Audit — 2026-06-09

Scope: full codebase (contracts/, backend/) at commit `1a45225`, prior to production-hardening feature work.
Baseline: 94/94 forge tests pass; all Go packages pass (`go test ./...`).

## Scores (1–10)

| Dimension   | Score | Evidence |
|-------------|-------|----------|
| Speed       | 8     | Partial indexes on every hot path (`002_indexes.sql`, `007_effective_bids.sql`); server-rendered HTMX (no hydration cost); 2 s block poll. Drag: trending score worker issues 3 queries × 500 collections × 3 windows per minute (N+1); `HeaderByNumber` re-fetched per block in watcher. |
| Scalability | 8     | Stateless across instances: Postgres-backed rate limiter, SIWE nonce store, SSE via LISTEN/NOTIFY (`92f1d45`, `26de809`); PgBouncer transaction-pooler-safe pgx mode (`f406e5a`). Drag: indexer + keeper run in **every** instance — no leader election; two instances with `KEEPER_KEY` double-broadcast settle txs (wasted gas, nonce races). |
| Reliability | 7     | Idempotent on-chain refunds (zeroed escrow skipped); sweeper retries next tick on partial failure; DB upserts idempotent. Drag: single RPC endpoint is a hard SPOF (`main.go:63`); no graceful shutdown (SIGTERM kills in-flight settle broadcasts and SSE streams). |
| Latency     | 9     | SSE push on every indexed event; 2 s head poll ≈ Coston2 block time; 3 min anti-snipe extension absorbs confirmation jitter. |
| Uptime      | 6     | `/healthz` + DB-checked readiness exist (`rest.go:34`). Drag: no signal handling → ungraceful restarts; no keeper redundancy story (see Scalability); RPC SPOF compounds. |
| Security    | 9     | Contracts: CEI + `nonReentrant` everywhere, pull-payment fallback (`pendingReturns`), winner escrow consumed before transfers, bounded refund batches, immutable fee/recipient, no admin keys on escrow paths. App: SIWE domain binding, single-use Postgres nonces, JWT ≥32-byte secret enforced, per-IP rate limits, RLS enabled. Gap: 7 tables from `006_rework.sql` lack RLS (`nft_ownership`, `tracked_collections`, `nft_metadata`, `nft_attributes`, `notifications`, `profiles`, `reports`). |
| Gas         | 9     | Custom errors throughout (no require strings); packed structs (Listing = 2 slots, documented); `uint128` cumulative escrow; batched `refundLosers` with per-call gas bound; `batchList` ≤50. No dedicated GasOptimizer library needed — remaining wins are <1 % (e.g. `_bidders` push could be event-reconstructed, but keeper enumeration depends on it). |

## Prioritized fix list

**P0 — before any new feature code**
1. Wire `rpcpool.Pool` into `cmd/server/main.go` + `indexer.Runner` (replace single `*ethclient.Client`). Pool exists with tests; only injection missing. Fixes RPC SPOF, adds <3 s failover + round-robin headroom.
2. Migration `011_rls_rework.sql`: enable RLS + policies on the 7 uncovered tables.

**P1**
3. Graceful shutdown: trap SIGINT/SIGTERM → `app.Shutdown()` + ctx cancel → indexer drains.
4. Keeper single-flight guard: Postgres advisory lock so only one instance broadcasts keeper txs (multi-instance safe without leader-election infra).

**P2**
5. Trending score worker: collapse N+1 into one grouped query per window.
6. Watcher: reuse header timestamps across `processRange`/`processTransfers` (already partially shared via `blockTimes`).

**P3 — feature work (this branch)**
7. `MarketplaceManager` + token integration hooks (see ARCHITECTURE notes in TOKEN_HOOKS.md).

## Spec-compliance snapshot (pasted session spec vs shipped code)

Already shipped, verified by reading source — not reimplemented:
- Cumulative bidding (per-bid records + `effective_bids` view + on-chain `cumulative` mapping) — `AuctionHouse.sol:72`, `007_effective_bids.sql`.
- No auto-refund on outbid + `OutbidNotification` event + SSE push — `AuctionHouse.sol:207`, indexer `baf2f82`.
- Keeper auto-settlement + autonomous loser refunds + expired-offer refunds — `runner.go`, `keeper_refund.go` (`1a45225`).
- Fee: immutable 1.5 % seller-pays at settlement — `MarketplaceCore.sol:23`.
- Listing/offer fund distribution is atomic with the sale action (`buy`/`acceptOffer`) — no separate user-triggered release exists.

Stack note: the live stack is **Go Fiber + HTMX + Foundry** (migration completed 2026-05-25), not Next.js/Wagmi/Hardhat/gRPC. Spec items tied to the old stack (Wagmi RPC config, gRPC services) map to their equivalents here: RPC rotation lands in the Go backend only; the HTMX frontend talks only to the backend, never to an RPC directly — so backend rotation covers 100 % of RPC traffic.

## Immutable vs upgradeable — decision record

Spec asked for tradeoffs before implementing MarketplaceManager:

| Option | Funds-trapped risk | Audit posture | Admin-key risk |
|--------|--------------------|---------------|----------------|
| (a) UUPS/Transparent proxies | High — upgrade key can change escrow logic | Invalidates June hardening audit (98-test baseline) | Highest |
| (b) Immutable cores + Manager as role/registry layer; pause gates **entries only** | None — `settle`/`refundLosers`/`withdrawRefund`/`refundExpiredOffer` are never pausable | Core escrow logic byte-identical to audited pattern | Limited to halting *new* activity |
| (c) Fully immutable, no manager | None | Unchanged | None — but no circuit breaker, no role layer for token hooks |

**Chosen: (b).** Cores stay immutable and permissionless on every exit path ("pausable entries, unstoppable exits"). Manager provides AccessControl roles, atomic entry-pause, address registry, audit events, and `setTokenAddress` hook. Coston2 redeploy was already required for the seller-pays model, so the redeploy cost is sunk. Reversible before mainnet (gated on external audit + multisig regardless).
