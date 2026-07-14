// Package api wires all REST handlers and SSE using Go Fiber.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/cors"
	flog "github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp/fasthttpadaptor"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/chain"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	connectv1 "github.com/OfficialA1manac/MagicWebb/backend/internal/connectrpc/marketplacev1"
	marketplacev1connect "github.com/OfficialA1manac/MagicWebb/backend/internal/connectrpc/marketplacev1/marketplacev1connect"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/connectrpc/interceptors"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"

	"connectrpc.com/connect"
	"connectrpc.com/grpcreflect" // gRPC server reflection for grpcurl discovery
	"github.com/OfficialA1manac/MagicWebb/backend/internal/indexer"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/ratelimit"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/ws"

	graphql "github.com/OfficialA1manac/MagicWebb/backend/internal/graphql"
)

// Strict-transport / content-security baseline. CSP locks scripts to self +
// the explicitly allow-listed CDNs (or self-hosted) so a compromised CDN
// can't inject behavior. frame-ancestors 'none' plus X-Frame-Options DENY
// blocks clickjacking; Referrer-Policy keeps URLs out of cross-origin
// Referer headers (so wallet addresses?action=foo don't leak). X-Content-
// Type-Options=nosniff across all responses prevents MIME sniffing even
// where image handlers already set it.
const (
	cspHeader = "default-src 'self'; " +	// All JS bundles are SELF-HOSTED under /static (htmx, ethers, alpinejs,
	// qrcode, wc-bundle, wallet.js, ws.js). No third-party CDN script
	// dependencies remain. The AppKit bridge (appkit-bridge.js) is built
	// by Astro/Vite from app/src/appkit-bridge.js and served same-origin.
		// Alpine.js evaluates expressions via `new Function()` at runtime, so
		// 'unsafe-eval' is required for any x-data / x-show / x-if to mount
		// under CSP. The alternative (@alpinejs/csp) requires a maintained
		// build pipeline and pre-compiled templates — accepting 'unsafe-eval'
		// here is the lowest-friction fix for self-hosted Alpine without
		// weakening XSS mitigation (templates remain literal script content;
		// only the Alpine runtime parses expressions).
		//
		// 'unsafe-inline' is required so the server-rendered inline <script>
	// blocks in templates/layout.html execute: the runtime-config inject
	// (window.MW_MARKETPLACE = '{{.MarketplaceAddr}}') and the inline wallet
	// init IIFE. Both blocks contain only env-controlled values plus literal
		// JS — Go's html/template auto-escapes the injected strings — so the
		// 'unsafe-inline' tradeoff is the standard practical match for
		// self-hosted Alpine + dynamic injection.
	"script-src 'self' 'unsafe-inline' 'unsafe-eval'; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
		// font-src: Google Fonts (Inter, JetBrains Mono) + Reown AppKit fonts
		// (KHTeka, KHTekaMono). AppKit's self-hosted web components load these
		// from fonts.reown.com via @font-face CSS rules inside wc-bundle.js.
	"font-src 'self' https://fonts.gstatic.com https://fonts.reown.com; " +
		"img-src 'self' data: blob: https:; " +
		// connect-src: WalletConnect relay and RPC endpoints required for
		// wallet pairing and blockchain interaction. The self-hosted WC bundle
		// (wc-bundle.js) handles wallet pairing via WalletConnect relay.
		// AppKit bridge is also self-hosted — no CDN dependencies for scripts —
		// but Reown AppKit's init() fetches project configuration from
		// api.reown.com (formerly api.web3modal.org) via fetch(), which is
		// governed by connect-src, not script-src. cca-lite.coinbase.com is
		// the Coinbase Wallet SDK amp endpoint loaded by AppKit internally.
	"connect-src 'self' https://coston2-api.flare.network https://flare-api.flare.network https://songbird-api.flare.network https://rpc.walletconnect.com https://*.walletconnect.com https://*.walletconnect.org wss://relay.walletconnect.com wss://*.walletconnect.com wss://relay.walletconnect.org wss://*.walletconnect.org wss://www.walletlink.org https://api.reown.com https://api.web3modal.org https://cca-lite.coinbase.com https://*.reown.com; " +
		// worker-src: blob workers needed by WalletConnect SDK crypto relay.
	"worker-src 'self' blob:; " +
		// frame-src: WalletConnect + Reown verify iframes + explorer panel.
	"frame-src 'self' https://*.walletconnect.com https://*.walletconnect.org https://verify.walletconnect.com https://verify.walletconnect.org https://*.reown.com; " +
		"frame-ancestors 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'; " +
		"object-src 'none'; " +
		"upgrade-insecure-requests"
	hstsHeader        = "max-age=63072000; includeSubDomains; preload"
	permissionsPolicy = "geolocation=(), microphone=(), camera=(), payment=(self \"https://magicwebb.xyz\"), usb=()"
	referrerPolicy    = "strict-origin-when-cross-origin"
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
		// Cross-Origin-Opener-Policy: "same-origin-allow-popups" is the
	// wallet-safe value. "same-origin" breaks Coinbase Wallet SDK +
	// WalletConnect popup flows; omitting COOP entirely works but leaks
	// the opener relationship to cross-origin navigations. This value
	// isolates the browsing context from cross-origin openers while
	// still allowing the wallet popup window to access its opener.
	c.Set("Cross-Origin-Opener-Policy", "same-origin-allow-popups")
	// Cross-Origin-Resource-Policy: "same-origin" prevents cross-origin
	// documents from embedding our resources (images, scripts, styles).
	// The CSP frame-ancestors 'none' already blocks framing, but CORP
	// adds a defence-in-depth layer at the resource-fetch level.
	c.Set("Cross-Origin-Resource-Policy", "same-origin")
		return c.Next()
	}
}

