package api

import (
	"encoding/json"
	"math"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// mantissaBound is the largest integer losslessly representable in float64
// (1<<53 = 9007199254740992). Values outside ±mantissaBound should stay as
// float64 once they survive the JSON round-trip so the consumer reads the
// approximation explicitly rather than silently reading garbage after a
// lossy cast. Declared at package scope so both reattachIntFields (map)
// and reattachSlice ([]any) share the same boundary without inner duplicates.
const mantissaBound = 1 << 53

// marketMetrics returns the canonical marketplace metrics plus the SSE
// publisher's saturation counters. The response is FLAT (top-level keys):
// the existing /metrics template uses .Metrics.TotalActiveListings /
// .TotalSales / .GrossVolumeWei / .TotalAuctions, so wrapping under .market
// would silently break that binding. The round-trip via json.Marshal +
// json.Unmarshal into map[string]any keeps original field names verbatim;
// reattachIntFields() converts Go's default-JSON-decoder float64 back to
// int64 for whole-number fields within the lossless 2^53 mantissa range.
//
// Marshal/Unmarshal failures are NOT silently dropped: the response still
// returns 200 with the SSE counters and a `metrics_unavailable` sentinel
// field carrying the failure reason so the /metrics template can render an
// explicit "Market metrics temporarily unavailable" banner instead of
// rendering zero-valued tiles silently. log.Error records the actual
// failure for operators.
func marketMetrics(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		m, err := q.GetMarketMetrics(c.Context())
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		out := fiber.Map{
			"sse_dropped_total":     sse.DroppedTotal.Load(),
			"sse_saturation_streak": sse.SaturationStreak.Load(),
		}
		if m == nil {
			out["metrics_unavailable"] = "no market metrics data"
			return c.JSON(out)
		}
		b, merr := json.Marshal(m)
		if merr != nil {
			log.Error().Err(merr).Msg("metrics: marshal failed")
			out["metrics_unavailable"] = "marshal failed: " + merr.Error()
			return c.JSON(out)
		}
		var flat map[string]any
		if uerr := json.Unmarshal(b, &flat); uerr != nil {
			log.Error().Err(uerr).Msg("metrics: unmarshal failed")
			out["metrics_unavailable"] = "unmarshal failed: " + uerr.Error()
			return c.JSON(out)
		}
		reattachIntFields(flat)
		for k, v := range flat {
			out[k] = v
		}
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

// reattachIntFields walks a map[string]any decoded from JSON and re-casts
// every numeric float64 -> int64 if the value is whole AND fits within the
// float64 mantissa precision boundary (lossless round-trip). Recurses into
// nested maps AND slice-of-[]any so a struct with sub-structs OR nested
// array fields gets the same protection.
//
// Boundary:
//   - ±mantissaBound (±1<<53 = ±9007199254740992): exact integers.
//   - NaN: math.Trunc(NaN)=NaN; NaN!=NaN -> falls through equality guard.
//   - +Inf: math.Trunc(+Inf)=+Inf; +Inf==+Inf TRUE but +Inf<=mantissaBound
//     is FALSE (IEEE 754: no finite bound satisfies Inf) -> stays as float64.
//   - market struct has int64 + string fields only (no *big.Int); wei-string
//     field stays as string and never touches the math path.
func reattachIntFields(m map[string]any) {
	for k, v := range m {
		switch t := v.(type) {
		case float64:
			if t == math.Trunc(t) && t >= -mantissaBound && t <= mantissaBound {
				m[k] = int64(t)
			}
		case map[string]any:
			reattachIntFields(t)
		case []any:
			reattachSlice(t)
		}
	}
}

// reattachSlice handles the []any branch (and recursive [][]any / []map[]any
// / mixed nested arrays) so a future market struct with sub-array fields
// gets the same lossless float64->int64 reattach. Without this, a 2D JSON
// array would silently keep its inner floats as float64 even when they are
// integer-valued within mantissa range.
func reattachSlice(s []any) {
	for i, item := range s {
		switch inner := item.(type) {
		case map[string]any:
			reattachIntFields(inner)
		case []any:
			reattachSlice(inner)
		case float64:
			if inner == math.Trunc(inner) && inner >= -mantissaBound && inner <= mantissaBound {
				s[i] = int64(inner)
			}
		}
	}
}
