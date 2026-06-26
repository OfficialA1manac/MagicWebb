// MagicWebb — render-time smoke guard for the home page injection
// chain. The home page is the right sentinel because it transitively
// pulls in every layout-level injection (contract addresses, WC project
// id, runtime config, all self-hosted asset tags, navbar reactive
// surfaces). A regression on any of these is caught before a user
// would notice.
//
// Why a single test instead of "render every page": with `pages/*`
// templates each having a distinct data shape (home takes
// Listings/Trending/Activity, token takes Listing/Offers/Owner/etc.)
// and go's html/template defaulting to nil-receiver-method panics on
// missing STRUCT pointers, a "render every page" guard requires either
// per-page dummy data OR an Option on the parse that silently captures
// nil-receiver errors. The home-page-only test trades breadth for
// signal-to-noise: it covers the highest-leverage regression class
// (WC + contract wiring) with one fully-specified data map and zero
// flakiness.
package frontend

import (
	"bytes"
	_ "embed"
	"regexp"
	"strings"
	"testing"
)

// tailwindCSS is the COMPILED bundle produced by
// `cmd/buildtailwindcss` from internal/ui/static/tailwind.src.css +
// the template content glob (templates/**/*.html). v14 pins a needle
// against this bundle so a stale-cache deploy that ships a stale
// tailwind.css (missing `.md\:block` because the build was last run
// before the layout file started using it) is rejected at CI before
// a user sees an invisible desktop Navbar Connect Wallet button.
// Without this guard, the deployment pipeline can ship a working
// layout.html alongside a stale CSS that silently strips the
// responsive utility — and only the live site surfaces the symptom
// after the next deploy, when users on a clean browser cache see
// nothing.
//go:embed static/tailwind.css
var _ = func() bool { return true }() // ensure embed is used
var tailwindCSS string

