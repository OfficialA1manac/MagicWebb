# Code Review: Production Hardening (chore/production-hardening)

**Reviewed:** 2026-06-09
**Depth:** deep (cross-file: rpcpool → indexer.Runner → cmd/server; MarketplaceManager gating ↔ core entry/exit paths)
**Scope:** commits since `1a45225`
**Status:** issues_found
**Findings:** 4 Critical / 5 Warning / 7 Info

## Summary

The contract-side invariant — "pausable entries, unstoppable exits" — is correctly implemented: `entryGate` sits only on `list/list1155/batchList/buy`, `create/create1155/bid`, `makeOffer/makeOffer1155/acceptOffer`; every exit path (`settle`, `refundLosers`, `withdrawRefund`, `cancel`, `cancelEarly`, `rejectOffer`, `refundExpiredOffer`) is verified ungated. `entriesAllowed()` is a `view` interface call (compiled to STATICCALL), so the external call in `entryGate` cannot mutate state and the `nonReentrant`/`entryGate` ordering is safe. The serious problems are on the Go side: the keeper single-flight lock is unsound when `POSTGRES_URL` points at the Supabase transaction pooler (the codebase explicitly supports :6543), the rpcpool's round-robin breaks the watcher's assumption of a single consistent chain view (silent event gaps), and the new `isAlreadyBroadcast` heuristic converts never-broadcast keeper transactions into reported successes that the loser-refund sweeper then marks permanently complete in the DB.

---

## Critical Issues

### CR-01: Watcher advances its cursor past failed log ranges — permanent, unhealable event gaps

**File:** `backend/internal/indexer/runner.go:153-157`, `:189-191`, `:210-212`

**Issue:** In `runWatcher`, `processRange` swallows `FilterLogs` errors (logs and returns), yet the caller unconditionally executes `lastBlock = newHead` (line 157). The failed range is never retried in-process. Worse, the *next successful* `processRange` calls `SetIndexedBlock(chainID, to)` with a later block (line 210), so the persisted cursor also jumps past the gap — a restart does **not** heal it. Missed `AuctionSettled` / `OfferAccepted` / `LoserRefunded` events permanently corrupt the keeper's DB view (`GetSettledUnrefundedAuctions`, `GetRefundableExpiredOffers`), silently breaking autonomous settlement/refunds. Additionally, live catch-up does not chunk: after any outage longer than `GETLOGS_BLOCK_CAP` blocks, the single un-chunked `FilterLogs(lastBlock+1, newHead)` is rejected by public-RPC caps *every tick*, and the swallow-then-advance bug drops the whole gap. (The advance-on-error existed before this branch, but the file is in scope and the rpcpool rework is the right moment to fix it.)

**Fix:**
```go
// processRange returns error; runWatcher only advances on success and
// reuses backfill() (chunked) for catch-up:
if err := r.backfill(ctx, lastBlock+1, newHead, contracts, topics, chainID); err == nil {
    lastBlock = newHead
} // else: keep lastBlock, retry the same range next tick
```
Make `processRange`/`backfill` return the first error, and never call `SetIndexedBlock` for ranges after a failed one.

### CR-02: Round-robin splits head discovery and log fetching across unsynchronized endpoints — silent empty results

**File:** `backend/internal/rpcpool/pool.go:77-95` (cross-file with `backend/internal/indexer/runner.go:148-160`, `:183-188`)

**Issue:** `runWatcher` gets `newHead` from `eth.BlockNumber` (served by endpoint A) and then calls `eth.FilterLogs(lastBlock+1, newHead)` (served by endpoint B, one cursor increment later). Public RPCs are routinely 1–3 blocks apart. `eth_getLogs` with a `toBlock` above a node's local head is **not an error** on most clients — it returns whatever logs the node has (often none for the tip blocks). The pool reports success, the watcher advances `lastBlock` (and via CR-01, the DB cursor), and events in the skew window are silently and permanently lost. This failure mode is *introduced* by this branch: with the previous single `ethclient.Client`, head and logs always came from one consistent view. `HeaderByNumber` for `serverTimeMs`/block-times has the same skew problem (can error or return nil header for a block the serving node hasn't seen).