// MWServerBuildSHA is the git SHA the running binary was compiled from.
// Injected at link time via `go build -ldflags '-X .../api.MWServerBuildSHA=<sha>'`
// by the Makefile (driven by `git rev-parse HEAD` at build time) so the
// /healthz endpoint can return an X-MW-Build-SHA header. tools/check-fly-sync.sh
// reads this header off the live magicwebb.fly.dev to verify Fly is serving
// the SHA that's actually on origin/main — closing the v23 deploy-drift
// class of bug where Fly registered a new release successfully but the
// Docker layer cache pinned the previous binary's static assets.
var MWServerBuildSHA = "unknown"

// Mount registers all REST + SSE + WebSocket routes on the Fiber app.
// serverTimeMs is updated atomically by the indexer; the /api/v1/server-time
// endpoint reads it under the rate-limited api group.
func Mount(app *fiber.App, q *db.Q, bcast *sse.Broadcaster, rl *ratelimit.Limiter, cfg *config.Config, eth chain.Caller, serverTimeMs *int64, apiKeyStore auth.APIKeyStore, auditLog auth.AuditLogger) {
	app.Use(securityHeaders())
	app.Use(cors.New(cors.Config{
		AllowOrigins:     buildOrigins(cfg.FrontendURL, cfg.Env),
		AllowMethods:     "GET,POST,PUT,DELETE,OPTIONS",
		AllowHeaders:     "Content-Type,Authorization",
		AllowCredentials: true,
	}))

	app.Use(flog.New(flog.Config{
		Format: "${time} ${method} ${path} ${status} ${latency}\n",
	}))

	// gzip/brotli compression on every compressible response — bandwidth
	// is by far the largest line item in our Fly bill and the JSON HTMX
	// partials (listings/auctions/activity/token_live/etc.) compress >10x.
	app.Use(compress.New(compress.Config{
		Level: compress.LevelDefault,
	}))

	// /healthz = liveness. Must respond 200 within ~3.5s even when the
	// process is degraded — used by Fly's `[checks.health]` (30s interval,
	// 5s timeout) to decide whether a machine is replaceable. Probes BOTH
	// the DB (via q.Ping) AND the RPC pool (via eth.BlockNumber on a 3s ctx
	// deadline): a wedged RPC pool would otherwise leak past this route
	// until the first /api/v1/listings or /api/v1/auctions call surfaces a
	// dial-timeout to a real user.
	app.Get("/healthz", func(c *fiber.Ctx) error {
		pingCtx, cancel := context.WithTimeout(c.Context(), 3*time.Second)
		defer cancel()
		if err := q.Ping(pingCtx); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).SendString("db unhealthy")
		}
		rpcCtx, cancelRPC := context.WithTimeout(c.Context(), 3*time.Second)
		defer cancelRPC()
		if _, err := eth.BlockNumber(rpcCtx); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).SendString("rpc unhealthy")
		}
		// v23.1 — X-MW-Build-SHA header. Injected via Makefile -ldflags as
		// `api.MWServerBuildSHA` (see top-of-file var declaration). Surfaced
		// here so tools/check-fly-sync.sh can read it off the live site
		// and assert it equals `git rev-parse origin/main` — the v74-class
		// deploy-drift bug class (Fly records a new release, but the
		// Docker layer cache still serves the previous build's static
		// assets) becomes loudly detectable in CI instead of silently
		// observable by a user hours later.
		c.Set("X-MW-Build-SHA", MWServerBuildSHA)
		return c.SendStatus(fiber.StatusOK)
	})
	// /readyz = readiness. DB-only (q.Ping verifies connectivity, NOT
	// row-writability or migration state). Kept narrower than /healthz so
	// an upstream RPC outage does not take /readyz down while the DB is
	// fine — the liveness check on /healthz drives machine replacement
	// separately.
	app.Get("/readyz", func(c *fiber.Ctx) error {
		if err := q.Ping(c.Context()); err != nil {
			return c.Status(fiber.StatusServiceUnavailable).SendString("db unhealthy")
		}
		return c.SendStatus(fiber.StatusOK)
	})

	// WebSocket endpoint for bidirectional real-time communication.
	// Supports authenticated (SIWE JWT cookie) and unauthenticated connections.
	// Action queries (get_listing, get_auction, etc.) are served via a
	// Connect-RPC client pointing to the local HTTP server.
	wsClient := marketplacev1connect.NewMarketplaceServiceClient(
		http.DefaultClient,
		"http://localhost"+cfg.HTTPAddr,
	)
	wsHandler := ws.NewHandler(cfg, bcast, q, wsClient, func() int64 { return atomic.LoadInt64(serverTimeMs) })
	app.Get("/ws", wsHandler.HandleWebSocket)

	// GraphQL endpoint for rich data queries.
	// POST /graphql — execute queries against the GraphQL schema.
	// GET /graphql — returns documentation page.
	// GET /graphiql — interactive GraphQL IDE.
	// Rate-limited at the same tier as /api/v1 to prevent arbitrary-depth
	// queries from becoming a DB DoS vector.
	//
	// GraphQL resolvers delegate to Connect-RPC when a gRPC client is
	// provided, decoupling presentation from storage. The client points
	// to the local Fiber server (which also mounts the Connect-RPC handler).
	gql := graphql.NewGraphQLServer(q, bcast, wsClient)
	gqlLimiter := rateLimitMiddleware(rl)
	app.Post("/graphql", gqlLimiter, gql.HandlePOST)
	app.Get("/graphql", gqlLimiter, gql.HandleGET)
	app.Get("/graphiql", gqlLimiter, gql.HandleGraphiQL)

	// ── Connect-RPC (gRPC-Web / gRPC / Connect protocol) ─────────────────
	// Serves the MarketplaceService defined in marketplace.proto. Clients
	// can use any supported protocol (Connect JSON, Connect Protobuf, gRPC,
	// gRPC-Web). The handler is mounted at /marketplace.v1.MarketplaceService/*
	// and dispatched by Connect-RPC's built-in router.
	//
	// This service replaces the WebSocket action-based queries (get_listing,
	// get_auction, get_offer, get_token) with standard typed RPCs that
	// work over HTTP/1.1, HTTP/2, or any gRPC-compatible transport.
	// Not rate-limited — each RPC is a simple DB query comparable to the
	// equivalent REST endpoint.
	connectSrv := connectv1.NewServer(q, bcast)

	// ── gRPC interceptors: metrics, tiered rate limiting, auth, deadlines ────
	// Applied to every Connect-RPC handler. Order matters:
	//  1. Metrics — outermost: records latency even on rate-limit rejections
	//  2. Tiered rate limit — per-procedure limits (list:120/min, search:30/min)
	//  3. Auth — extracts JWT caller for audit (public-read, not enforced)
	//  4. Deadline — applies 30s default timeout when none set
	grpcInterceptors := connect.WithInterceptors(
		interceptors.MetricsInterceptor(),
		interceptors.TieredRateLimitInterceptor(rl, interceptors.DefaultRateLimits()),
		interceptors.AuthInterceptorWithAPIKeys(cfg.JWTSecret, false, apiKeyStore, auditLog), // public-read + API key support (AUTH-3)
		interceptors.DeadlineInterceptor(30*time.Second),
	)
	connectPath, connectHandler := marketplacev1connect.NewMarketplaceServiceHandler(connectSrv, grpcInterceptors)
	// Adapt the net/http Connect-RPC handler for Fiber's fasthttp.
	app.All(connectPath+"*", func(c *fiber.Ctx) error {
		fasthttpadaptor.NewFastHTTPHandler(connectHandler)(c.Context())
		return nil
	})

	// ── gRPC Server Reflection ──────────────────────────────────────────
	// Enables discovery tools (grpcurl, grpc_cli) to list services and
	// call methods without the proto file. Both v1 and v1alpha reflection
	// handlers are registered for maximum client compatibility.
	reflector := grpcreflect.NewStaticReflector(
		marketplacev1connect.MarketplaceServiceName,
	)
	reflectV1Path, reflectV1Handler := grpcreflect.NewHandlerV1(reflector)
	reflectV1AlphaPath, reflectV1AlphaHandler := grpcreflect.NewHandlerV1Alpha(reflector)
	for _, r := range []struct {
		path    string
		handler http.Handler
	}{
		{reflectV1Path, reflectV1Handler},
		{reflectV1AlphaPath, reflectV1AlphaHandler},
	} {
		p := r.path
		h := r.handler
		app.All(p+"*", func(c *fiber.Ctx) error {
			fasthttpadaptor.NewFastHTTPHandler(h)(c.Context())
			return nil
		})
	}

	api := app.Group("/api/v1", rateLimitMiddleware(rl))

	// RL-1: Tiered rate-limit groups for expensive/privileged endpoints.
	// Separate groups at /api/v1 prefix (not nested) because each service
	// registers its own path segments (e.g. SearchService registers /search).
	// Non-overlapping path sets prevent Fiber route conflicts.
	//
	// Key prefixes ("search", "admin", "api") scoped per-tier prevent
	// cross-tier bucket interference — a search at 30/min uses a different
	// bucket from a listing at 60/min even for the same client IP.
	apiSearch := app.Group("/api/v1", tieredRateLimitMiddleware(rl, "search", 30, time.Minute))
	apiAdmin := app.Group("/api/v1", tieredRateLimitMiddleware(rl, "admin", 10, time.Minute))

	// ── CACHE-1: Distributed-ready caches for read-heavy endpoints ──────
	// When REDIS_URL is configured and the go-redis dependency is compiled
	// in, these caches are Redis-backed (shared across all instances).
	// Without Redis, they degrade to in-memory (per-instance) automatically.
	//
	// 30s TTL for trending (scores recomputed periodically, stale is fine)
	trendingCache := cache.NewRedisOrMemory(cfg.RedisURL, 30*time.Second)
	// 10s TTL for activity (more real-time, but DB query is the expensive part)
	activityCache := cache.NewRedisOrMemory(cfg.RedisURL, 10*time.Second)

	// Domain-specific route registrations.
	NewListingsService(q, eth).RegisterRoutes(api)
	NewAuctionsService(q).RegisterRoutes(api)
	NewOffersService(q).RegisterRoutes(api)
	NewCollectionsService(q, trendingCache).RegisterRoutes(api)
	ms := NewMediaService(q, eth, rl)
	ms.RegisterRoutes(api)
	NewWalletService(q).RegisterRoutes(api)
	NewNotificationsService(q).RegisterRoutes(api, cfg)
	NewProfilesService(q).RegisterRoutes(api, cfg)
	NewAdminService(q, cfg).RegisterRoutes(apiAdmin, cfg)
	NewSearchService(q).RegisterRoutes(apiSearch)
	NewSavedSearchesService(q).RegisterRoutes(api, cfg)
	NewMetricsService(q, activityCache, wsHandler).RegisterRoutes(api)
	NewIndexerService(q, cfg.ChainID).RegisterRoutes(api)

	// Image-by-hash route: registered at app level (NOT under the rate-limited
	// /api/v1 group) because it serves locally-stored blobs from the database
	// — there is no outbound HTTP fetch involved, so the SSRF / abuse surface
	// is minimal (it can only serve bytes already committed to the image store).
	// Pages with 48+ listing cards each load their image from this endpoint;
	// rate limiting that would block legitimate page loads. The /api/v1/media
	// proxy endpoint (which DOES make outbound fetches) remains rate-limited.
	app.Get("/api/v1/img/:sha256", ms.HandleImageByHash())

	// Server-time endpoint (used by auction countdown timers). Moved
	// from mountUI's bare app.Get into the rate-limited api group so
	// it inherits rateLimitMiddleware (60 req/min per IP).
	api.Get("/server-time", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"unix_ms": atomic.LoadInt64(serverTimeMs)})
	})
}

