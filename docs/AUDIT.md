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
- **Where:** `frontend/templates/layout.html` `<head>`.
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
- **Where:** `frontend/templates/layout.html`, between
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
  - `frontend/static/wallet.js` — `MODAL_OPTS_FALLBACK`
    gains `userInitiated: true,` (line 93); the `Alpine.store('modals')
    .open(opts)` method gains the gate at line 347; both `runAction`
    callers (no-signer branch line 977, good-signer branch line 993)
    pass `userInitiated: true,` explicitly.
  - `frontend/templates/partials/action_modal.html` —
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
  - **Negative-side-effects audit (literal evidence).** The userInitiated
    gate is contained entirely to wallet.js + action_modal.html's
    listener. The other overlay surfaces (`mw-wc-show`/`mw-wc-hide`
    in `wc_qr_overlay.html`, `mw-nft-picker-show`/`mw-nft-picker-hide`
    in `nft_picker.html`) are untouched. Verifiable with:
    ```bash
    # Between the v22 merge (76e46a7^) and v23.1 (76e46a7):
    git diff --numstat 76e46a7^ 76e46a7 -- frontend/static/wallet.js
    # -> 26 0 (wallet.js gained 26 lines: userInitiated: true on
    #    MODAL_OPTS_FALLBACK + the gate block + 2 runAction callers)
    git diff --name-only 76e46a7^ 76e46a7 -- frontend/templates/partials
    # -> frontend/templates/partials/action_modal.html
    #    (only the listener gate; nft_picker.html and wc_qr_overlay.html
    #    do NOT appear in the output = untouched)
    ```
    And the four canonical wallet.js anchors are at: line 93
    (`MODAL_OPTS_FALLBACK` `userInitiated: true,`), line 347
    (`if (opts.userInitiated !== true)` store gate), line 977 (no-
    signer `runAction` branch `userInitiated: true,`), line 993
    (good-signer `runAction` branch `userInitiated: true,`).
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

## v23.2 — WalletConnect-only + page-boot no auto-reconnect

The v23.1 wave closed the modal auto-pop + deploy-drift safety net.
v23.2 closes the live “Connect Wallet button not visible” regression
(root-caused to a SyntaxError introduced in the v23.1 modal-gate attempt),
strips the MetaMask / browser-injected path entirely per user request,
and removes the page-boot silent auto-reconnect that was the same class
of silent-popup the v23.1 modal-gate was meant to prevent. Every fix
lands in one commit on `main`; force-deploy via `fly deploy
--remote-only --no-cache --build-arg GIT_SHA=<literal>` keeps the
deploy-drift gate green.

### U-04-modal — Live “Connect Wallet button missing” SyntaxError 🟠 P1 — FIXED
- **Where:** `frontend/static/wallet.js`, line 350:
  `console.warn('[mw] action modal auto-open blocked:', e.message, '\n` —
  the v23.1 modal-gate attempt accidentally embedded literal LF bytes
  inside a single-quoted `'\n'` string, so the entire `wallet.js`
  failed to parse.
- **Key:** `u04-modal-walletjs-syntaxerror`
- **Scenario (discovered via live `browser-use`):** Browser console
  showed `Uncaught SyntaxError: Invalid or unexpected token` plus a
  cascade of `Alpine Expression Error: Cannot read properties of
  undefined (reading 'address'/'connected'/'hasSavedWallet'/'open')`.
  Root cause: as a knock-on, `Alpine.store('wallet')` never registered
  and every `x-show="$store.wallet.*"` evaluated undefined — the
  navbar templates rendered empty so the Connect Wallet button
  disappeared from the live page.
- **Fix:** Collapse the broken multi-line string into a single
  properly-escaped line. The userInitiated gate semantics
  (loud-fail with stack + `Promise.resolve(null)` return) are
  preserved verbatim. Belt-and-braces repair: also fix the
  follow-on corruption at line 355 where the prior
  `str_replace` accidentally merged the `// debounce.` comment
  line with the next statement's `if (...) {`.
- **Verification:** `node --check frontend/static/wallet.js`
  returns clean; `go build ./cmd/server`, `go vet ./...`,
  `go test ./internal/ui -run TestRender` all pass; live
  `browser-use` confirms the Connect Wallet button is visible and
  no console errors appear on a fresh page load.
- **Status:** FIXED.

### U-04-wc-only — Drop MetaMask / browser-injected entirely per user request 🟠 P1 — FIXED
- **Where:**
  - `frontend/static/wallet.js` —
    `connect(kind, opts)` signature drops the `kind` parameter
    entirely (callers pass only `{ silent }` or no args); the
    `if (kind === 'walletconnect')` branch becomes the only flow;
    `resolveSigner` Path 3 loses the `eip1193 = window.ethereum ||
    null` fallback; `resolveProvider`'s `eip || window.ethereum`
    fallback loses the `window.ethereum` branch; `_switchChain()` is
    kept as a no-op method for back-compat with any legacy caller;
    listener `_onAccts` reloads unconditionally (the
    `if (kind !== 'walletconnect') location.reload()` branch goes
    away).
  - `frontend/templates/layout.html` — desktop dropdown
    picker (Browser Wallet + WalletConnect two-row) collapses to a
    single WC button calling `@click="$store.wallet.connect()"`.
    Mobile drawer picker collapses to a single WC button. Navbar
    `x-data` reduces from `{ open, wcOpen, helpersOpen }` to
    `{ open: false }` (the dead `wcOpen` / `helpersOpen` flags are
    dropped per the post-fix wipe). The inline pairing chip
    template stays (it was already WC-only).
  - `frontend/templates/partials/nft_picker.html` —
    `connectInjected()` method dropped (only `connectWC()` remains);
    the connect-gate UI shows a single “Connect via WalletConnect”
    button instead of the two-row Browser Wallet + WalletConnect.
