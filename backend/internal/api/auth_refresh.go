package api

import (
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/ratelimit"
)

// MountAuthRoutes registers the /auth/logout endpoint on the Fiber app.
// /auth/refresh is already registered in cmd/server/main.go (refreshHandler)
// — this function only adds the logout endpoint.
//
// Call from main.go:
//
//	MountAuthRoutes(app, cfg, rl, refreshStore)
func MountAuthRoutes(app *fiber.App, cfg *config.Config, rl *ratelimit.Limiter, store auth.RefreshStore) {
	// Light rate limiting: logout is called at most once per session.
	// 10/min prevents abuse without impacting legitimate use.
	logoutLimiter := tieredRateLimitMiddleware(rl, "auth", 10, time.Minute)
	app.Post("/auth/logout", logoutLimiter, handleLogout(cfg, store))
}

// handleLogout reads the refresh token cookie, verifies it, revokes the
// token family, and clears both cookies. Idempotent — calling logout
// without a valid token is a no-op (200 returned, cookies cleared).
//
// Request:  POST /auth/logout  (no body — refresh token is in the mw_r_ cookie)
// Response: 200 { "status": "ok" }
func handleLogout(cfg *config.Config, store auth.RefreshStore) fiber.Handler {
	return func(c *fiber.Ctx) error {
		_, addr, familyID, _, err := extractRefreshToken(c, cfg.JWTSecret)
		if err != nil {
			// No valid refresh token — still clear cookies so the client
			// state is consistent. This covers the case where the user
			// already logged out but the frontend retried.
			clearAllCookiesByName(c)
			return c.JSON(fiber.Map{"status": "ok"})
		}

		// Revoke the token family (best-effort; DB failure is non-fatal
		// since the cookies are cleared on the client side either way).
		if revokeErr := store.RevokeFamily(c.Context(), addr, familyID); revokeErr != nil {
			log.Warn().Err(revokeErr).Str("family_id", familyID).Str("address", addr).
				Msg("logout: family revoke failed (non-fatal — cookies cleared)")
		}

		// Clear both cookies by exact name using the known address.
		clearBothCookies(c, addr)

		log.Info().Str("address", addr).Msg("logout: session terminated")
		return c.JSON(fiber.Map{"status": "ok"})
	}
}

// ── Cookie helpers ──────────────────────────────────────────────────────────

func clearAccessCookie(c *fiber.Ctx, addr string) {
	if addr == "" {
		return
	}
	c.Cookie(&fiber.Cookie{
		Name:     auth.CookieNameAccess(addr),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HTTPOnly: true,
	})
}

func clearRefreshCookie(c *fiber.Ctx, addr string) {
	if addr == "" {
		return
	}
	c.Cookie(&fiber.Cookie{
		Name:     auth.CookieNameRefresh(addr),
		Value:    "",
		Path:     "/auth",
		MaxAge:   -1,
		HTTPOnly: true,
	})
}

// clearBothCookies clears both the access and refresh cookies for a specific wallet.
func clearBothCookies(c *fiber.Ctx, addr string) {
	clearAccessCookie(c, addr)
	clearRefreshCookie(c, addr)
}

// clearAllCookiesByName scans the Cookie header for all mw_a_*, mw_r_*, and
// mw_s_* cookies and clears each one by exact name. This is the only correct
// way to clear cookies when we don't know the specific wallet address.
func clearAllCookiesByName(c *fiber.Ctx) {
	hdr := c.Get("Cookie")
	if hdr == "" {
		return
	}
	seen := make(map[string]struct{})
	for _, part := range strings.Split(hdr, ";") {
		p := strings.TrimSpace(part)
		if !strings.HasPrefix(p, "mw_a_") && !strings.HasPrefix(p, "mw_r_") && !strings.HasPrefix(p, "mw_s_") {
			continue
		}
		eq := strings.IndexByte(p, '=')
		if eq <= 0 {
			continue
		}
		name := p[:eq]
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		c.Cookie(&fiber.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HTTPOnly: true,
		})
	}
}

// ── Refresh token extraction ────────────────────────────────────────────────

// extractRefreshToken reads the refresh JWT from an mw_r_ cookie, verifies
// it, and returns the token string, wallet address, family_id, and token_id (jti).
func extractRefreshToken(c *fiber.Ctx, secret string) (token string, addr string, familyID string, tokenID string, err error) {
	// Scan for mw_r_ cookies.
	hdr := c.Get("Cookie")
	if hdr == "" {
		return "", "", "", "", fmt.Errorf("no cookies in request")
	}

	var refreshValue string
	for _, part := range strings.Split(hdr, ";") {
		p := strings.TrimSpace(part)
		if !strings.HasPrefix(p, "mw_r_") {
			continue
		}
		eq := strings.IndexByte(p, '=')
		if eq <= 0 {
			continue
		}
		refreshValue = p[eq+1:]
		break
	}
	if refreshValue == "" {
		return "", "", "", "", fmt.Errorf("no mw_r_ cookie found")
	}

	// Verify the refresh JWT — returns the wallet address from the JWT subject.
	address, err := auth.VerifyRefreshToken(refreshValue, secret)
	if err != nil {
		return "", "", "", "", fmt.Errorf("invalid refresh token: %w", err)
	}
	if address == "" {
		return "", "", "", "", fmt.Errorf("refresh token has no subject")
	}

	// Parse claims to get family_id and token_id for rotation.
	fid, tid := auth.ParseRefreshClaims(refreshValue, secret)
	if fid == "" || tid == "" {
		return "", "", "", "", fmt.Errorf("refresh token missing family_id or token_id")
	}

	return refreshValue, address, fid, tid, nil
}
