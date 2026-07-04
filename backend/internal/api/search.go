package api

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// addrPattern matches a full Ethereum address (0x + 40 hex chars).
var addrPattern = regexp.MustCompile(`^0x[a-fA-F0-9]{40}$`)

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
	query := strings.TrimSpace(c.Query("q"))
	if len(query) < 2 {
		return writeErr(c, fiber.StatusBadRequest, "q must be at least 2 characters")
	}
	limit := 20
	if lim := c.Query("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil {
			if n < 1 {
				n = 1
			} else if n > 50 {
				n = 50
			}
			limit = n
		}
	}

	// Exact address path: if the query is a valid 0x-prefixed address, try to
	// resolve it as a collection first. This lets users paste a contract address
	// into the search bar and land on the collection page, even though the
	// full-text search only indexes name + symbol (not address).
	if addrPattern.MatchString(query) {
		if coll, err := s.q.GetCollectionByAddress(c.Context(), strings.ToLower(query)); err == nil {
			return c.JSON([]db.SearchResult{{
				Kind:       "collection",
				Collection: coll.Address,
				Name:       coll.Name,
			}})
		}
		// No exact address match — fall through to full-text search so the
		// user still gets partial/name results if any exist.
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
