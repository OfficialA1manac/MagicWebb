package api

import (
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

func search(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		query := c.Query("q")
		if len(query) < 2 {
			return writeErr(c, fiber.StatusBadRequest, "q must be at least 2 characters")
		}
		limit := 20
		if lim := c.Query("limit"); lim != "" {
			if n, err := strconv.Atoi(lim); err == nil {
				limit = n
			}
		}
		results, err := q.Search(c.Context(), query, limit)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		if results == nil {
			results = []db.SearchResult{}
		}
		return c.JSON(results)
	}
}
