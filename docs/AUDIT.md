# MagicWebb — Defect Tracking (Audit v19 + v20)

Living defect ledger for the MagicWebb marketplace (Solidity contracts + Go
backend + Alpine/Tailwind frontend, deployed on Flare). Each finding carries
a stable ID + semantic key, severity, location, status, scenario, and fix
sketch anchored to the function signature + a lexically-stable constant
declaration (so the citation survives field renames). The **Priority Stack**
at the bottom is the order this repository should be worked on; it carries
findings flagged by adjacent code sweeps that have NOT been fixed yet.

## Severity legend

| Tier | Meaning                                                                                       |
|------|-----------------------------------------------------------------------------------------------|
| 🔴 P0 | **Single-attacker system-wide stranding** — Release blocker. **Acquisition criterion:** *attacker-triggered* AND *cooperator-free* AND any ONE of: (i) escrow permanently stranded (>0 wei, no recovery path) for any user, (ii) system-wide DoS lasting ≥ 1 RPC cycle (~2s), (iii) ≥ 50 % of the caller base affected for ≥ 1 minute, (iv) attacker's net cost to execute ≈ 0. If a finding meets (i)/(ii)/(iii)/(iv), and at least one of those triggers is confirmable in a unit / fuzz harness, P0. Otherwise P1. |
| 🟠 P1 | Single-user fund trap / wrong-chain or wrong-account tx / DoS-with-recoverable-state (no permanent loss but operationally painful). Fix within current sprint. |
| 🟡 P2 | Cleanup / leak / hard-to-exploit DoS / hardening. Tracked for follow-up.                              |
| ⚪ P3 | Dead code / refactor / perf. Nice-to-have.                                                              |

The 🔴 P0 rule currently has **two holders** in this doc:

1. **C-01** (anti-snipe) — a single malicious bidder can strand every
   other bidder's escrow indefinitely.
2. **`onTransferBatch`** (Priority Stack entry) — a single hostile
   `TransferBatch` log can OOG the chain indexer and silently stall all
   downstream settlement + rework.

Both qualify under the acquisition criterion above. C-01 is in the v20
audit; `onTransferBatch` was caught by the post-v20 adjacent sweep and
lives in the Priority Stack.

Status values: **OPEN**, **PATCH READY** (working-tree, pre-commit),
or **FIXED** (committed + verified).

---

## v19 — Frontend (wallet.js / SIWE)

### F-01 — `chainChanged` listener gated to WalletConnect only 🟠 P1 — PATCH READY
- **Where:** `backend/internal/ui/static/wallet.js`, the provider init
  handler that registers EIP-1193 listeners. **Same anchor as F-02.**
- **Key:** `f01-chainchanged-listener-scope`
- **Scenario:** Injected providers (MetaMask, Rabby, Brave) NEVER receive
  `chainChanged` events because the listener was registered under a
  `if (kind === 'walletconnect')` gate. Combined with ethers v6
  (`BrowserProvider` caches `.provider.network` at construction), the
  next tx after a switched network instant-reverts on the wrong chain
  with no UI feedback. User loses gas + has to manually reload.
- **Reproduction:** Open MM on Coston2 → switch to Songbird RPC in MM
  → return to the MagicWebb tab → attempt a bid/accept → wallet rejects
  → no UI signal that the failure is a chain mismatch.
- **Fix sketch:** Drop the WC-only gate on `chainChanged`; register on
  both `injected` + `walletconnect` kinds. Injected handlers call
  `window.location.reload()` so the cached signer + provider are rebuilt
  against the new chain. The `_eipHandlers` named-ref teardown prevents
  listener stacking across reconnect cycles on the `window.ethereum`
  singleton.
- **Status:** PATCH READY (wallet.js working-tree patch from v20 step 1).

### F-02 — `accountsChanged` listener gated to WalletConnect only 🟠 P1 — PATCH READY
- **Where:** `backend/internal/ui/static/wallet.js`, provider init
  handler — same function body as F-01 (`wallet.js:connect()`) but
  separate listener registration. The fix patch covers both listener
  registrations; if one lands without the other, the bug returns.
- **Key:** `f02-accountschanged-listener-scope`
- **Scenario:** Same shape as F-01, but for account switches. The cached
  EIP-1193 Signer is bound to the originally-reported address; subsequent
  bids or accepts go out under the wrong account, producing wallet-side
  rejections. The user has no signal that the failure trace belongs to
  the wrong-account path.
- **Reproduction:** Open MM with Account A active → in MM switch to
  Account B → return to MagicWebb → attempt a bid → wallet rejects under
  the wrong address → no UI signal.
