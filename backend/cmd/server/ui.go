package main

import (
	"context"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/api"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/frontend"
)

// render executes the named template, injecting the contract addresses and
// WalletConnect project id the layout passes to wallet.js — server config is
// the single source of truth so a redeploy can never strand the frontend on
// stale hardcoded addresses.
func render(c *fiber.Ctx, name string, data any) error {
	t, ok := frontend.Templates[name]
	if !ok {
		return c.Status(fiber.StatusInternalServerError).SendString("template not found: " + name)
	}
	if m, ok := data.(fiber.Map); ok {
		if _, present := m["MarketplaceAddr"]; !present {
			m["MarketplaceAddr"] = config.C.MarketplaceAddr
		}
		if _, present := m["AuctionAddr"]; !present {
			m["AuctionAddr"] = config.C.AuctionAddr
		}
		if _, present := m["OfferBookAddr"]; !present {
			m["OfferBookAddr"] = config.C.OfferBookAddr
		}
		if _, present := m["WCProjectID"]; !present {
			m["WCProjectID"] = config.C.WCProjectID
		}
		// Chain-metadata block. The single source of truth (config.C)
		// populates the per-render data map so layout.html + every
		// page referencing MW_NETWORK_* / MW_NATIVE_CURRENCY /
		// window.MW_RPC_URL gets the env-correct values without a
		// redeploy. Templates can still override any of these fields
		// by passing them in their own fiber.Map — the `if _, present:`
		// guard respects caller-set values (same pattern as the
		// contract addrs above).
		//
		// Currency is the v24.0+ WalletConnect config field that was
		// missing: the user's WalletConnect v2 modal pairs the chain
		// via chains:[1]+optionalChains:[CHAIN_ID]+rpcMap:{CHAIN_ID:RPC_URL},
		// and the JS-side wallet.js uses NATIVE_CURRENCY + NETWORK_NAME
		// everywhere a label says "FLR" or "Flare Network" so a future
		// Different networks can be configured via .env vars.
		//
		// RPCURL auto-inject is required because layout.html line 149
		// references `{{.RPCURL}}` — without it the zero-value fallback
		// would inject `window.MW_RPC_URL = '';` on every live render,
		// silently breaking the user error path that displays the
		// connected chain's RPC URL. The smoke test passes by manual
		// data-map setup; this auto-inject CLOSES the gap so the prod
		// path mirrors the test path.
		if _, present := m["RPCURL"]; !present {
			m["RPCURL"] = config.C.RPCURL
		}
		if _, present := m["ExplorerURL"]; !present {
			m["ExplorerURL"] = config.C.ExplorerURL
		}
		if _, present := m["NetworkName"]; !present {
			m["NetworkName"] = config.C.NetworkName
		}
		if _, present := m["NativeCurrency"]; !present {
			m["NativeCurrency"] = config.C.NativeCurrency
		}
		if _, present := m["ChainID"]; !present {
			m["ChainID"] = config.C.ChainID
		}
	}
	c.Set("Content-Type", "text/html; charset=utf-8")
	execName := filepath.Base(name)
	if strings.HasPrefix(name, "partials/") {
		execName = name
	}
	return t.ExecuteTemplate(c.Response().BodyWriter(), execName, data)
}

// ── Full page handlers ────────────────────────────────────────────────────────

func uiHome(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx := c.Context()
		// Aggregates populate the live counter pills (Listings / Auctions /
		// Collections / 24h Volume). Each call fall-tolerates a DB hiccup —
		// the page still renders with `0` counts rather than a 500, but we
		// log a Warn so an outage surfaces in centralized logging.
		listingCount, err := q.CountActiveListings(ctx)
		if err != nil {
			log.Warn().Err(err).Str("where", "uiHome.CountActiveListings").Msg("uiHome: counter query failed")
		}
		auctionCount, err := q.CountActiveAuctions(ctx)
		if err != nil {
			log.Warn().Err(err).Str("where", "uiHome.CountActiveAuctions").Msg("uiHome: counter query failed")
		}
		collectionCount, err := q.CountCollections(ctx)
		if err != nil {
			log.Warn().Err(err).Str("where", "uiHome.CountCollections").Msg("uiHome: counter query failed")
		}
		volume24h, err := q.TotalVolume24hWei(ctx)
		if err != nil {
			log.Warn().Err(err).Str("where", "uiHome.TotalVolume24hWei").Msg("uiHome: aggregator query failed")
		}
		listings, err := q.ListActiveListings(ctx, db.ListingsFilter{Limit: 12})
		if err != nil {
			log.Warn().Err(err).Msg("uiHome: ListActiveListings")
		}
		trending, err := q.GetTrendingCollections(ctx, "24h", 8)
		if err != nil {
			log.Warn().Err(err).Msg("uiHome: GetTrendingCollections")
		}
		activity, err := q.GetRecentTransactions(ctx, 10)
		if err != nil {
			log.Warn().Err(err).Msg("uiHome: GetRecentTransactions")
		}
		return render(c, "pages/home.html", fiber.Map{
			"Title":           "Home",
			"Listings":        listings,
			"Trending":        trending,
			"Activity":        activity,
			"ListingCount":    listingCount,
			"AuctionCount":    auctionCount,
			"CollectionCount": collectionCount,
			"Volume24hWei":    volume24h,
		})
	}
}