**Fix:** Pin each watcher iteration to one endpoint (e.g., add `p.Pinned(ctx, fn)` that runs `BlockNumber` + `FilterLogs` + `HeaderByNumber` against the same node, failing over the whole iteration), or conservatively index only to `newHead - K` (K = 2–3 confirmation blocks), which also adds reorg tolerance.

### CR-03: `isAlreadyBroadcast` converts never-broadcast txs into successes; sweeper then marks refunds complete on unconfirmed broadcasts

**File:** `backend/internal/rpcpool/pool.go:143-159` (cross-file with `backend/internal/indexer/runner.go:385-416`, `:470-495`; `backend/internal/indexer/keeper_refund.go:105-131`)

**Issue:** `SendTransaction` treats any error containing `"nonce too low"` as "a prior endpoint already accepted this tx". That is a false-positive class: `"nonce too low"` equally means *a different tx already consumed this nonce* — exactly what happens when `PendingNonceAt` (round-robined to a lagging endpoint that hasn't seen the keeper's pending tx) returns a stale nonce. The new tx was **never broadcast anywhere**, yet `SendTransaction` returns `nil`. Consequences traced through the callers:

1. `sweepLoserRefunds` sets `allSent = true` and calls `MarkLosersRefunded(auctionID)` — the auction is closed out in the DB **forever** and the sweeper never retries, even though `refundLosers` never reached the chain. Losers' escrow sits in the contract until someone manually calls `refundLosers`/`withdrawRefund`, defeating the stated guarantee ("no losing bidder ever has to call a function to get paid"), with a DB state that says refunds happened.
2. Multi-tx ticks (several `sendSettle` calls, or multiple 50-address refund batches back-to-back) each call `PendingNonceAt` fresh against rotating endpoints; two txs can be assigned the same nonce, so batch N+1 silently *replaces* batch N in the mempool while both were logged "tx sent".
3. There is no receipt confirmation anywhere — even a genuinely broadcast tx can be dropped/underpriced/out-of-gas, and `MarkLosersRefunded` still fires.

**Fix:**
- Maintain the keeper nonce locally (fetch once, increment per signed tx, resync on error) instead of `PendingNonceAt` per tx against rotating endpoints — or pin all keeper writes to a sticky endpoint.
- In `SendTransaction`, only short-circuit on `"already known"`/`"already exists"` (same-hash duplicates). On `"nonce too low"`, verify the tx actually exists (e.g., `eth_getTransactionByHash`) before claiming success.
- Gate `MarkLosersRefunded` on a mined receipt (or on observing the `LoserRefunded` events through the indexer), not on broadcast.

### CR-04: Keeper advisory lock is unsound through the transaction pooler, and lock loss is never detected (split-brain)

**File:** `backend/internal/db/keeperlock.go:22-54` (cross-file with `backend/cmd/server/main.go:77-78`, `backend/internal/db/pool.go:56-63`, `backend/internal/indexer/runner.go:89-107`)

**Issue:** Two independent defects in the single-flight guarantee:

1. **Pooler incompatibility.** `WaitKeeperLock` runs `pg_try_advisory_lock` on a connection from the *general* pgxpool. The codebase explicitly supports `POSTGRES_URL` pointing at the Supabase transaction-mode pooler on :6543 (`pool.go:60-63` special-cases it; `SessionDSN` exists precisely because that pooler can't hold session state). Session-level advisory locks through a transaction-mode pooler are broken: the lock attaches to whichever *server* connection the pooler picks for that statement; the later `pg_advisory_unlock` (or the client disconnect) may hit a different server connection, so the lock leaks on an orphaned server conn. After the holder dies, **no instance can ever reacquire the keeper lock** until the pooler recycles that server connection — keepers stop cluster-wide (settles/refunds halt; funds stay claimable on-chain only manually).
2. **No liveness monitoring.** Even on a direct connection, the lock is acquired once and never checked again. If the lock connection dies (network blip, Postgres restart/failover) Postgres releases the lock immediately, another instance acquires it and starts its keepers — while this instance's keeper goroutines keep running until process exit. During that split-brain window both instances broadcast with the same keeper key: nonce races, mutual tx replacement, and (via CR-03) "nonce too low" being misreported as success. The `keeperGate` is consumed exactly once in `Run()` (runner.go:93-99) with no re-check path.

**Fix:**
- Acquire the lock on a dedicated direct/session connection (`pgx.Connect(ctx, db.SessionDSN(...))`), exactly like the SSE bridge does — never through the shared pool.
- Have `WaitKeeperLock` return a `context.Context` derived from the caller's ctx that it cancels when a periodic ping (`SELECT 1` every ~10s) on the lock connection fails; run the three keeper goroutines under that context so they stop the moment lock ownership is no longer provable, then loop back to re-acquire.

---

## Warnings

### WR-01: Graceful shutdown does not actually wait for indexer/keeper drain

**File:** `backend/cmd/server/main.go:104-121`

**Issue:** The comment claims "indexer/keepers drain (no settle broadcast cut mid-flight)", but the code cannot deliver that: the signal goroutine calls `app.ShutdownWithTimeout(10s)` then `cancel()`; `app.Listen` returns as soon as HTTP shutdown completes — racing with (and possibly before) `cancel()` — and `main` returns immediately. Nothing waits for `runner.Run(ctx)` to finish, so the process exits with keeper sign/broadcast calls mid-flight and `defer pool.Close()` tearing the DB out from under in-flight queries. The advisory lock's best-effort unlock in `release()` also never executes (conn close releases it, but only after TCP teardown).

**Fix:**
```go
indexerDone := make(chan struct{})
go func() { defer close(indexerDone); runner.Run(ctx) }()
...
if err := app.Listen(...); err != nil { log.Fatal()... }
cancel()
select {
case <-indexerDone:
case <-time.After(15 * time.Second):
    log.Warn().Msg("indexer drain timed out")
}
```

### WR-02: `MarketplaceCore` accepts any non-zero `manager_` — a wrong address permanently bricks every entry path on immutable contracts

**File:** `contracts/src/MarketplaceCore.sol:44-48`, `:52-57`

**Issue:** `manager` is immutable and unvalidated. If a deployment passes an EOA or typo'd address (anything non-zero without `entriesAllowed()`), every `entryGate` call reverts forever — Solidity's high-level call to a codeless address fails the extcodesize check, and a contract lacking the function reverts on decode. Since the cores are intentionally immutable, the only remedy is a full redeploy (exits still work, so no funds trapped, but the deployment is dead on arrival for new activity). Secondary note on the hostile-manager question: because `entriesAllowed()` is a STATICCALL, a malicious manager can never touch funds or reenter, but it *can* selectively censor entries (branch on `tx.origin`/`gasleft()` inside the view) or burn the gas stipend — DoS only, consistent with the design invariant, but worth documenting that "manager compromise" includes targeted censorship of entries, not just a global pause.

**Fix:**
```solidity
constructor(address recipient, address manager_) {
    if (recipient == address(0)) revert ZeroAddress();
    if (manager_ != address(0)) {
        if (manager_.code.length == 0) revert ZeroAddress(); // or NotContract
        // optional belt-and-braces: require it answers the probe
        IMarketplaceManager(manager_).entriesAllowed();
    }
    feeRecipient = recipient;
    manager      = manager_;
}
```
Deploy order already creates the manager first, so this costs nothing.

### WR-03: Mainnet deploy script does not enforce the multisig requirement and under-asserts the handover

**File:** `contracts/script/DeployFlare.s.sol:18`, `:46-49`, `:62-71` (mirrored in `DeployCoston2.s.sol`)

**Issue:** The header says "CREATOR_ADDR — fee recipient + manager admin (Safe multi-sig REQUIRED on mainnet)" but nothing enforces it — an EOA passes silently, handing `DEFAULT_ADMIN_ROLE` + all fees to a single key on mainnet (this contradicts the project's own mainnet gating on multisig). The post-deploy assertions also miss two things the e2e script has to test for instead: (a) deployer renounced `OPERATOR_ROLE` (only `DEFAULT_ADMIN_ROLE` is asserted at line 70), and (b) when `KEEPER_ADDR` is set, that `KEEPER_ROLE` was actually granted. The handover *ordering* itself is correct (grants to creator precede deployer renounces, so a mid-sequence failure can never strand the manager admin-less).

**Fix:** In `DeployFlare.s.sol` add:
```solidity
require(creator.code.length > 0, "CREATOR_ADDR must be a multisig contract on mainnet");
...
require(!manager.hasRole(manager.OPERATOR_ROLE(), deployer), "deployer must have renounced operator");
if (keeper != address(0)) require(manager.hasRole(manager.KEEPER_ROLE(), keeper), "keeper role missing");
```

### WR-04: `notifications_self_update` grants full-row UPDATE; RLS policies depend on unverified JWT claim shape

**File:** `backend/internal/db/migrations/011_rls_rework.sql:28-30` (and `:26-43` generally)

**Issue:** The update policy lets an authenticated user rewrite *every column* of their notification rows (message text, type, links, timestamps — whatever the schema holds), not just a read/seen flag; RLS constrains *which rows*, not *which columns*. Forged notification content rendered to the same user is a stored-XSS/social-engineering vector if the UI trusts those fields. Separately, all six self-policies compare against `request.jwt.claims->>'sub'` verbatim: if the JWT `sub` carries an EIP-55 checksummed address while rows store lowercase (the Go backend lowercases everywhere), every policy silently fails closed; and these policies are only live at all if the `authenticated` role actually has table-level `GRANT`s and the JWTs carry `role: authenticated` — neither is established in this migration.

**Fix:** Restrict the updatable surface with column-level grants, e.g.
```sql
REVOKE UPDATE ON notifications FROM authenticated;
GRANT UPDATE (read) ON notifications TO authenticated;
```
and normalize case in the policies: `user_addr = lower(current_setting('request.jwt.claims', true)::jsonb->>'sub')`. Verify (or add) the `GRANT SELECT/INSERT/UPDATE` statements this migration implicitly relies on.

### WR-05: rpcpool robustness gaps — flat 3s timeout for heavy ops, no ctx-cancel check in the failover loop, illusory dial validation, unvalidated/undeduped URL list

**File:** `backend/internal/rpcpool/pool.go:37`, `:56-63`, `:77-95`; `backend/internal/config/config.go:120-123`, `:152-164`

**Issue:** Four related robustness defects:
1. `DefaultTimeout = 3s` applies uniformly, including `FilterLogs` over 30-block chunks on slow public RPCs — chronic timeouts make every backfill chunk burn a full failover round (n × 3s) and can stall backfill indefinitely on a marginal-latency provider set.
2. `call` never checks `ctx.Err()` between attempts: once the parent ctx is cancelled (shutdown), it still iterates all endpoints, each failing instantly, producing misleading "failing over" warnings and a bogus "failed on all N endpoints" error that masks the cancellation.
3. `ethclient.DialContext` is lazy for `http(s)` URLs — it validates the URL syntax only. The "dial failures are fatal only if no endpoint comes up" startup guarantee is illusory: a typo'd-but-well-formed URL joins the rotation and fails ~1/n of all calls forever.
4. `parseURLList` does not dedupe or validate schemes, and if `RPC_URLS` is set but omits `RPC_URL`, the (still-required) primary silently drops out of rotation — likely surprising operationally.

**Fix:** Take a per-op timeout (or an op→timeout map; ≥15s for `FilterLogs`); add `if ctx.Err() != nil { return zero, ctx.Err() }` at the top of the failover loop; perform a real health probe (`BlockNumber` with a short timeout) per endpoint in `New`; dedupe URLs and consider always including `RPCURL` in the set.

---

## Info

### IN-01: Dead `Config.ServerTimeMs` field with misleading comment

**File:** `backend/internal/config/config.go:69-71`
**Issue:** The field claims to be "set by the indexer on each new block… accessed atomically", but `main.go` uses a separate local `serverTimeMs` variable; the Config field is never written or read. **Fix:** delete the field.

### IN-02: Duplicate Postgres rate limiter; `/auth/verify` echoes unnormalized address

**File:** `backend/cmd/server/main.go:61`, `:97`, `:184`
**Issue:** `authRL := ratelimit.NewPg(pool)` duplicates `rl` (same backing store, same `"auth:"` keys — harmless but pointless); `tokenResp` returns `req.Address` as supplied while the JWT is issued for the lowercased address — cosmetic inconsistency for clients comparing the two. **Fix:** reuse `rl`; return `addr`.

### IN-03: `RefundPushed`/`LoserRefunded` emitted even when the push failed into `pendingReturns`

**File:** `contracts/src/AuctionHouse.sol:261-263`, `:298-300` (pre-existing, noted because the indexer/UI sync refund rows from these events)
**Issue:** Both events fire whether the ETH actually landed or fell back to pull-withdrawal, so the indexer can show a bidder "refunded" who still must call `withdrawRefund`. **Fix (off-chain, since contracts are immutable):** have the indexer/keeper cross-check `pendingReturns(bidder)` after these events and surface a "withdraw required" state.

### IN-04: `minNext` uint128 cast can truncate at extreme values

**File:** `contracts/src/AuctionHouse.sol:202`
**Issue:** `uint128(uint256(a.leaderTotal) + inc)` truncates silently if `leaderTotal + inc > type(uint128).max`, nullifying the min-increment check. Requires ~1.7e20 ETH of escrow — unreachable on Flare; documented for completeness only.

### IN-05: e2e script nits

**File:** `contracts/script/e2e_local.sh:26`, `:94-106`
**Issue:** (a) `diff()` shadows the coreutils binary within the script (works, but a trap for future edits); (b) Section E verifies entries halt while paused and that `entriesAllowed` flips back, but never re-executes an entry after unpause to prove recovery. **Fix:** rename to `sub()`; add one post-unpause `list` + PASS check.

### IN-06: Swallowed keeper-gate error; no `Pool.Close`; registry doesn't verify core↔manager binding

**File:** `backend/internal/indexer/runner.go:95-97`; `backend/internal/rpcpool/pool.go`; `contracts/src/MarketplaceManager.sol:105-118`
**Issue:** (a) `if err != nil { return }` drops the gate error without logging — correct today (only ctx-cancel) but silently disables keepers if a future gate returns real errors; log it. (b) `rpcpool.Pool` has no `Close()`, leaking ethclient transports on shutdown (moot at process exit, matters for tests). (c) `setCoreContracts` accepts any contracts; it cannot check `core.manager() == address(this)` generically, but the deploy scripts could assert it (they do, lines 65-67 — fine; just don't re-point the registry to cores bound to a different manager).

### IN-07: Migration idempotency and grant assumptions

**File:** `backend/internal/db/migrations/011_rls_rework.sql`
**Issue:** `CREATE POLICY` has no `IF NOT EXISTS`; if any of these policies were applied manually (e.g., via the Supabase dashboard during the 006 incident response this migration references), `goose up` will fail and block startup (`main.go:44-46` is fatal on migration error). **Fix:** wrap in `DROP POLICY IF EXISTS ...; CREATE POLICY ...` pairs, matching the Down section's defensive style.

---

## Cross-file verification notes (no findings)

- **entryGate exit audit:** `settle`, `refundLosers`, `cancelEarly`, `withdrawRefund` (AuctionHouse), `cancel` (Marketplace), `rejectOffer`, `refundExpiredOffer` (OfferBook) confirmed ungated. `acceptOffer` is gated, which during a pause delays — but cannot trap — bidder escrow (refundable at expiry ≤ 14 days via the ungated `refundExpiredOffer`). Invariant holds.
- **Reentrancy via entryGate:** `entriesAllowed()` is `view` → STATICCALL; cannot reenter mutably regardless of modifier order. Entry functions without `nonReentrant` (`list`, `create`) perform no value transfers. No issue.
- **Anti-snipe underflow:** `a.endsAt - block.timestamp` at AuctionHouse.sol:212 is safe — `block.timestamp >= a.endsAt` reverts at line 173.
- **Deploy handover ordering:** grants to creator strictly precede deployer renounces in both scripts; a mid-sequence failure can never leave the manager admin-less.
- **`*rpcpool.Pool` satisfies `indexer.EthClient`:** method sets match exactly; `main.go:77` wiring is correct.

---

_Reviewer: Claude (gsd-code-reviewer) — adversarial deep review_

---

## Resolution log (2026-06-09, same session)

| Finding | Status | Resolution |
|---------|--------|------------|
| CR-01 | **Fixed** | `processRange`/`backfill` propagate errors; watcher cursor (`lastBlock` + persisted) advances only after a fully successful range; live catch-up reuses chunked `backfill` so outages heal within getLogs caps. |
| CR-02 | **Fixed** | Pool selection changed round-robin → **sticky-until-failure** (one consistent chain view for head/logs/nonces); plus `headLag = 2` clamp for reorg/failover-skew tolerance. |
| CR-03 | **Fixed** | `"nonce too low"` no longer treated as broadcast success (test-pinned); sticky endpoint removes the rotating `PendingNonceAt` skew; `MarkLosersRefunded` now requires a **mined successful receipt per batch** (`waitMined`, 60 s) — unconfirmed broadcasts retry next tick. |
| CR-04 | **Fixed** | `WaitKeeperLock` now dials a dedicated session connection via `SessionDSN` (never the shared pool → transaction-pooler safe) and returns a liveness-monitored `lockCtx` (10 s pings); keepers run under it and the runner re-acquires in a loop — split-brain window closed. |
| WR-01 | **Fixed** | `main` waits on `indexerDone` (15 s cap) after `cancel()`; HTTP shutdown no longer races the drain. |
| WR-02 | **Fixed** | `MarketplaceCore` constructor validates non-zero `manager_`: must have code and answer `entriesAllowed()` (probe). Tests added. Hostile-manager = entry-censorship-only documented in MarketplaceManager header. |
| WR-03 | **Fixed** | `DeployFlare` requires `CREATOR_ADDR.code.length > 0` (Safe); both scripts assert operator renounce + keeper role grant. |
| WR-04 | **Fixed** | 011 policies normalize `lower(sub)`; column-level `GRANT UPDATE (read)` on notifications and non-`verified` columns on profiles; explicit SELECT/INSERT grants added. |
| WR-05 | **Fixed** | Per-op timeout (15 s for `FilterLogs`), `ctx.Err()` check in failover loop, startup health probe with healthy-first ordering, URL dedupe, `RPC_URL` always merged into the rotation set. |
| IN-01 | **Fixed** | Dead `Config.ServerTimeMs` removed. |
| IN-02 | **Fixed** | Duplicate limiter removed; `/auth/verify` returns the normalized address. |
| IN-03 | Open (accepted) | Contracts immutable; indexer-side `pendingReturns` cross-check deferred to a UI task. |
| IN-04 | Open (accepted) | Unreachable at Flare supply (~1.7e20 ETH); documented. |
| IN-05 | **Fixed** | `diff` → `sub`; post-unpause entry re-execution check added (18 checks total). |
| IN-06 | **Fixed** | Gate errors logged; `Pool.Close()` added. Registry note stands. |
| IN-07 | **Fixed** | All policies wrapped in `DROP POLICY IF EXISTS` pairs. |

Post-fix verification: forge 117/117 · Go suite green (`go vet` clean) · anvil E2E 18/18 including fresh deploy with admin-handover asserts.
