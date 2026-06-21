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
package ui

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
var tailwindCSS string

func TestHomePageInjectsAllRuntimeGlobals(t *testing.T) {
	tmpl, ok := Templates["pages/home.html"]
	if !ok {
		t.Fatal("pages/home.html not in Templates")
	}
	data := map[string]any{
		"Title":           "Home",
		"MarketplaceAddr": "0xMarketF00Dbabe",
		"AuctionAddr":     "0xAuctionF00Dbabe",
		"OfferBookAddr":   "0xOfferF00Dbabe",
		"WCProjectID":     "af6aba4c71274871c3d77a60050171ba",
		"ExplorerPrefix":  "https://coston2-explorer.flare.network",
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
		{"MW_WC_PROJECT_ID", "window.MW_WC_PROJECT_ID = 'af6aba4c71274871c3d77a60050171ba'"},
		{"MW_MARKETPLACE",   "window.MW_MARKETPLACE   = '0xMarketF00Dbabe'"},
		{"MW_AUCTION",       "window.MW_AUCTION       = '0xAuctionF00Dbabe'"},
		{"MW_OFFERBOOK",     "window.MW_OFFERBOOK     = '0xOfferF00Dbabe'"},
		{"MW_EXPLORER",      "https://coston2-explorer.flare.network"},
	// Self-hosted assets served with `?v=14` cache-buster — bumping
	// from v13 forces returning browsers to re-fetch layout.html (and
	// the compiled tailwind.css) so the v14 fix lands on users that
	// loaded the previous shell. v14 ships two coupled changes:
	//   1. templates/layout.html — the navbar Connect Wallet's parent
	//      `<div class="relative">` swaps its responsive class from the
	//      v12 workaround `hidden md:flex` back to the idiomatic
	//      `hidden md:block`. The v12 workaround was needed because
	//      the JIT-compiled tailwind.css for the v11/v12 deploy was
	//      MISSING the `.md\:block` utility (Tailwind's content-scan
	//      only compiles classes used in templates — adding a class
	//      without rebuilding the bundle silently strips it). v14 runs
	//      `cmd/buildtailwindcss` and the utility re-enters the bundle.
	//   2. internal/ui/static/tailwind.css — recompiled from the
	//      current template content glob, so it contains `.md\:block`
	//      (and any other responsive utility added since the prior
	//      build). The md-block-utility-present check below reads
	//      the bundled CSS via go:embed and asserts the utility is
	//      present, denying a stale-bundle deploy in CI.
	// Mounted under /static/* with a 60-second Cache-Control: max-
	// age=60 (see mountStatic) so the baseline freshness policy isn't
	// solely reliant on the bump.
	{"tailwind-static-link", "tailwind.css?v=14"},
	{"wallet-js-defer",      "wallet.js?v=14"},
	{"qrcode-min-js-defer",  "qrcode.min.js?v=14"},
	{"ethers-umd-defer",     "ethers.umd.min.js?v=14"},
	{"cdn-min-js-defer",     "cdn.min.js?v=14"},
	{"htmx-min-js-defer",    "htmx.min.js?v=14"},
		// WC v6 overlay protocol: positive-command events (mw-wc-show /
		// mw-wc-hide) replace the prior flag-gated listeners that
		// leaked state across auto-reconnect. Validate every wire-point.
		{"wc-show-event-listener", "mw-wc-show"},
		{"wc-hide-event-listener", "mw-wc-hide"},
		{"wc-overlay-root-id",     "wc-overlay-root"},
		{"wc-modal-root-id",       "wc-modal-root"},
		{"wc-esc-handler-present", "Escape"},
		// The Got-it and × buttons must still funnel through close().
		{"wc-gotit-button-clicks-close", "Got it"},
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
		{"mobile-drawer-browser-button",  "Browser Wallet"},
		{"mobile-drawer-wc-button",       "WalletConnect"},
		// 1s polling guard: every live grid AND the activity ticker
		// must carry `every 1s [!document.hidden]` so the listing /
		// auction / home surfaces refresh at most once per second AND
		// stop polling when the tab is hidden (otherwise a long-lived
		// background tab hammers the DB).
		{"home-activity-1s-poll",   "activity-ticker"},
		{"home-listings-grid-poll", "id=\"listings-grid\""},
		{"every-1s-condition",      "every 1s [!document.hidden]"},
		// WC v2 wiring: partial body, picker connect call, persistent navbar reopen chip
		{"wc-qr-overlay-renders", "Scan to pair"},
		{"WC-connect-call",       "store.wallet.connect('walletconnect')"},
		{"wc-pair-chip",          "Scan QR on your phone"},
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
	// v16 — SyntaxError in the inline WC overlay script that broke
	// Alpine init entirely. The inline <script> in partials/wc_qr_overlay.html
	// MUST successfully parse and define window.MW_WC_OVERLAY_STATE for
	// any page that loads the partial. A parser error there wedges
	// Alpine's x-data evaluation across every page, which is what kept
	// the desktop navbar Connect Wallet button invisible across v9-v15
	// even when the layout markup itself was correct. Re-running the
	// layout-level fix would not surface this regression without a
	// run-time check; we pin the substring so go test parses it
	// implicitly via the template engine.
	//
	// Negative-check: the parsed-as-JS broken pattern (lines starting
	// with bare "(1) Clear the reactive ..." tokens — missing `//`
	// comment slashes) MUST not appear ANYWHERE in the rendered body.
	// The substring below is the exact heredoc-bleed that caused the
	// Uncaught SyntaxError in production. If a future commit
	// re-introduces a numbered-list comment block in inline JS the
	// smoke test catches it before deploy.
	if !strings.Contains(body, "window.MW_WC_OVERLAY_STATE") {
		t.Logf("  FAIL  wc-overlay-state-defined\n        window.MW_WC_OVERLAY_STATE absent from rendered body — inline JS in partials/wc_qr_overlay.html failed to parse (likely a JavaScript SyntaxError) and the x-data factory never got defined; Alpine hydration is wedged.")
		missing = append(missing, "wc-overlay-state-defined")
		fail++
	} else {
		t.Logf("  PASS  wc-overlay-state-defined")
	}
	if strings.Contains(body, "(1) Clear the reactive") {
		t.Logf("  FAIL  no-wc-syntax-error-leak\n        Bare \"(1) Clear the reactive ...\" tokens reappeared in rendered body without `//` comment slashes — this is the JavaScript SyntaxError that wedges Alpine init and keeps the navbar Connect Wallet button invisible. See partials/wc_qr_overlay.html closing block.")
		missing = append(missing, "no-wc-syntax-error-leak")
		fail++
	} else {
		t.Logf("  PASS  no-wc-syntax-error-leak")
	}
}