func uiListings(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		sort := c.Query("sort", "recent")
		traits := map[string]string{}
		for k, v := range c.Queries() {
			if len(k) > 6 && k[:6] == "trait_" && v != "" {
				traits[k[6:]] = v
			}
		}
		f := db.ListingsFilter{
			Collection: c.Query("collection"),
			Sort:       sort,
			Traits:     traits,
			Limit:      48,
		}
		rows, _ := q.ListActiveListings(c.Context(), f)
		return render(c, "pages/listings.html", fiber.Map{
			"Title":      "Listings",
			"Listings":   rows,
			"Collection": c.Query("collection"),
			"Sort":       sort,
			// Count powers the "X active" badge in pages/listings.html.
			// Renders as `{{shortNumber .Count}}` — shortNumber expects int64.
			"Count": int64(len(rows)),
		})
	}
}

func uiAuctions(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		rows, _ := q.ListAuctions(c.Context(), db.AuctionsFilter{Status: "active", Limit: 24})
		return render(c, "pages/auctions.html", fiber.Map{
			"Title":    "Auctions",
			"Auctions": rows,
			"Now":      time.Now().Unix(),
			// Count powers the "X active" badge in pages/auctions.html.
			// Renders as `{{shortNumber .Count}}` — shortNumber expects int64.
			"Count": int64(len(rows)),
		})
	}
}

func uiAuctionDetail(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, _ := atoi64(c.Params("id"))
		m := auctionPageData(c.Context(), q, id)
		m["Title"] = fmt.Sprintf("Auction #%d", id)
		return render(c, "pages/auction.html", m)
	}
}

func uiOffers(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := strings.ToLower(c.Query("addr"))
		m := offersPageData(c.Context(), q, addr)
		m["Title"] = "Offers"
		return render(c, "pages/offers.html", m)
	}
}

func uiProfile(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := strings.ToLower(c.Params("addr"))
		m := profilePageData(c.Context(), q, addr)
		m["Title"] = "Profile"
		return render(c, "pages/profile.html", m)
	}
}

func uiCollection(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx := c.Context()
		addr := c.Params("addr")
		col, err := q.GetCollection(ctx, addr)
		if err != nil {
			log.Warn().Err(err).Str("collection", addr).Msg("uiCollection: GetCollection")
		}
		// Stats are computed against the collection address; failures fall
		// through to zeros so the page header still renders the collection
		// name + the listing grid below.
		stats, err := q.GetCollectionStats(ctx, addr)
		if err != nil {
			log.Warn().Err(err).Str("collection", addr).Msg("uiCollection: GetCollectionStats")
		}
		listings, err := q.ListActiveListings(ctx, db.ListingsFilter{Collection: addr, Limit: 48})
		if err != nil {
			log.Warn().Err(err).Str("collection", addr).Msg("uiCollection: ListActiveListings")
		}
		title := addr
		if col != nil && col.Name != "" {
			title = col.Name
		}
		return render(c, "pages/collection.html", fiber.Map{
			"Title":      title,
			"Collection": col,
			"Stats":      stats,
			"Listings":   listings,
			"Count":      int64(len(listings)),
		})
	}
}

func uiToken(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := c.Params("addr")
		id := c.Params("id")
		m := tokenPageData(c.Context(), q, addr, id)
		m["Title"] = "Token #" + id
		// Increment view count once per page load (NOT on every 1s partial refresh —
		// that's why this is in uiToken, not in tokenPageData which partialToken
		// also calls).
		_ = q.IncrementTokenViews(c.Context(), addr, id)
		return render(c, "pages/token.html", m)
	}
}

