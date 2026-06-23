# Changelog

All notable changes to MagicWebb — contracts, backend, frontend,
docs — are tracked here. Versions follow the audit ledger cadence
(`v19` = wallet.js audit, `v20` = Solidity audit, `v21` = indexer +
DB + API + docs).

## v21 — 2026-06-22 — Priority Stack unlock

This release closes every item in the audit Priority Stack
(`docs/AUDIT.md`) and ships the seed-testnet harness for live
verification. Backend compile + vet + pg-less unit tests pass;
live site https://magicwebb.fly.dev returns 200 OK across every
public route and `/events` SSE remains streaming-clean.

### Fixed (Priority Stack — all items now FIXED)

- 🔴 **P0 `onTransferBatch`** — indexer no longer OOMs on hostile
  TransferBatch logs. Bound `idsOff`/`valsOff` by data footprint
  AND `maxBatchLength = 1024` BEFORE the inner loop; cross-validate
  `idsLen == valsLen` and array-extends-past-boundary.
  Anchor: `backend/internal/indexer/handlers.go::maxBatchLength`.
- 🟠 **P1 `processTransfersWallClock`** — processTransfers no
  longer poisons rows with `time.Now().Unix()` when the core-event
  FilterLogs didn't return a log for that block. Per-block header
  fetch with 2 s context timeout; log+skip on failure; memoize so
  the next Transfer log in the same chunk reuses the cached
  timestamp without an extra RPC.
- 🟠 **P1 `getRecentTxnsLimit`** — LIMIT pushed into each
  UNION ALL branch via parens so Postgres can honour per-branch
  indexed scans; outer wrapper caps the merge.
- 🟠 **P1 `getEffectiveBidsLimit`** — hard-cap at `LIMIT 200` so
  contested 10k-bid auctions no longer OOM the page renderer.
- 🟠 **P1 `clientIpSpoof`** — `clientIP` trusts `Fly-Client-IP`
  first (mathematically unspoofable from outside), then RFC 7239
  `Forwarded` (with `stripAddrPort` for bracketed IPv6 + port
  stripping), then `X-Forwarded-For` rightmost, then fasthttp
  `c.IP()`. Fiber config: `EnableTrustedProxyCheck: false` +
  `ProxyHeader: "Fly-Client-IP"`.
- 🟡 **P2 `parseWeiHelper`** — central `ParseWei(s)` +
  `ParseWeiOrZero(s)` helper. Five prior `big.Int.SetString`
  sites rewritten to route through it; malformed input is now a
  WARN log instead of a silent `0`.

### SSE belt-and-braces

- `sse/cancel` scope moved from handler-scope (which fired
  prematurely) into the writer callback; an additional
  sync.Once-guarded `vctx.Done()` goroutine ensures
  cancel-on-early-disconnect even when SetBodyStreamWriter's
  callback never runs.
- Sentinel `: connected\n\n` first-byte flush so fasthttp commits
  response headers + first chunk in the same TCP write.

### Documentation

- `docs/AUDIT.md` — v21 section awaits priority stack unlock with
  verification evidence; feature-flow appendix A–K added.
- `docs/USER_GUIDE.md` — full user-flow walkthrough A–P, what-to-
  do-when table, event-to-feature map, live test matrix.
- `tools/seed-testnet/` — new Go CLI harness (`main.go` + README)
  with `--dry-run`, `--seed-*` per-feature-count flags,
  `--teardown` flag, audit-grade `seeded_by` row tagging.

### Tooling

- `tools/seed-testnet` ships a `go.mod` and uses `crypto.Keccak256`
  (EVM-canonical) for calldata selectors. ABI packing via
  `accounts/abi` for `setApprovalForAll`, `list`,
  `create` calls.
- `tools/seed-testnet/main.go` synthesises per-run addresses via
  HD derivation from a SHA256-seed so dry-runs produce stable
  keys for QA reproduction.

## v20 — contracts (handover from prior release)

The audit ledger's v20 row is unchanged here — those fixes
(C-01 anti-snipe, C-02 stalled-state recovery, C-03 offer withdraw
fallback, C-04 refundLosers gas-bounds) are all FIXED via the
existing AuditFuzz harnesses under `contracts/test/`. See
`docs/AUDIT.md` for the per-row evidence.

## v19 — wallet.js (handover from prior release)

The v19 rows F-01/F-02/F-03 are landed in
`backend/internal/ui/static/wallet.js`: chainChanged /
accountsChanged listeners on both eip1193 kinds, SIWE
typed-error path, no silent auto-reconnect. Manual live
verification on https://magicwebb.fly.dev confirms the fixes.
