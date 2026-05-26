package api

import (
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

func marketMetrics(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		m, err := q.GetMarketMetrics(c.Context())
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		return c.JSON(m)
	}
}

func recentActivity(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		limit := 50
		if s := c.Query("limit"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				limit = n
			}
		}
		rows, err := q.GetRecentTransactions(c.Context(), limit)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		return c.JSON(rows)
	}
}