// ── Middleware ───────────────────────────────────────────────────────────────

// jwtMiddleware authenticates either via an Authorization: Bearer token OR
// an address-bound HttpOnly session cookie (the cookie is set by the SIWE
// verify handler). The cookie path is what makes JWTs uninteresting to
// XSS — even a successful script injection can't read HttpOnly storage.
//
// Multiple mw_a_<addr-prefix> (and legacy mw_s_) cookies can be present after a wallet
// switch (old cookie was set, new one issued). The middleware tries every
// match and accepts the first one that verifies; tokens for other wallets
// are simply ignored.
// JwtMiddleware authenticates requests via Bearer token or session cookie.
func JwtMiddleware(cfg *config.Config) fiber.Handler {
	return jwtMiddleware(cfg)
}

func jwtMiddleware(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		verify := func(token string) string {
			a, err := auth.VerifyAccessToken(token, cfg.JWTSecret)
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

// sessionCookieNames scans cookie headers for mw_s_ / mw_a_ / mw_r_ cookie names.
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
		if !strings.HasPrefix(p, "mw_s_") && !strings.HasPrefix(p, "mw_a_") {
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
	return tieredRateLimitMiddleware(rl, "api", 60, time.Minute)
}

// tieredRateLimitMiddleware returns a rate-limit middleware with a custom
// limit, window, and key prefix (RL-1: per-endpoint tiered rate limits).
// The keyPrefix scopes each tier's bucket independently — without it, a
// search request at 30/min would share the same IP bucket as a listing
// request at 60/min, causing cross-tier interference.
func tieredRateLimitMiddleware(rl *ratelimit.Limiter, keyPrefix string, limit int, window time.Duration) fiber.Handler {
	return func(c *fiber.Ctx) error {
		key := keyPrefix + "|" + ClientIP(c)
		if !rl.Allow(key, limit, window) {
			c.Set("Retry-After", strconv.Itoa(int(window.Seconds())))
			c.Set("X-RateLimit-Limit", strconv.Itoa(limit))
			c.Set("X-RateLimit-Remaining", "0")
			c.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(window).Unix(), 10))
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "rate limit exceeded"})
		}
		return c.Next()
	}
}

