package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
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

// WSStatsProvider is the interface the WS handler exposes for metrics collection.
// Decouples api.MetricsService from the ws package to avoid circular imports.
type WSStatsProvider interface {
	ActiveConns() int
	TotalSubscriptions() int
	EventsSent() int64
	TotalConns() int64
	// WS rate-limiting + rejection gauges for Prometheus dashboards.
	MsgRateLimited() int64
	ConnsRejectedIP() int64
	ConnsRejectedGlobal() int64
}

// MetricsService handles marketplace metrics, recent activity, and SSE counters.
type MetricsService struct {
	q     *db.Q
	cache cache.CacheInterface
	ws    WSStatsProvider // optional — nil when WS handler is not wired
}

// NewMetricsService creates a MetricsService. The cache backend can be
// either in-memory (*cache.Cache) or Redis-backed (*cache.RedisCache) —
// both implement cache.CacheInterface (CACHE-1).
func NewMetricsService(q *db.Q, c cache.CacheInterface, ws WSStatsProvider) *MetricsService {
	return &MetricsService{q: q, cache: c, ws: ws}
}

// RegisterRoutes registers metrics and activity routes under the given router group.
func (s *MetricsService) RegisterRoutes(api fiber.Router) {
	api.Get("/metrics", s.handleMetrics)
	api.Get("/metrics/gas", s.handleGasMetrics)
	api.Get("/metrics/gas/alerts", s.handleGasAlerts)
	api.Get("/activity", ValidateQuery(QuerySchema{
		{Name: "limit", Type: ParamInt},
		{Name: "address", Type: ParamAddress},
		{Name: "collection", Type: ParamAddress},
		{Name: "token_id"},
	}), s.handleRecentActivity)
}

func (s *MetricsService) handleGasMetrics(c *fiber.Ctx) error {
	summary, err := s.q.GetGasMetricsSummary(c.Context())
	if err != nil {
		return c.JSON(fiber.Map{"error": "gas metrics unavailable"})
	}
	logs, err := s.q.GetRecentGasLogs(c.Context(), 25)
	if err != nil {
		logs = []db.GasLogRow{}
	}
	return c.JSON(fiber.Map{
		"summary":     summary,
		"recentLogs":  logs,
	})
}

