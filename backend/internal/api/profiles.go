package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// profileFanoutHeader marks a server-to-server sibling profile fetch. A
// request carrying it never fans out again, so two networks that list each
// other in NETWORK_URLS cannot ping-pong a miss between themselves.
const profileFanoutHeader = "X-MW-Profile-Fanout"

// maxSiblingProfileBody caps how much of a sibling's response we will read —
// a profile is a few hundred bytes; anything near this limit is not one.
const maxSiblingProfileBody = 64 * 1024

// profileResponse is a ProfileRow plus the chain the data came from. Profiles
// are per-network (each deployment has its own database), so when a profile
// was carried over from a sibling network the UI can say so.
type profileResponse struct {
	*db.ProfileRow
	SourceChain uint64 `json:"source_chain"`
}

// ProfilesService handles profile-related API operations.
type ProfilesService struct {
	q       *db.Q
	chainID uint64
	// siblings are the other deployed networks from NETWORK_URLS, chain 114
	// first (that is where users edit profiles today), own origin excluded.
	siblings []config.Network
	httpc    *http.Client
	// merged caches the exact JSON served by handleGet (local or carried
	// over), 60s TTL. handlePut refreshes the entry so an edit is visible
	// immediately on its own network.
	merged cache.CacheInterface
}

// NewProfilesService creates a ProfilesService.
func NewProfilesService(q *db.Q, cfg *config.Config) *ProfilesService {
	s := &ProfilesService{
		q:       q,
		chainID: cfg.ChainID,
		httpc:   &http.Client{Timeout: 3 * time.Second},
		merged:  cache.NewRedisOrMemory(cfg.RedisURL, 60*time.Second),
	}
	// Chain 114 first, then the catalogue order for the rest.
	for _, wantFirst := range []bool{true, false} {
		for _, n := range cfg.Networks {
			if n.Current || n.URL == "" {
				continue
			}
			if (n.ChainID == 114) == wantFirst {
				s.siblings = append(s.siblings, n)
			}
		}
	}
	return s
}

// RegisterRoutes registers all profile-related routes under the given router group.
func (s *ProfilesService) RegisterRoutes(api fiber.Router, cfg *config.Config) {
	api.Get("/profile/:addr", s.handleGet)
	api.Put("/profile/:addr", jwtMiddleware(cfg), s.handlePut)
}

// profileIsEmpty reports whether the profile has none of the fields a user
// actually fills in set — the shape GetProfile returns for a missing row.
func profileIsEmpty(p *db.ProfileRow) bool {
	return p.DisplayName == "" && p.Tag == "" && p.Bio == "" && p.AvatarURI == ""
}

// cachedBytes normalises a cache hit to raw JSON. Cache writers MUST store
// the JSON as a string, not []byte: the Redis backend JSON-round-trips the
// stored value, and a []byte comes back base64-encoded. The []byte case below
// covers legacy in-memory entries written before that rule.
func cachedBytes(v any) []byte {
	switch b := v.(type) {
	case []byte:
		return b
	case string:
		return []byte(b)
	}
	return nil
}

func (s *ProfilesService) profileCacheKey(addr string) string {
	return "profile-merged:" + addr
}

func (s *ProfilesService) handleGet(c *fiber.Ctx) error {
	addr := strings.ToLower(c.Params("addr"))
	if addr == "" {
		return writeErr(c, fiber.StatusBadRequest, "address required")
	}
	if v, ok := s.merged.Get(s.profileCacheKey(addr)); ok {
		if body := cachedBytes(v); body != nil {
			c.Set("Content-Type", "application/json")
			return c.Send(body)
		}
	}
	p, err := s.q.GetProfile(c.Context(), addr)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	resp := profileResponse{ProfileRow: p, SourceChain: s.chainID}
	if profileIsEmpty(p) {
		log.Debug().
			Str("address", addr).
			Msg("profiles: empty local profile — the address may have never set up a profile here or the profiles row is missing.")
		// Read-through to sibling networks: profiles are edited per-network,
		// so a wallet that set one up on another chain still gets a face
		// here. Server-side only, reads only — never writes. Skipped when
		// this request IS a sibling's read-through (no ping-pong), and only
		// for well-formed addresses (never proxy arbitrary path segments).
		if c.Get(profileFanoutHeader) == "" && isValidHexAddress(addr) {
			if remote := s.fetchFromSiblings(c.Context(), addr); remote != nil {
				resp = *remote
			}
		}
	}
	body, err := json.Marshal(resp)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	// A fanout request never reads siblings, so its (possibly empty) result
	// must not become the answer served to normal callers for the full TTL.
	// Stored as a string: the Redis backend JSON-round-trips values, and a
	// []byte would come back base64-encoded.
	if c.Get(profileFanoutHeader) == "" {
		s.merged.Set(s.profileCacheKey(addr), string(body))
	}
	c.Set("Content-Type", "application/json")
	return c.Send(body)
}

