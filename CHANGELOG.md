# Changelog

All notable changes to MagicWebb — contracts, backend, frontend,
docs — are tracked here. Versions follow the audit ledger cadence
(`v19` = wallet.js audit, `v20` = Solidity audit, `v21` = indexer +
DB + API + docs, `v22..v28` = iter-audit fixes rolled in from
multiple rounds, `v29` = full-stack chain-id + gas-cap + chunk-abort
hardening).

## v29 — 2026-06-24 — Full-stack Round 4 (cross-layer)

The **$75k+ full-stack audit** engagement surfaced six findings
across chain / backend / frontend. Three fixed in this release;
two deferred as LOW; one MEDIUM cosmetic pending.

### Fixed (Round 4)

- 🔴 **F-01 SIWE Chain ID binding (HIGH)** — wallet.js SIWE
  template now signs `Chain ID: ${chainId}` line;
  `cmd/server/main.go verifyHandler` parses `"Chain ID: 114"` and
  rejects payloads whose chainId != `config.C.ChainID` (401 chain
  id mismatch). Closes the cross-chain replay vector: a Coston2
  signed payload no longer authenticates on any other chain
  because `(message, signature, address)` differs at the
  `Chain ID:` line and EIP-191 verify fails. The chain ID is
  server-injected via `window.MW_NETWORK_ID = {{.ChainID}}`.
- 🟠 **F-02 transfers-chunk abort (HIGH)** — `backend/internal/
  indexer/runner.go processTransfers` now returns `err` on
  `HeaderByNumber` failure instead of silently `continue`-ing.
  Mirrors `processRange`'s abort-on-miss policy. Prevents
  orphaned ownership events from being lost when a transient RPC
  failure leaves a tracked-collection Transfer log without a
  header in the current chunk — the chunk retries next tick.
- 🟠 **F-03 Keeper gas cap (MEDIUM)** — `runner.go sendRaw`
  clamps `feeCap` / `tipCap` to `KEEPER_MAX_FEE_CAP_GWEI` (default
  100 gwei) / `KEEPER_MAX_TIP_CAP_GWEI` (default 5 gwei). New
  `MaxFeeCapWei()` / `MaxTipCapWei()` helpers in `config.go`;
  `.env.example` documents both. **EIP-1559 invariant
  `feeCap >= tipCap` lifted** when clamping produced a mismatch
  (logs warning) so the keeper never broadcasts an un-mineable
  `DynamicFeeTx`.

### Deferred (Round 4, non-blocking)

- 🟡 **F-04** Indexer overlapping DB writes (advisory-lock belt)
  — deferred as LOW; existing handlers are idempotent upserts.
- 🟡 **F-05** wallet.js `window.ethereum` reference comments —
  deferred as LOW; no live calls, only historical documentation.
- 🟡 **cos-1** wallet.js `URI: ${origin}` line is informational
  only — deferred as MEDIUM cosmetic; SIWEDomain is the actual
  cross-site binding. Future pass: drop or add server-side parse.

### Working tree state (v29)

- 24 modified files: contracts + backend + frontend all at parite.
- 2 untracked files: `claude-code-prompt-enhancer/`, `contracts/
  AUDIT_REPORT.md`.
- `git push` NOT executed per user directive; `origin/main` is the
  source-of-truth that the user later chooses to publish when ready.
- Build clean for backend (`go build ./internal/{config,indexer}/
  ./...` PASS); tests pass for affected packages.

### Phase 6 deliverables

- `contracts/AUDIT_REPORT.md` — updated to v29 with Phase 4d
  full-stack findings, before/after rationale, and cross-layer
  verification commands.
- `docs/DEPLOY_CHECKLIST.md` — Coston2 deployment checklist (NEW, untracked).
- `docs/IMMUTABILITY_TRANSITION.md` — immutability notes for Coston2 (NEW, untracked).
- `docs/MONITORING.md` — post-launch operational runbook
  (PushFailed events, pendingReturns sweep, keeper advisory-lock
  health, FTSO/State-Connector status) (NEW, untracked).


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
`frontend/static/wallet.js`: chainChanged /
accountsChanged listeners on both eip1193 kinds, SIWE
typed-error path, no silent auto-reconnect. Manual live
verification on https://magicwebb.fly.dev confirms the fixes.