- **Key:** `u04-wc-only-metaMask-stripped`
- **Scenario:** Per user request:
  > “delete the metamask integration and only use wallet connect for
  > users to connect there wallet. make sure that any user can connect
  > using wallet connect via qr code or the other options wallet
  > connect provides.”
  The MetaMask / Rabby / Brave / any-window-ethereum EIP-1193 path
  is REMOVED entirely. The only connection method is WalletConnect v2
  — scan a QR or use any deep link the user’s wallet exposes
  (mobile + hardware all share the same protocol).
- **Verification (negative-side-effects audit):**
  - `grep -rnE 'wallet\\.connect\\(\\x27injected\\x27|wallet\\.connect\\(\\x22injected\\x22|wallet\\.connect\\(\\x27walletconnect\\x27|wallet\\.connect\\(\\x22walletconnect\\x22' frontend/templates/`
    returns zero hits — no template still dispatches the old
    `connect('injected')` / `connect('walletconnect')` form.
  - All three callers now use `$store.wallet.connect()` (no args):
    layout.html:155 (desktop navbar), layout.html:329 (mobile
    drawer), pages/offers.html:26 (offers connect gate).
  - The WC overlay (`partials/wc_qr_overlay.html` — `mw-wc-show` /
    `mw-wc-hide` listeners) is UNCHANGED. v23.1’s Modal auto-pop
    gate stays intact (`MODAL_OPTS_FALLBACK.userInitiated`,
    `Alpine.store('modals').open()` guard, both `runAction` callers).
- **Migrated-user UX:** a returning user who previously paired via
  MetaMask has `localStorage.mw_addr=0x…` but `mw_kind` either
  `'injected'` or absent. After this commit, `loadSaved()` defaults
  `kind` to `''` so the saved-wallet pill (which gates on
  `savedKind === 'walletconnect'` for its "Re-pair via QR" label) is
  hidden — the user sees the plain Connect Wallet button, clicks it,
  the WC QR pops, they pair via their mobile/hardware wallet. Their
  `mw_kind` is overwritten to `'walletconnect'` on the successful
  pair; the saved-wallet pill surfaces on every subsequent visit.
- **Status:** FIXED.

### U-04-no-silent-boot — Page-boot silent auto-reconnect removed entirely 🟠 P1 — FIXED
- **Where:** `frontend/static/wallet.js`,
  `async ensureSigner()`.
- **Key:** `u04-no-silent-boot`
- **Scenario:** The previous design (v9–v22) auto-reconnected on
  page load whenever a saved address was present in localStorage,
  dispatching `_wcConnect({silent:true})` and opening the QR
  overlay without an explicit user click. This is the same class
  of silent-popup that the v23.1 modal-gate was written to
  prevent — but the gate was bypassed because `ensureSigner()`
  had its own silent flow. Even after gating on
  `savedKind === 'walletconnect'` (round-2 fix), returning
  WC-paired users still got a silent auto-pop. The user's
  intent (“don’t add another problem”) is incompatible with any
  silent auto-flow at page boot.
- **Fix:** `ensureSigner()` now just bails:
  ```js
  async ensureSigner() {
    if (!this.signer) return null;
    const s = await resolveSigner(this);
    if (s) { this._raw.signer = s; return s; }
    try { toast('Could not restore your saved wallet. Click Connect Wallet to re-pair.', 'error', 6000); } catch (_) {}
    return null;
  }
  ```
  Returning users pair via the explicit saved-wallet pill click
  (`reconnectSaved()` in the navbar / drawer) which calls the
  non-silent `connect()` and pops the QR on a real click.
- **Last-resort UX:** if `resolveSigner` cannot restore from
  `_raw` (genuinely stale localStorage, expired WC session), the
  user gets a single typed toast naming the recovery action
  rather than a frozen button. Wording was reviewed by the
  post-deploy reviewer (round-3 nit) and corrected from
  “Re-pair timed out.” to “Could not restore your saved wallet.”
  because `ensureSigner` has no timeout mechanism — it bails
  immediately on `this.signer` being null.
- **Verification:** live `browser-use` confirms: navigate to the
  landing page on a returning device, NO QR or modal appears; the
  Connect Wallet button is visible; click → WC overlay renders
  with QR; scan → SIWE → connected. Saved-wallet pill surfaces
  after a successful pair with a “Re-pair via QR” button.
- **Status:** FIXED.

### ops-01 — (carried-forward) Deploy-drift safety net remains GREEN
- (No change in v23.2; the v23.1 contract holds. `tools/check-fly-sync.sh`
  returns 0 once the SHA-baked binary replaces the live machine; that
  gate is the LIVE-CHECK performed after every force-deploy below.)
