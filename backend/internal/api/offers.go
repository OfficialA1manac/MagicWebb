package api

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// OffersService handles offer-related API operations.
type OffersService struct {
	q *db.Q
}

// NewOffersService creates an OffersService.
func NewOffersService(q *db.Q) *OffersService {
	return &OffersService{q: q}
}

// RegisterRoutes registers all offer-related routes under the given router group.
func (s *OffersService) RegisterRoutes(api fiber.Router) {
	api.Get("/offers", s.handleList)
	api.Get("/offers/:collection/:id/position", s.handlePosition)
}

// handleList returns offer positions, filterable by collection/token/bidder/owner/status.
func (s *OffersService) handleList(c *fiber.Ctx) error {
	f := db.OffersFilter{
		Collection: c.Query("collection"),
		TokenID:    c.Query("token_id"),
		Bidder:     c.Query("bidder"),
		Owner:      c.Query("owner"),
		Status:     c.Query("status"),
	}
	if lim := c.Query("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil {
			if n < 1 {
				n = 1
			} else if n > 200 {
				n = 200
			}
			f.Limit = n
		}
	}
	rows, err := s.q.ListOffers(c.Context(), f)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if rows == nil {
		rows = []db.OfferRow{}
	}
	return c.JSON(rows)
}

// handlePosition aggregates all pending positions on one token.
func (s *OffersService) handlePosition(c *fiber.Ctx) error {
	coll := strings.ToLower(c.Params("collection"))
	tokenID := c.Params("id")
	rows, err := s.q.GetActiveOffersForToken(c.Context(), coll, tokenID, 200)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if rows == nil {
		rows = []db.OfferRow{}
	}
	total := new(big.Int)
	best := "0"
	for _, r := range rows {
		if p, ok := new(big.Int).SetString(r.AmountWei, 10); ok {
			total.Add(total, p)
		}
	}
	if len(rows) > 0 {
		best = rows[0].AmountWei // rows ordered principal DESC
	}
	truncated := len(rows) >= 200
	return c.JSON(fiber.Map{
		"collection": coll,
		"token_id":   tokenID,
		"positions":  rows,
		"count":      len(rows),
		"highest":    best,
		"total_wei":  total.String(),
		"truncated":  truncated,
	})
}