- **Fix sketch:** Same F-01 patch; injected path triggers
  `window.location.reload()` (cannot safely rebind without tearing the
  ethers Signer down), WalletConnect path retains `SignerClient`-driven
  hot-swap because WC reconnect never tears down DOM state.
- **Status:** PATCH READY (same wallet.js working-tree patch as F-01).

### F-03 — Silent SIWE failure 🟠 P1 — PATCH READY
- **Where:** SIWE connect path in `backend/internal/ui/static/wallet.js`
  (deadlock-escape + provider-rebuild wrapper, in `siweConnect()` +
  `preflight()`).
- **Key:** `f03-silent-siwe-failure`
- **Scenario:** A non-recovering exception during `personal_sign` (wrong
  nonce, wrong domain, user denial re-coded as `ACTION_REJECTED`) was
  swallowed by a generic `.catch()` and never surfaced. The user saw the
  connect button silently revert to its idle state — they had no signal
  whether the failure was a domain mismatch vs. a user denial vs. a
  wallet bug. Support tickets cited "the connect button just doesn't
  work."
- **Reproduction:** Set `SIWE_DOMAIN` to a stale value (e.g. previous
  deploy) → click Connect → observe the connect button silently returns
  to idle without an error path.
- **Fix sketch:** Replace the generic `.catch` with a typed try/catch
  that maps each non-recoverable case to a user-readable error message,
  re-rendering the connect modal with the failure reason. Add a soft
  preflight that detects domain mismatch BEFORE the SIWE round-trip so
  the user gets a deterministic error path most of the time.
- **Status:** PATCH READY (same working-tree patch). F-03 and C-04 are
  both 🟠 P1 by the same "DoS-with-recoverable-state / UX-trust" rule
  — the operation fails but no funds are lost; the user has to retry.

---

## v20 — Solidity contracts

### C-01 (audit-#1) — Anti-snipe accumulation permanently stalls auction 🔴 P0 — FIXED
- **Where:** `contracts/src/AuctionHouse.sol`, function `bid()` —
  anti-snipe block gated on the lexically-stable constant
  `EXTENSION_WINDOW = 3 minutes` (the bool-flip that controls the gate
  is implementation detail; the constant is what makes the audit-fix
  grep-stable across field renames).
- **Key:** `c01-anti-snipe-accumulation`
- **Scenario:** The pre-fix code extended `endsAt += EXTENSION_WINDOW`
  on ANY non-zero bid inside the closing window, regardless of whether
  the bid actually took the lead. A single malicious bidder could fire
  1-wei bids every block inside the last 3 minutes to keep the auction
  open indefinitely — every other bidder's escrow would be stranded
  forever, refund path blocked by the pending closing window. This is
  the only 🔴 P0 in the v20 audit round because the attack surface is
  universal and attacker-controlled.
- **Fix sketch:** Gate the extension on a new-lead flag that flips ONLY
  when leadership actually changes (or first qualifying bid). Sub-
  threshold accumulation no longer extends. Patch is the new-lead guard
  inside `AuctionHouse.bid()`, gated by `EXTENSION_WINDOW = 3 minutes`.
- **Status:** FIXED. Verified by
  `contracts/test/AuditFuzz.t.sol::testFuzz_antiSnipe1kLateBids` —
  1000 accreting 1-wei bids produce zero further extensions past the
  single new-lead push (asserted at the loop tail via
  `assertEq(_endsAt(id), endAfterLead, …)` invariant; the audit-fix
  boundary is enforced by
  `assertEq(extendedEndsAt - endAfterLead, 0)`).

### C-02 (audit-#2) — Seller hijacks stalled delivery; old code rewarded it 🟠 P1 — FIXED
- **Where:** `contracts/src/AuctionHouse.sol`, function `settle()` +
  new helpers `settleUnstuck()` + `reclaim()` — all anchored to the
  lexically-stable constant `STALL_WINDOW = 7 days`. New errors
  `NotStalled` / `StallNotOver`, new events `AuctionStalled` /
  `AuctionReclaimed`. New field on `Auction` struct:
  `uint64 stalledAt;`.
- **Key:** `c02-stalled-state-recovery`
- **Scenario:** Pre-fix, when `safeTransferFrom` reverted in `settle()`
  (seller revoked approval or moved the NFT elsewhere after `endsAt`),
  the contract auto-cancelled and refunded the winner in full. The
  seller hadn't been paid, but they could also not bring the auction
  back; meanwhile the winner had no NFT. Worse, a malicious seller
  could deliberately revoke approval to unilaterally cancel a winning
  auction — making the winner's escrow hostage until someone re-bid,
  which the winner had no incentive to do.