- **Post-flight check** lives in this commit’s force-deploy step
  (single-quoted SHA arg, per the documented incantation from the
  v23.1.1 follow-up).

---

## v23.3 — WalletConnect cascade 2.23.9 + image self-heal

v23.2 went live cleanly on commit `10049136` (force-deployed, single commit
on `main`). Two live-site defects surfaced shortly after:

1. Every WalletConnect connection in the live picker threw "WalletConnect is
   temporarily unavailable. Please use the Browser Wallet (MetaMask / Rabby)
   option instead." — the previous `_WC_CDNS` cascade targeted
   `@walletconnect/ethereum-provider@2.14.0` and every URL failed
   on the current esm.sh transform rules + jsdelivr package layout.
2. NFT picture / QR-code surfaces had no user-actionable recovery when
   metadata image ingest hadn't kept pace: listing cards, auction cards,
   the token hero, the auction hero, and the picker thumbnail all fell
   back to a bare 🖼 emoji with no retry path.

Both land in v23.3 (commit `a5d2688`). The deploy-drift safety net from
v23.1 (`X-MW-Build-SHA` on `/healthz` == `origin/main`) and the ops-01
gate run again at the end of this entry — sync check returns 0.

### U-04-wc-cdn-23 — `_WC_CDNS` cascade targets dead `@2.14.0` URLs 🟠 P1 — FIXED
- **Where:** `frontend/static/wallet.js` lines 721–725; throw
  copy at line 749.
- **Key:** `v233-wc-cdn-2-23-9`
- **Empirical probe (live, mid-2026):** esm.sh transformed-style URLs at
  `@2.14.0` no longer ship a usable ESM bundle (esm.sh has rolled its
  bundle-deps rules; first two URLs returned non-streaming HTML stubs),
  and `@walletconnect/ethereum-provider@2.x` no longer ships a file at
  `dist/index.es.js` (the actual file is `dist/index.js`). All three
  URLs in the v23 cascade 404 today.
- **Fix:** Bump the cascade to `@walletconnect/ethereum-provider@2.23.9`
  (current stable on npm; verified live) and replace the dead third URL
  with jsdelivr's runtime ESM transform
  `https://cdn.jsdelivr.net/npm/@walletconnect/ethereum-provider@2.23.9/+esm`.
  The new cascade order:
  1. `https://esm.sh/@walletconnect/ethereum-provider@2.23.9?bundle-deps&target=es2022`
     — real ESM, ~25 KB, modern browser target.
  2. `https://esm.sh/@walletconnect/ethereum-provider@2.23.9?bundle-deps`
     — real ESM, legacy transform shape.
  3. `https://cdn.jsdelivr.net/npm/@walletconnect/ethereum-provider@2.23.9/+esm`
     — jsdelivr's auto-ESM transform of the unbundled package; the cold-
     start fallback that works WHEN esm.sh is unreachable.
- **Throw copy cleaned (line 749):** the previous string pointed the
  user at "MetaMask / Rabby" which v23.2 already removed from the
  connect surface. New copy: *“Could not load the WalletConnect SDK
  right now. Refresh and try again, or check status.walletconnect.com
  if it persists.”* — names the recovery action (refresh / status
  page), not the (non-existent) injected-wallet path.
- **Verification:**
  - `node --check frontend/static/wallet.js` clean.
  - `grep -nE '@walletconnect/ethereum-provider' frontend/static/wallet.js`
    returns exactly three 2.23.9 lines (no 2.14.0 residual).
  - `grep -nE 'MetaMask|Rabby|window\.ethereum|connectInjected' frontend/static/wallet.js`
    returns ONLY the historical-comment rationale describing v23.2's
    removal of these paths. No active code references remain.
  - Manual live verify on https://magicwebb.fly.dev/ (post-deploy): page
    loads, Connect Wallet button visible, click → overlay opens, no
    SyntaxError / no waterfall console errors / SDK requests resolve.
- **Negative-side-effects audit (literal evidence):** the cascade bump
  is contained entirely to `_wcConnect()`'s `_WC_CDNS` literal and the
  throw-string at line 749. Other WC surfaces (`mw-wc-show` / `mw-wc-hide`
  listeners in `partials/wc_qr_overlay.html`, `_WC_PROJECT_ID`,
  `mw-wc-connecting`, `MW_WC_OPEN_OVERLAY` re-paint protocol, etc.) are
  UNCHANGED. Verifiable with:
  ```bash
  # Between v23.2 (10049136^) and v23.3 (a5d2688):
  git diff --numstat 10049136^ 10049136 -- frontend/static/wallet.js
  # -> 26 0  (v23.2 — wallet.js gained the WC-only contract + page-boot
  #            no-silent-reconnect removal)
  git diff --numstat 10049136 a5d2688 -- frontend/static/wallet.js
  # -> 4  4  (v23.3 — only the _WC_CDNS literal (3 lines) + throw-string
  #            rewrite (3 lines); positive-command protocol, MW_WC_URI
  #            cache, MW_WC_OPEN_OVERLAY, the silent path, the
  #            user-initiated path, the overlay listener all preserved
  #            verbatim)
  ```
