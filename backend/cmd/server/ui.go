package main

import (
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/ui"
)

// render executes the named template, injecting the contract addresses and
// WalletConnect project id the layout passes to wallet.js — server config is
// the single source of truth so a redeploy can never strand the frontend on
// stale hardcoded addresses.
func render(c *fiber.Ctx, name string, data any) error {
	t, ok := ui.Templates[name]
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
		})
	}
}

func uiAuctionDetail(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, _ := atoi64(c.Params("id"))
		auction, err := q.GetAuction(c.Context(), id)
		if err != nil {
			return c.Status(fiber.StatusNotFound).SendString("auction not found")
		}
		bids, _ := q.GetBidsForAuction(c.Context(), id)
		// Cumulative model: per-bidder effective totals (sum of all their bids),
		// highest first — row 0 is the current winner-elect.
		effective, _ := q.GetEffectiveBids(c.Context(), id)
		now := time.Now()
		return render(c, "pages/auction.html", fiber.Map{
			"Title":         fmt.Sprintf("Auction #%d", auction.AuctionID),
			"Auction":       auction,
			"Bids":          bids,
			"EffectiveBids": effective,
			"Now":           now.Unix(),
			"Ended":         auction.EndsAt.Before(now),
			"EndsAtUnix":    auction.EndsAt.Unix(),
		})
	}
}

func uiOffers(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := strings.ToLower(c.Query("addr"))
		sent, _ := q.ListOffers(c.Context(), db.OffersFilter{Bidder: addr, Limit: 50})
		received, _ := q.ListOffers(c.Context(), db.OffersFilter{Owner: addr, Limit: 50})
		return render(c, "pages/offers.html", fiber.Map{
			"Title":    "Offers",
			"Sent":     sent,
			"Received": received,
			"Addr":     addr,
		})
	}
}

func uiProfile(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := strings.ToLower(c.Params("addr"))
		listings, _ := q.ListActiveListings(c.Context(), db.ListingsFilter{Seller: addr, Limit: 24})
		activity, _ := q.GetRecentTransactions(c.Context(), 20)
		// "Withdraw required": sweeper-verified pendingReturns balance — the
		// rare case where the contract's automatic refund push failed.
		pendingWei, _ := q.GetVerifiedPendingWithdrawal(c.Context(), addr)
		return render(c, "pages/profile.html", fiber.Map{
			"Title":      "Profile",
			"Addr":       addr,
			"Listings":   listings,
			"Activity":   activity,
			"PendingWei": pendingWei,
		})
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
		ctx := c.Context()
		addr := c.Params("addr")
		id := c.Params("id")
		listing, err := q.GetListing(ctx, addr, id)
		if err != nil {
			// Not-found is expected for unlisted tokens; only log warnings
			// for actual DB failures.
			if !strings.Contains(err.Error(), "not found") {
				log.Warn().Err(err).Str("collection", addr).Str("token", id).Msg("uiToken: GetListing")
			}
		}
		col, err := q.GetCollection(ctx, addr)
		if err != nil {
			log.Warn().Err(err).Str("collection", addr).Msg("uiToken: GetCollection")
		}
		var standard string
		var collectionVerified bool
		if col != nil {
			standard = col.Standard
			collectionVerified = col.Verified
		}
		owner, err := q.GetTokenOwner(ctx, addr, id)
		if err != nil {
			log.Warn().Err(err).Str("collection", addr).Str("token", id).Msg("uiToken: GetTokenOwner")
		}
		tokenName, tokenImage, err := q.GetTokenMeta(ctx, addr, id)
		if err != nil {
			log.Warn().Err(err).Str("collection", addr).Str("token", id).Msg("uiToken: GetTokenMeta")
		}
		offers, err := q.ListOffers(ctx, db.OffersFilter{
			Collection: addr,
			TokenID:    id,
			Status:     "pending",
			Limit:      20,
		})
		if err != nil {
			log.Warn().Err(err).Str("collection", addr).Str("token", id).Msg("uiToken: ListOffers")
		}
		traits, err := q.GetTokenAttributes(ctx, addr, id)
		if err != nil {
			log.Warn().Err(err).Str("collection", addr).Str("token", id).Msg("uiToken: GetTokenAttributes")
		}
		return render(c, "pages/token.html", fiber.Map{
			"Title":              "Token #" + id,
			"Contract":           addr,
			"TokenID":            id,
			"Listing":            listing,
			"Offers":             offers,
			"Owner":              owner,
			"Standard":           standard,
			"CollectionVerified": collectionVerified,
			"TokenName":          tokenName,
			"TokenImageURI":      tokenImage,
			"Traits":             traits,
			// Description placeholder: when an upgraded indexer stores a
			// description column, surface it here. Today the field is empty
			// so the about-card is hidden — keeping the wiring in place
			// means a future migration needs no template change.
			"Description": "",
		})
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
		m, _ := q.GetMarketMetrics(c.Context())
		activity, _ := q.GetRecentTransactions(c.Context(), 20)
		return render(c, "pages/metrics.html", fiber.Map{
			"Title":    "Metrics",
			"Metrics":  m,
			"Activity": activity,
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

// helpers
func atoi64(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

func staticFS() http.FileSystem {
	sub, err := fs.Sub(ui.FS, "static")
	if err != nil {
		panic("ui: static sub: " + err.Error())
	}
	return http.FS(sub)
}

func mountStatic(app *fiber.App) {
	app.Use("/static", filesystem.New(filesystem.Config{
		Root:   staticFS(),
		MaxAge: 3600,
	}))
}
