// MagicWebb — render-time smoke guard for the home page injection
// chain. This is the test that catches the class of regressions
// surfaced during the wallet-smoke setup pass:
//
//   1. `pages/auction.html` was once committed truncated at cb2f67a
//      (138 lines, mid `<p >`). A package-init panic of the form
//      `template: auction.html:139: unexpected EOF` followed. Fixed
//      upstream of this test.
//   2. `layout.html` references `{{template "partials/action_modal.html" .}}`
//      but `partialPaths` in embed.go once omitted that partial —
//      same package-init panic mode. Fixed by adding it to
//      `partialPaths[]` upstream of this test.
//   3. The WalletConnect v2 wiring (project id, contract addresses,
//      the connect picker, the in-page QR overlay) is all injected at
//      the layout level. A future refactor could silently strip any
//      of these. The browser DevTools console wouldn't surface this
//      class of issue until the user clicked; with this test, CI catches
//      it.
//
// Why a single test instead of "render every page": with `pages/*`
// templates each having a distinct data shape (home takes
// Listings/Trending/Activity, token takes Listing/Offers/Owner/etc.)
// and go's html/template defaulting to nil-receiver-method panics on
// missing STRUCT pointers, a "render every page" guard requires
// either per-page dummy data OR an Option on the parse that
// silently captures nil-receiver errors. The home-page-only test
// trades breadth for signal-to-noise: it covers the highest-leverage
// regression class (WC + contract wiring) with one fully-specified
// data map and zero flakiness.
package ui

import (
	"bytes"
	"strings"
	"testing"
)

// TestHomePageInjectsAllRuntimeGlobals asserts the 11 critical
// runtime globals that the layout-level injection chain produces.
// Each missing substring represents a real regression mode (e.g., a
// future refactor accidentally drops the MW_WC_PROJECT_ID line and
// the WalletConnect v2 picker silently breaks — what a live wallet
// smoke would have surfaced as a dev-tools console error).
//
// Drove the discovery of two latent regressions on this branch:
//   - `cb2f67a` truncated auction.html mid-`<p >` → package-init panic
//   - `partials/action_modal.html` missing from embed.go's
//     `partialPaths[]` → same panic mode, harder to spot because the
//     file existed on disk and was just skipped during ParseFS.
//
// Dummy data is rich for the home page (Listings, Trending, Activity,
// counters) because home.html references those fields directly.
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
		// Self-hosted assets (replacing the public-CDN JIT loader)
		{"tailwind-static-link", "href=\"/static/tailwind.css\""},
		{"wallet-js-defer",      "src=\"/static/wallet.js\" defer"},
		{"ethers-umd-defer",     "src=\"/static/ethers.umd.min.js\" defer"},
		// WC v2 wiring
		{"wc-qr-overlay-renders", "Scan to pair"},                                    // partial body emitted into home page render
		{"WC-connect-call",       "store.wallet.connect('walletconnect')"},            // picker chip handler
		// Alpine x-data attribute proves reactive surfaces render
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
	if fail > 0 {
		t.Fatalf("%d render-smoke checks failed: %v", fail, missing)
	}
}