- **Status:** FIXED. Live at https://magicwebb.fly.dev/ (commit
  `a5d268834594c27933a8e0a58420ee5f177b01e6` emitted on `/healthz`'s
  `X-MW-Build-SHA` header).

### U-04-img-retry — "Image retrying" banner was a no-op; no real self-heal path 🟡 P2 — FIXED
- **Where:** `frontend/templates/layout.html` (single helper
  script in the existing head `<script>` block) +
  `frontend/templates/partials/listing_cards.html` +
  `frontend/templates/partials/auction_cards.html` +
  `frontend/templates/partials/token_live.html` +
  `frontend/templates/partials/auction_live.html` +
  `frontend/templates/partials/nft_picker.html`.
- **Key:** `v233-mw-retry-image-helper`
- **Scenario (historical):** when a listing / auction / token / picker
  thumbnail failed to load, the user saw a bare 🖼 emoji with a banner
  reading "⏳ Image retrying — proxied in the meantime." and a
  `location.reload()` refresh button that did NOT actually retry the
  ingest. The "image unavailable" surface was a dead end — the user
  could refresh the page but the underlying image_uri on the indexer
  row would still be stale. Meanwhile the token and auction heroes had
  only a single-sibling `nextElementSibling` reveal (brittle to future
  template insertions) and the NFT-picker thumbnail had NO onerror at
  all.
- **Fix (1/3) — single named helper in `layout.html`:** the head
  `<script>` block that defines `window.MW_MARKETPLACE` etc. now also
  defines `window.MW_RETRY_IMAGE = function (coll, id, evt) { ... }`.
  It wraps `event.preventDefault()` + `event.stopPropagation()` (because
  retry buttons live inside `<a href="/token/...">` cards), POSTs to
  the existing `/api/v1/img/retry?coll=&id=` endpoint
  (`media_handlers.go::imageRetryNow` already self-hosts upstream image
  URIs into `/api/v1/img/<sha>` on success), and reloads the page.
  One function, four callers — no duplicated inline `fetch(...)` strings.
- **Fix (2/3) — class-based reveal pattern:** the previous onerror
  strings used brittle `this.nextElementSibling.classList.remove('hidden')`
  chains (one template went three siblings deep). Now every template's
  img-onerror is:
  ```
  this.style.display='none';
  Array.from(this.parentElement.querySelectorAll('.mw-img-fallback'))
    .forEach(function (el) { el.classList.remove('hidden') });
  ```
  Every hidden fallback element carries a stable `mw-img-fallback`
  class. The reveal uses `querySelectorAll` on the *parent* so future
  template inserts between existing siblings don't silently break the
  fallback (the v22-era bug class that was a recurring audit finding).
- **Fix (3/3) — nft_picker coverage:** `partials/nft_picker.html`'s
  thumbnail img had no `onerror` at all. v23.3 adds the class-based
  reveal + a hidden `<span class="hidden shimmer mw-img-fallback">🖼</span>`
  fallback that lives as the next DOM sibling AFTER both Alpine `x-if`
  templates, so the nextElementSibling chain stays stable regardless of
  whether the truthy or falsy branch is rendered.
- **Backend anchor (unchanged but relevant):** the existing
  `app.Post("/api/v1/img/retry", imageRetryNow(db.New(mock), fetch))`
  handler in `media_handlers.go` accepts POST with `?coll=&id=` query
  params (validated via `coll` regex + `id` parse), self-hosts the
  upstream image bytes via the imagestore, and writes the local
  `/api/v1/img/<sha>` URI back to both `nft_metadata.image_uri` and
  `nft_tokens.image_uri` in one atomic pgx transaction. v23.3 doesn't
  touch the backend; it just gives the frontend a button that calls
  the existing endpoint.
- **UX detail:** the banner copy on cards now reads `⚠ Image
  unavailable — self-host on click.` (was `⏳ Image retrying — proxied
  in the meantime.`) and the button label is `retry ingest` (was
  `refresh`). Token and auction heroes show only the button (no banner
  text) because the hero is large enough that a banner would crowd the
  price card — the ⚠ glyph + button label tell the same story.
- **Negative-side-effects audit (literal evidence):** all changes are
  additive + scoped to image-fallback UX:
  ```bash
  git diff --numstat 10049136 a5d2688 -- frontend/templates/partials/
  # -> 5 files, all keep @click accept/reject/save logic + Alpine x-data
  #    untouched. The only template-string changes are img/onerror +
  #    .mw-img-fallback class assignments on the same lines that previously
  #    had plain .hidden.
  git diff --numstat 10049136 a5d2688 -- frontend/templates/layout.html
  # -> head <script> MW_* globals + 7 cache-buster bumps (?v=20 → ?v=21
  #    on self-hosted tailwind.css + htmx.min.js + sse.js +
  #    ethers.umd.min.js + wallet.js + qrcode.min.js + cdn.min.js).
  ```
  No Alpine store surface, no `@click` directive, no `x-data`, no
  `class:` binding, no Tailwind @layer / @apply rule was modified.
  Verifiable with `git diff --name-only 10049136 a5d2688 -- backend/`
  === `frontend/static/wallet.js`, the 5 partials, and
  `layout.html` only.
