package api

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/chain"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// ListingsService handles listing-related API operations.
type ListingsService struct {
	q   *db.Q
	eth chain.Caller
	// listCache holds the exact JSON served by handleList. Listings had NO
	// cache at all while collections/trending/activity/wallet all had one, so
	// every browse hit Postgres. Short TTL: listings must still feel live.
	listCache cache.CacheInterface
}

// NewListingsService creates a ListingsService. cache may be nil (tests).
func NewListingsService(q *db.Q, eth chain.Caller, listCache cache.CacheInterface) *ListingsService {
	return &ListingsService{q: q, eth: eth, listCache: listCache}
}

// RegisterRoutes registers all listing-related routes under the given router group.
func (s *ListingsService) RegisterRoutes(api fiber.Router) {
	api.Get("/listings", ValidateQuery(QuerySchema{
		{Name: "collection", Type: ParamAddress},
		{Name: "seller", Type: ParamAddress},
		{Name: "sort", OneOf: []string{"recent", "price_asc", "price_desc"}},
		{Name: "limit", Type: ParamInt},
		{Name: "min_price", Type: ParamWei},
		{Name: "max_price", Type: ParamWei},
		{Name: "traits"},
	}), s.handleList)
	api.Get("/listings/:collection/:id/preflight", s.handlePreflight)
	api.Get("/listings/:collection/:id", s.handleGet)
	api.Post("/token/:collection/:id/view", s.handleTokenView)
}

func (s *ListingsService) handleList(c *fiber.Ctx) error {
	f := db.ListingsFilter{
		Collection: strings.ToLower(c.Query("collection")),
		// Seller was passed through RAW while collection was lowercased, so a
		// caller sending a lowercase address (which the profile page does)
		// matched nothing, while an EIP-55 checksummed one happened to work.
		// Both are normalized now; storage is lowercase (migration 039).
		Seller: strings.ToLower(c.Query("seller")),
		Sort:   c.Query("sort", "recent"),
	}
	if lim := c.Query("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil {
			if n < 1 {
				n = 1
			} else if n > 100 {
				n = 100
			}
			f.Limit = n
		}
	}
	// Parse price range filters (in wei) with validation
	if mp := c.Query("min_price"); mp != "" {
		if !isValidWeiStr(mp) {
			return writeErr(c, fiber.StatusBadRequest, "min_price must be a non-negative integer wei value")
		}
		f.MinPriceWei = mp
	}
	if mp := c.Query("max_price"); mp != "" {
		if !isValidWeiStr(mp) {
			return writeErr(c, fiber.StatusBadRequest, "max_price must be a non-negative integer wei value")
		}
		f.MaxPriceWei = mp
	}
	// Parse trait filters: traits=trait_type:value,trait_type:value
	if traits := c.Query("traits"); traits != "" {
		f.Traits = map[string]string{}
		for _, pair := range strings.Split(traits, ",") {
			parts := strings.SplitN(pair, ":", 2)
			if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
				f.Traits[parts[0]] = parts[1]
			}
		}
	}
	// Cache key is the full filter set — two different filters must never
	// share an entry. Stored as a string: the Redis backend JSON-round-trips
	// values and a []byte would come back base64-encoded (see cachedBytes).
	ck := fmt.Sprintf("ls:%s|%s|%s|%s|%s|%d|%v",
		f.Collection, f.Seller, f.Sort, f.MinPriceWei, f.MaxPriceWei, f.Limit, f.Traits)
	if s.listCache != nil {
		if v, ok := s.listCache.Get(ck); ok {
			if body := cachedBytes(v); body != nil {
				c.Set("Content-Type", "application/json")
				return c.Send(body)
			}
		}
	}

	rows, err := s.q.ListActiveListings(c.Context(), f)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if rows == nil {
		rows = []db.ListingRow{}
	}
	if len(rows) == 0 {
		log.Debug().
			Str("collection", f.Collection).
			Str("seller", f.Seller).
			Str("sort", f.Sort).
			Msg("listings: query returned zero results — no active listings match the filter criteria")
	}
	body, err := json.Marshal(rows)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if s.listCache != nil {
		s.listCache.Set(ck, string(body))
	}
	c.Set("Content-Type", "application/json")
	return c.Send(body)
}

func (s *ListingsService) handleGet(c *fiber.Ctx) error {
	// Addresses are stored lower-cased (enforced by addrStr() on the write path
	// and backfilled by migration 039); normalize so EIP-55 checksummed URLs
	// match. This comment previously asserted lowercase storage while the chain
	// indexer was in fact writing checksummed values — the mismatch is what made
	// every listing 404 here.
	collection := strings.ToLower(c.Params("collection"))
	id := c.Params("id")
	row, err := s.q.GetListing(c.Context(), collection, id)
	if err != nil {
		if isNotFound(err) {
			return writeErr(c, fiber.StatusNotFound, "listing not found")
		}
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	return c.JSON(row)
}

func (s *ListingsService) handlePreflight(c *fiber.Ctx) error {
	return listingPreflightWithChain(s.q, s.eth)(c)
}

// handleTokenView increments the view counter for a token (fire-and-forget).
// This is a lightweight POST — no auth required, no response body beyond 204.
func (s *ListingsService) handleTokenView(c *fiber.Ctx) error {
	collection := strings.ToLower(c.Params("collection"))
	id := c.Params("id")
	_ = s.q.IncrementTokenViews(c.Context(), collection, id)
	return c.SendStatus(fiber.StatusNoContent)
}
