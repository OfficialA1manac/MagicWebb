package api

import (
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// ProfilePageService serves GET /api/v1/profile-page/:addr — a single
// composite of every list the profile page needs (listings, auctions, offers
// sent/received, platform metrics, activity, created collections), fetched
// concurrently in one round trip. It collapses six separate REST calls plus a
// wasteful /collections?limit=200 client-side filter into one request.
//
// It deliberately does NOT include the wallet NFT inventory or the profile
// row: those stay as their own endpoints because the wallet endpoint carries
// the explorer-merge + X-MW-Degraded semantics the client relies on to keep a
// last-known-good grid, and the profile endpoint carries cross-chain
// carry-over. Both are already cached and correct; folding them in here would
// duplicate that logic. Net: the profile page goes from 9 requests to 3
// (this + profile + wallet), all in parallel.
type ProfilePageService struct {
	q       *db.Q
	metrics *MetricsService
}

func NewProfilePageService(q *db.Q, metrics *MetricsService) *ProfilePageService {
	return &ProfilePageService{q: q, metrics: metrics}
}

func (s *ProfilePageService) RegisterRoutes(api fiber.Router) {
	api.Get("/profile-page/:addr", s.handleGet)
}

type profilePageResponse struct {
	Listings           []db.ListingRow    `json:"listings"`
	Auctions           []db.AuctionRow    `json:"auctions"`
	OffersSent         []db.OfferRow      `json:"offersSent"`
	OffersReceived     []db.OfferRow      `json:"offersReceived"`
	Metrics            fiber.Map          `json:"metrics"`
	Activity           []db.ActivityRow   `json:"activity"`
	CreatedCollections []db.CollectionRow `json:"createdCollections"`
}

func (s *ProfilePageService) handleGet(c *fiber.Ctx) error {
	addr := strings.ToLower(c.Params("addr"))
	if !isValidHexAddress(addr) {
		return writeErr(c, fiber.StatusBadRequest, "address required")
	}

	var resp profilePageResponse

	// Best-effort, section-level: a single failing list must not withhold the
	// whole page. Each section that errors is logged and left empty; the
	// response is always 200. Profile identity and wallet NFTs live in their
	// own endpoints (the client's last-known-good guard keys off those), so an
	// empty list section here degrades gracefully rather than blanking a good
	// profile. All sections run concurrently, metrics included.
	var wg sync.WaitGroup
	ctx := c.Context()
	run := func(name string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				log.Warn().Err(err).Str("section", name).Str("addr", addr).
					Msg("profile-page: section failed, serving it empty")
			}
		}()
	}

	run("listings", func() error {
		rows, err := s.q.ListActiveListings(ctx, db.ListingsFilter{Seller: addr, Sort: "recent", Limit: 24})
		resp.Listings = rows
		return err
	})
	run("auctions", func() error {
		rows, err := s.q.ListAuctions(ctx, db.AuctionsFilter{Seller: addr, Limit: 20})
		resp.Auctions = rows
		return err
	})
	run("offersSent", func() error {
		rows, err := s.q.ListOffers(ctx, db.OffersFilter{Bidder: addr, Limit: 50})
		resp.OffersSent = rows
		return err
	})
	run("offersReceived", func() error {
		rows, err := s.q.ListOffers(ctx, db.OffersFilter{Owner: addr, Limit: 50})
		resp.OffersReceived = rows
		return err
	})
	run("activity", func() error {
		rows, err := s.q.GetRecentTransactionsByAddress(ctx, addr, 20)
		resp.Activity = rows
		return err
	})
	run("createdCollections", func() error {
		rows, err := s.q.ListCollectionsByCreator(ctx, addr)
		resp.CreatedCollections = rows
		return err
	})
	run("metrics", func() error {
		resp.Metrics = s.metrics.BuildResponse(ctx)
		return nil
	})

	wg.Wait()

	// Guarantee [] not null so the client's Array checks are simple.
	if resp.Listings == nil {
		resp.Listings = []db.ListingRow{}
	}
	if resp.Auctions == nil {
		resp.Auctions = []db.AuctionRow{}
	}
	if resp.OffersSent == nil {
		resp.OffersSent = []db.OfferRow{}
	}
	if resp.OffersReceived == nil {
		resp.OffersReceived = []db.OfferRow{}
	}
	if resp.Activity == nil {
		resp.Activity = []db.ActivityRow{}
	}
	if resp.CreatedCollections == nil {
		resp.CreatedCollections = []db.CollectionRow{}
	}

	return c.JSON(resp)
}