// ClientIP returns the real client IP for rate limiting.
// Exported so cmd/server/main.go auth handlers can use the same trust-hierarchy-
// aware resolution as the rate limiter, instead of calling c.IP() directly.
//
// Trust hierarchy (top wins):
//   1. Fly-Client-IP — Fly.io's reverse-proxy-stamped header;
//      mathematically unspoofable from the outside because Fly's edge
//      strips any inbound copy.
//   2. RFC 7239 Forwarded `for=` — modern standard; bracket-stripped
//      IPv6 + port-stripped form.
//   3. X-Forwarded-For rightmost — legacy; right-trusted only when
//      behind a known reverse proxy (and the previous rightmost-XFF
//      pattern that this method replaces is exactly the bypass the
//      audit fix closes).
//   4. fasthttp's RemoteAddr — last-resort so the bucket key isn't blank.
//
// Audit: `clientIpSpoof` 🟠 P1.
func ClientIP(c *fiber.Ctx) string {
	if v := strings.TrimSpace(c.Get("Fly-Client-IP")); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Get("Forwarded")); v != "" {
		// RFC 7239: `Forwarded: for=192.0.2.1;by=...;proto=https` or
		// quoted `for="[2001:db8::1]:443"`. Split on `;` then pick the
		// `for=` segment, strip any quotes / brackets / port suffix.
		for _, part := range strings.Split(v, ";") {
			p := strings.TrimSpace(part)
			low := strings.ToLower(p)
			if !strings.HasPrefix(low, "for=") {
				continue
			}
			id := strings.Trim(p[4:], " \"")
			id = stripAddrPort(id)
			if id != "" {
				return id
			}
		}
	}
	if v := strings.TrimSpace(c.Get("X-Forwarded-For")); v != "" {
		// Right-trusted (RFC 7239 rightmost entry semantics). The header
		// may carry `client, proxy1, proxy2`; the most-recent hop is
		// rightmost because compliant proxies APPEND. Spoofability is
		// bounded by trusting only this path behind the explicit
		// Fiber ProxyHeader configuration (Fly-Client-IP).
		if i := strings.LastIndex(v, ","); i >= 0 {
			v = strings.TrimSpace(v[i+1:])
		} else {
			v = strings.TrimSpace(v)
		}
		if v != "" {
			return v
		}
	}
	return c.IP()
}

