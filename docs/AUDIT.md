# MagicWebb — Defect Tracking (Audit v19 + v20 + v21)

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

The 🔴 P0 rule previously had **two holders** in this doc. As of **v21**, both
are FIXED:

1. **C-01** (anti-snipe) — committed, harness-verified (`AuditFuzz.t.sol::testFuzz_antiSnipe1kLateBids`).
2. **`onTransferBatch`** (indexer OOG) — committed in v21, off-by-block bound check passes the malicious `idsLen=2**256` log invariant.

Status values: **OPEN**, **PATCH READY** (working-tree, pre-commit),
or **FIXED** (committed + verified).

---

## v22 — Live-site sweep (post-v21 release on magicwebb.fly.dev)

The v21 ledger closed every Priority Stack item with a CI-verified fix.
v22 opened in response to a fresh end-to-end browser-use sweep against
the live site, which surfaced three concrete defects:

### U-01 — Bare `/profile` route 404 🟠 P1 — FIXED
- **Where:** `backend/cmd/server/ui.go` (new helper `uiProfileRedirect`
  + helpers `cookieNames`, `isEthAddr`); `backend/cmd/server/main.go`
  registers `app.Get("/profile", uiProfileRedirect)` after the existing
  `/profile/:addr` route.
- **Key:** `v22-profile-bare-redirect`
- **Scenario (historical):** A user typing `/profile` in the address
  bar — or clicking any deep link that pointed at the bare path
  (no `:addr`) — hit Fiber's default 404 because only the parametrised
  `/profile/:addr` was registered. The navbar has no `/profile` link
  that doesn't already carry an address, but copy-pasted links from
  external pages or muscle-memory URL entry stranded the user on a
  bare "Cannot GET /profile" page.
- **Fix:** Server-side rescue route. Walks every cookie in the
  request, finds any `mw_s_<prefix>` cookie (the SIWE session cookie),
  verifies its JWT against `JWT_SECRET` + `DefaultAudience` via
  `auth.Verify`, extracts the wallet address from the `sub` claim,
  validates the `0x[40 hex]` format, then 302-redirects to
  `/profile/<lowercase addr>`. If no valid session cookie is present
  (logged-out visitor), 307-redirects to `/listings` so the visitor
  lands somewhere useful. Uses full signature validation (not
  `jwt.ParseUnverified`) so a stolen-but-unforgeable cookie cannot
  redirect a stranger to an attacker-controlled profile.
- **Status:** FIXED. Merged into `main` at commit `312098c` and live on
  https://magicwebb.fly.dev/ (verified via `GET /profile → 307 → /listings`
  + `GET /profile/<addr>` round-trip).

### U-02 — `/favicon.ico` 404 (noisy console error) 🟡 P2 — FIXED
- **Where:** `backend/internal/ui/templates/layout.html` `<head>`.
- **Key:** `v22-favicon-inline-svg`
- **Scenario (historical):** Every page load logged a `GET /favicon.ico
  404` in browser console. Cosmetic but recurrent — DI'd every sweep
  audit and is the kind of oso-level lint check that hides real errors
  in CI console capture.
- **Fix:** Inline `<link rel="icon" type="image/svg+xml" href="data:image/svg+xml;utf8,...">`
  in `<head>`. Same-origin SVG data URL featuring the ✦ glyph on the
  brand sky background (matches the navbar logo). Browser scribes
  the implicit favicon request as rendered — never hits a 404.
- **Status:** FIXED.

### U-03 — Stray `</div>` prematurely closed navbar right-cluster flex 🟠 P1 — FIXED
- **Where:** `backend/internal/ui/templates/layout.html`, between
  the dropdown's `Connection is non-custodial…` paragraph and the
  `<!-- Notification bell -->` comment.
- **Key:** `v22-navbar-div-stacking`
- **Scenario (historical):** A closing `</div>` (indent 6) was
  placed between the wallet dropdown's closing tag and the bell
  template, prematurely closing the `<div class="flex items-center gap-2">`
  right-cluster. Every subsequent element — the bell template, the
  connected-pill template, the saved-wallet-pill template, the WC
  pairing chip template, and the hamburger button — was dropped out
  of the flex container. Visually the page rendered OK in some
  browsers (flex below was still inline-rendering) but the bell +
  connected pill + saved pill + WC chip + hamburger all lived on
  the same horizontal line break. Alpine `x-if` evaluates the
  templates in document order, and dropping the pill + bell out of
  the flex sometimes triggered `AddEventListener` order races that
  hid the connect button's click handler. Confirmed via a count of
  `<div` opens (3 inside the dropdown block) vs `</div>` closes
  (4); the extra close was orphaning the flex container.
- **Fix:** Removed the stray `</div>`. The right-cluster flex
  container now closes correctly after the hamburger button.
- **Status:** FIXED.

### v22-samesite-strict-ux 🟡 P2 — FIXED
- **Where:** `backend/cmd/server/main.go::setSessionCookie()`.
- **Key:** `v22-samesite-strict-ux`
- **Scenario (historical):** SIWE session cookies were emitted with
  `SameSite: "Strict"`. `Strict` blocks the browser from attaching
  the authentication cookie on **any** cross-origin top-level GET
  navigation — including first-page-load arrivals from Twitter,
  Discord, Telegram, etc. Symptom: a user clicking a MagicWebb link
  in chat was silently signed-out on every fresh inbound visit and
  had to reconnect their wallet before any state-aware UI surfaced.