// fetchFromSiblings asks each sibling network's API for the profile and
// returns the first non-empty one, stamped with that sibling's chain id.
// Every failure mode — network down, slow, non-200, garbage body — degrades
// to nil so the local (empty) response is never broken by a sibling.
func (s *ProfilesService) fetchFromSiblings(ctx context.Context, addr string) *profileResponse {
	for _, n := range s.siblings {
		p, err := s.fetchSiblingProfile(ctx, n, addr)
		if err != nil {
			log.Debug().Err(err).Uint64("chain", n.ChainID).Str("address", addr).
				Msg("profiles: sibling profile fetch failed; continuing")
			continue
		}
		if !profileIsEmpty(p) {
			return &profileResponse{ProfileRow: p, SourceChain: n.ChainID}
		}
	}
	return nil
}

func (s *ProfilesService) fetchSiblingProfile(ctx context.Context, n config.Network, addr string) (*db.ProfileRow, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		n.URL+"/api/v1/profile/"+url.PathEscape(addr), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(profileFanoutHeader, "1")
	res, err := s.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fiber.NewError(res.StatusCode, "sibling returned non-200")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxSiblingProfileBody))
	if err != nil {
		return nil, err
	}
	p := &db.ProfileRow{}
	if err := json.Unmarshal(body, p); err != nil {
		return nil, err
	}
	p.Address = addr // trust our own notion of the address, not the sibling's
	return p, nil
}

// isValidProfileTag reports whether every rune is a letter, digit, space,
// dash or underscore. The empty (unset) tag is valid.
func isValidProfileTag(tag string) bool {
	for _, r := range tag {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != ' ' && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func (s *ProfilesService) handlePut(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" {
		return writeErr(c, fiber.StatusUnauthorized, "unauthorized")
	}
	if target := strings.ToLower(c.Params("addr")); target != "" && target != addr {
		return writeErr(c, fiber.StatusForbidden, "cannot edit another profile")
	}
	var u struct {
		DisplayName string `json:"display_name"`
		Tag         string `json:"tag"`
		Bio         string `json:"bio"`
		AvatarURI   string `json:"avatar_uri"`
		BannerURI   string `json:"banner_uri"`
		Twitter     string `json:"twitter"`
		Website     string `json:"website"`
	}
	if err := bodyDecode(c, &u); err != nil {
		return writeErr(c, fiber.StatusBadRequest, "invalid request body")
	}
	u.Tag = strings.TrimSpace(u.Tag) // trimmed; empty = unset (stored as NULL)
	if len(u.DisplayName) > 64 || len(u.Tag) > 32 || len(u.Bio) > 500 {
		return writeErr(c, fiber.StatusBadRequest, "field too long")
	}
	if !isValidProfileTag(u.Tag) {
		return writeErr(c, fiber.StatusBadRequest, "tag may only contain letters, digits, spaces, dashes and underscores")
	}
	for _, uri := range []string{u.AvatarURI, u.BannerURI, u.Website} {
		if uri != "" && !isAllowedScheme(uri) {
			return writeErr(c, fiber.StatusBadRequest, "uri must use http or https scheme")
		}
	}
	p := db.ProfileRow{
		Address: addr, DisplayName: u.DisplayName, Tag: u.Tag, Bio: u.Bio,
		AvatarURI: u.AvatarURI, BannerURI: u.BannerURI, Twitter: u.Twitter, Website: u.Website,
	}
	if err := s.q.UpsertProfile(c.Context(), p); err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	// Fetch the canonical stored row so the response reflects exactly what
	// was persisted rather than the zero-values from our local struct.
	saved, err := s.q.GetProfile(c.Context(), addr)
	if err != nil {
		// The upsert succeeded, so a read-back failure is transient. Return
		// the local struct as a degraded response rather than 5xx. The
		// merged cache is left alone — its 60s TTL bounds the staleness.
		log.Warn().Err(err).Str("address", addr).
			Msg("profiles: read-back after upsert failed; returning degraded response")
		return c.JSON(profileResponse{ProfileRow: &p, SourceChain: s.chainID})
	}
	resp := profileResponse{ProfileRow: saved, SourceChain: s.chainID}
	// Refresh the read cache so the edit is visible immediately on this
	// network (writes are never proxied — editing stays local per network).
	if body, err := json.Marshal(resp); err == nil {
		s.merged.Set(s.profileCacheKey(addr), string(body))
	}
	return c.JSON(resp)
}