// stripAddrPort normalises RFC-7239 `for=` payloads:
//   • `[2001:db8::1]:443` → `2001:db8::1` (bracketed IPv6 with :port)
//   • `2001:db8::1`       → `2001:db8::1` (bare IPv6, kept)
//   • `192.0.2.1:443`     → `192.0.2.1` (IPv4 with :port)
//   • `192.0.2.1`         → `192.0.2.1` (bare IPv4, kept)
// Quoted-obfuscation forms (`for="_foo"`) are stripped by the caller.
func stripAddrPort(id string) string {
	if id == "" {
		return id
	}
	if strings.HasPrefix(id, "[") {
		// Bracketed IPv6 — strip the `[...]` envelope. If a `:port`
		// follows after `]`, strip that too.
		if end := strings.Index(id, "]"); end >= 0 {
			inner := id[1:end]
			if rest := id[end+1:]; rest != "" {
				if strings.HasPrefix(rest, ":") && looksLikePort(rest[1:]) {
					return inner
				}
			}
			return inner
		}
		// Malformed bracket pair — drop the prefix and return rest.
		return id[1:]
	}
	// Bare IPv6 (multiple colons) — port wouldn't make sense, keep as-is.
	if strings.Count(id, ":") > 1 {
		return id
	}
	// IPv4:port — strip `:port` only when the suffix is numeric.
	if colon := strings.LastIndex(id, ":"); colon >= 0 {
		suf := id[colon+1:]
		if looksLikePort(suf) {
			return id[:colon]
		}
	}
	return id
}

