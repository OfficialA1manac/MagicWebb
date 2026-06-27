package api

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// AuctionsService handles auction-related API operations.
type AuctionsService struct {
	q *db.Q
}

// NewAuctionsService creates an AuctionsService.
func NewAuctionsService(q *db.Q) *AuctionsService {
	return &AuctionsService{q: q}
}

// RegisterRoutes registers all auction-related routes under the given router group.
func (s *AuctionsService) RegisterRoutes(api fiber.Router) {
	api.Get("/auctions", s.handleList)
	api.Get("/auctions/:id", s.handleGet)
	api.Get("/auctions/:id/bids", s.handleBids)
	api.Get("/server-time", handleServerTime)
}

func (s *AuctionsService) handleList(c *fiber.Ctx) error {
	f := db.AuctionsFilter{
		Collection: c.Query("collection"),
		Seller:     c.Query("seller"),
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
	rows, err := s.q.ListAuctions(c.Context(), f)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if rows == nil {
		rows = []db.AuctionRow{}
	}
	return c.JSON(rows)
}

func (s *AuctionsService) handleGet(c *fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return writeErr(c, fiber.StatusBadRequest, "invalid auction id")
	}
	row, err := s.q.GetAuction(c.Context(), id)
	if err != nil {
		if isNotFound(err) {
			return writeErr(c, fiber.StatusNotFound, "auction not found")
		}
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	return c.JSON(row)
}

func (s *AuctionsService) handleBids(c *fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return writeErr(c, fiber.StatusBadRequest, "invalid auction id")
	}
	rows, err := s.q.GetBidsForAuction(c.Context(), id)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if rows == nil {
		rows = []db.BidRow{}
	}
	return c.JSON(rows)
}

func handleServerTime(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"unix_ms": time.Now().UnixMilli()})
}
