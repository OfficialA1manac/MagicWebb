package api

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// adminMiddleware requires SIWE JWT AND caller in cfg.AdminAddrs allowlist.
func adminMiddleware(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		hdr := c.Get("Authorization")
		if !strings.HasPrefix(hdr, "Bearer ") {
			return writeErr(c, fiber.StatusUnauthorized, "missing token")
		}
		addr, err := auth.Verify(strings.TrimPrefix(hdr, "Bearer "), cfg.JWTSecret)
		if err != nil {
			return writeErr(c, fiber.StatusUnauthorized, "invalid token")
		}
		if !cfg.IsAdmin(addr) {
			return writeErr(c, fiber.StatusForbidden, "not an admin")
		}
		c.Locals(string(auth.CallerKey), strings.ToLower(addr))
		return c.Next()
	}
}

type verifyCollectionReq struct {
	Verified bool `json:"verified"`
}

// verifyCollection toggles tracked_collections.verified for a collection.
func verifyCollection(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := strings.ToLower(strings.TrimSpace(c.Params("addr")))
		if !isHexAddr(addr) {
			return writeErr(c, fiber.StatusBadRequest, "invalid address")
		}
		var req verifyCollectionReq
		// Default: verify=true if no body sent
		req.Verified = true
		_ = bodyDecode(c, &req)
		if err := q.SetCollectionVerified(c.Context(), addr, req.Verified); err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		return c.JSON(fiber.Map{"address": addr, "verified": req.Verified})
	}
}