// looksLikePort returns true when s is a non-empty all-digit string.
// Empty input is NOT a port (returns false).
func looksLikePort(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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

// ── JSON helpers ─────────────────────────────────────────────────────────────

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

// ── Reindex Collection ──────────────────────────────────────────────────────

type reindexCollectionReq struct {
	Collection string `json:"collection"`
	FromBlock  uint64 `json:"from_block"`
}

// handleReindexCollection forces a re-scan of Transfer events for a specific
// collection from a given block. This makes past holdings visible after a
// collection is added to TRACKED_COLLECTIONS.
func handleReindexCollection(runner *indexer.Runner, cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := Caller(c)
		if addr == "" || !cfg.IsAdmin(addr) {
			return writeErr(c, fiber.StatusForbidden, "admin only")
		}

		var req reindexCollectionReq
		if err := bodyDecode(c, &req); err != nil {
			return writeErr(c, fiber.StatusBadRequest, "invalid request body")
		}

		req.Collection = strings.TrimSpace(req.Collection)
		if req.Collection == "" || len(req.Collection) != 42 || !strings.HasPrefix(req.Collection, "0x") {
			return writeErr(c, fiber.StatusBadRequest, "invalid collection address")
		}

		if req.FromBlock == 0 {
			req.FromBlock = 1 // default: scan from block 1 (genesis is empty)
		}

		log.Info().
			Str("admin", addr).
			Str("collection", req.Collection).
			Uint64("from_block", req.FromBlock).
			Msg("admin: reindex-collection requested")

		// Run the reindex synchronously so the caller gets the result.
		// For large collections this can take seconds to minutes;
		// the admin caller should set a generous HTTP timeout.
		scanned, err := runner.ReindexCollection(c.Context(), strings.ToLower(req.Collection), req.FromBlock)
		if err != nil {
			log.Error().Err(err).
				Str("collection", req.Collection).
				Int("scanned", scanned).
				Msg("admin: reindex-collection failed")
			return writeErr(c, fiber.StatusInternalServerError, err.Error())
		}

		return c.JSON(fiber.Map{
			"collection":     strings.ToLower(req.Collection),
			"blocks_scanned": scanned,
			"status":         "complete",
		})
	}
}