func uiSearch(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		qry := c.Query("q")
		var results []db.SearchResult
		if len(qry) >= 2 {
			results, _ = q.Search(c.Context(), qry, 40)
		}
		return render(c, "pages/search.html", fiber.Map{
			"Title":   "Search",
			"Query":   qry,
			"Results": results,
		})
	}
}

func uiMetrics(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// uiMetrics renders /metrics (HTML); the JSON sibling at
		// /api/v1/metrics calls BuildMarketResponse too so both surfaces
		// see the SAME wrapped map (sse_dropped_total, sse_saturation_streak,
		// flat market fields, plus the metrics_unavailable sentinel when
		// the query races). Passing the wrapped map as `.Metrics` lets the
		// template use the same `{{with .Metrics}}{{.TotalActiveListings}}{{end}}`
		// shape as before, AND access `.metrics_unavailable` inside the
		// with-scope so a single banner template shows the unavailable
		// state on both surfaces.
		metricsMap := api.NewMetricsService(q, nil, nil).BuildResponse(c.Context())
		activity, _ := q.GetRecentTransactions(c.Context(), 20)
		return render(c, "pages/metrics.html", fiber.Map{
			"Title":    "Metrics",
			"Metrics":  metricsMap,
			"Activity": activity,
		})
	}
}

func uiGasMetrics(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		summary, err := q.GetGasMetricsSummary(c.Context())
		if err != nil {
			return render(c, "pages/gas_metrics.html", fiber.Map{
				"Title":          "Gas Costs",
				"Error":          "Gas metrics unavailable",
				"NativeCurrency": config.C.NativeCurrency,
			})
		}
		logs, err := q.GetRecentGasLogs(c.Context(), 50)
		if err != nil {
			logs = []db.GasLogRow{}
		}

		// Compute percentages for the efficiency gauge bars.
		// Protect against division by zero when TotalTxCount is 0.
		var settlePct, refundPct, sweepPct, offerPct float64
		if summary.TotalTxCount > 0 {
			total := float64(summary.TotalTxCount)
			settlePct = float64(summary.SettleCount) / total * 100
			refundPct = float64(summary.RefundLosersCount) / total * 100
			sweepPct = float64(summary.FeeSweepCount) / total * 100
			offerPct = float64(summary.RefundOfferCount) / total * 100
		}

		// Build alerts for anomalous gas cost conditions.
		var alerts []string
		if summary.TotalTxCount > 0 {
			if summary.AvgGasPriceGwei > 100 {
				alerts = append(alerts, "High gas price: avg "+fmt.Sprintf("%.1f", summary.AvgGasPriceGwei)+" gwei — consider adjusting KEEPER_MAX_FEE_CAP_GWEI")
			}
			if summary.AvgGasUsed > 0 && summary.AvgGasUsed > 200000 {
				alerts = append(alerts, "High gas usage per tx: avg "+fmt.Sprintf("%d", summary.AvgGasUsed)+" units — check for stuck auctions")
			}
		}

		return render(c, "pages/gas_metrics.html", fiber.Map{
			"Title":          "Gas Costs",
			"Summary":        summary,
			"RecentLogs":     logs,
			"Alerts":         alerts,
			"NativeCurrency": config.C.NativeCurrency,
			"SettlePct":      settlePct,
			"RefundPct":      refundPct,
			"SweepPct":       sweepPct,
			"OfferPct":       offerPct,
		})
	}
}

func uiAdminStalled(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		return render(c, "pages/admin_stalled.html", fiber.Map{
			"Title": "Admin — Stalled Auctions",
		})
	}
}

// ── HTMX partials ─────────────────────────────────────────────────────────────

func partialListings(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		rows, _ := q.ListActiveListings(c.Context(), db.ListingsFilter{
			Collection: c.Query("collection"),
			Sort:       c.Query("sort", "recent"),
			Limit:      48,
		})
		return render(c, "partials/listing_cards.html", fiber.Map{"Listings": rows})
	}
}

func partialAuctions(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		rows, _ := q.ListAuctions(c.Context(), db.AuctionsFilter{Status: "active", Limit: 24})
		return render(c, "partials/auction_cards.html", fiber.Map{
			"Auctions": rows,
			"Now":      time.Now().Unix(),
		})
	}
}

func partialActivity(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		rows, _ := q.GetRecentTransactions(c.Context(), 15)
		return render(c, "partials/activity_feed.html", fiber.Map{"Activity": rows})
	}
}

