package api

import (
	"context"
	"encoding/json"
	"math"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// mantissaBound is the largest integer losslessly representable in float64
// (1<<53 = 9007199254740992). Values outside ±mantissaBound stay as float64
// once they survive the JSON round-trip so the consumer reads the
// approximation explicitly rather than silently reading garbage after a
// lossy cast. Declared at package scope so both reattachIntFields (map)
// and reattachSlice ([]any) share the same boundary without inner duplicates.
const mantissaBound = 1 << 53

// BuildMarketResponse is the SINGLE SOURCE OF TRUTH for /api/v1/metrics
// (JSON) and /metrics (HTML page) responses. Both endpoints call into it
// so:
//
//   - the on-page banner and the JSON consumers see the same shape;
//   - the SSE saturation counters (DroppedTotal, SaturationStreak) are
//     always present even when the metrics query races with a transient
//     DB outage — the saturation panel renders correctly while the
//     "metrics temporarily unavailable" banner is shown;
//   - the metrics_unavailable sentinel is appended AFTER flat-key merge
//     so a future market field that happens to share that name cannot
//     silently clobber it.
//
// Returns a fiber.Map the caller hands to c.JSON(...) or to ui.render(...).
// Never returns an error: a degraded metrics path still answers 200 with
// the SSE counters and the sentinel so the page is informative rather
// than silently zero-rendered.
func BuildMarketResponse(ctx context.Context, q *db.Q) fiber.Map {
	out := fiber.Map{
		"sse_dropped_total":     sse.DroppedTotal.Load(),
		"sse_saturation_streak": sse.SaturationStreak.Load(),
	}
	// Promoted sentinel value: customer-facing landing string only.
	// The internal failure detail goes to log.Error so operators see
	// the real cause without surfacing DB-driver wording to end-users.
	const unavailableMsg = "metrics temporarily unavailable"

	m, err := q.GetMarketMetrics(ctx)
	if err != nil {
		log.Error().Err(err).Msg("metrics: GetMarketMetrics failed")
		out["metrics_unavailable"] = unavailableMsg
		return out
	}
	if m == nil {
		out["metrics_unavailable"] = unavailableMsg
		return out
	}
	b, merr := json.Marshal(m)
	if merr != nil {
		log.Error().Err(merr).Msg("metrics: marshal failed")
		out["metrics_unavailable"] = unavailableMsg
		return out
	}
	var flat map[string]any
	if uerr := json.Unmarshal(b, &flat); uerr != nil {
		log.Error().Err(uerr).Msg("metrics: unmarshal failed")
		out["metrics_unavailable"] = unavailableMsg
		return out
	}
	reattachIntFields(flat)
	for k, v := range flat {
		out[k] = v
	}
	// Apply sentinel AFTER the flat merge so no future MarketMetric field
	// with the same name silently overwrites the unavailable flag.
	// Empty/missing keys aren't a concern; setting explicitly ensures
	// downstream consumers can safely check `.metrics_unavailable` truthiness.
	out["metrics_unavailable"] = "" // absent = available
	return out
}

// marketMetrics serves /api/v1/metrics. Bridges into BuildMarketResponse
// so JSON and HTML keep the same shape.
func marketMetrics(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		return c.JSON(BuildMarketResponse(c.Context(), q))
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
//   - The market struct has int64 + string fields only (no *big.Int); the
//     wei-string field stays as string and never touches the math path.
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

// reattachSlice handles the []any branch (and recursive [][]any /
// []map[]any / mixed nested arrays) so a future market struct with
// sub-array fields gets the same lossless float64->int64 reattach.
// Without this, a 2D JSON array would silently keep its inner floats as
// float64 even when they are integer-valued within mantissa range.
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