// MountReindexRoute registers the admin reindex-collection endpoint.
// Call AFTER the indexer Runner is created so the handler can access it.
func MountReindexRoute(app *fiber.App, runner *indexer.Runner, cfg *config.Config) {
	app.Post("/api/v1/admin/indexer/reindex-collection", JwtMiddleware(cfg),
		handleReindexCollection(runner, cfg))
}

// MountAPIKeyRoutes registers the API key management endpoints on the admin
// rate-limited group (AUTH-3). Called from main.go after the API key store
// and audit logger are created. Uses the same rate limiter instance as Mount()
// and registers on the same admin tier (10/min) with JWT middleware.
func MountAPIKeyRoutes(app *fiber.App, q *db.Q, cfg *config.Config, rl *ratelimit.Limiter, apiKeyStore auth.APIKeyStore, auditLog auth.AuditLogger) {
	adminSvc := NewAdminService(q, cfg).WithAPIKeyStore(apiKeyStore, auditLog)
	// Create an admin-tier sub-group at /api/v1/admin/apikeys with 10/min rate limit.
	// Uses a distinct sub-path (/apikeys) to avoid conflicting with the
	// existing /api/v1/admin/* routes registered by AdminService.RegisterRoutes.
	apikeyGroup := app.Group("/api/v1/admin/apikeys", tieredRateLimitMiddleware(rl, "admin", 10, time.Minute))
	apikeyGroup.Post("/", JwtMiddleware(cfg), adminSvc.handleCreateAPIKey)
	apikeyGroup.Get("/", JwtMiddleware(cfg), adminSvc.handleListAPIKeys)
	apikeyGroup.Delete("/:id", JwtMiddleware(cfg), adminSvc.handleRevokeAPIKey)
}