func (s *MetricsService) handleGasAlerts(c *fiber.Ctx) error {
	limit := 20
	if ls := c.Query("limit"); ls != "" {
		if n, err := strconv.Atoi(ls); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	alerts, err := s.q.ListGasAlerts(c.Context(), limit)
	if err != nil {
		return c.JSON(fiber.Map{"error": "gas alert history unavailable"})
	}
	if alerts == nil {
		alerts = []db.GasAlertRow{}
	}
	return c.JSON(fiber.Map{
		"alerts": alerts,
	})
}

func (s *MetricsService) handleMetrics(c *fiber.Ctx) error {
	return c.JSON(s.BuildResponse(c.Context()))
}

func (s *MetricsService) handleRecentActivity(c *fiber.Ctx) error {
	limit := 50
	if ls := c.Query("limit"); ls != "" {
		if n, err := strconv.Atoi(ls); err == nil && n > 0 {
			if n > 200 {
				n = 200
			}
			limit = n
		}
	}
	address := strings.ToLower(c.Query("address"))
	collection := strings.ToLower(c.Query("collection"))
	tokenID := c.Query("token_id")

	// ── Cache check: global and address-scoped queries only ────────────
	// Token-specific queries (collection+tokenID) are personal / rare and
	// not cached. Global activity (no params) and address activity are the
	// hot paths hit by every homepage / profile page load.
	if address != "" && collection == "" && tokenID == "" {
		ckey := fmt.Sprintf("act:%s:%d", address, limit)
		if cached, ok := s.cache.Get(ckey); ok {
			return c.JSON(cached)
		}
	}
	if address == "" && collection == "" && tokenID == "" {
		ckey := fmt.Sprintf("act:g:%d", limit)
		if cached, ok := s.cache.Get(ckey); ok {
			return c.JSON(cached)
		}
	}

	var rows []db.ActivityRow
	var err error

	// Route to the most specific query based on which params are present.
	// Priority: all-three (AND) > collection+token > address > global.
	// Reject partial token filters: collection+token_id must be together;
	// address mixed with collection (without token_id) is also invalid.
	// Map TokenActivityRow → ActivityRow so the JSON shape (camelCase
	// fields like amountWei, tokenId) stays consistent regardless of
	// which DB query was used.
	var tokenRows []db.TokenActivityRow
	switch {
	case collection != "" && tokenID != "" && address != "":
		tokenRows, err = s.q.GetTokenActivityByAddress(c.Context(), collection, tokenID, address, limit)
	case collection != "" && tokenID != "":
		tokenRows, err = s.q.GetTokenActivity(c.Context(), collection, tokenID, limit)
	case address != "" && collection != "":
		return writeErr(c, fiber.StatusBadRequest, "address and collection together require token_id")
	case collection != "":
		return writeErr(c, fiber.StatusBadRequest, "collection requires token_id")
	case tokenID != "":
		return writeErr(c, fiber.StatusBadRequest, "token_id requires collection")
	case address != "":
		rows, err = s.q.GetRecentTransactionsByAddress(c.Context(), address, limit)
	default:
		rows, err = s.q.GetRecentTransactions(c.Context(), limit)
	}
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	// Map token rows → ActivityRow for consistent camelCase JSON shape.
	// Only runs when the token path was taken (collection+token_id case);
	// for address-only and global paths, rows is already populated by the
	// DB query above and tokenRows stays nil, so we skip the mapping.
	if tokenRows != nil {
		mapped := make([]db.ActivityRow, 0, len(tokenRows))
		for _, tr := range tokenRows {
			mapped = append(mapped, db.ActivityRow{
				Type:       tr.Type,
				Collection: collection,
				TokenID:    tokenID,
				AmountWei:  tr.AmountWei,
				Timestamp:  tr.Timestamp,
				TxHash:     tr.TxHash,
			})
		}
		rows = mapped
	}
	// Guarantee `[]` not `null` for empty responses (avoids JSON `null`).
	if rows == nil {
		rows = []db.ActivityRow{}
	}

	// Store successful responses in the cache so subsequent callers skip
	// the DB round-trip. Only cache global and address-scoped queries
	// (the hot paths); token-specific activity is rare and personal.
	if tokenRows == nil {
		if address != "" {
			s.cache.Set(fmt.Sprintf("act:%s:%d", address, limit), rows)
		} else if collection == "" && tokenID == "" {
			s.cache.Set(fmt.Sprintf("act:g:%d", limit), rows)
		}
	}

	return c.JSON(rows)
}

// BuildResponse is the SINGLE SOURCE OF TRUTH for /api/v1/metrics
// (JSON) responses. It is also used by the UI render path so both the
// on-page banner and the JSON consumers see the same shape.
//
//   - the SSE saturation counters (DroppedTotal, SaturationStreak) are
//     always present even when the metrics query races with a transient
//     DB outage — the saturation panel renders correctly while the
//     "metrics temporarily unavailable" banner is shown;
//   - the stalled auction counts are appended so ops can see at a glance
//     whether auctions are backing up (keeper/refund-sweeper health);
//   - the metrics_unavailable sentinel is appended AFTER flat-key merge
//     so a future market field that happens to share that name cannot
//     silently clobber it.
//
// Never returns an error: a degraded metrics path still answers 200 with
// the SSE counters and the sentinel so the page is informative rather
// than silently zero-rendered.
func (s *MetricsService) BuildResponse(ctx context.Context) fiber.Map {
	out := fiber.Map{
		"sse_dropped_total":      sse.DroppedTotal.Load(),
		"sse_saturation_streak":  sse.SaturationStreak.Load(),
		"sse_client_drops_total": sse.DroppedClientsGauge(), // SSE-2
	}
	out["ws_connections"]        = int64(0)
	out["ws_subscriptions"]      = 0
	out["ws_events_sent"]        = int64(0)
	out["ws_total_conns"]        = int64(0)
	out["ws_msg_rate_limited"]   = int64(0)
	out["ws_conns_rejected_ip"]  = int64(0)
	out["ws_conns_rejected_global"] = int64(0)
	if s.ws != nil {
		out["ws_connections"]        = int64(s.ws.ActiveConns())
		out["ws_subscriptions"]      = s.ws.TotalSubscriptions()
		out["ws_events_sent"]        = s.ws.EventsSent()
		out["ws_total_conns"]        = s.ws.TotalConns()
		out["ws_msg_rate_limited"]   = s.ws.MsgRateLimited()
		out["ws_conns_rejected_ip"]  = s.ws.ConnsRejectedIP()
		out["ws_conns_rejected_global"] = s.ws.ConnsRejectedGlobal()
	}
	const unavailableMsg = "metrics temporarily unavailable"

	m, err := s.q.GetMarketMetrics(ctx)
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

	// Append stalled auction counts. Best-effort: a DB failure here does
	// not taint the rest of the metrics response — the counts are simply
	// absent from the JSON when the query fails.
	if stalled, serr := s.q.GetStalledAuctionCounts(ctx); serr == nil && stalled != nil {
		b2, merr2 := json.Marshal(stalled)
		if merr2 == nil {
			var flat2 map[string]any
			if uerr2 := json.Unmarshal(b2, &flat2); uerr2 == nil {
				reattachIntFields(flat2)
				for k, v := range flat2 {
					out[k] = v
				}
			}
		}
	}

	out["metrics_unavailable"] = ""

	// CACHE-4: surface cache hit/miss counters in the JSON metrics response.
	if GlobalCaches.Trending != nil {
		for k, v := range GlobalCaches.Trending.Stats() {
			out[k] = v
		}
	}
	if GlobalCaches.Activity != nil {
		for k, v := range GlobalCaches.Activity.Stats() {
			// Aggregate: add activity cache counters on top of trending.
			if existing, ok := out[k].(int64); ok {
				out[k] = existing + v
			} else {
				out[k] = v
			}
		}
	}

	// GQL-2: surface GraphQL response cache stats alongside the in-memory
	// cache counters. Prefixed graphql_cache_ to distinguish from the REST
	// cache counters.
	if GlobalGraphQLCache != nil {
		for k, v := range GlobalGraphQLCache.Stats() {
			out[k] = v
		}
	}

	return out
}

// reattachIntFields walks a map[string]any decoded from JSON and re-casts
// every numeric float64 -> int64 if the value is whole AND fits within the
// float64 mantissa precision boundary (lossless round-trip). Recurses into
// nested maps AND slice-of-[]any so a struct with sub-structs OR nested
// array fields gets the same protection.
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