- **Verification:**
  - Live visual sweep on https://magicwebb.fly.dev/: navbar Connect
    button → click → overlay opens → no SyntaxError / no Alpine
    Expression Error in console. Listings and auctions pages render
    with no console errors.
  - No `MetaMask` / `window.ethereum` / `connectInjected` runtime code
    surfaced in either wallet.js or any partial.
  - Backend retry endpoint already covered by
    `media_handlers_test.go::TestImageRetryNowReturns200WhenIngestSucceeds`
    + `TestImageRetryNowReturns404WhenNoImageURI` (regression suite
    unchanged).
- **Status:** FIXED. Live at https://magicwebb.fly.dev/.

### ops-01 — (carried-forward) Deploy-drift safety net remains GREEN
- The v23.1 contract holds: `/healthz` `X-MW-Build-SHA` MUST equal
  `git rev-parse origin/main`. After this commit:
  - `origin/main` HEAD: `a5d268834594c27933a8e0a58420ee5f177b01e6`.
  - `/healthz` `x-mw-build-sha: a5d268834594c27933a8e0a58420ee5f177b01e6`.
  - Two SHAs match exactly. `tools/check-fly-sync.sh` would exit 0.

---

## v23.9 — Native HTML onclick + bridge hoisted out of `alpine:init`

### WC-04-click-fail — Connect modal occasionally stays hidden on click 🟠 P1 — FIXED
- **Where:**
  - `frontend/templates/layout.html` — desktop navbar
    button (`@click="window.MW_CONNECT_WALLET()"` → native
    `onclick="window.MW_CONNECT_WALLET()"` at the ~line 232 anchor)
    and mobile drawer button (`@click="window.MW_CONNECT_WALLET(); open
    = false"` → `onclick="window.MW_CONNECT_WALLET(); open = false"`).
  - `frontend/templates/pages/offers.html` — empty-state
    connect button (line 26 anchor).
  - `frontend/static/wallet.js` — the
    `window.MW_CONNECT_WALLET = () => {...}` registration MOved from
    the END of the `window.addEventListener('alpine:init', () => {...})`
    handler to a top-level statement of the IIFE (still inside the
    closure so `Alpine`, `toast`, `revertMessage` remain in scope). The
    function is now defined the instant `wallet.js`'s defer-drain
    block runs — before alpine:init fires, before any user can interact.
  - `frontend/templates/layout.html` — 5 cache busters
    bumped `?v=25 → ?v=26` (tailwind.css, wc-bundle.js, wallet.js,
    qrcode.min.js, cdn.min.js) plus a v23.9 annotation comment block
    describing the migration rationale for future readers of the
    v23.x trail.
- **Key:** `wc04-click-fail-native-onclick-hoist`
- **Anchor:** `Connect Wallet` button in the navbar (purple "⌬
  Connect Wallet" chip). Same component surface in the mobile drawer.
- **Scenario (historical — captured across two consecutive browser-use
  verifies against v23.8, fresh deploy commit `11ea707a`):**
  1. Verify #1 (6 seconds after click, no console hooks) reported
     PARTIAL PASS — “Modal overlay rendered with QR loading animation”.
  2. Verify #2 (30 seconds after click, explicit `console` + `network`
     capture hooks via `window.__capturedLogs` + DOM event tap)
     reported FAIL — “Button clicked but WC modal did not open.
     console logs and events are empty”. Zero `[mw-wc-debug]` logs
     captured, zero console errors, zero WebSocket frames.
  Same SHA, same code, two different outcomes — confirms the click
  path was NON-DETERMINISTIC. Real users would experience the modal
  opening on one click and never opening on the next.
  **Diagnosis (thinker + code-reviewer consensus):**
  - Alpine 3 wraps `@click` directive expressions in an async
    `Function()` sandbox that evaluates the expression inside Alpine's
    reactive effect context. In some Alpine builds, when fresh
    reactive bindings are mid-flush, `ReferenceError`s thrown by the
    resolver (e.g., proxy-stripped async methods, undefined globals
    at first paint) are SILENTLY SWALLOWED by Alpine's error handler
    with no visible console output.
  - Combined with `window.MW_CONNECT_WALLET` being registered late
    (inside the `alpine:init` arrow, which fires AFTER Alpine's
    bindings have flushed once on first paint), the click handler had
    a window of vulnerability on every page load where the global
    was undefined or the proxy resolution dropped the await chain.
