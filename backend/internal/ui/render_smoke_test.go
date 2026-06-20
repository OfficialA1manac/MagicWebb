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
	"strings"
	"testing"
)

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
		// Self-hosted assets served with `?v=10` cache-buster — bumping
		// from v9 forces returning browsers to re-fetch wallet.js so the
		// v10 fixes (chain-switch re-throw in connect, _forceUnhide
		// removal in WC overlay + NFT picker for Alpine transition
		// correctness) land on users that loaded the previous shell.
		// Mounted under /static/* with a 60-second Cache-Control: max-age=60
		// (see mountStatic) so the baseline freshness policy isn't solely
		// reliant on the bump.
		{"tailwind-static-link", "tailwind.css?v=10"},
		{"wallet-js-defer",      "wallet.js?v=10"},
		{"qrcode-min-js-defer",  "qrcode.min.js?v=10"},
		{"ethers-umd-defer",     "ethers.umd.min.js?v=10"},
		{"cdn-min-js-defer",     "cdn.min.js?v=10"},
		{"htmx-min-js-defer",    "htmx.min.js?v=10"},
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
		// Alpine x-data proves reactive surfaces render
		{"alpine-x-data", "x-data"},
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
	if fail > 0 {
		t.Fatalf("%d render-smoke checks failed: %v", fail, missing)
	}
}
