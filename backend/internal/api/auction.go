package api

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

func listAuctions(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		f := db.AuctionsFilter{
			Collection: c.Query("collection"),
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
		rows, err := q.ListAuctions(c.Context(), f)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		if rows == nil {
			rows = []db.AuctionRow{}
		}
		return c.JSON(rows)
	}
}

func getAuction(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil {
			return writeErr(c, fiber.StatusBadRequest, "invalid auction id")
		}
		row, err := q.GetAuction(c.Context(), id)
		if err != nil {
			if isNotFound(err) {
				return writeErr(c, fiber.StatusNotFound, "auction not found")
			}
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		return c.JSON(row)
	}
}

func serverTime() fiber.Handler {
	return func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"unix_ms": time.Now().UnixMilli()})
	}
}

func getAuctionBids(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := strconv.ParseInt(c.Params("id"), 10, 64)
		if err != nil {
			return writeErr(c, fiber.StatusBadRequest, "invalid auction id")
		}
		rows, err := q.GetBidsForAuction(c.Context(), id)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		if rows == nil {
			rows = []db.BidRow{}
		}
		return c.JSON(rows)
	}
}