- **Fix:** `SameSite: "Lax"`. `Lax` is the web standard for session
  cookies — cookie IS sent on top-level cross-origin GETs (so inbound
  links re-authenticate the user mid-render) but is NOT sent on
  cross-site sub-resource loads or POSTs (CSRF defences are unchanged).
  The JWT signature gate on every mutating endpoint remains the
  authoritative defence; the cookie is for browser-navigation auth.
- **Status:** FIXED.



---

## v23.1 — Modal auto-popup gate + Fly/GitHub deploy-drift safety net

The v23 release closed the WalletConnect CDN resilience defect but was
deployed in two waves because the v74-class silent-drift bug surfaced
mid-rollout. v23.1 closes both halves and arms the runtime contract so
neither can regress silently again.

### U-04 — Action modal auto-pops without a user click 🟠 P1 — FIXED
- **Where:**
  - `backend/internal/ui/static/wallet.js` — `MODAL_OPTS_FALLBACK`
    gains `userInitiated: true,` (line 93); the `Alpine.store('modals')
    .open(opts)` method gains the gate at line 347; both `runAction`
    callers (no-signer branch line 977, good-signer branch line 993)
    pass `userInitiated: true,` explicitly.
  - `backend/internal/ui/templates/partials/action_modal.html` —
    `x-on:open-action.window` listener gains a precondition gate that
    forwards only when `($event.detail || {}).userInitiated === true`;
    otherwise `console.warn` and silently ignore.
- **Key:** `u04-actionmodal-userInitiated-gate`
- **Scenario (historical):** The action_modal partial rendered with the
  fallback title `'Confirm action'` whenever `modals.open(opts)` was
  called with an opts object whose `.title` was empty or undefined (or
  any time a dispatch arrived with no detail at all). Any stray
  `open-action` window event from a third-party extension, a stale
  embedscript dispatch, OR a future caller forgetting to set
  `.title` would surface the modal up unprompted — the exact “Confirm
  action” popup a user reported seeing on a fresh browser visit.
- **Fix:** Two-layer user-initiated gate.
  1. **Listener layer (`action_modal.html`):** the
     `x-on:open-action.window` handler explicitly checks
     `($event.detail || {}).userInitiated === true` BEFORE forwarding
     to `modals.open(...)`. Third-party or browser-extension dispatches
     without the flag are logged via `console.warn` and dropped on
     the floor.
  2. **Store layer (`wallet.js`):** `Alpine.store('modals').open(opts)`
     sanitises opts via `MODAL_OPTS_FALLBACK`, then REQUIRES
     `opts.userInitiated === true`. Anything missing the flag (NO
     detail at all, opts without the key, opts with the key set to a
     falsy value) hits `console.warn` with a stack-trace excerpt and
     returns `Promise.resolve(null)` WITHOUT flipping `this.open = true`.
     Even if a future caller forgets to pass `userInitiated:true`, the
     modal stays closed and the user sees nothing.
  3. **Caller hygiene (both `runAction` branches + the fallback):** all
     three places that ever call `modals.open(opts)` now do so with
     `userInitiated: true,` as the first key. Belt-and-braces against
     a future refactor that re-introduces a no-flag caller path.
- **Negative side effects audited:** the existing busy-guard loop (8 s
  wait + null on timeout) still works — the recursive `this.open(opts)`
  inside the loop carries the same `userInitiated:true` opts object
  from the original successful call. Double-click debounce is
  unaffected. WC overlay (`mw-wc-show` / `mw-wc-hide`) is unaffected
  (different event surface). NFT picker (`mw-nft-picker-show`) is
  unaffected.
- **Verification:**
  - `go build ./...` clean.
  - `go vet ./...` clean.
  - `go test -count=1 -run='TestHomePageInjectsAllRuntimeGlobals' ./internal/ui/...`
    passes (layout HTML + asset cache-busters untouched).
  - Manual + browser-use live QA against https://magicwebb.fly.dev/:
    Navbar render, scroll, hover across page transitions — modal stays
    hidden. Clicking a market button still opens the modal in the
    normal buy/list/cancel flow. Clicking the WalletConnect picker
    still opens the WC QR overlay.
  - `tools/check-fly-sync.sh` exits 0 once the SHA-baked binary
    replaces the live machine (post-deploy verification below).
- **Status:** FIXED. Verified live at https://magicwebb.fly.dev/.
  Commits `76e46a7` (initial v23.1 push: wallet.js /
  action_modal.html / rest.go var / Makefile ldflags / deploy.yml / tool
  script) and the follow-up Dockerfile ARG + deploy.yml `--build-arg`
  + AUDIT.md entry chain.

### ops-01 — Deploy drift: Fly recorded a release but served old static assets 🟠 P1 — FIXED
- **Where:** five files in concert form one safety net.
  - `Dockerfile` lines 11–16: `ARG GIT_SHA=unknown` + ldflags injection
    `-X github.com/.../api.MWServerBuildSHA=${GIT_SHA}` in the
    `go build` step.
  - `Makefile` `build:` target: same ldflags injection driven by
    `git rev-parse HEAD`. Makefile + Dockerfile agree on the linker
    symbol.
  - `backend/internal/api/rest.go`: package var
    `var MWServerBuildSHA = "unknown"` (default-fallback aligned with
    Dockerfile ARG); `/healthz` handler sets
    `c.Set("X-MW-Build-SHA", MWServerBuildSHA)` before returning the
    200 OK.
  - `tools/check-fly-sync.sh` (new): curl `/healthz`, parse the
    `X-MW-Build-SHA` header (case-insensitive), assert equality with
    `git rev-parse origin/main` (with a short-SHA tolerance).
  - `.github/workflows/deploy.yml` post-deploy step runs the script
    after `curl /healthz`. Exit 1 marks the Actions run RED.
  - `Makefile` `check-fly-sync:` target wraps the script for ops.
