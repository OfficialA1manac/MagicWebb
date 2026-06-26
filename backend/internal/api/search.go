package api

import (
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// SearchService handles full-text search across tokens, collections, and profiles.
type SearchService struct {
	q *db.Q
}

// NewSearchService creates a SearchService.
func NewSearchService(q *db.Q) *SearchService {
	return &SearchService{q: q}
}

// RegisterRoutes registers the search route under the given router group.
func (s *SearchService) RegisterRoutes(api fiber.Router) {
	api.Get("/search", s.handleSearch)
}

func (s *SearchService) handleSearch(c *fiber.Ctx) error {
	query := c.Query("q")
	if len(query) < 2 {
		return writeErr(c, fiber.StatusBadRequest, "q must be at least 2 characters")
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
	results, err := s.q.Search(c.Context(), query, limit)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if results == nil {
		results = []db.SearchResult{}
	}
	return c.JSON(results)
}