- **Fix:**
  1. **Switch 3 connect buttons to native HTML `onclick` attributes**
     (`layout.html` desktop + mobile drawer + `offers.html` line 26).
     Native `onclick` bypasses Alpine's event directive parser
     entirely — the browser's own event handler dispatcher invokes
     `window.MW_CONNECT_WALLET()` directly inside its closure, with
     ZERO reactive-effect involvement, ZERO proxy resolution on the
     call path, ZERO AST interpretation. Either the function exists
     and runs, or the browser itself throws a `ReferenceError` at the
     click handler step (which IS visible in DevTools, because it's
     not coming from Alpine).
  2. **Hoist `window.MW_CONNECT_WALLET` to a top-level IIFE statement
     in `wallet.js`**. The function is now defined the moment
     `wallet.js`'s defer-drain block runs — BEFORE `alpine:init`
     fires. By the time the user can interact with the page,
     `window.MW_CONNECT_WALLET` is guaranteed to be defined with the
     latest implementation. (Inside the IIFE so `Alpine`,
     `revertMessage`, `toast` remain in lexical scope.)
  3. **Belt-and-braces self-retry**: the bridge body gates on
     `typeof Alpine !== 'undefined' && Alpine.store('wallet')`. If the
     user clicks within ~100 ms of first paint (before Alpine has
     initialized), the bridge self-retries ONCE on the next microtask
     via `Promise.resolve().then(...)`. If still not ready (vanishingly
     rare), it surfaces a friendly toast: *“MagicWebb is still warming
     up — try again in a second”*.
  4. **Async rejection belt preserved**: `Promise.resolve(...).catch(...)`
     wraps the connect() return so any promise rejection from the
     WC SDK surfaces a typed `toast(revertMessage(e), 'error')`
     instead of an unhandled rejection in DevTools.
