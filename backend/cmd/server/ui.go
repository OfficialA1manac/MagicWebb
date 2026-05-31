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

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/ui"
)

// render executes the named template, injecting global contract addresses
// and the WalletConnect projectId into the layout-level context.
func render(c *fiber.Ctx, name string, data any) error {
	t, ok := ui.Templates[name]
	if !ok {
		return c.Status(fiber.StatusInternalServerError).SendString("template not found: " + name)
	}
	// Layout uses .MarketplaceAddr / .AuctionAddr / .OfferBookAddr / .WCProjectID
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
		listings, _ := q.ListActiveListings(c.Context(), db.ListingsFilter{Limit: 12})
		trending, _ := q.GetTrendingCollections(c.Context(), "24h", 8)
		activity, _ := q.GetRecentTransactions(c.Context(), 10)
		return render(c, "pages/home.html", fiber.Map{
			"Title":    "Home",
			"Listings": listings,
			"Trending": trending,
			"Activity": activity,
		})
	}
}

func uiListings(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		sort := c.Query("sort", "recent")
		f := db.ListingsFilter{
			Collection: c.Query("collection"),
			Sort:       sort,
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
		now := time.Now()
		return render(c, "pages/auction.html", fiber.Map{
			"Title":      fmt.Sprintf("Auction #%d", auction.AuctionID),
			"Auction":    auction,
			"Bids":       bids,
			"Now":        now.Unix(),
			"Ended":      auction.EndsAt.Before(now),
			"EndsAtUnix": auction.EndsAt.Unix(),
		})
	}
}

func uiOffers(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := strings.ToLower(c.Query("addr"))
		var sent []db.OfferPositionRow
		var received []db.OfferPositionRow
		if addr != "" {
			sent, _ = q.GetOfferPositionsByBidder(c.Context(), addr)
			// "received" = positions on tokens this address owns. Approximate via nft_ownership.
			received, _ = q.GetReceivedOfferPositions(c.Context(), addr)
		}
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
		profile, _ := q.GetProfile(c.Context(), addr)
		if profile == nil {
			profile = &db.ProfileRow{Address: addr}
		}
		listings, _ := q.ListActiveListings(c.Context(), db.ListingsFilter{Seller: addr, Limit: 24})
		activity, _ := q.GetRecentTransactions(c.Context(), 20)
		return render(c, "pages/profile.html", fiber.Map{
			"Title":    "Profile",
			"Addr":     addr,
			"Profile":  profile,
			"Listings": listings,
			"Activity": activity,
		})
	}
}

func uiCollection(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := c.Params("addr")
		col, _ := q.GetCollection(c.Context(), addr)
		listings, _ := q.ListActiveListings(c.Context(), db.ListingsFilter{Collection: addr, Limit: 48})
		title := addr
		if col != nil && col.Name != "" {
			title = col.Name
		}
		return render(c, "pages/collection.html", fiber.Map{
			"Title":      title,
			"Collection": col,
			"Listings":   listings,
		})
	}
}

func uiToken(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := strings.ToLower(c.Params("addr"))
		id := c.Params("id")
		listings, _ := q.GetListingsForToken(c.Context(), addr, id)
		var primaryListing *db.ListingRow
		if len(listings) > 0 {
			primaryListing = &listings[0]
		}
		positions, _ := q.GetOfferPositionsForToken(c.Context(), addr, id)
		attrs, _ := q.GetNFTAttributes(c.Context(), addr, id)
		return render(c, "pages/token.html", fiber.Map{
			"Title":     "Token #" + id,
			"Contract":  addr,
			"TokenID":   id,
			"Listing":   primaryListing,
			"Listings":  listings,
			"Positions": positions,
			"Attrs":     attrs,
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