- **Key:** `ops01-fly-github-drift-safety-net`
- **Scenario (historical):** The v74 release was reported as
  *“up for over an hour without a live update”* even though `git push
  origin main` succeeded and `fly deploy` registered a new release.
  Investigation showed Fly's Docker layer cache pinned the previous
  binary’s static assets even though the new release succeeded —
  resulting in the served `wallet.js`, `tailwind.css`, and HTMX
  templates silently falling out of sync with `origin/main`. The user
  sees the old frontend; CI registers a green deploy; the divergence
  can persist undetected for hours.
- **Fix:** Runtime contract — `X-MW-Build-SHA` header on `/healthz`
  equals `git rev-parse origin/main`. Any drift fails the post-deploy
  GitHub Actions step loudly. The “bake SHA into the binary” path comes
  from THREE independent aligned sources (Dockerfile ARG, Makefile
  ldflags, rest.go package var) so a change to one without the others
  surfaces as a compile error or a green-default that fails the gate.
- **Reporting cadence:** `make check-fly-sync` (manual) AND automatic
  in `deploy.yml`. Loud failure on drift or on a deploy that forgot
  to pass `--build-arg GIT_SHA=…` (the binary ships with the literal
  `unknown` baked in, which fails the gate immediately).
- **Local manual fly-deploy surface:** `fly deploy --remote-only
  --no-cache --build-arg "GIT_SHA=$(git rev-parse HEAD)"` is the
  canonical incantation; `--no-cache` keeps the layer-cache-pinning bug
  class from biting again.
- **Status:** FIXED. Live and gating every CI deploy from this commit.
  Follow-up commits (Dockerfile ARG + `deploy.yml` `--build-arg` +
  this AUDIT entry) complete the runtime contract.

### ops-01 / v23.1.1 — Gate coupling + rolling-deploy race window — FOLLOW-UP FIXED
- **Where:**
  - `.github/workflows/deploy.yml`: the `Post-deploy sync gate`
    step's `if:` changed from `success()` to `always()`. Comment
    block expanded to explain why a future operator narrowing back
    would re-introduce the silent-skip regression.
  - Same file: the `Post-deploy smoke check (curl /healthz)` step's
    `sleep 5` bumped to `sleep 30` so the rolling-deploy swap window
    cannot route the smoke curl to the OLD machine mid-transition
    (which would surface an OUT-OF-DATE SHA to check-fly-sync.sh and
    produce a false-positive DRIFT on a healthy deploy).
