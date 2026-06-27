package api

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// ProfilesService handles profile-related API operations.
type ProfilesService struct {
	q *db.Q
}

// NewProfilesService creates a ProfilesService.
func NewProfilesService(q *db.Q) *ProfilesService {
	return &ProfilesService{q: q}
}

// RegisterRoutes registers all profile-related routes under the given router group.
func (s *ProfilesService) RegisterRoutes(api fiber.Router, cfg *config.Config) {
	api.Get("/profile/:addr", s.handleGet)
	api.Put("/profile/:addr", jwtMiddleware(cfg), s.handlePut)
}

func (s *ProfilesService) handleGet(c *fiber.Ctx) error {
	addr := strings.ToLower(c.Params("addr"))
	if addr == "" {
		return writeErr(c, fiber.StatusBadRequest, "address required")
	}
	p, err := s.q.GetProfile(c.Context(), addr)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	return c.JSON(p)
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
		Bio         string `json:"bio"`
		AvatarURI   string `json:"avatar_uri"`
		BannerURI   string `json:"banner_uri"`
		Twitter     string `json:"twitter"`
		Website     string `json:"website"`
	}
	if err := bodyDecode(c, &u); err != nil {
		return writeErr(c, fiber.StatusBadRequest, "invalid request body")
	}
	if len(u.DisplayName) > 64 || len(u.Bio) > 500 {
		return writeErr(c, fiber.StatusBadRequest, "field too long")
	}
	for _, uri := range []string{u.AvatarURI, u.BannerURI, u.Website} {
		if uri != "" && !isAllowedScheme(uri) {
			return writeErr(c, fiber.StatusBadRequest, "uri must use http or https scheme")
		}
	}
	p := db.ProfileRow{
		Address: addr, DisplayName: u.DisplayName, Bio: u.Bio,
		AvatarURI: u.AvatarURI, BannerURI: u.BannerURI, Twitter: u.Twitter, Website: u.Website,
	}
	if err := s.q.UpsertProfile(c.Context(), p); err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	// Fetch the canonical stored row so the response includes the
	// authoritative `verified` field (set by admin via a separate endpoint)
	// rather than the zero-value from our local struct.
	saved, err := s.q.GetProfile(c.Context(), addr)
	if err != nil {
		// The upsert succeeded, so a read failure is transient. Return
		// the local struct as a degraded response rather than 5xx.
		// Preserve the input fields but re-fetch to keep verified
		// truthful; if re-fetch fails, fall back gracefully.
		oldProfile, getErr := s.q.GetProfile(c.Context(), addr)
		if getErr == nil {
			p.Verified = oldProfile.Verified
		}
		return c.JSON(p)
	}
	return c.JSON(saved)
}