// partialToken re-renders the live region of /token/:addr/:id by
// rendering `partials/token_live.html` (data-only, no Alpine x-data)
// with the same data shape uiToken assembles for `pages/token.html`.
// Sharing the data helper guarantees the live partial stays consistent
// with the full page if a new field is added (e.g. RoyaltyInfo).
func partialToken(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := c.Params("addr")
		id := c.Params("id")
		return render(c, "partials/token_live.html", tokenPageData(c.Context(), q, addr, id))
	}
}

// tokenPageData is the single source of truth for everything the
// token page needs. uiToken renders pages/token.html with this map;
// partialToken renders partials/token_live.html with the same map.
// The page-level template wraps the body in an Alpine x-data so the
// user's action-tab / form-input state persists across 1s htmx swaps
// of the live region inside.
func tokenPageData(ctx context.Context, q *db.Q, addr, id string) fiber.Map {
	listing, err := q.GetListing(ctx, addr, id)
	if err != nil && !strings.Contains(err.Error(), "not found") {
		log.Warn().Err(err).Str("collection", addr).Str("token", id).Msg("tokenPageData: GetListing")
	}
	col, err := q.GetCollection(ctx, addr)
	if err != nil {
		log.Warn().Err(err).Str("collection", addr).Msg("tokenPageData: GetCollection")
	}
	var standard string
	var collectionVerified bool
	if col != nil {
		standard = col.Standard
		collectionVerified = col.Verified
	}
	owner, _ := q.GetTokenOwner(ctx, addr, id)
	tokenName, tokenImage, err := q.GetTokenMeta(ctx, addr, id)
	if err != nil {
		log.Warn().Err(err).Str("collection", addr).Str("token", id).Msg("tokenPageData: GetTokenMeta")
	}
	offers, _ := q.ListOffers(ctx, db.OffersFilter{
		Collection: addr,
		TokenID:    id,
		Status:     "pending",
		Limit:      20,
	})
	traits, _ := q.GetTokenAttributes(ctx, addr, id)
	activity, _ := q.GetTokenActivity(ctx, addr, id, 30)
	_, description, _, animationURI, metadataURI, fetchedAt, _ := q.GetTokenFullMetadata(ctx, addr, id)
	return fiber.Map{
		"Listing":            listing,
		"Offers":             offers,
		"Owner":              owner,
		"Standard":           standard,
		"CollectionVerified": collectionVerified,
		"TokenName":          tokenName,
		"TokenImageURI":      tokenImage,
		"Traits":             traits,
		"Activity":           activity,
		"Description":        description,
		"AnimationURI":       animationURI,
		"MetadataURI":        metadataURI,
		"FetchedAt":          fetchedAt,
		"Contract":           addr,
		"TokenID":            id,
	}
}

// partialAuctionDetail re-renders the live region of /auction/:id with
// the same data shape uiAuctionDetail assembles. Bid form + countdown
// + settle button are NOT in the partial — they live on the page-level
// template whose Alpine x-data wraps the grid, so they survive the
// 1s htmx swap of the live region inside.
func partialAuctionDetail(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, _ := atoi64(c.Params("id"))
		return render(c, "partials/auction_live.html", auctionPageData(c.Context(), q, id))
	}
}

// auctionPageData assembles everything the auction page + the auction
// live partial need. See tokenPageData — same single-source-of-truth
// pattern.
func auctionPageData(ctx context.Context, q *db.Q, id int64) fiber.Map {
	auction, err := q.GetAuction(ctx, id)
	var bids any
	var effective any
	if err == nil {
		bids, _ = q.GetBidsForAuction(ctx, id)
		effective, _ = q.GetEffectiveBids(ctx, id)
	}
	now := time.Now()
	m := fiber.Map{
		"Auction":       auction,
		"Bids":          bids,
		"EffectiveBids": effective,
		"Now":           now.Unix(),
		"Ended":         auction != nil && auction.EndsAt.Before(now),
	}
	if auction != nil {
		m["EndsAtUnix"] = auction.EndsAt.Unix()
		m["Title"] = fmt.Sprintf("Auction #%d", auction.AuctionID)
	}
	return m
}

// partialOffers re-renders the live region of /offers. Tab state on the
// page-level template owns the active-tab gating; the partial just
// re-renders the offer rows so accepted/rejected/expired updates appear
// within 1s.
func partialOffers(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := strings.ToLower(c.Query("addr"))
		return render(c, "partials/offers_live.html", offersPageData(c.Context(), q, addr))
	}
}

