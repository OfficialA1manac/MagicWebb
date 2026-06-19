// Package api wires all REST handlers and SSE using Go Fiber.
package api

import (
	"bufio"
	"encoding/json"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	flog "github.com/gofiber/fiber/v2/middleware/logger"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/chain"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/imagestore"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/media"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/ratelimit"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// Strict-transport / content-security baseline. CSP locks scripts to self +
// the explicitly allow-listed CDNs (or self-hosted) so a compromised CDN
// can't inject behavior. frame-ancestors 'none' plus X-Frame-Options DENY
// blocks clickjacking; Referrer-Policy keeps URLs out of cross-origin
// Referer headers (so wallet addresses?action=foo don't leak). X-Content-
// Type-Options=nosniff across all responses prevents MIME sniffing even
// where image handlers already set it.
const (
	cspHeader = "default-src 'self'; " +
		// Self-hosted JS bundles (htmx/ethers/alpinejs) live under /static,
		// served same-origin. Only esm.sh remains external — wallet.js
		// dynamically imports @walletconnect/ethereum-provider from there
		// at button-click time (gated by the user's explicit WalletConnect
		// picker selection — never on page boot). api.reown.com + WC relay
		// wss:// channels are required for the QR pairing / multi-wallet
		// relay — block any of them and WalletConnect silently fails.
		"script-src 'self' https://esm.sh; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
		"font-src 'self' https://fonts.gstatic.com; " +
		"img-src 'self' data: blob: https: ipfs:; " +
		"connect-src 'self' https://coston2-api.flare.network https://ipfs.io https://dweb.link https://gateway.pinata.cloud https://api.reown.com https://*.walletconnect.com wss://relay.walletconnect.com wss://*.walletconnect.com; " +
		"frame-src 'self' https://*.walletconnect.com https://verify.walletconnect.com; " +
		"frame-ancestors 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'"
	hstsHeader          = "max-age=63072000; includeSubDomains; preload"
	permissionsPolicy   = "geolocation=(), microphone=(), camera=(), payment=(self \"https://magicwebb.xyz\"), usb=()"
	referrerPolicy      = "strict-origin-when-cross-origin"
)

// securityHeaders installs the response headers above on every response.
// Tighten the CSP if your deployment uses self-hosted bundles exclusively;
// the connect-src list is the only path that touches third-party origins.
func securityHeaders() fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Set("Content-Security-Policy", cspHeader)
		c.Set("Strict-Transport-Security", hstsHeader)
		c.Set("X-Frame-Options", "DENY")
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("Referrer-Policy", referrerPolicy)
		c.Set("Permissions-Policy", permissionsPolicy)
		c.Set("Cross-Origin-Opener-Policy", "same-origin")
		return c.Next()
	}
}

// Mount registers all REST + SSE routes on the Fiber app.
func Mount(app *fiber.App, q *db.Q, bcast *sse.Broadcaster, rl *ratelimit.Limiter, cfg *config.Config, eth chain.Caller) {
	app.Use(securityHeaders())
	app.Use(cors.New(cors.Config{
		AllowOrigins:     buildOrigins(cfg.FrontendURL, cfg.Env),
		AllowMethods:     "GET,POST,PUT,OPTIONS",
		AllowHeaders:     "Content-Type,Authorization",
		AllowCredentials: true,
	}))

	app.Use(flog.New(flog.Config{
		Format: "${time} ${method} ${path} ${status} ${latency}\n",
	}))

	app.Get("/healthz", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })
	app.Get("/readyz", func(c *fiber.Ctx) error {
		if err := q.Ping(c.Context()); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).SendString("db unhealthy")
		}
		return c.SendStatus(fiber.StatusOK)
	})

	app.Get("/events", sseHandler(bcast))

	api := app.Group("/api/v1", rateLimitMiddleware(rl))

	api.Get("/listings", listListings(q))
	api.Get("/listings/:collection/:id/preflight", listingPreflightWithChain(q, eth))
	api.Get("/listings/:collection/:id", getListing(q))
	api.Get("/media", mediaProxy(q))
	// User-triggered immediate self-host of an upstream image. The slow-path
	// retry worker (indexer.runImageRetryWorker) does the same work on a
	// 60-min cadence; this endpoint just runs it synchronously on click.
	// POST is the right verb because the call writes to nft_metadata and
	// nft_tokens; idempotent (repeat clicks land on the same /api/v1/img/
	// path). Auth not required — the per-IP rate limiter in api group caps
	// abuse.
	api.Post("/img/retry", imageRetryNow(q, media.FetchBytes))
	app.Get(imagestore.PathPrefix+"/:sha256", imageByHash(q))
	api.Get("/collections", listCollections(q))
	api.Get("/collections/:address/traits", collectionTraits(q))
	api.Get("/collections/:address", getCollection(q))
	api.Get("/trending", getTrending(q))

	api.Get("/auctions", listAuctions(q))
	api.Get("/auctions/:id", getAuction(q))
	api.Get("/auctions/:id/bids", getAuctionBids(q))
	api.Get("/server-time", serverTime())

	api.Get("/offers", listOffers(q))
	api.Get("/offers/:collection/:id/position", offerPosition(q))

	// Wallet NFT enumeration (picker source).
	api.Get("/wallet/:addr/nfts", walletNFTs(q))

	// Notifications (in-app, SSE-backed).
	api.Get("/notifications", jwtMiddleware(cfg), listNotifications(q))
	api.Post("/notifications/read", jwtMiddleware(cfg), markNotificationsRead(q))

	// Profiles.
	api.Get("/profile/:addr", getProfile(q))
	api.Put("/profile/:addr", jwtMiddleware(cfg), putProfile(q))

	// Trust & safety.
	api.Post("/reports", jwtMiddleware(cfg), createReport(q))
	api.Post("/admin/verify", jwtMiddleware(cfg), adminVerify(q, cfg))
	api.Post("/admin/collections/verify", jwtMiddleware(cfg), adminVerifyCollection(q, cfg))

	api.Get("/search", search(q))
	api.Get("/metrics", marketMetrics(q))
	api.Get("/activity", recentActivity(q))
	api.Get("/indexer/status", indexerStatus(q))
}

