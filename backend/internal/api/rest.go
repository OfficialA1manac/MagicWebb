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
	"github.com/OfficialA1manac/MagicWebb/backend/internal/ratelimit"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// Mount registers all REST + SSE routes on the Fiber app.
func Mount(app *fiber.App, q *db.Q, bcast *sse.Broadcaster, rl *ratelimit.Limiter, cfg *config.Config, eth chain.Caller) {
	app.Use(cors.New(cors.Config{
		AllowOrigins:     buildOrigins(cfg.FrontendURL),
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
	api.Get("/media", mediaProxy())
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

func jwtMiddleware(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		hdr := c.Get("Authorization")
		if !strings.HasPrefix(hdr, "Bearer ") {
			return writeErr(c, fiber.StatusUnauthorized, "missing token")
		}
		addr, err := auth.Verify(strings.TrimPrefix(hdr, "Bearer "), cfg.JWTSecret)
		if err != nil {
			return writeErr(c, fiber.StatusUnauthorized, "invalid token")
		}
		c.Locals(string(auth.CallerKey), addr)
		return c.Next()
	}
}

func rateLimitMiddleware(rl *ratelimit.Limiter) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !rl.Allow(clientIP(c), 60, time.Minute) {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "rate limit exceeded"})
		}
		return c.Next()
	}
}

func clientIP(c *fiber.Ctx) string {
	if xff := c.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	return c.IP()
}

func buildOrigins(frontendURL string) string {
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