- **Severity rationale:** 🟠 P1 because it requires seller cooperation
  (revoke or move NFT) — not attacker-controlled.
- **Fix sketch:** On delivery failure, set the
  `uint64 stalledAt;` field on the `Auction` struct (do NOT latch
  `settled = true`) and emit `AuctionStalled`. `settleUnstunk()` lets
  the seller re-approve + retry delivery;
  `reclaim(id)` is callable after `STALL_WINDOW = 7 days` and refunds
  the winner in full + cancels — bound so the seller can't hold escrow
  hostage forever.
- **Status:** FIXED. Verified by the rewritten
  `contracts/test/AuctionHouseSettleSafety.t.sol::test_settleParksWhenSellerMovedNftThenReclaim`
  +
  `…::test_settleParksWhenApprovalRevokedThenReclaim`.

### C-03 (audit-#3) — Offer refund reverts when bidder is a contract dead to receive ETH 🟠 P1 — FIXED
- **Where:** `contracts/src/OfferBook.sol`, functions `rejectOffer()` +
  `refundExpiredOffer()` + new helper `_pushPullRefund()` — all
  anchored to the lexically-stable declaration
  `mapping(address => uint256) public pendingReturns;` (the mapping
  declaration IS the audit-fix invariant — if a future refactor removes
  this mapping, the fix is gone). New function `withdrawRefund()`
  with restore-on-failure.
- **Key:** `c03-offer-pull-fallback`
- **Scenario:** Pre-fix, both reject / refund paths called `_pay(bidder,
  principal)` which was a plain forwarder — reverted when the bidder was
  a contract without a payable `receive()`. The revert bubbled out of
  the `rejectOffer` / `refundExpiredOffer` call, leaving the offer
  record zeroed AND the bidder's ether permanently trapped in the book
  (no `pendingReturns` mapping existed). The seller / keeper had to
  write a custom withdraw helper that didn't exist.
- **Severity rationale:** 🟠 P1 because it traps a SINGLE bidder's
  escrow (not system-wide).
- **Fix sketch:** Replace `_pay` with `_pushPullRefund(to, amount)` that
  tries the forwarder first and on failure stores the credit in
  `pendingReturns[to]`. Bidders call `withdrawRefund()` (with restore-
  on-failure) once their contract is payable again. Symmetric pattern
  to `AuctionHouse.pendingReturns`.
- **Fix footnote:** The same audit-pass needed a small compile-fix to
  `OfferBook.sol` — the missing `WithdrawFailed` symbol in the
  `MarketplaceCore` import list was a latent bug because no pre-fix
  test exercised `withdrawRefund()`.
- **Status:** FIXED. Verified by
  `contracts/test/AuditFuzz.t.sol::test_offerExpiredRefundPushFallback`.

### C-04 (audit-#4) — `refundLosers` unbounded batch + no per-call gas cap 🟠 P1 — FIXED
- **Where:** `contracts/src/AuctionHouse.sol`, function `refundLosers()`
  — iteration guarded by the new `error BatchTooLarge();` reversion
  (revert when `batch.length > 200`) + per-loop
  `b.call{gas: 50_000, value: amt}("")`. Anchored to the lexically-
  stable `50_000` gas-cap value + the `200` ceiling — these two
  literals are the audit-fix invariants.
- **Key:** `c04-refundlosers-gas-bound`
- **Scenario:** Pre-fix, the loop iterated over arbitrary-length batch
  and forwarded full tx-remaining gas to each `call{value: amt}("")`. A
  griefing receiver's `receive()` could consume >63/64 of the remaining
  forward gas (EIP-150 rule), rolling forward iteration's bookkeeping
  and clawing back prior pendingReturns credits. Single attacker
  address = roll the entire cleanup of every other legitimate bid.
- **Severity rationale:** 🟠 P1 — **DoS-with-recoverable-state**, not
  fund loss: the failed-iteration call parks the credit in
  `pendingReturns[b]` for later pull; the prior-iteration credits
  are not actually clawed back, the attacker only forces the keeper to
  retry. Worst case the cleanup is delayed by grief, but no funds are
  lost. (Same tier as F-03 — UX-trust / DoS-with-recovery.)
