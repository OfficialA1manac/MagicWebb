package api

import (
	"strconv"

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
	api.Get("/listings", s.handleList)
	api.Get("/listings/:collection/:id/preflight", s.handlePreflight)
	api.Get("/listings/:collection/:id", s.handleGet)
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
