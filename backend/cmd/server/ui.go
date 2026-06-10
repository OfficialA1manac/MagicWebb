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

// render executes the named template.
func render(c *fiber.Ctx, name string, data any) error {
	t, ok := ui.Templates[name]
	if !ok {
		return c.Status(fiber.StatusInternalServerError).SendString("template not found: " + name)
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
		addr := c.Params("addr")
		listings, _ := q.ListActiveListings(c.Context(), db.ListingsFilter{Seller: addr, Limit: 24})
		activity, _ := q.GetRecentTransactions(c.Context(), 20)
		return render(c, "pages/profile.html", fiber.Map{
			"Title":    "Profile",
			"Addr":     addr,
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
		addr := c.Params("addr")
		id := c.Params("id")
		listing, _ := q.GetListing(c.Context(), addr, id)
		offers, _ := q.ListOffers(c.Context(), db.OffersFilter{
			Collection: addr,
			TokenID:    id,
			Status:     "pending",
			Limit:      20,
		})
		return render(c, "pages/token.html", fiber.Map{
			"Title":    "Token #" + id,
			"Contract": addr,
			"TokenID":  id,
			"Listing":  listing,
			"Offers":   offers,
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