// offersPageData: see tokenPageData.
func offersPageData(ctx context.Context, q *db.Q, addr string) fiber.Map {
	sent, _ := q.ListOffers(ctx, db.OffersFilter{Bidder: addr, Limit: 50})
	received, _ := q.ListOffers(ctx, db.OffersFilter{Owner: addr, Limit: 50})
	return fiber.Map{
		"Sent":     sent,
		"Received": received,
		"Addr":     addr,
	}
}

// partialProfile re-renders the live region of /profile/:addr (listings
// grid + activity rows). The profile avatar / edit panel is Alpine on
// the page-level template and survives the swap.
func partialProfile(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := strings.ToLower(c.Params("addr"))
		return render(c, "partials/profile_live.html", profilePageData(c.Context(), q, addr))
	}
}

// profilePageData: see tokenPageData.
func profilePageData(ctx context.Context, q *db.Q, addr string) fiber.Map {
	listings, _ := q.ListActiveListings(ctx, db.ListingsFilter{Seller: addr, Limit: 24})
	activity, _ := q.GetRecentTransactions(ctx, 20)
	// "Withdraw required": sweeper-verified pendingReturns balance — the
	// rare case where the contract's automatic refund push failed.
	pendingWei, _ := q.GetVerifiedPendingWithdrawal(ctx, addr)
	// User's auctions (as seller)
	auctions, _ := q.ListAuctions(ctx, db.AuctionsFilter{Seller: addr, Limit: 20})
	// User's offers (sent)
	offersSent, _ := q.ListOffers(ctx, db.OffersFilter{Bidder: addr, Limit: 20})
	// User's offers (received — tokens they own that have pending offers)
	offersReceived, _ := q.ListOffers(ctx, db.OffersFilter{Owner: addr, Limit: 20})
	// User's NFTs in wallet
	nfts, _ := q.WalletNFTs(ctx, addr)
	// Platform-wide metrics
	metrics, _ := q.GetMarketMetrics(ctx)
	return fiber.Map{
		"Addr":           addr,
		"Listings":       listings,
		"Activity":       activity,
		"PendingWei":     pendingWei,
		"Auctions":       auctions,
		"OffersSent":     offersSent,
		"OffersReceived": offersReceived,
		"NFTs":           nfts,
		"Metrics":        metrics,
		"Now":            time.Now().Unix(),
	}
}

// uiProfileRedirect is the bare /profile rescue route. The route table
// has /profile/:addr (a specific user's page) but NOT /profile — a
// user typing that in the address bar (or clicking any link that points
// at the bare path) hits Fiber's 404. We rescue by:
//  1. Walking every cookie on the request.
//  2. Looking for any mw_s_<prefix> cookie (the SIWE session cookie).
//  3. Verifying its JWT against JWT_SECRET + DefaultAudience.
//  4. Returning a 302 to /profile/<sub> where sub = wallet address.
//  5. Falling back to a 307 to /listings when no valid session cookie
//     is present (this is what a logged-out browser sees).
//
// We deliberately use auth.Verify (signature validation) rather than
// jwt.ParseUnverified — a stolen cookie with a forged signature MUST
// be rejected, otherwise an attacker could redirect any visitor to a
// profile they control. The cost is a single HMAC compute per session.
func uiProfileRedirect(c *fiber.Ctx) error {
	for _, name := range cookieNames(c) {
		if !strings.HasPrefix(name, "mw_s_") {
			continue
		}
		raw := c.Cookies(name)
		if raw == "" {
			continue
		}
		addr, _, err := auth.Verify(raw, config.C.JWTSecret, auth.DefaultAudience)
		if err != nil || !isEthAddr(addr) {
			continue
		}
		return c.Redirect("/profile/"+strings.ToLower(addr), fiber.StatusFound)
	}
	// No valid session cookie — send the visitor to the marketplace
	// homepage so they land somewhere useful instead of a 404.
	return c.Redirect("/listings", fiber.StatusTemporaryRedirect)
}

// cookieNames returns every cookie name from the Fiber context. Fiber 2.x
// exposes the cookie name only via c.Cookies(name) and c.Request().Header
// has the raw Cookie header — we parse it ourselves so we don't depend
// on a future Fiber helper. Cheap: ≤ a handful of cookies in practice.
func cookieNames(c *fiber.Ctx) []string {
	hdr := c.GetReqHeaders()["Cookie"]
	if len(hdr) == 0 {
		return nil
	}
	var names []string
	for _, line := range hdr {
		for _, seg := range strings.Split(line, ";") {
			seg = strings.TrimSpace(seg)
			if i := strings.IndexByte(seg, '='); i > 0 {
				names = append(names, seg[:i])
			}
		}
	}
	return names
}