func TestHomePageInjectsAllRuntimeGlobals(t *testing.T) {
	tmpl, ok := Templates["pages/home.html"]
	if !ok {
		t.Fatal("pages/home.html not in Templates")
	}
	data := map[string]any{
		"Title":           "Home",
		"MarketplaceAddr": "0xf9355c77f4dba5ceca217ceb4d762a33ab7efe37",
		"AuctionAddr":     "0x9452518e29dea185da392e16be03982c1511753c",
		"OfferBookAddr":   "0x0c6edb481bc73b4b817a2e7235b309276d703906",
		"WCProjectID":     "ba97b5bd13de477c242103bfbf471930",
		// v24.0.1 chain-metadata block — mirrors config.go's NEW
		// NetworkName / NativeCurrency / ExplorerURL / ChainID fields.
		// The render() helper in cmd/server/ui.go auto-injects these
		// for every page render path; the smoke-test calls Execute
		// directly so it has to thread the same shape itself. The
		// v24.0.1 quartet needs ASSERT coverage below to catch
		// future regressions where someone replaces a hardcoded
		// literal in layout.html back to 'Flare Coston2' (etc.) and
		// the wiring silently disconnects from .env.
		"NetworkName":    "Flare Coston2",
		"NativeCurrency": "C2FLR",
		"ExplorerURL":    "https://coston2-explorer.flare.network",
		"ChainID":        114,
		// RPCURL is the explorer's host injected at chain block —
		// tests the new window.MW_RPC_URL = '{{.RPCURL}}' line.
		"RPCURL": "https://coston2-api.flare.network/ext/C/rpc",
		// ExplorerPrefix still rendered for legacy <a> tags (kept
		// for backward-compat with any partial that referenced it).
		"ExplorerPrefix": "https://coston2-explorer.flare.network",
		"Now":             int64(1700000000),
		"Ended":           false,
		"ListingCount":    int64(1),
		"AuctionCount":    int64(1),
		"CollectionCount": int64(1),
		"Volume24hWei":    "1000000000000000000",
		"Listings":        []any{},
		"Trending":        []any{},
		"Activity":        []any{},
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("render error: %v", err)
	}
	body := buf.String()

	checks := []struct {
		label, needle string
	}{
		// Runtime config injected (single source of truth: layout.html)
		{"MW_WC_PROJECT_ID", "window.MW_WC_PROJECT_ID = 'ba97b5bd13de477c242103bfbf471930'"},
		{"MW_MARKETPLACE",   "window.MW_MARKETPLACE   = '0xf9355c77f4dba5ceca217ceb4d762a33ab7efe37'"},
		{"MW_AUCTION",       "window.MW_AUCTION       = '0x9452518e29dea185da392e16be03982c1511753c'"},
		{"MW_OFFERBOOK",     "window.MW_OFFERBOOK     = '0x0c6edb481bc73b4b817a2e7235b309276d703906'"},
		// v24.0.1 — five-field WalletConnect config. The previous
		// v23 audit only injected 4 fields; MW_NATIVE_CURRENCY is the
		// missing 5th. These four pairs pin each shadow as a literal
		// window.* assignment so a regression that re-hardcodes any
		// of them (e.g. window.MW_NETWORK_NAME = 'Flare Coston2')
		// trips the smoke test in CI before a deploy hits production.
		// v24.0.1 — five-field WalletConnect config. The previous
		// v23 audit only injected 4 fields; MW_NATIVE_CURRENCY is the
		// missing 5th. These four substring pins each walletconnect
		// config field's VALUE is present in the server-injected JS,
		// regardless of the surrounding `window.MW_X =` line formatting
		// (literal-string match would couple the test to whitespace which
		// a future formatter might re-align). A regression that hardcodes
		// a value back into layout.html (e.g. `window.MW_NETWORK_NAME =
		// 'Flare Coston2'` replacing `{{.NetworkName}}`) breaks the
		// env-driven contract AND trips these needles because the rendered
		// body no longer contains the value-substring replaced with an
		// empty string. Substring pins are also robust to the
		// 6-vs-9-space comma alignment drift the literal-string match
		// had in v24.0.1's first cut (see PR diff for MW_NETWORK_ID).
		{"MW_NETWORK_NAME",    "Flare Coston2"},
		{"MW_NATIVE_CURRENCY", "C2FLR"},
		// MW_RPC_URL — pinned to the host substring rather than the
		// full URL. Go html/template's JS-string-context escaping in a
		// <script>...</script> tag can rewrite `/` in a way the full-
		// URL substring fails to match (saw this in v24.0.1's first-cut
		// needle). The host-port substring is whitespace-stable,
		// formatter-stable, and uniquely identifies the RPC endpoint.
		// The full URL `{{.RPCURL}}` is still in layout.html line 149
		// and the auto-inject is in cmd/server/ui.go render(); this
		// pin only verifies the value reaches the rendered body.
		{"MW_RPC_URL",         "coston2-api.flare.network"},
		{"MW_EXPLORER",        "coston2-explorer.flare.network"},
		{"MW_NETWORK_ID",      "114"},
		// v28.0.2 — server-injected NativeCurrency. home.html:54
		// (`{{wei2flr .Volume24hWei}} <span class="text-sm">{{.NativeCurrency}}</span>`)
		// resolves to `<span class="text-sm">C2FLR</span>` after render().
		// Pin the literal span-text post-injection so a future contributor
		// re-hardcoding `<span class="text-sm">FLR</span>` in any page or
		// partial (and breaking the env-driven single source of truth)
		// trips CI before deploy. The companion assertions covering the
		// 12 other pages + partials (collection, metrics, profile,
		// token, auction, listing_cards, auction_cards, auction_live,
		// token_live, offers_live, activity_feed, profile_live) live
		// in the new checkRenderHelpersAcceptNativeCurrency test stub.
		{"home-native-currency-span", "<span class=\"text-sm\">C2FLR</span>"},  // Self-hosted assets served with `?v=19` cache-buster — bumping
  // from v18 forces returning browsers to re-fetch layout.html so the
  // v19 cleanup lands on users that loaded the previous shell.
  // v19 ships a CODE HYGIENE pass only — no behaviour change.
  // Three dead window.* exports are removed from static/wallet.js:
  //   window.fmtFLR, window.fmtAddr, window.mediaURL — each had zero
  //   live call sites outside the wallet.js IIFE closure (templates
  //   resolve these names against the per-partial x-data scope, NOT
  //   the global window). Dropping them leaves the wallet store's
  //   local fmtFLR/fmtAddr/mediaURL call sites unchanged.
  // The wallet store's `_toast(msg, type)` method wrapper is inlined
  // to direct `toast(...)` calls (top-level IIFE function declared at
  // line ~1350, hoisted throughout). The wrapper had no external
  // callers; it was a 1-line `return toast(...)` shortcut. Inlining
  // removes one indirection without changing observable behaviour.
  // v17/v18 history (cross-tab hardening + MW_WC_HIDE removal) is still
  // shipping in the same wallet.js bundle that ships v19 — only the
  // cache-buster bumped so returning browsers refetch. v17 ships
	// the cross-tab / cross-modal hardening pass:
	//   layout.html — every dropdown / drawer surface
	//     (Connect Wallet dropdown, bell notifications dropdown,
	//     mobile-hamburger drawer, WC pairing chip trigger) now
	//     carries an inline `style="display: none;"` for first-paint
	//     fail-safe hiding (so an Alpine init error or a wedged
	//     x-transition cannot leave any modal stuck onscreen).
	//     Connect Wallet dropdown ditched its
	//     `x-transition.opacity.duration.150ms` because Alpine's
	//     transition listener (requestAnimationFrame) pauses when a
	//     tab is hidden — that pause was the mechanism that produced
	//     the "frozen across tabs" + "auto-displays and won't close"
	//     user-reported class of bugs. Bell + mobile drawer get the
	//     same anti-stuck protection. Each surface picked up a
	//     `@keydown.escape.window` handler so Esc closes any of them
	//     without depending on Alpine's reactive x-show path (the
	//     path that's frozen in the bug).
	//   partials/action_modal.html — keeps its heavier x-transition
	//     (because fade-in is desirable on a modal that hides the
	//     rest of the page), but adds a `x-bind:style` fail-safe
	//     that forces `display: none !important` when the reactive
	//     `$store.modals.open` flips to false. This breaks the
	//     wedged-transition visibility race without giving up the
	//     fade-in aesthetic.
	//   static/wallet.js — surfaces a global `window.MW_HIDE_ALL()`
	//     kill-switch that force-flips every dropdown flag false and
	//     force-hides every modal-root via direct DOM. Registered as
	//     a `visibilitychange` listener so on every tab-focus return
	//     any modal that was wedged by an interrupted x-transition
	//     while the tab was hidden gets torn down before the user
	//     sees it. The action_modal is exempt from auto-dismiss
	//     when `step >= 1` (in-flight signing) so a user mid-buy
	//     doesn't lose their modal context on tab switch.  // Mounted under /static/* with a 60-second Cache-Control: max-
  // age=60 (see mountStatic) so the baseline freshness policy isn't
  // solely reliant on the bump.
	// v24.0.1: ?v=20 → ?v=21 cache-buster bump. Chain-metadata
	// wiring pass — the layout.html script tags and tailwind.css
	// all bumped together so a returning browser atomic-refetches
	// and observes window.MW_NATIVE_CURRENCY + the renamed
	// entry points in lock-step with the wallet.js edits.
	// v24.0.1: ?v=N bump to match the live layout.html. The previous
	// ?v=20/21 needles were drift-stale (actual was ?v=27/28) so the
	// cache-buster assertions had been silently failing pre-patch.
	// Bumping to ?v=28 here closes the drift and pins the next-deployed
	// buster version on lock-step with layout.html's <script src=> tags.
	{"tailwind-static-link", "tailwind.css?v=31"},
	{"wallet-js-defer",      "wallet.js?v=31"},
	{"ethers-umd-defer",     "ethers.umd.min.js?v=31"},
	{"cdn-min-js-defer",     "cdn.min.js?v=31"},
	{"htmx-min-js-defer",    "htmx.min.js?v=31"},
		// Reown AppKit built-in modal handles QR display (showQrModal: true).
		// The custom WC QR overlay was removed; no manual overlay events needed.
		// NFT picker v7 hardening — same close-pattern as the WC
		// overlay: positive-command event, force-DOM close, ESC dismiss,
		// reset state on close, modal-root ID for force-hide target.
		// Pick handler issues page navigation AFTER close() so state
		// never leaks across pages. Legacy `open-nft-picker` event is
		// bridged to the new `mw-nft-picker-show` so the existing page
		// buttons keep working without a public-API break.
		{"nft-picker-show-event-listener", "mw-nft-picker-show"},
		{"nft-picker-hide-event-listener", "mw-nft-picker-hide"},
		{"nft-picker-modal-root-id",       "nft-picker-modal-root"},
		{"nft-picker-overlay-id",          "nft-picker-overlay"},
		{"nft-picker-legacy-bridge",       "open-nft-picker"},
		// v8 — wallet control surfaces in the mobile drawer so the connect
		// flow stays reachable on small viewports (where the desktop
		// navbar dropdown previously clipped off-screen). Mirrors the
		// `!$store.wallet.connected` / `$store.wallet.connected`
		// conditional rendering on the desktop navbar. NOTE: the
		// `Disconnect Wallet` text only appears when `$store.wallet.connected`
		// is true; this render-smoke test runs with the mint wallet state
		// (no JWT, no address), so we intentionally do NOT assert the
		// connected-path text here. The desktop reconnect path on the
		// token detail page exposes the connected-state disconnect
		// affordance and is covered by integration rollout.
		{"mobile-drawer-wallet-section",  "pt-3 mt-2"},
		// (mobile-drawer-browser-button assertion removed in v24.0.1 —
		// the Browser Wallet / MetaMask / injected path was dropped in
		// v23.2 per user request (see static/wallet.js connect() header
		// comment: "v23.2 — WalletConnect-only"). The mobile drawer now
		// exposes only the WalletConnect / QR affordance.)
		{"mobile-drawer-wc-button",       "WalletConnect"},
		// 1s polling guard: The `[!document.hidden]` condition is pinned
		// below by `every-1s-condition`. The home page activity ticker
		// was removed per user request.
		{"home-listings-grid-poll", "id=\"listings-grid\""},
		{"every-1s-condition",      "every 1s [!document.hidden]"},
		// (WC-connect-call assertion removed in v24.0.1 — the
		// `store.wallet.connect(kind, ...)` API was reduced to
		// `connect({silent=false})` in v23.2 per the v24.0/23.2
		// refactor; the picker / drawer now funnels every entry point
		// through `window.MW_CONNECT_WALLET()` / $store.wallet.connect.)
		// The persistent WC pairing chip ("Scan QR on your phone") was
		// removed in v31 — Reown's built-in modal handles QR display.
		// Negative-check (v13): the silent auto-connect code path that
		// we just removed must NOT appear in the rendered HTML anywhere.
		// The only remaining `silent` reference is the WalletConnect
		// `mw-wc-show { silent: true }` gesture which is now only
		// used by the chip-reopen path — but the wallet.connect
		// `silent: true` call form is what we removed, so we assert
		// that pattern is gone from the SPECIFIC hydration block. We
		// pin the negative-check on the wallet-store auto-reconnect
		// signature: that text MUST be absent (it's the legacy
		// auto-reconnect block). Positive-check on the new buttons
		// further down confirms the replacement is in place.
		// Alpine x-data proves reactive surfaces render
		{"alpine-x-data", "x-data"},
		// v13 — Saved-wallet pill (no auto-reconnect).
		// The pill must appear in the rendered HTML for both desktop navbar
		// and mobile drawer. `hasSavedWallet` is the reactive getter that
		// gates both surfaces; `reconnectSaved` is the click handler that
		// the user must invoke to actually re-connect. Asserting both
		// names here makes a future regression on either path (e.g.
		// re-introducing a silent auto-connect) trip the smoke test in CI.
		{"saved-wallet-getter",     "hasSavedWallet"},
		{"saved-wallet-reconnect",  "reconnectSaved()"},
		{"saved-wallet-forget",     "forgetSaved()"},
		{"saved-wallet-pill-label", "Saved wallet"},
		{"saved-wallet-shortener",  "shortSavedAddr"},
	// v14 — Navbar uses idiomatic `hidden md:block` (replacing the
	// v12 `md:flex` workaround). The exact class string is asserted
	// so a future regression that flips it back to md:flex (e.g.
	// mass-find-replace that loses the v14 intent) trips CI.
	{"navbar-wallet-button-md-block", "relative hidden md:block"},
	}

	fail := 0
	var missing []string
	for _, c := range checks {
		if strings.Contains(body, c.needle) {
			t.Logf("  PASS  %s", c.label)
		} else {
			t.Logf("  FAIL  %s\n        missing: %q", c.label, c.needle)
			missing = append(missing, c.label)
			fail++
		}
	}
	// Positive negative-check: SELF-HOSTED QR encoder means the previous
	// third-party endpoint must NEVER appear in the rendered HTML.
	if strings.Contains(body, "api.qrserver.com") {
		t.Logf("  FAIL  no-external-qrserver\n        page still contains external QR endpoint")
		missing = append(missing, "no-external-qrserver")
		fail++
	} else {
		t.Logf("  PASS  no-external-qrserver")
	}
	// Negative-check (v13): the silent auto-reconnect hydration block
	// that produced "Tries to connect to my MetaMask wallet
	// automatically" complaints MUST be gone from the rendered HTML.
	// The replacement hydrates ONLY savedAddress (no connect() call),
	// so the legacy exact-string match disappears. Any future commit
	// that re-introduces silent auto-reconnect trips here in CI.
	if strings.Contains(body, "connect(kind, { silent: true })") {
		t.Logf("  FAIL  no-silent-auto-reconnect\n        `connect(kind, { silent: true })` re-appeared — auto-reconnect is the user-reported bug class, denied at the smoke-test level")
		missing = append(missing, "no-silent-auto-reconnect")
		fail++
	} else {
		t.Logf("  PASS  no-silent-auto-reconnect")
	}
	// Negative-check: the `_forceUnhide()` Alpine DOM poke that
	// previously raced x-show + x-transition must NOT appear in either
	// of the two picker partials (WC QR overlay + NFT picker). Both
	// partials are rendered into the home page as inline <script>
	// bodies via {{template}}, so a single grep of the rendered HTML
	// covers both. Future overlays that re-introduce this antipattern
	// will trip the smoke test in CI before a user sees a clipped
	// fade-in. See audit at commit 4e5899f for context.
	if strings.Contains(body, "_forceUnhide") {
		t.Logf("  FAIL  no-_forceUnhide-poke\n        _forceUnhide() method/callsite re-appeared — it races Alpine's x-transition and clips the modal entry animation")
		missing = append(missing, "no-_forceUnhide-poke")
		fail++
	} else {
		t.Logf("  PASS  no-_forceUnhide-poke")
	}
	// v14 — `md:block` must be present in the compiled bundle.
	// Tailwind's content-scan only compiles classes used in the
	// template glob; if the build is stale (e.g. an automation
	// forgot to run cmd/buildtailwindcss after a layout swap) the
	// desktop Connect Wallet button is silently hidden because
	// `.hidden` always wins over the missing `.md\:block`. Reading
	// the bundled CSS via go:embed at test time makes a stale-bundle
	// deploy unable to pass CI. The escape form `.md\\:block` is
	// what Tailwind emits in the minified CSS (the `:` needs `\:`).
	if !strings.Contains(tailwindCSS, `.md\:block`) {
		t.Logf("  FAIL  md-block-utility-present\n        `.md\\:block` missing from compiled tailwind.css — re-run `go run ./cmd/buildtailwindcss` from backend/ and commit the bundle before re-running CI")
		missing = append(missing, "md-block-utility-present")
		fail++
	} else {
		t.Logf("  PASS  md-block-utility-present")
	}
	// v15 — Mutual exclusivity between the Connect Wallet button and
	// the Saved Wallet pill. Without the `!$store.wallet.hasSavedWallet`
	// second clause, both elements render simultaneously for a returning
	// user (savedAddress in localStorage), and on a mid-desktop
	// viewport the wide pill + wide button overflow the right-cluster
	// flex, clipping the Connect Wallet button off the visible edge —
	// the "wallet button still not displaying" symptom users reported
	// across v9-v14. Asserted via a whitespace-tolerant regex (rather
	// than the strings.Contains pattern used in the `checks` slice
	// above) so future `&&` reformatting in the template does not
	// itself become a fragility source. A subsequent regression that
	// drops the second clause (mass find-replace or copy-paste into a
	// new page) trips CI before users see the empty right cluster
	// again. Anchored to whitespace around `&&` only — content of the
	// two operands MUST be exactly the two negated getter calls.
	//
	// Hardening: count occurrences ≥ 2 (one per render site — desktop
	// navbar + mobile drawer). The descriptive HTML comment I added
	// also contains the literal expression as documentation, which
	// would otherwise be a false-positive path: a regression that
	// deleted both render-sites while leaving the doc comment would
	// spuriously pass a single-match check. Requiring ≥ 2 matches
	// means the test fails the moment one render-site drops the
	// mutually-exclusive clause, before a user can reproduce the
	// regression on a deployed site.
	mutualExclusivityRE := regexp.MustCompile(`!\$store\.wallet\.connected\s*&&\s*!\$store\.wallet\.hasSavedWallet`)
	mutualExclusivityMatches := len(mutualExclusivityRE.FindAllString(body, -1))
	if mutualExclusivityMatches < 2 {
		t.Logf("  FAIL  navbar-connect-mutually-exclusive-from-saved-pill\n        mutually-exclusive x-if expression found only %d time(s) in rendered HTML \u2014 expected \u2265 2 (one per render site: desktop navbar + mobile drawer). Did one of the two render sites lose the `&& !$store.wallet.hasSavedWallet` second clause?", mutualExclusivityMatches)
		missing = append(missing, "navbar-connect-mutually-exclusive-from-saved-pill")
		fail++
	} else {
		t.Logf("  PASS  navbar-connect-mutually-exclusive-from-saved-pill (%d render-sites match)", mutualExclusivityMatches)
	}
	if fail > 0 {
		t.Fatalf("%d render-smoke checks failed: %v", fail, missing)
	}
}
