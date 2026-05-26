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

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/ui"
)

// render executes the template for the given key.
// Pages ("pages/home.html") execute via their base name which calls layout.
// Partials ("partials/listing_cards.html") execute by their defined name.
func render(c *fiber.Ctx, name string, data any) error {
	t, ok := ui.Templates[name]
	if !ok {
		return c.Status(fiber.StatusInternalServerError).SendString("template not found: " + name)
	}
	c.Set("Content-Type", "text/html; charset=utf-8")
	execName := filepath.Base(name) // "home.html", "listing_cards.html", etc.
	if strings.HasPrefix(name, "partials/") {
		execName = name // partials use their full defined name
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
			"Listings": listings,
			"Trending": trending,
			"Activity": activity,
		})
	}
}

func uiListings(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		f := db.ListingsFilter{
			Collection: c.Query("collection"),
			Limit:      48,
		}
		rows, _ := q.ListActiveListings(c.Context(), f)
		return render(c, "pages/listings.html", fiber.Map{"Listings": rows})
	}
}

func uiAuctions(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		rows, _ := q.ListAuctions(c.Context(), db.AuctionsFilter{Status: "active", Limit: 24})
		return render(c, "pages/auctions.html", fiber.Map{"Auctions": rows})
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
		return render(c, "pages/auction.html", fiber.Map{
			"Auction": auction,
			"Bids":    bids,
			"Now":     time.Now().Unix(),
		})
	}
}

func uiOffers(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := strings.ToLower(c.Query("addr"))
		sent, _ := q.ListOffers(c.Context(), db.OffersFilter{Bidder: addr, Limit: 50})
		received, _ := q.ListOffers(c.Context(), db.OffersFilter{Owner: addr, Limit: 50})
		return render(c, "pages/offers.html", fiber.Map{
			"Sent":     sent,
			"Received": received,
			"Addr":     addr,
		})
	}
}

func uiProfile(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := c.Params("addr")
		listings, _ := q.ListActiveListings(c.Context(), db.ListingsFilter{Seller: addr, Limit: 24})
		return render(c, "pages/profile.html", fiber.Map{
			"Addr":     addr,
			"Listings": listings,
		})
	}
}

func uiCollection(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := c.Params("addr")
		col, _ := q.GetCollection(c.Context(), addr)
		listings, _ := q.ListActiveListings(c.Context(), db.ListingsFilter{Collection: addr, Limit: 48})
		return render(c, "pages/collection.html", fiber.Map{
			"Collection": col,
			"Listings":   listings,
		})
	}
}

func uiToken(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := c.Params("addr")
		id := c.Params("id")
		listing, _ := q.GetListing(c.Context(), addr, id)
		return render(c, "pages/token.html", fiber.Map{
			"Contract": addr,
			"TokenID":  id,
			"Listing":  listing,
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
		return render(c, "pages/search.html", fiber.Map{"Query": qry, "Results": results})
	}
}

func uiMetrics(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		m, _ := q.GetMarketMetrics(c.Context())
		activity, _ := q.GetRecentTransactions(c.Context(), 20)
		return render(c, "pages/metrics.html", fiber.Map{"Metrics": m, "Activity": activity})
	}
}

// ── HTMX partials ─────────────────────────────────────────────────────────────

func partialListings(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		rows, _ := q.ListActiveListings(c.Context(), db.ListingsFilter{
			Collection: c.Query("collection"),
			Limit:      48,
		})
		return render(c, "partials/listing_cards.html", fiber.Map{"Listings": rows})
	}
}

func partialAuctions(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		rows, _ := q.ListAuctions(c.Context(), db.AuctionsFilter{Status: "active", Limit: 24})
		return render(c, "partials/auction_cards.html", fiber.Map{"Auctions": rows})
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

// staticFS returns a http.FileSystem rooted at the embedded static/ directory.
func staticFS() http.FileSystem {
	sub, err := fs.Sub(ui.FS, "static")
	if err != nil {
		panic("ui: static sub: " + err.Error())
	}
	return http.FS(sub)
}

// mountStatic registers the /static route using the embedded FS.
func mountStatic(app *fiber.App) {
	app.Use("/static", filesystem.New(filesystem.Config{
		Root:   staticFS(),
		MaxAge: 3600,
	}))
}