// isEthAddr is a strict-format check on the JWT sub claim — 0x prefix
// + 40 lowercase-or-uppercase hex chars. Anything else is treated as
// "no valid session cookie found" and the request falls through to
// /listings. Defence-in-depth: even if a JWT signing key is leaked,
// the redirect only ever lands on a syntactically-valid EVM address.
func isEthAddr(s string) bool {
	if len(s) != 42 || !strings.HasPrefix(s, "0x") {
		return false
	}
	for _, r := range s[2:] {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// helpers
func atoi64(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

func staticFS() http.FileSystem {
	sub, err := fs.Sub(frontend.FS, "static")
	if err != nil {
		panic("frontend: static sub: " + err.Error())
	}
	return http.FS(sub)
}

// mountStatic serves /static/* self-hosted assets from the embedded FS.
// MaxAge is intentionally short (60s) so a code-level deploy surfaces
// to users within a minute — was 3600s which held returning users on
// stale wallet.js / tailwind.css bytes for up to an hour and masked
// regressions on every deploy. We also still bump `?v=N` on every URL
// in layout.html (force-refetch on version-bump deploys) so this
// short MaxAge is the *baseline* freshness policy, not the only one.
func mountStatic(app *fiber.App) {
	app.Use("/static", filesystem.New(filesystem.Config{
		Root:   staticFS(),
		MaxAge: 60,
	}))
}

// mountAstro serves Astro-built pages from app/dist/ at the root URL prefix.
// In dev mode the Astro dev server handles requests directly (with proxy from
// :4321 to Go on :8080). In production, this route serves the pre-built static
// output so all pages work without a separate Node process.
//
// The filesystem middleware serves Astro's index.html for /, Astro's
// listings/index.html for /listings, etc. For paths where Astro doesn't
// have a page (e.g. /auctions, /token/:addr/:id), the middleware passes
// through to the Go HTMX route handlers below — so the Go backend pages
// remain reachable while Astro takes over the pages it provides.
//
// ASTRO_DIST_DIR env var overrides the path (default "../app/dist" for dev;
// in the Docker image the value is "/app/dist").
func mountAstro(app *fiber.App) {
	distPath := envOrDefault("ASTRO_DIST_DIR", "../app/dist")
	log.Info().Str("path", distPath).Msg("mounting Astro static pages at root /")

	// Custom middleware: serves Astro-built files from distPath when they
	// exist, otherwise calls c.Next() to pass through to the Go HTMX route
	// handlers. Uses fiber.Ctx.SendFile (fasthttp-native) to avoid the
	// net/http ↔ fasthttp type mismatch that http.FileServer would cause.
	app.Use("/", func(c *fiber.Ctx) error {
		path := c.Path()

		// Skip filesystem stat on API, auth, static, SSE, partials, and
		// health-check paths — those are served by dedicated route handlers.
		if strings.HasPrefix(path, "/api/") ||
			strings.HasPrefix(path, "/auth/") ||
			strings.HasPrefix(path, "/static/") ||
			strings.HasPrefix(path, "/partials/") ||
			path == "/healthz" ||
			path == "/readyz" {
			return c.Next()
		}

		// Bare /profile and /profile/ serve the Astro profile page
		// (profile/index.html). If no address is in the URL path,
		// the client-side JS shows a "Connect your wallet" prompt.
		// Previously these were hand-rolled to uiProfileRedirect
		// which 307'd to /listings when no session cookie was found.
		if path == "/profile" || path == "/profile/" {
			if idxPath := filepath.Join(distPath, "profile", "index.html"); fileExists(idxPath) {
				c.Set("Cache-Control", "public, max-age=300")
				return sendHTMLWithConfig(c, idxPath)
			}
			return c.Next()
		}

		// Normalise the path to a file path relative to the Astro dist dir.
		rel := strings.TrimPrefix(path, "/")
		if rel == "" {
			rel = "index.html"
		}

		// Sanitise the relative path to prevent directory traversal.
		// filepath.Join resolves ../ segments via filepath.Clean, so a
		// malicious /../../../etc/passwd would escape distPath. We enforce
		// that the resolved path stays under the Astro dist root.
		cleanRel := filepath.Clean(rel)
		fullPath := filepath.Join(distPath, cleanRel)
		cleanDist := filepath.Clean(distPath)
		if !strings.HasPrefix(fullPath, cleanDist+string(filepath.Separator)) && fullPath != cleanDist {
			// Path escapes the dist directory — reject and pass through.
			return c.Next()
		}

		// Try exact file match.
		if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
			// HTML pages get a short cache (5 min) so deploys surface quickly.
			// Hashed JS/CSS assets (from Vite) get 1 year — they're immutable.
			if strings.HasSuffix(fullPath, ".html") {
				c.Set("Cache-Control", "public, max-age=300")
				return sendHTMLWithConfig(c, fullPath)
			} else {
				c.Set("Cache-Control", "public, max-age=31536000, immutable")
				return c.SendFile(fullPath)
			}
		}

		// Try directory index.html.
		indexPath := filepath.Join(distPath, cleanRel, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			// Redirect /listings → /listings/ so relative asset paths resolve.
			// Use 302 (temporary) so browsers don't cache the redirect — if
			// the site architecture changes, users won't be stuck in a loop.
			if !strings.HasSuffix(c.Path(), "/") {
				return c.Redirect(c.Path()+"/", fiber.StatusFound)
			}
			c.Set("Cache-Control", "public, max-age=300")
			return sendHTMLWithConfig(c, indexPath)
		}

		// Catch-all: Astro pages that use client-side URL parsing.
		// /token/* → token/index.html (JS parses addr + id from pathname)
		// /profile/:addr → profile/index.html (JS parses addr from pathname)
		// /auction/:id → auction/index.html (JS parses id from pathname)
		// /collection/:addr → collection/index.html (JS parses addr from pathname)
		var catchAlls = []struct{ prefix, dir string }{
			{"token/", "token"},
			{"profile/", "profile"},
			{"auction/", "auction"},
			{"collection/", "collection"},
			{"search/", "search"},
		}
		for _, ca := range catchAlls {
			if strings.HasPrefix(cleanRel, ca.prefix) && cleanRel != ca.dir {
				if idxPath := filepath.Join(distPath, ca.dir, "index.html"); fileExists(idxPath) {
					c.Set("Cache-Control", "public, max-age=300")
					return sendHTMLWithConfig(c, idxPath)
				}
			}
		}

		// No Astro file for this path — pass through to Go HTMX routes.
		return c.Next()
	})
}

// jsStringEscape makes a string safe for embedding inside a JavaScript
// single-quoted string literal. It escapes backslashes and single quotes
// — the two characters that could break out of the '...' context and
// enable script injection. Newlines are also escaped to keep the script
// on one line.
func jsStringEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", ``)
	// Escape < so that </script> in a config value cannot terminate
	// the script block early (HTML5 parser ends <script> on </script).
	s = strings.ReplaceAll(s, `<`, `\x3C`)
	return s
}

// astroConfigScript returns the <script> block injected into every Astro HTML
// response so the frontend's window.MW_* globals reflect the running
// server's chain config (RPC URL, chain ID, network name, native currency
// symbol, block explorer URL). Without this, Astro pages fall back to
// the hardcoded Coston2 defaults in BaseLayout.astro and the wallet UI
// shows the wrong chain / currency on mainnet deployments.
//
// The script overwrites the defaults set in BaseLayout.astro <head> ONLY
// if the server's config differs — the `||` fallback chain in the
// BaseLayout script runs first (SSR), then this block runs and replaces
// the values with the authoritative server config.
//
// All string values are jsStringEscape'd to prevent XSS via config values
// containing quotes or backslashes (defence-in-depth even though config
// is operator-controlled).
func astroConfigScript() string {
	return fmt.Sprintf(`<script>
window.MW_CHAIN_ID='%d';
window.MW_RPC_URL='%s';
window.MW_NETWORK_NAME='%s';
window.MW_NATIVE_CURRENCY='%s';
window.MW_EXPLORER='%s';
window.MW_WC_PROJECT_ID='%s';
window.MW_MARKETPLACE='%s';
window.MW_AUCTION='%s';
window.MW_OFFERBOOK='%s';
</script>`,
		config.C.ChainID,
		jsStringEscape(config.C.RPCURL),
		jsStringEscape(config.C.NetworkName),
		jsStringEscape(config.C.NativeCurrency),
		jsStringEscape(config.C.ExplorerURL),
		jsStringEscape(config.C.WCProjectID),
		jsStringEscape(config.C.MarketplaceAddr),
		jsStringEscape(config.C.AuctionAddr),
		jsStringEscape(config.C.OfferBookAddr),
	)
}

// sendHTMLWithConfig serves an Astro-built HTML file with the server's
// chain config injected as a <script> block immediately before </head>.
// This is the Astro equivalent of the Go-template injection that
// render() does for HTMX pages — both paths ensure window.MW_* globals
// reflect the running server's chain (Coston2, Flare mainnet, Songbird)
// without rebuilding the Astro frontend.
//
// It also replaces <span class="mw-cur">C2FLR</span> placeholders with
// the actual native currency symbol server-side, eliminating the flash
// of the default "C2FLR" text that would otherwise appear before the
// client-side JS updater runs.
func sendHTMLWithConfig(c *fiber.Ctx, htmlPath string) error {
	// Check the cache — but validate that the file hasn't changed on disk
	// since it was cached. This handles Astro rebuilds during a rolling
	// deploy without requiring a process restart.
	if cached, ok := htmlCache.Load(htmlPath); ok {
		entry := cached.(htmlCacheEntry)
		if fi, err := os.Stat(htmlPath); err == nil && fi.ModTime().Equal(entry.modtime) {
			// File modtime matches cache timestamp — serve cached content.
			c.Set("Content-Type", "text/html; charset=utf-8")
			return c.SendString(entry.content)
		}
		// File has been updated (or Stat failed) — fall through to recompute.
	}

	body, err := os.ReadFile(htmlPath)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("failed to read page")
	}
	content := string(body)

	// Grab the modtime AFTER reading the file (so the cache timestamp
	// is never older than the contents we cached). The stat is cheap
	// and runs only on cache miss.
	var modtime time.Time
	if fi, err := os.Stat(htmlPath); err == nil {
		modtime = fi.ModTime()
	}

	// Inject the config script just before </head>. The Astro-built
	// HTML always has a </head> tag (BaseLayout.astro guarantees it).
	// Using string replacement is safe because: (1) the script is
	// static per-process (computed once at init from config.C), and
	// (2) </head> appears exactly once in a well-formed HTML document.
	idx := strings.Index(content, "</head>")
	if idx < 0 {
		content = astroConfigScript() + content
	} else {
		content = content[:idx] + astroConfigScript() + content[idx:]
	}

	// Server-side replacement of .mw-cur span content so the correct
	// currency symbol renders immediately (no FOUC).
	curPlaceholder := `<span class="mw-cur">C2FLR</span>`
	curReplacement := `<span class="mw-cur">` + html.EscapeString(config.C.NativeCurrency) + `</span>`
	content = strings.ReplaceAll(content, curPlaceholder, curReplacement)
	// Also update .mw-net-name spans (used in the homepage testnet badge).
	netPlaceholder := `<span class="mw-net-name">Flare Coston2</span>`
	netReplacement := `<span class="mw-net-name">` + html.EscapeString(config.C.NetworkName) + `</span>`
	content = strings.ReplaceAll(content, netPlaceholder, netReplacement)

	// Store in cache before serving. The modtime snapshot ensures the
	// next request can detect if the file was touched by a deploy roll.
	htmlCache.Store(htmlPath, htmlCacheEntry{content: content, modtime: modtime})

	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(content)
}

// htmlCache caches Astro HTML file content with server config injected.
// The astroConfigScript() and currency/network replacements are static per
// process (they only depend on config.C, which is immutable after Load()).
// Without caching, every uncached request re-reads the file from disk and
// re-runs strings.Index + 2× strings.ReplaceAll — avoidable disk I/O and
// string-alloc churn on the hot path. The cache is a sync.Map keyed by
// absolute path; entries store the processed content alongside the file's
// modification time at the time of caching. On lookup, the modtime is
// re-checked via os.Stat: if the file has changed (e.g. an Astro rebuild
// during a rolling deploy), the entry is invalidated and recomputed.
var htmlCache sync.Map

// htmlCacheEntry holds a cached HTML page together with the file modtime
// at the moment it was cached. On lookup, the caller must verify that
// the file's current modtime still matches entry.modtime — if not, the
// cache is stale and must be recomputed.
type htmlCacheEntry struct {
	content string
	modtime time.Time
}

// envOrDefault reads an env var, returning the default if empty or unset.
func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// fileExists returns true if the path is a regular file (not a directory).
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