- **Fix sketch:** Two changes:
  1. `if (batch.length == 0 || batch.length > 200) revert BatchTooLarge();`
     — keeps a single call well inside a block's gas budget without
     skipping any legitimate non-winning bidders.
  2. `b.call{gas: 50_000, value: amt}("")` — caps EIP-150 forwarded
     gas so a hostile `receive()` burns at most 50k per iteration;
     outer frame stays safely non-OOG, and on call-failure the credit
     parks in `pendingReturns[b] += amt` for later pull.
- **Status:** FIXED. Verified by
  `contracts/test/AuditFuzz.t.sol::test_refundLosersGriefingHalfBatchDoesNotOOG` —
  100 EOA losers + 100 GreedyReceiver bidders (default-`blocked=true`
  so their `receive()` always reverts) in a 200-batch. Outer call
  completes; EOA losers paid directly; greedy receivers parked in
  `pendingReturns` and pullable via `withdrawRefund()`; no OOG.

---

## Priority Stack (next 6 to fix in a follow-up pass)

These are not from the v19 / v20 audit rounds themselves, but were
flagged by adjacent code sweeps — the post-v20 review of
`backend/internal/...` (queries.go, indexer handlers, rest.go,
marketplace.go). They are ranked by likelihood × impact; the rank is
the source of truth for what to work on next.

> **ID convention:** Items carry a *semantic key* (camelCase, no numeric
> prefix). Numeric labels may reflow as items are added / closed; grep by
> key. First versions of this doc used P-01..P-06 numeric labels but those
> invalidated PR descriptions and commit messages when items shifted
> order — semantic keys are stable.

1. **`onTransferBatch`** 🔴 P0 — `backend/internal/indexer/handlers.go::onTransferBatch()`.
   `idsLen` / `valsLen` are decoded entirely from untrusted log data and
   the `chunk()` helper silently zero-pads past the dataload boundary,
   so the loop can run billions of times on a hostile-`idsLen`
   `TransferBatch` log. Bind lengths by `len(l.Data)/64` BEFORE the loop.

2. **`processTransfersWallClock`** 🟠 P1 — `backend/internal/indexer/runner.go::processTransfers()`.
   The audit-fix on `processRange` — the **`log.Warn + continue` skip
   pattern when `HeaderByNumber` fails on a single log** — does NOT
   propagate to the lower transfer-log dispatch; that loop still falls
   back to `bt = uint64(time.Now().Unix())` (wall-clock poison).
   **This is 🟠 P1, not 🔴 P0 — the `log.Warn + continue` retry
   pattern in `processRange` means the NEXT iteration of the indexer
   loop re-fetches the failing header** (the outer drainer re-enters
   `processRange` for the same block's next log; the failed header
   re-tries sequentially), capping the propagation to a single ~2s
   staleness window per RPC stall. Blast radius is **per-block (NOT
   per-session)**: each block's transfers use only that block's
   header time. If the post-v20 sweep ever finds the outer drainer
   ALSO skipping retries on header failure, escalate to 🔴 P0.

3. **`getRecentTxnsLimit`** 🟠 P1 — `backend/internal/db/queries.go::GetRecentTransactions()`.
   `LIMIT $1` is applied to the outer `UNION ALL`, not pushed into each
   `(SELECT ... LIMIT $1)` subquery. Postgres cannot push the LIMIT
   into a standard unindexed union → full historical scan + in-memory
   sort on every `/api/v1/activity` call.

4. **`getEffectiveBidsLimit`** 🟠 P1 — `backend/internal/db/queries.go::GetEffectiveBids()`.
   No `LIMIT` clause. A contested auction with 10k+ tiny incremental
   bids → OOM during the pages where it's rendered address-by-address.
   Append `LIMIT 200` after clamping the input.

5. **`clientIpSpoof`** 🟠 P1 — `backend/internal/api/rest.go::clientIP()`.
   Manual rightmost-`X-Forwarded-For` extraction is fine behind Fly.io
   but trivially spoofable when traffic ever bypasses the proxy. Switch
   to Fiber `c.IP()` + explicit `TrustProxies` policy.

6. **`parseWeiHelper`** 🟡 P2 — `backend/internal/db/queries.go` at the
   five `big.Int.SetString` sites for `wei::text` / `volume::text` /
   `price::text`. `SetString` silently returns `false` and leaves the
   int at `0` on parse failure (un-coalesced NULL, schema drift).
   Centralize behind a `ParseWei(s) (*big.Int, error)` helper.

---

## Verification matrix

