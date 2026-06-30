package api

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/chain"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// ListingsService handles listing-related API operations.
type ListingsService struct {
	q   *db.Q
	eth chain.Caller
}

// NewListingsService creates a ListingsService.
func NewListingsService(q *db.Q, eth chain.Caller) *ListingsService {
	return &ListingsService{q: q, eth: eth}
}

// RegisterRoutes registers all listing-related routes under the given router group.
func (s *ListingsService) RegisterRoutes(api fiber.Router) {
	api.Get("/listings", ValidateQuery(QuerySchema{
		{Name: "collection", Type: ParamAddress},
		{Name: "seller", Type: ParamAddress},
		{Name: "sort", OneOf: []string{"recent", "price_asc", "price_desc"}},
		{Name: "limit", Type: ParamInt},
		{Name: "min_price", Type: ParamWei},
		{Name: "max_price", Type: ParamWei},
		{Name: "traits"},
	}), s.handleList)
	api.Get("/listings/:collection/:id/preflight", s.handlePreflight)
	api.Get("/listings/:collection/:id", s.handleGet)
	api.Post("/token/:collection/:id/view", s.handleTokenView)
}

func (s *ListingsService) handleList(c *fiber.Ctx) error {
	f := db.ListingsFilter{
		Collection: c.Query("collection"),
		Seller:     c.Query("seller"),
		Sort:       c.Query("sort", "recent"),
	}
	if lim := c.Query("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil {
			if n < 1 {
				n = 1
			} else if n > 100 {
				n = 100
			}
			f.Limit = n
		}
	}
	// Parse price range filters (in wei) with validation
	if mp := c.Query("min_price"); mp != "" {
		if !isValidWeiStr(mp) {
			return writeErr(c, fiber.StatusBadRequest, "min_price must be a non-negative integer wei value")
		}
		f.MinPriceWei = mp
	}
	if mp := c.Query("max_price"); mp != "" {
		if !isValidWeiStr(mp) {
			return writeErr(c, fiber.StatusBadRequest, "max_price must be a non-negative integer wei value")
		}
		f.MaxPriceWei = mp
	}
	// Parse trait filters: traits=trait_type:value,trait_type:value
	if traits := c.Query("traits"); traits != "" {
		f.Traits = map[string]string{}
		for _, pair := range strings.Split(traits, ",") {
			parts := strings.SplitN(pair, ":", 2)
			if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
				f.Traits[parts[0]] = parts[1]
			}
		}
	}
	rows, err := s.q.ListActiveListings(c.Context(), f)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if rows == nil {
		rows = []db.ListingRow{}
	}
	return c.JSON(rows)
}

func (s *ListingsService) handleGet(c *fiber.Ctx) error {
	collection := c.Params("collection")
	id := c.Params("id")
	row, err := s.q.GetListing(c.Context(), collection, id)
	if err != nil {
		if isNotFound(err) {
			return writeErr(c, fiber.StatusNotFound, "listing not found")
		}
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	return c.JSON(row)
}

func (s *ListingsService) handlePreflight(c *fiber.Ctx) error {
	return listingPreflightWithChain(s.q, s.eth)(c)
}

// handleTokenView increments the view counter for a token (fire-and-forget).
// This is a lightweight POST — no auth required, no response body beyond 204.
func (s *ListingsService) handleTokenView(c *fiber.Ctx) error {
	collection := c.Params("collection")
	id := c.Params("id")
	_ = s.q.IncrementTokenViews(c.Context(), collection, id)
	return c.SendStatus(fiber.StatusNoContent)
}
