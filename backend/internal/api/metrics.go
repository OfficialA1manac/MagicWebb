package api

import (
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

func marketMetrics(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		m, err := q.GetMarketMetrics(c.Context())
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		// Saturate SSE counters alongside the marketplace metrics so the
		// /metrics page gets the saturation alert panel in the same response.
		// Monotonically increasing counter + consecutive-streak gauge: UI shows
		// a banner when streak > 0, a single counter chip when dropped_total > 0.
		dropped := sse.DroppedTotal.Load()
		streak  := sse.SaturationStreak.Load()
		if m == nil {
			return c.JSON(fiber.Map{
				"sse_dropped_total":     dropped,
				"sse_saturation_streak": streak,
			})
		}
		// Flatten the response shape: db-side fields at the top, the SSE
		// saturation fields appended alongside. Frontend templates can render
		// both from the same map.
		out := fiber.Map{
			"sse_dropped_total":     dropped,
			"sse_saturation_streak": streak,
		}
		// Best-effort flatten via JSON roundtrip is unnecessary; we explicitly
		// memcpy the market map fields by marshalling + re-decoding if the
		// shape is opaque. For now we keep market as a nested field for the
		// template convenience (`{{ .Metrics.TotalActiveListings }}`).
		out["market"] = m
		return c.JSON(out)
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
