package api

import (
	"math/big"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// listOffers returns offer positions, filterable by collection/token/bidder/owner/status.
func listOffers(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		f := db.OffersFilter{
			Collection: c.Query("collection"),
			TokenID:    c.Query("token_id"),
			Bidder:     c.Query("bidder"),
			Owner:      c.Query("owner"),
			Status:     c.Query("status"),
		}
		if lim := c.Query("limit"); lim != "" {
			if n, err := strconv.Atoi(lim); err == nil {
				f.Limit = n
			}
		}
		rows, err := q.ListOffers(c.Context(), f)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		if rows == nil {
			rows = []db.OfferRow{}
		}
		return c.JSON(rows)
	}
}

// offerPosition aggregates all pending positions on one token: the individual
// stacked positions plus the highest position and total escrowed across bidders.
func offerPosition(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		coll := strings.ToLower(c.Params("collection"))
		tokenID := c.Params("id")
		rows, err := q.GetActiveOffersForToken(c.Context(), coll, tokenID, 200)
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
		return c.JSON(fiber.Map{
			"collection": coll,
			"token_id":   tokenID,
			"positions":  rows,
			"count":      len(rows),
			"highest":    best,
			"total_wei":  total.String(),
		})
	}
}