// ── Middleware ────────────────────────────────────────────────────────────────

// jwtMiddleware authenticates either via an Authorization: Bearer token OR
// an address-bound HttpOnly session cookie (the cookie is set by the SIWE
// verify handler). The cookie path is what makes JWTs uninteresting to
// XSS — even a successful script injection can't read HttpOnly storage.
//
// Multiple `mw_s_<addr-prefix>` cookies can be present after a wallet
// switch (old cookie was set, new one issued). The middleware tries every
// match and accepts the first one that verifies; tokens for other wallets
// are simply ignored.
func jwtMiddleware(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		verify := func(token string) string {
			a, err := auth.Verify(token, cfg.JWTSecret, auth.DefaultAudience)
			if err != nil {
				return ""
			}
			return a
		}
		var addr string
		if hdr := c.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
			addr = verify(strings.TrimPrefix(hdr, "Bearer "))
		}
		if addr == "" {
			for _, name := range sessionCookieNames(c) {
				if v := c.Cookies(name); v != "" {
					if a := verify(v); a != "" {
						addr = a
						break
					}
				}
			}
		}
		if addr == "" {
			return writeErr(c, fiber.StatusUnauthorized, "missing token")
		}
		c.Locals(string(auth.CallerKey), addr)
		return c.Next()
	}
}

// sessionCookieNames scans cookie headers for any mw_s_<addr-prefix> name.
// Multiple entries are commonly present (one per previously-connected wallet);
// the middleware validates each in turn. Returns nil when no candidate.
func sessionCookieNames(c *fiber.Ctx) []string {
	hdr := c.Get("Cookie")
	if hdr == "" {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	for _, part := range strings.Split(hdr, ";") {
		p := strings.TrimSpace(part)
		if !strings.HasPrefix(p, "mw_s_") {
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
		out = append(out, name)
	}
	return out
}

func rateLimitMiddleware(rl *ratelimit.Limiter) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !rl.Allow(clientIP(c), 60, time.Minute) {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "rate limit exceeded"})
		}
		return c.Next()
	}
}

// clientIP returns the real client IP for rate limiting. Fly.io's load
// balancer (and any compliant RFC 7239 reverse proxy) *appends* the
// originating client address to any existing X-Forwarded-For header, so the
// real IP is the rightmost entry. Trusting parts[0] would let any caller
// spoof their IP by sending `X-Forwarded-For: 1.2.3.4` before the head
// balancer gets the request. An empty / whitespace-only XFF falls through
// to `c.IP()` so the rate-limit bucket key can never be blank.
func clientIP(c *fiber.Ctx) string {
	xff := strings.TrimSpace(c.Get("X-Forwarded-For"))
	if xff != "" {
		if i := strings.LastIndex(xff, ","); i >= 0 {
			xff = strings.TrimSpace(xff[i+1:])
		} else {
			xff = strings.TrimSpace(xff)
		}
		if xff != "" {
			return xff
		}
	}
	return c.IP()
}

// buildOrigins returns the comma-separated CORS AllowOrigins list. In
// production we ONLY allow the configured FrontendURL — no localhost
// fallbacks, so a compromised dev-machine port on the user's network can't
// pull credentials. In development we additionally permit loopback origins
// so the SPA can run from any of the canonical Vite/Go ports.
func buildOrigins(frontendURL, env string) string {
	if env == "production" {
		return frontendURL
	}
	origins := frontendURL
	if !strings.Contains(origins, "localhost") {
		origins += ",http://localhost:3000,http://localhost:8080,http://127.0.0.1:3000,http://127.0.0.1:8080"
	}
	return origins
}

// ── JSON helpers ──────────────────────────────────────────────────────────────

func writeErr(c *fiber.Ctx, status int, msg string) error {
	return c.Status(status).JSON(fiber.Map{"error": msg})
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "not found") || strings.Contains(s, "no rows")
}

func bodyDecode(c *fiber.Ctx, v any) error {
	return json.Unmarshal(c.Body(), v)
}

// ── SSE handler ───────────────────────────────────────────────────────────────

func sseHandler(bcast *sse.Broadcaster) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")
		c.Set("X-Accel-Buffering", "no")

		ch, cancel, ok := bcast.Subscribe()
		if !ok {
			return c.Status(fiber.StatusServiceUnavailable).SendString("too many subscribers")
		}
		defer cancel()

		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case msg, ok := <-ch:
					if !ok {
						return
					}
					_, _ = w.WriteString(msg)
					_ = w.Flush()
				case <-ticker.C:
					_, _ = w.WriteString(": keepalive\n\n")
					_ = w.Flush()
				}
			}
		})
		return nil
	}
}