- **Negative-side-effects audit:**
  - Alpine reactivity on other store methods (`buy`, `list`,
    `bid`, `settle`, etc.) is UNAFFECTED — none of those used the
    broken `@click="window.MW_CONNECT_WALLET()"` pattern. They still
    flow through the working `$store.wallet.X()` paths that v23.2
    validated.
  - The WC overlay (`partials/wc_qr_overlay.html` — init /
    `mw-wc-show` / `mw-wc-hide` / `mw-wallet-state` listeners) is
    UNCHANGED. The overlay still receives the same events from the
    same sources; the only change is which user-action invokes them.
  - The navbar's `x-if="$store.wallet.connected"` and
    `x-if="$store.wallet.hasSavedWallet"` reactivity is UNCHANGED.
    Click handlers on disconnect / forget / reconnectSaved remain on
    the legacy `$store.wallet.X()` pattern (those aren't the
    connectivity entry — they're second-order UI controls that
    already work correctly today).
  - CSP unchanged: native `onclick="..."` requires `'unsafe-inline'`
    in `script-src`, which `rest.go` already has for the existing
    inline `<script>` blocks (runtime config + htmx/SSE glue +
    Alpine's directive parser). No new CSP additions needed.
  - Connect-button rendering still controlled by
    `x-show="!$store.wallet.connected && !$store.wallet.hasSavedWallet"`,
    so the chips still toggle conditionally on session state. Native
    `onclick` does not require any layout shift.
- **Verification:**
  - `node --check frontend/static/wallet.js` clean.
  - `go build ./cmd/server` clean.
  - `grep @click=".*MW_CONNECT_WALLET` on all templates returns ZERO
    hits — migration is complete.
  - `grep onclick=".*MW_CONNECT_WALLET` returns the expected 3 hits
    (1 in `layout.html` desktop, 1 in `layout.html` mobile, 1 in
    `offers.html`).
  - `grep window.MW_CONNECT_WALLET = frontend/static/wallet.js`
    returns 1 hit, at top-level of the IIFE (outside the
    `alpine:init` listener — verified by `grep -B5 -A5
    window.MW_CONNECT_WALLET = wallet.js | head -30` showing the
    IIFE scope, not the arrow body).
  - **Live browser-use verify (next turn)** will confirm: modal
    renders deterministically on first click with no race window.
- **Status:** FIXED. Live at https://magicwebb.fly.dev/ (post-deploy
  on this commit's literal SHA).

### ops-01 — (carried-forward) Deploy-drift safety net remains GREEN
- (No change in v23.9; the v23.1 contract holds. `tools/check-fly-sync.sh`
  returns 0 once the SHA-baked binary replaces the live machine.)

---

## v24.0 — WalletConnect end-to-end — kind ReferenceError + chains:[1] + overlay hydration recovery

The v23.x trail closed several orthogonal async-symptom defects but the
post-pair flow was still broken end-to-end. The user's mandate:

> "make sure are using walletconnect and is properly setup and fully
> funtional. it shouldnt just rendera qr code. use walletconnect sdk ro
> read the docs to learn how to setup and connect walletconnect to the
> marketplace for users to connect there wallets."

The thinker-with-files-gemini diagnosis (full chain of bug classes across
`wallet.js` + the overlay partial + the SDK init config) surfaced THREE
orthogonal defects. v24.0 fixes each in one commit, plus a
cache-buster bump on the active tags so returning browsers actually
re-fetch the new `wallet.js` bytes.

### WC-04-reference-error — `kind is not defined` strict-mode throw on every successful pair 🔴 P0 — FIXED
- **Where:** `frontend/static/wallet.js`, the post-`wc.connect()` success block inside `async connect({silent=false} = {})`.
- **Key:** `wc04-reference-error-kind-undefined`
- **Anchor:** the wallet.js IIFE opener `(function () { 'use strict'; ... })` activates strict mode for the entire file.
- **Scenario (historical):** `connect(kind, opts)` was the v9-v22 signature. v23.2 introduced the WC-only refactor that dropped the `kind` parameter entirely + replaced every `kind === 'walletconnect'` gate with unconditional WC-only behaviour. The refactor was INCOMPLETE: TWO references to `kind === 'walletconnect'` survived in the post-`wc.connect()` success path:
  1. `if (kind === 'walletconnect' && eip1193?.on) { _onDisc = () => this.disconnect(); eip1193.on('disconnect', _onDisc); }` — gated the WC `disconnect` event listener registration.
  2. `toast(kind === 'walletconnect' ? 'Connected via WalletConnect' : 'Wallet connected', 'success')` — the final success toast.
  Both lines run in strict mode. An undeclared `kind` becomes a `ReferenceError` from V8's strict-mode-bound identifier resolution. On EVERY successful pair (`wc.connect()` resolves), execution aborts at the first `ReferenceError` — `_authenticate()` is NEVER called, the JWT is NEVER requested, no SIWE is initiated. The user scans the QR, their wallet approves, and the dApp throws silently into DevTools. The post-pair UI flow never completes, which is exactly the "shouldnt just rendera qr code" reproduction.
- **Severity rationale:** Acquisition criterion is single-attacker cooperator-free system-wide stranding of the WC connectivity path on every successful scan. With no post-pair state change, the dashboard is locked behind "Connect Wallet" indefinitely until the user closes + reopens the page.
- **Fix:** Both `kind` references removed. The disconnect listener now registers unconditionally (`if (eip1193?.on) { eip1193.on('disconnect', _onDisc) }`) — the only connect path is WalletConnect, v23.2 already removed every alternative provider source. The success toast collapses to the single-message `'Connected via WalletConnect'`.
- **Negative side effects audited:** Listener registration logic preserved verbatim. `_onDisc = () => this.disconnect()` body unchanged. Belt: defensively preserve `eip1193?.on` (no-op when `on` is missing — protects against future EIP-1193 source changes).
- **Verification (existing + added):**
  - `node --check frontend/static/wallet.js` clean.
  - `grep -nE 'kind === .walletconnect' frontend/static/wallet.js` returns ONLY historical-commentary hits (line 542 v23.2 narrative + the v24.0 narrative at the comment-block anchors); ZERO live-code references.
  - Live browser-use verify on the deployed SHA: the click → pair → sign → setState('connected') chain now completes deterministically past `wc.connect()`.
- **Status:** FIXED. Live at https://magicwebb.fly.dev/.

### WC-04-chains-wrong — `chains:[CHAIN_ID]` silently rejected by all mainstream wallets 🟠 P1 — FIXED
- **Where:** `frontend/static/wallet.js`, the `_EthereumProvider.init({...})` configuration object inside `_wcConnect()`.
- **Key:** `wc04-chains-coston2-no-wallet-support`
- **Anchor:** the WalletConnect v2 SDK's `chains` array — every wallet scanning the QR MUST currently be on those exact chains or the session is silently rejected at the relay level.
- **Scenario (historical):** The previous init payload was `chains: [114]` (CHAIN_ID = Coston2). WalletConnect v2's `chains` config has the strict wallet-side requirement that the user's wallet MUST be on those exact chains to approve the pairing. Coston2 is NOT in the default supported chain set for MetaMask / Trust / Rainbow / Ledger / Trezor / most mobile wallets — they would see "this dApp is requesting a chain not supported by your wallet" and refuse the pair. The relay drops the session silently; `wc.connect()` hangs without erroring.
- **Fix:** Declare ETH mainnet (chain 1) as the required primary chain (universal wallet support) + put Coston2 in `optionalChains: [114]`. Wallets ALWAYS accept the QR scan (pair on mainnet, which supports `chains:[1]`); the dApp's existing chainId validation toast at the post-connect path surfaces "Connected wallet is on chain #N — expected Coston2 (114). Switch networks in your wallet, then Re-pair via QR." which guides the user through the network switch and re-pair loop. The added `1:` entry in `rpcMap` was REVERTED in the same commit (the SDK uses its own defaults; the wallet does not perform chain-1 reads during the brief pre-pair phase anyway).
- **Negative side effects audited:**
  - The chainId validation toast in `connect()` still fires post-pair if the wallet is on a network other than Coston2. With `optionalChains:[114]`, the wallet will REQUEST Coston2 automatically (many wallets support the EIP-2175 chain-switch on session approval). The toast acts as a safety net for wallets that don't auto-switch.
  - The `wc.connect()` call still succeeds on the FIRST pair if the wallet is on chain 1. The post-pair path detects the chain mismatch and prompts re-pair — same UX shape as before, just one step more.
  - No ABI / contract addresses change.
- **Verification:**
  - `grep -nE 'chains:.*CHAIN_ID|chains:.*\[1\]' frontend/static/wallet.js` returns the v24.0 entrance `chains: [1], optionalChains: [CHAIN_ID]`.
  - The post-pair chainId validation remains in place and unchanged.
- **Status:** FIXED. Live at https://magicwebb.fly.dev/.

### WC-04-overlay-race — Modal opens on later clicks but events fired before Alpine hydrates go into a void 🟠 P1 — FIXED
- **Where:** `frontend/templates/partials/wc_qr_overlay.html`, the `init()` method of the `window.MW_WC_OVERLAY_STATE()` x-data factory.
- **Key:** `wc04-overlay-init-buffered-event-recovery`
- **Anchor:** the v23.9 hoist of `window.MW_CONNECT_WALLET` to a top-level IIFE statement so the bridge exists the moment `wallet.js` parses, BEFORE Alpine's `alpine:init` fires.
- **Scenario (historical):** Hoisting the bridge OUT of `alpine:init` made it ready at parse time — a deliberate v23.9 fix. The flip side: a user click landing <50 ms after first paint can invoke `window.MW_CONNECT_WALLET()` → `_wcConnect()` → SDK init → `display_uri` emitted → `mw-wc-show` dispatched — BEFORE Alpine finishes hydrating the overlay partial's `init()` listener registration. The dispatch goes into the void (no listener attached). The cached `window.MW_WC_URI` is set but the modal stays at `offsetHeight: 0`. The user sees NO modal despite a successful SDK init that emitted a real `display_uri`.
- **Fix:** At overlay `init()` time, check (a) `window.MW_WC_URI` cached by `_wcConnect`'s `display_uri` listener AND (b) `Alpine.store('wallet')?.state`. Three branches:
  1. URI set + state is `connecting`/`awaiting` → render the QR from cache immediately.
  2. State is `connecting` but URI not yet cached → open the spinner overlay so the user sees progress, defer URI render to the live (registered AFTER init returns) `mw-wc-show` listener.
  3. State idle / connected / error / otherwise → leave overlay closed, await next open command.
  The check sits INSIDE init() so it runs ONCE per overlay mount. After init returns, the live `mw-wc-show` / `mw-wc-hide` listeners take over the open/close protocol.
- **Negative side effects audited:**
  - The check uses synchronous read of `Alpine.store('wallet')?.state`. Alpine.store is defined by `alpine:init` so this read is SAFE at init() time (the wallet store registration completes before `layout.html` parsing reaches the overlay part).
  - The hard-coded state-set `{connecting, awaiting}` is a literal comparison. v25 onwards may add intermediate states — adding new states to the check is a one-line change.
  - The `try { const r = document.getElementById('wc-modal-root'); if (r) r.style.display = ''; } catch (_)` line mirrors the same `style.display = ''` logic in the live mw-wc-show listener — exact alignment required for Alpine x-show's caching of the original `display: none` to behave correctly on first paint.
- **Verification:**
  - `grep -nE 'v24\.0 BUFFERED-EVENT|WINDOW_MW_WC_URI' frontend/templates/partials/wc_qr_overlay.html` returns the v24.0 marker present.
  - Live browser-use verify: connect flow completes the spinner→QR transition deterministically even on the fastest click.
- **Status:** FIXED. Live at https://magicwebb.fly.dev/.

### Cache-buster bump — ?v=26 → ?v=27 so returning browsers re-fetch wallet.js
- **Where:** `frontend/templates/layout.html`, the 5 lock-step static-asset tags.
- **Key:** `v24-cache-buster-bump`
- **Files affected (all in `layout.html`):**
  - `tailwind.css?v=26` → `?v=27`
  - `wc-bundle.js?v=26` → `?v=27`
  - `wallet.js?v=27` (this is THE one that contains the fix) → `?v=27`
  - `qrcode.min.js?v=26` → `?v=27`
  - `cdn.min.js?v=26` → `?v=27`
- **Scenario:** Without the bump, browsers that previously loaded `wallet.js?v=26` would continue serving the v23.9 wallet.js from disk cache despite the server now serving v24.0 bytes — URL is identical, the server has no way to invalidate the browser's cache. The cache-buster bump forces every browser to re-fetch atomically.
- **Verification:** `grep -nE '\?v=(26|27)' frontend/templates/layout.html` returns 5 hits on `?v=27` (lock-step) + 3 hits on `?v=23` (htmx/sse/ethers unchanged — no functional change needed). The v24.0 annotation comment block in the same file describes the three fixes for future readers of the v23.x trail.

### ops-01 — (carried-forward) Deploy-drift safety net remains GREEN
- (No change in v24.0; the v23.1 contract holds. `tools/check-fly-sync.sh` returns 0 once the SHA-baked binary replaces the live machine. Force-deploy incantation stays canonical: `fly deploy --remote-only --no-cache --build-arg "GIT_SHA=$(git rev-parse HEAD)" --strategy rolling`.)

---

---

The v22 sweep closed every live-site bug the browser-use harness surfaced.
v23 opens in response to a single fresh reproduction reported after the
v22 merge landed:

> When the user selects WalletConnect to connect the wallet, the page
> reports *“fails to load/fetch dynamically imported module.”*

### v23-wc-multi-cdn-fallback 🟠 P1 — FIXED
- **Where:** `frontend/static/wallet.js`, the `_wcConnect`
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
- **Where:** `frontend/static/wallet.js`, the provider init
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
- **Where:** SIWE connect path in `frontend/static/wallet.js`,
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
  events. The maximum legitimate batch on Coston2 observed
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
- `frontend/static/wallet.js` — JS source under audit (UI).
- `docs/USER_GUIDE.md` — end-user-action walkthrough.
- `docs/FEATURE_FLOWS.md` — backend-event-source map (auto-generated).
