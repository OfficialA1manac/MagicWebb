package api

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

func getProfile(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := strings.ToLower(strings.TrimSpace(c.Params("addr")))
		if !isHexAddr(addr) {
			return writeErr(c, fiber.StatusBadRequest, "invalid address")
		}
		p, err := q.GetProfile(c.Context(), addr)
		if err != nil {
			if isNotFound(err) {
				// Return an empty profile rather than 404 — UI is simpler
				return c.JSON(db.ProfileRow{Address: addr})
			}
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		return c.JSON(p)
	}
}

type profileReq struct {
	DisplayName string `json:"display_name"`
	Bio         string `json:"bio"`
	AvatarURI   string `json:"avatar_uri"`
	BannerURI   string `json:"banner_uri"`
	Twitter     string `json:"twitter"`
	Website     string `json:"website"`
}

func upsertProfile(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := strings.ToLower(strings.TrimSpace(c.Params("addr")))
		caller, ok := c.Locals(string(auth.CallerKey)).(string)
		if !ok || !strings.EqualFold(caller, addr) {
			return writeErr(c, fiber.StatusForbidden, "can only edit your own profile")
		}
		var req profileReq
		if err := bodyDecode(c, &req); err != nil {
			return writeErr(c, fiber.StatusBadRequest, "invalid body")
		}
		p := db.ProfileRow{
			Address:     addr,
			DisplayName: req.DisplayName,
			Bio:         req.Bio,
			AvatarURI:   req.AvatarURI,
			BannerURI:   req.BannerURI,
			Twitter:     req.Twitter,
			Website:     req.Website,
		}
		if err := q.UpsertProfile(c.Context(), p); err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		return c.JSON(p)
	}
}