- **Key:** `ops01-v2311-gate-coupling-and-rolling-race`
- **Scenario (in v23.1 initial push):** the audit's original gate
  was `if: success()` — if the upstream `curl /healthz` smoke step
  failed (DB unreachable, /healthz returned 503, network blip during
  machine warm-up), the drift-detection step was SKIPPED silently.
  Two classes of deploy bug therefore reported GREEN to Actions:
  binary-broken deploys AND SHA-drift deploys. Separately, the 5 s
  grace was shorter than the typical rolling-swap transition on a
  single-machine app, so a curl landed on the OLD machine during the
  swap could see the previous commit's SHA — both false-positive
  (gating healthy deploys RED) and false-negative (blocking the gate
  if the OLD machine's /healthz pre-emptively returned 503).
- **Fix:**
  1. `if: always()` on the sync gate so the drift check runs in
     every terminal state (success / failure / cancelled). check-
     fly-sync.sh already exits 2 on degraded `/healthz` (curl 000,
     non-200, 200-but-no-X-MW-Build-SHA header), so a smoke-check
     failure today ALSO lights up the gate with a loud diagnostic.
     Operators see exactly which class of bug the deploy had instead
     of ambiguous green.
  2. `sleep 30` in the smoke check covers the rolling swap window on
     a single-machine app. With both fixes in place, the gate is
     both always-on AND race-free.
- **Status:** FIXED. Verified end-to-end via force-deploy with
  `--build-arg "GIT_SHA=$(git rev-parse HEAD)"` and a
  `check-fly-sync.sh` post-deploy run returning 0.

---

## v23 — Picker CDN resilience (post-v22 release)

The v22 sweep closed every live-site bug the browser-use harness surfaced.
v23 opens in response to a single fresh reproduction reported after the
v22 merge landed:

> When the user selects WalletConnect to connect the wallet, the page
> reports *“fails to load/fetch dynamically imported module.”*

### v23-wc-multi-cdn-fallback 🟠 P1 — FIXED
- **Where:** `backend/internal/ui/static/wallet.js`, the `_wcConnect`
  method (anchored to the existing `_WC_CDNS` constant inside the
  function).
- **Key:** `v23-wc-multi-cdn-fallback`
- **Scenario (historical):** `_wcConnect` previously issued exactly one
  dynamic import against
  `https://esm.sh/@walletconnect/ethereum-provider@2.14.0?bundle`.
  esm.sh deprecated the `?bundle` parameter (it now returns a 3.8 KB
  stub that re-exports from another esm.sh URL — fine in principle,
  but on Coston2 + the user’s network the downstream URL frequently
  failed to resolve, surfacing as the “fails to load” error in the
  picker toast). One CDN × one import shape = single point of failure
  for every magicwebb.fly.dev user picking WalletConnect.
- **Fix:** Iterate over three candidate URLs in priority order:
  1. `esm.sh?bundle-deps&target=es2022` (best bundle shape)
  2. `esm.sh?bundle-deps` (legacy shape, still valid)
  3. `cdn.jsdelivr.net/npm/.../index.es.js` (provider-shipped ESM build,
     no esm.sh dependency)
  Try each in a single `for` loop; break on first successful
  `EthereumProvider.init(...)`. If *every* URL fails (network outage,
  corporate proxy block, etc.) the function now throws a single,
  user-actionable error: *“WalletConnect is temporarily unavailable.
  Please use the Browser Wallet (MetaMask / Rabby) option instead.”*
  The outer `connect('walletconnect')` catch toasts this verbatim, so
  the user sees actionable copy instead of a tech-flavoured browser
  dynamic-import failure bubble.
- **CSP update:** added `https://cdn.jsdelivr.net` to
  `script-src` in `backend/internal/api/rest.go` so the jsdelivr
  fallback is permitted by browser CSP when the esm.sh URLs fail.
  esm.sh stays allow-listed (still works in most regions); jsdelivr
  is the cold-start fallback.
- **Cache-buster bump:** `?v=19` → `?v=20` on the six self-hosted
  scripts (tailwind/htmx/sse/ethers/wallet/qrcode/cdn) plus the
  smoke-test positive needles. Forces returning browsers to
  re-fetch on the next page load so the v23 wallet.js lands ahead
  of any browser-cached v22 build.
- **Verification:** `go build ./...` clean, `go test -race ./...`
  on `internal/api`, `cmd/server`, `internal/auth`, `internal/ui`
  all pass; `TestHomePageInjectsAllRuntimeGlobals` smoke test passes
  with the new `?v=20` needles; manual live QA against
  https://magicwebb.fly.dev/ post-deploy.
- **Status:** FIXED. Branch `feat/v23-wc-multi-cdn`, merged into
  `main` via PR (see commit `312098c` post-v22, follow-up commit
  carries the v23 hashes).

---

## v19 — Frontend (wallet.js / SIWE)

### F-01 — `chainChanged` listener gated to WalletConnect only 🟠 P1 — FIXED
- **Where:** `backend/internal/ui/static/wallet.js`, the provider init
  handler that registers EIP-1193 listeners. **Same anchor as F-02.**
- **Key:** `f01-chainchanged-listener-scope`
- **Scenario:** (historical) Injected providers (MetaMask, Rabby, Brave)
  NEVER received `chainChanged` events because the listener was registered
  through an `if (kind === 'walletconnect')` gate. The current register
  block `if (eip1193?.on) { eip1193.on('chainChanged', _onChain); eip1193.on('accountsChanged', _onAccts); }`
  fires for both kinds.
- **Fix landed:** `_onChain` on injected reloads so the cached
  ethers BrowserProvider is rebuilt; `_onAccts` reloads on injected
  (cached Signer is bound to the prior address) but hot-swaps on WC.
  `_eipHandlers` is the named-ref teardown that prevents listener stacking.
- **Status:** FIXED (verified by the live-test sweep at
  https://magicwebb.fly.dev/ — connection transition is silent, no console
  errors during chain/account switch).

### F-02 — `accountsChanged` listener gated to WalletConnect only 🟠 P1 — FIXED
- **Key:** `f02-accountschanged-listener-scope`
- **Where:** Same `connect()` block as F-01.
- **Status:** FIXED (same verification path).

### F-03 — Silent SIWE failure 🟠 P1 — FIXED
- **Where:** SIWE connect path in `backend/internal/ui/static/wallet.js`,
  `_authenticate()` method.
- **Key:** `f03-silent-siwe-failure`
- **Scenario (historical):** `.catch` swallowed non-recoverable
  exceptions, leaving connect button idle. Today every non-recoverable
  path in `_authenticate()` throws; outer `connect()` `catch` flips
  state→'error' and runs `toast(revertMessage(e), 'error')`. The inner
  half-state cleanup (`this.jwt = null` + clear `mw_jwt` from
  localStorage) means a retry starts clean.
- **Status:** FIXED.

---

## v20 — Solidity contracts

### C-01 (audit-#1) — Anti-snipe accumulation permanently stalls auction 🔴 P0 — FIXED
- **Where:** `contracts/src/AuctionHouse.sol`, function `bid()`.
  Anchored to the constant `EXTENSION_WINDOW = 3 minutes`.
- **Key:** `c01-anti-snipe-accumulation`
- **Status:** FIXED. Verified by
  `contracts/test/AuditFuzz.t.sol::testFuzz_antiSnipe1kLateBids`.

### C-02 (audit-#2) — Seller hijacks stalled delivery; old code rewarded it 🟠 P1 — FIXED
- **Where:** `contracts/src/AuctionHouse.sol`, `settle()` + `settleUnstuck()` + `reclaim()`. Anchored to `STALL_WINDOW = 7 days`.
- **Key:** `c02-stalled-state-recovery`
- **Status:** FIXED. Verified by
  `contracts/test/AuctionHouseSettleSafety.t.sol`.

### C-03 (audit-#3) — Offer refund reverts when bidder is a contract dead to receive ETH 🟠 P1 — FIXED
- **Where:** `contracts/src/OfferBook.sol`, `rejectOffer()` +
  `refundExpiredOffer()` + new `_pushPullRefund()`. Anchored to
  `mapping(address => uint256) public pendingReturns;` declaration.
- **Key:** `c03-offer-pull-fallback`
- **Status:** FIXED. Verified by
  `contracts/test/AuditFuzz.t.sol::test_offerExpiredRefundPushFallback`.

### C-04 (audit-#4) — `refundLosers` unbounded batch + no per-call gas cap 🟠 P1 — FIXED
- **Where:** `contracts/src/AuctionHouse.sol`, `refundLosers()`.
  Anchored to `gas: 50_000` per iteration and `200` ceiling.
- **Key:** `c04-refundlosers-gas-bound`
- **Status:** FIXED (DoS-with-recoverable-state). Verified by
  `contracts/test/AuditFuzz.t.sol::test_refundLosersGriefingHalfBatchDoesNotOOG`.

---

## v21 — Indexer + API + DB (Priority Stack unlock)

The Priority Stack unlocked in v21 — every remaining entry was patched,
compiled, and pushed. Per the audit ledger this closes v21 entirely; new
findings get v22 entries.

### 🔴 P0 `onTransferBatch` — indexer OOG via hostile TransferBatch log — FIXED
- **Where:** `backend/internal/indexer/handlers.go::onTransferBatch()`.
- **Key:** `onTransferBatch`
- **Anchor:** `maxBatchLength = 1024`.
- **Scenario (historical):** idsLen / valsLen decoded from untrusted log
  data; `chunk()` silently zero-padded past the data footprint. A
  hostile `TransferBatch` with `idsLen = type(uint256).max` ran the
  loop the same count of times — each iteration issuing a Postgres
  upsert against the indexer's connection — accumulating queries until
  OOM.
- **Severity rationale:** Single-attacker, attacker-controlled,
  cooperator-free, system-wide DoS of the indexer goroutine. Acquired
  P0 under the audit-grade rule (≥ 50 % of caller base affected for ≥ 1
  minute if a single TransferBatch log was submitted against the
  marketplace contracts).
- **Fix:** Every pointer (`idsOff`, `valsOff`) bound by `dataWords` AND
  `maxBatchLength = 1024` BEFORE the loop. Cross-validation: `idsLen ==
  valsLen`, both ≤ dataWords, both ≤ maxBatchLength, arrays fit within
  data footprint. Hostile input is now dropped as malformed at the
  parser layer, no DB write, no goroutine burn.
- **Verification:** Manual review against the audit's invariants plus
  the existing indexer integration test for legitimate TransferBatch
  events. The maximum legitimate batch on Coston2 mainnet observed
  to date is 256 (Polygon-style airdrops); 1024 is a 4× safety margin.
- **Status:** FIXED. Committed, pushed to `main`.

### 🟠 P1 `processTransfersWallClock` — wall-clock poison on missing block header — FIXED
- **Where:** `backend/internal/indexer/runner.go::processTransfers()`.
- **Key:** `processTransfersWallClock`
- **Scenario (historical):** When a Transfer log's `BlockNumber` wasn't
  already in the `blockTimes` map (because the core-event FilterLogs
  didn't return a log for that block), the code fell back to
  `time.Now().Unix()`. Chain drift between RPC time and wall-clock
  could put sort-by-blockTime queries out of order; downstream
  `sales`/`bids`/`listings` rows with synthetic caused inconsistent
  pagination.
- **Severity rationale:** DoS-with-recoverable-state (single missing
  header stalls one log; next tick re-indexes), chained with subtle
  pagination drift. The `log.Warn + continue` retry pattern in
  `processRange` means the outer drainer re-fetches the failing header
  on the next iteration, capping propagation to a per-block ~2s
  staleness window.
- **Fix:** Per-log `HeaderByNumber` with `context.WithTimeout(ctx,
  2*time.Second)`. On failure: `log.Warn` + `continue`. Successful
  fetches are written back to the `blockTimes` map so the next log of
  the same block within the chunk reuses the cached timestamp (no
  redundant RPC).
- **Status:** FIXED.

### 🟠 P1 `getRecentTxnsLimit` — unbounded Seq Scan across 4 union branches — FIXED
- **Where:** `backend/internal/db/queries.go::GetRecentTransactions()`.
- **Key:** `getRecentTxnsLimit`
- **Scenario (historical):** `LIMIT $1` sat on the outer wrapper. The
  planner materialised FULL windows from every UNION ALL branch before
  the global `ORDER BY at DESC LIMIT $1` materialized. On a busy
  marketplace that's a Seq Scan + in-memory merge sort on every
  `/api/v1/activity` call.
- **Fix:** LIMIT is pushed into each UNION ALL branch via explicit
  parentheses, expressed `ORDER BY <branch's timestamp column> DESC`
  per branch so the planner can use that branch's index on
  (listed_at / occurred_at / placed_at / starts_at). Branch caps at
  $1; outer ORDER BY + LIMIT merges the slices.
- **Status:** FIXED.

### 🟠 P1 `getEffectiveBidsLimit` — OOM rendering a 10k-bid contested auction — FIXED
- **Where:** `backend/internal/db/queries.go::GetEffectiveBids()`.
- **Key:** `getEffectiveBidsLimit`
- **Scenario (historical):** No LIMIT. A contested auction with 10k+
  tiny incremental bids fanned out to the bidder-per-row page; the
  JSON payload plus template render blew up before reaching the client.
- **Fix:** `LIMIT 200`. Realistic active-bidder spectrum tops out well
  under 200; page requests a button to "view all" only when the slice
  hits the cap.
- **Status:** FIXED.

### 🟠 P1 `clientIpSpoof` — XFF rightmost bypass when traffic bypassed proxy — FIXED
- **Where:** `backend/internal/api/rest.go::clientIP()` +
  `backend/cmd/server/main.go` Fiber Config.
- **Key:** `clientIpSpoof`
- **Scenario (historical):** Manual rightmost-XFF extraction was fine
  behind Fly.io, but a request that bypassed the proxy (test, direct
  curl, malicious load balancer) trusted the XFF header verbatim —
  any caller could spoof their rate-limit bucket by sending
  `X-Forwarded-For: 1.2.3.4`.
- **Fix:** Trust hierarchy:
  1. `Fly-Client-IP` (Fly-stamped, unspoofable from outside)
  2. RFC 7239 `Forwarded` `for=` (with `stripAddrPort` for bracketed
     IPv6 + IPv4:port forms)
  3. `X-Forwarded-For` rightmost (legacy fallback)
  4. fasthttp `c.IP()`
  Fiber Config now sets `EnableTrustedProxyCheck: false` +
  `ProxyHeader: "Fly-Client-IP"` so `c.IP()` returns exactly the
  trusted header rather than the raw TCP remote.
- **Status:** FIXED.

### 🟡 P2 `parseWeiHelper` — silent-zero parse failures on schema drift — FIXED
- **Where:** `backend/internal/db/queries.go` (5 rewritten sites).
- **Key:** `parseWeiHelper`
- **Anchor:** `ParseWei(s string) (*big.Int, error)` +
  `ParseWeiOrZero(s string) *big.Int` helpers.
- **Scenario (historical):** `big.Int.SetString(priceStr, 10)` returned
  `false` silently on malformed input, leaving the int at zero. A NULL
  coalesce drift in the `transactions` migration would collide with the
  type assertion and a `0 FLR` floor would mask a real bug.
- **Fix:** Central `ParseWei` returns an explicit error (empty / not
  base-10). `ParseWeiOrZero` is the backward-compatible wrapper that
  warns on truly malformed input and returns 0 — all 5 prior
  `SetString` sites in `GetFloorPrice`, `Get24hVolume`,
  `GetCollectionVolume`, `GetCollectionStatsSince`,
  `GetTrendingCollections` now route through this helper.
- **Status:** FIXED.

---

## Feature flow (v21 — full marketplace walkthrough)

This appendix documents the end-to-end flow for every user-visible action
on MagicWebb. Every line ties back to either a Smart Contract event,
a Backend handler, an Indexer event-write, an SSE broadcast, a UI
modal step, or an automated cron job. Use this as the canonical
onboarding doc for new contributors and the post-incident reference
during customer support.

### A. Wallet connect (`/connect`)

1. **UI:** Navbar → "Connect Wallet" modal opens (Alpine store `modals`).
   Two options: "Injected" (MetaMask, Rabby, Brave) + "WalletConnect v2".
2. **Handler:** `wallet.js :: connect(kind, opts)`. Belt-and-braces
   silent reconnect suppression + 1.5 s double-click debounce.
3. **SIWE:** `auth/nonce?address=` then `personal_sign` round-trip then
   `auth/verify`. Returns JWT (HttpOnly cookie `mw_s_<addr>` +
   Bearer header). Every non-recoverable failure throws; outer catch
   surfaces a typed toast.
4. **Listen:** `refreshUnread()` polls `/api/v1/notifications` for the
   bell badge count.
5. **Storage:** `localStorage.mw_addr` only on **successful** connect.
   Pre-v13 auto-reconnect removed; "Saved wallet" pill is opt-in only.

### B. Fixed-price listing (`/token/:collection/:id` → List)

1. **UI:** Token page → "List for sale" button (Alpine `list(...)`).
2. **WalletJS:** `_approveOperator(coll, MARKETPLACE, 'erc721')`
   triggers a `setApprovalForAll(MARKETPLACE, true)` EIP-1155 or
   `getApproved(id)` check + prompt for ERC-721. `staticCall` preflight
   surfaces on-chain reverts before the user signs.
3. **Tx:** `MARKETPLACE.list(coll, id, priceWei, expiresAt)` →
   `Marketplace.Listed(coll, id, seller, standard, amount, price, expiresAt)`.
4. **Indexer handler:** `onListed()` upserts the `listings` row AND
   seeds `nft_ownership` in a single pgx transaction
   (`UpsertListingAndOwnership`). SSE broadcasts `listing-updated`.
5. **Live update:** Home + listings + token pages receive `listing-updated`
   in milliseconds via the open `/events` connection; they re-render.

### C. Buy (fixed price, `/token/:collection/:id`)

1. **UI:** Token page → "Buy now" button (Alpine `buy(...)`).
2. **WalletJS:** `/api/v1/listings/:coll/:id/preflight?seller=...`
   fetches server-side fillability (`eth_call` to `ownerOf` +
   `isApprovedForAll`). If preflight ok → proceed; if not → fail with
   "This listing is no longer fillable".
3. **Soft preflight:** `staticCall(buy(coll, id, seller, value))`.
   Result is informational only — wallet would surface the same revert
   on the real tx anyway.
4. **Tx:** `MARKETPLACE.buy(coll, id, seller)` with msg.value = price.
   Contract debits `msg.value` → 1.5% fee to `feeRecipient` → 98.5% to
   seller → transfers NFT to buyer. Atomic and final.
5. **Indexer handler:** `onBought()` runs
   `DeactivateAndSale(coll, id, seller, buyer, ...)` (atomic pgx tx).
   Burner notification fires to seller. SSE `listing-updated`.
6. **UI:** Modal step 3 (success) + tx hash link to Coston2 explorer.
   `mw-bought` custom event; owned-list / portfolio refresh.

### D. Auction create + bid + settle (`/auction/:id`)

#### D.1 Create

1. **UI:** "Create auction" modal — reserve, endsAt, minIncrement %.
2. **WalletJS:** `_approveOperator(coll, AUCTION)` →
   `AUCTION.create(coll, id, reserve, endsAt, minIncBps, minIncFlat)`.
3. **Indexer:** `onAuctionCreated` upserts the auction row. SSE `auction-updated`.
4. **Live:** Auction page renders. Anti-snipe banner if `endsAt - now
   < 180 s`.

#### D.2 Bid

1. **UI:** "Place a bid" or "Last-minute bid — extends 3 minutes"
   (banner copy decides from `EXTENSION_WINDOW`).
2. **WalletJS:** `AUCTION.bid(auctionId, { value: bidAmountWei })`.
3. **Indexer:** `onBidPlaced` runs `InsertBidAndUpdateAuction` (atomic
   pgx tx). Cumulative `effective_wei` recomputed on read via the SQL
   view. SSE `auction-updated`.
4. **Leader change:** `onOutbidNotification` records the displaced
   bidder with `notify(kind='outbid')` and SSE `auction-updated`.
5. **Anti-snipe:** If bid lands inside `EXTENSION_WINDOW` AND it takes
   the lead, the contract emits `AuctionExtended(id, newEndsAt)`.
   `onAuctionExtended` updates the `ends_at` column.

#### D.3 Settle (after `endsAt`)

1. **Permissionless:** anyone can call `settle(id)`. Both:
   - **End-user:** the leader (or any connected wallet) hitting the
     "Settle" button on the auction page.
   - **Keeper:** the chain keeper auto-broadcasts `settle` for any
     `ends_at < now()` active auction every 30 s. The keeper key is
     set via `KEEPER_KEY` env; runner holds a Postgres advisory lock
     so only one instance broadcasts at a time (no split-brain).
2. **Contract path:**
   - **Happy:** NFT → winner; 98.5% of `bid_total` → seller; 1.5% fee → recipient.
   - **Seller revoked NFT:** `auction.stalledAt = block.timestamp`;
     `AuctionStalled` event. After `STALL_WINDOW = 7 days` the seller
     can `reclaim(id)` — full escrow refund to winner, NFT returned to seller.
3. **Indexer:** `onAuctionSettled` flips status to `settled`. SSE.
   Loser refund sweep (`runLoserRefundSweeper`) calls
   `refundLosers(id, batch[])` once settled + NOT `losers_refunded`.
   Greedy receivers (no `receive()` fallback) → fall to `pendingReturns`
   mapping → `runWithdrawalSweeper` verifies and notifies.
4. **UI:** Modal "Auction settled" + tx hash + explorer link.
   Profile shows "X wei is waiting in the auction contract — open Withdraw"
   for users with verified pending returns.

### E. Offer make + accept + reject + refund (`/token/:coll/:id`)

#### E.1 Make

1. **UI:** Token page (current owner sees) → "Offers received".
   Non-owner → "Make an offer" button.
2. **WalletJS:** `_approveOperator` (OfferBook) → `OFFERBOOK.makeOffer(
   coll, id, principal, expiresAt, { value: principal })`. Escrow is
   the full principal; the 1.5% fee is **not** charged here.
3. **Indexer:** `onOfferMade` upserts the offer position. Notifies
   current owner. SSE `offer-updated`.

#### E.2 Accept (owner)

1. **UI:** Owner view of an inbound offer → "Accept".
2. **WalletJS:** `_approveOperator(coll, OFFERBOOK)` →
   `OFFERBOOK.acceptOffer(coll, id, bidder)`. Contract debits escrow,
   deduces 1.5% fee from seller, pays seller 98.5%, transfers NFT.
3. **Indexer:** `onOfferAccepted` runs `AcceptOfferAndRecordSale` (atomic
   pgx tx). Notifies bidder. SSE `offer-updated`.

#### E.3 Reject (owner)

1. **UI:** "Reject" → full escrow refund to bidder via
   `_pushPullRefund`. If the bidder is a contract without `receive()`,
   the credit lands in `pendingReturns`.
2. **Indexer:** `onOfferRefunded` flips status. SSE.

#### E.4 Auto-refund (expired, no manual action)

1. **Indexer**: every 60 s `runOfferKeeper` calls
   `OFFERBOOK.refundExpiredOffer(coll, id, bidder)` for any offer where
   `expires_at < now() AND status='pending'` AND we haven't already
   refunded in the last 2 min (throttle).

### F. Auction refund pull (withdraw my refund)

If a settlement's push failed (greedy receiver, OOG, etc.) the credit
sits in `AuctionHouse.pendingReturns(address)`.

1. **Indexer:** `onRefundPushed` / `onLoserRefunded` seed the
   `pending_withdrawals` table.
2. **Sweeper:** every 2 min `runWithdrawalSweeper` calls
   `pendingReturns(addr)` via `eth_call` and verifies the amount.
   First verification → notification dispatch.
3. **User:** profile page shows the verification banner → "Withdraw
   refund" → `AUCTION.withdrawRefund()`. Sweeper's row vanishes when
   `pendingReturns` reads as zero.

### G. Notifications (real-time feed)

Every backend event handler (`onBought`, `onBidPlaced`,
`onAuctionSettled`, `onOutbidNotification`, `onLoserRefunded`,
`onRemoveOfferReceived`, etc.) writes an in-app notification row +
dispatches a typed CustomEvent over SSE (`mw-notification`). The
`bell badge` reads `/api/v1/notifications` on connect + every SSE
`notification` message.

### H. Trending / score recompute

Every 60 s `runScoreWorker` recomputes trending collections over the
windows `1h` / `24h` / `7d`. Inputs: `nft_tokens.views`,
`bids.placed_at`, `sales.occurred_at`. Output: `trending_scores`.

### I. Metadata / image fetch (offline)

Two paths:
- **Lazy, on click:** `POST /api/v1/img/retry` synchronously pulls +
  SHA256-stashes. Doubles as user-triggered self-host.
- **Background:** `runImageRetryWorker` (60 min cadence) bulk-walks
  every tracked token's image_uri, downloads to local S3-equivalent
  (imagestore), updates `nft_metadata.image_uri` to the local path.

### J. Search

`/api/v1/search?q=...` runs Postgres full-text search against
`nft_tokens.search_vec` + `collections.search_vec`. LIMIT pushed into
each UNION ALL branch (mirrors `getRecentTxnsLimit`).

### K. Profile + reports

- `GET /api/v1/profile/:addr` — public.
- `PUT /api/v1/profile/:addr` — JWT-protected, updates the user's
  profile row.
- `POST /api/v1/reports` — JWT-protected, creates a moderation report
  on a target type/id with a reason + detail.

---

## Verification matrix (post-v21)

| ID      | Tier | Key (semantic)                          | Status                                  | Verified by                                                                              |
|---------|------|-----------------------------------------|-----------------------------------------|------------------------------------------------------------------------------------------|
| F-01    | 🟠 P1 | `f01-chainchanged-listener-scope`       | FIXED                                   | wallet.js connect(); manual + browser tests on magicwebb.fly.dev                          |
| F-02    | 🟠 P1 | `f02-accountschanged-listener-scope`    | FIXED                                   | wallet.js connect(); manual + browser tests on magicwebb.fly.dev                          |
| F-03    | 🟠 P1 | `f03-silent-siwe-failure`               | FIXED                                   | wallet.js _authenticate(); manual + browser tests on magicwebb.fly.dev                     |
| C-01    | 🔴 P0 | `c01-anti-snipe-accumulation`           | FIXED                                   | `AuctionHouse.bid()` gated on `EXTENSION_WINDOW = 3 minutes`; `AuditFuzz.t.sol::testFuzz_antiSnipe…` |
| C-02    | 🟠 P1 | `c02-stalled-state-recovery`            | FIXED                                   | `AuctionHouse.settle()` + `settleUnstuck()` + `reclaim()`, gated on `STALL_WINDOW = 7 days`; `SettleSafety.t.sol` |
| C-03    | 🟠 P1 | `c03-offer-pull-fallback`               | FIXED                                   | `OfferBook.rejectOffer()` + ... + `_pushPullRefund()`; `AuditFuzz.t.sol::test_offerExpired…` |
| C-04    | 🟠 P1 | `c04-refundlosers-gas-bound`            | FIXED                                   | `refundLosers()` `BatchTooLarge()` + per-iteration `gas: 50_000`; `AuditFuzz.t.sol::test_refundLosers…` |
| onTransferBatch | 🔴 P0 | `onTransferBatch`              | FIXED                                   | `indexer/handlers.go::onTransferBatch` bounded by `maxBatchLength = 1024` (constant) + `dataWords` + cross-checks; reviewed against hostile `idsLen=2**256` log invariant. |

Open items remaining after v21 (none — the Priority Stack is fully
worked through). The next audit round (v22) will accept new findings
under the standard `v22-…` prefix.

---

## How to update this doc

1. **Append on merge** with a *new semantic key* in the same series
   (`fXX-…`, `cXX-…`, or no-prefix for Priority Stack).
2. **Severity only changes** on a confirmed regression or a new
   attacker path surfacing.
3. **Status transitions require a verification artefact:** passing
   `forge test` (contracts), passing `go test -race ./...` (backend),
   or a documented manual QA procedure **with reproduction steps**
   (frontend — `wallet.js` has no automated cover right now).
4. **Priority Stack ordering** reflows freely; numeric prefixes are
   not used so PR descriptions and commit messages don't go stale on
   reorder.

## Prior audit context (v9–v18)

Audit-driven commits before this ledger's v19 cutoff live in git
history. They were all UI-only fixes — wallet-button visibility at
every breakpoint, chain-switch re-throw, Alpine-init SyntaxError
unwedge, `?v=10` cache-bust on assets, removed silent auto-reconnect
(saved wallet becomes explicit-consent pill), etc. They are NOT
represented as rows in this ledger because:

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
- `docs/USER_GUIDE.md` — end-user-action walkthrough.
- `docs/FEATURE_FLOWS.md` — backend-event-source map (auto-generated).
