package api

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// CollectionsService handles collection-related API operations.
type CollectionsService struct {
	q     *db.Q
	cache cache.CacheInterface
	// chainID namespaces cache keys (cache.Key). Set by Mount.
	chainID uint64
}

// NewCollectionsService creates a CollectionsService. The cache backend
// can be either in-memory (*cache.Cache) or Redis-backed (*cache.RedisCache)
// — both implement cache.CacheInterface so the service doesn't care which
// backend is in use at runtime (CACHE-1).
func NewCollectionsService(q *db.Q, c cache.CacheInterface) *CollectionsService {
	return &CollectionsService{q: q, cache: c}
}

// RegisterRoutes registers all collection-related routes under the given router group.
func (s *CollectionsService) RegisterRoutes(api fiber.Router) {
	api.Get("/collections", s.handleList)
	api.Get("/collections/:address/traits", s.handleTraits)
	api.Get("/collections/:address/tokens", s.handleTokens)
	api.Get("/collections/:address", s.handleGet)
	api.Get("/trending", s.handleTrending)
}

func (s *CollectionsService) handleList(c *fiber.Ctx) error {
	limit := 50
	if lim := c.Query("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil {
			if n < 1 {
				n = 1
			} else if n > 200 {
				n = 200
			}
			limit = n
		}
	}
	rows, err := s.q.ListCollections(c.Context(), limit)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if rows == nil {
		rows = []db.CollectionRow{}
	}
	return c.JSON(rows)
}

func (s *CollectionsService) handleGet(c *fiber.Ctx) error {
	address := strings.ToLower(c.Params("address"))

	// CACHE-3: Collection stats (floor, volume, listed count) change at most
	// every few minutes. Cache the computed detail for 30s to avoid
	// 3 DB round-trips (GetCollection + GetFloorPrice + Get24hVolume +
	// GetListedCount) on every listing page load. Cache misses fall
	// through to the full DB path; cache hits return immediately.
	ckey := cache.Key(s.chainID, "col", address)
	if cached, ok := s.cache.Get(ckey); ok {
		return c.JSON(cached)
	}

	col, err := s.q.GetCollection(c.Context(), address)
	if err != nil {
		if isNotFound(err) {
			return writeErr(c, fiber.StatusNotFound, "not found")
		}
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	floor, _ := s.q.GetFloorPrice(c.Context(), address)
	vol, _ := s.q.Get24hVolume(c.Context(), address)
	listed, listedErr := s.q.GetListedCount(c.Context(), address)
	// Surface genuine DB errors from GetListedCount — swallowing them
	// turns a query failure into a misleading "listed_count=0" response.
	if listedErr != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}

	detail := collectionDetail{CollectionRow: *col, ListedCount: listed}
	// Best-effort: the badge breakdown is a tooltip, never a reason to 500.
	detail.VerifiedReason, _ = s.q.GetCollectionVerifiedReason(c.Context(), address)
	if floor != nil {
		detail.FloorPriceWei = floor.String()
	}
	if vol != nil {
		detail.Volume24hWei = vol.String()
	}

	// Cache the computed detail for subsequent requests.
	s.cache.Set(ckey, detail)
	return c.JSON(detail)
}

func (s *CollectionsService) handleTraits(c *fiber.Ctx) error {
	coll := strings.ToLower(c.Params("address"))
	m, err := s.q.ListTraitValues(c.Context(), coll)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if m == nil {
		m = map[string][]string{}
	}
	return c.JSON(m)
}

func (s *CollectionsService) handleTrending(c *fiber.Ctx) error {
	window := c.Query("window")
	if window == "" {
		window = "24h"
	}
	limit := 20
	if lim := c.Query("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil {
			if n < 1 {
				n = 1
			} else if n > 100 {
				n = 100
			}
			limit = n
		}
	}

	// Cache hit → return immediately, no DB query.
	ckey := cache.Key(s.chainID, "tr", window, strconv.Itoa(limit))
	if cached, ok := s.cache.Get(ckey); ok {
		return c.JSON(cached)
	}

	rows, err := s.q.GetTrendingCollections(c.Context(), window, limit)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if rows == nil {
		rows = []db.TrendingScore{}
	}

	// Cache the successful response for subsequent callers.
	s.cache.Set(ckey, rows)
	return c.JSON(rows)
}

// collectionDetail is the JSON shape for a collection with computed stats.
type collectionDetail struct {
	db.CollectionRow
	FloorPriceWei  string            `json:"floor_price_wei"`
	Volume24hWei   string            `json:"volume_24h_wei"`
	ListedCount    int               `json:"listed_count"`
	VerifiedReason db.VerifiedReason `json:"verified_reason"`
}

// collectionTokensPage is GET /api/v1/collections/:address/tokens.
type collectionTokensPage struct {
	// Collection is the parent's card facts (name, standard, verified,
	// creator) so a token card can render its badge without a second call.
	Collection db.CollectionRow        `json:"collection"`
	Tokens     []db.CollectionTokenRow `json:"tokens"`
	Page       int                     `json:"page"`
	Limit      int                     `json:"limit"`
	Total      int64                   `json:"total"`
}

// handleTokens pages through a collection's indexed tokens
// (?page=1&limit=48). Unknown collections 404 like handleGet.
func (s *CollectionsService) handleTokens(c *fiber.Ctx) error {
	address := strings.ToLower(c.Params("address"))
	if !isValidHexAddress(address) {
		return writeErr(c, fiber.StatusNotFound, "not found")
	}
	page, limit := 1, 48
	if v := c.Query("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 1 {
			page = n
		}
	}
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n < 1 {
				n = 1
			} else if n > 200 {
				n = 200
			}
			limit = n
		}
	}
	col, err := s.q.GetCollection(c.Context(), address)
	if err != nil {
		if isNotFound(err) {
			return writeErr(c, fiber.StatusNotFound, "not found")
		}
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	total, err := s.q.CountCollectionTokens(c.Context(), address)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	rows, err := s.q.ListCollectionTokens(c.Context(), address, limit, (page-1)*limit)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if rows == nil {
		rows = []db.CollectionTokenRow{}
	}
	return c.JSON(collectionTokensPage{Collection: *col, Tokens: rows, Page: page, Limit: limit, Total: total})
}
