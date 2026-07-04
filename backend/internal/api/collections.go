package api

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// CollectionsService handles collection-related API operations.
type CollectionsService struct {
	q     *db.Q
	cache *cache.Cache
}

// NewCollectionsService creates a CollectionsService.
func NewCollectionsService(q *db.Q, c *cache.Cache) *CollectionsService {
	return &CollectionsService{q: q, cache: c}
}

// RegisterRoutes registers all collection-related routes under the given router group.
func (s *CollectionsService) RegisterRoutes(api fiber.Router) {
	api.Get("/collections", s.handleList)
	api.Get("/collections/:address/traits", s.handleTraits)
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
	col, err := s.q.GetCollection(c.Context(), address)
	if err != nil {
		if isNotFound(err) {
			return writeErr(c, fiber.StatusNotFound, "collection not found")
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
	if floor != nil {
		detail.FloorPriceWei = floor.String()
	}
	if vol != nil {
		detail.Volume24hWei = vol.String()
	}
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
	ckey := fmt.Sprintf("tr:%s:%d", window, limit)
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
	FloorPriceWei string `json:"floor_price_wei"`
	Volume24hWei  string `json:"volume_24h_wei"`
	ListedCount   int    `json:"listed_count"`
}