| ID      | Tier | Key (semantic)                          | Status                                  | Verified by                                                                              |
|---------|------|-----------------------------------------|-----------------------------------------|------------------------------------------------------------------------------------------|
| F-01    | 🟠 P1 | `f01-chainchanged-listener-scope`       | PATCH READY                             | wallet.js `connect()` patch (same anchor as F-02); manual QA on Coston2 per Reproduction  |
| F-02    | 🟠 P1 | `f02-accountschanged-listener-scope`    | PATCH READY                             | wallet.js `connect()` patch (same anchor as F-01); manual QA on Coston2 per Reproduction  |
| F-03    | 🟠 P1 | `f03-silent-siwe-failure`               | PATCH READY                             | wallet.js `siweConnect()` + `preflight()`; manual QA on Coston2 per Reproduction           |
| C-01    | 🔴 P0 | `c01-anti-snipe-accumulation`           | FIXED                                   | `AuctionHouse.bid()` gated on `EXTENSION_WINDOW = 3 minutes`; `AuditFuzz.t.sol::testFuzz_antiSnipe…` |
| C-02    | 🟠 P1 | `c02-stalled-state-recovery`            | FIXED                                   | `AuctionHouse.settle()` + `settleUnstuck()` + `reclaim()`, gated on `STALL_WINDOW = 7 days`; `SettleSafety.t.sol` |
| C-03    | 🟠 P1 | `c03-offer-pull-fallback`               | FIXED                                   | `OfferBook.rejectOffer()` + `refundExpiredOffer()` + `_pushPullRefund()`, gated on the `mapping(address => uint256) pendingReturns` declaration; `AuditFuzz.t.sol::test_offerExpired…` |
| C-04    | 🟠 P1 | `c04-refundlosers-gas-bound`            | FIXED (DoS-with-recoverable-state)     | `AuctionHouse.refundLosers()` with `error BatchTooLarge()` + per-iteration `gas: 50_000`; `AuditFuzz.t.sol::test_refundLosers…` |

Open Priority Stack items (NOT yet covered by a fix):

| Key (semantic)                       | Tier | Where                              | Status |
|--------------------------------------|------|------------------------------------|--------|
| `onTransferBatch`                    | 🔴 P0 | `indexer/handlers.go::onTransferBatch()` | OPEN   |
| `processTransfersWallClock`          | 🟠 P1 | `indexer/runner.go::processTransfers()` | OPEN   |
| `getRecentTxnsLimit`                 | 🟠 P1 | `db/queries.go::GetRecentTransactions()` | OPEN   |
| `getEffectiveBidsLimit`              | 🟠 P1 | `db/queries.go::GetEffectiveBids()` | OPEN   |
| `clientIpSpoof`                      | 🟠 P1 | `api/rest.go::clientIP()` | OPEN   |
| `parseWeiHelper`                     | 🟡 P2 | `db/queries.go` at five `big.Int.SetString` sites | OPEN   |

---

## How to update this doc

1. **Append on merge** with a *new semantic key* in the same series
   (`fXX-…`, `cXX-…`, or no-prefix for Priority Stack).
2. **Severity only changes** on a confirmed regression or a new
   attacker path surfacing.
3. **Status transitions require a verification artefact:**
   passing `forge test` (contracts), passing `go test -race` (backend),
   or a documented manual QA procedure **with reproduction steps**
   (frontend — `wallet.js` has no automated cover right now).
4. **Priority Stack ordering** reflows freely; numeric prefixes are
   not used so PR descriptions and commit messages don't go stale on
   reorder.

## Prior audit context (v9–v18)

Audit-driven commits before this ledger's v19 cutoff live in git
history (see `git log --oneline` between `main` and the v18 tag).
They were all UI-only fixes — wallet-button visibility at every
breakpoint, chain-switch re-throw, Alpine-init SyntaxError unwedge,
`?v=10` cache-bust on assets, removed silent auto-reconnect (saved
wallet becomes explicit-consent pill), etc. They are NOT represented
as rows in this ledger because:

1. They were driven by ad-hoc UI feedback, not a formal audit sweep,
   so they don't carry the audit-fix → harness-verified pattern that
   this ledger's rows require.
2. They were shipped and verified via manual QA before this ledger
   opened.

If a future audit round revisits the wallet/UI surface, expand this
ledger to include those commits as F-XX rows with new semantic keys
and link each row to the commit SHA that landed the fix.

## See also

- `docs/DEPLOY_FLY.md` — deployment shape, secrets, rollback recipe.
- `backend/internal/db/migrations/` — Postgres schema under audit.
- `contracts/src/AuctionHouse.sol` and `OfferBook.sol` — Solidity
  source under audit.
- `backend/internal/ui/static/wallet.js` — JS source under audit (UI).
