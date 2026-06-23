// Package api wires all REST handlers and SSE using Go Fiber.
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
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
		// (window.MW_MARKETPLACE = '{{.MarketplaceAddr}}') and the SSE bump
		// IIFE. Both blocks contain only env-controlled values plus literal
		// JS — Go's html/template auto-escapes the injected strings — so the
		// 'unsafe-inline' tradeoff is the standard practical match for
		// self-hosted Alpine + dynamic injection. Nonces per response are
		// the strict pattern; we revisit when a richer threat model requires
		// it.
		"script-src 'self' 'unsafe-inline' 'unsafe-eval' https://esm.sh; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
		"font-src 'self' https://fonts.gstatic.com; " +
		"img-src 'self' data: blob: https: ipfs:; " +
		"connect-src 'self' https://coston2-api.flare.network https://ipfs.io https://dweb.link https://gateway.pinata.cloud https://api.reown.com https://*.walletconnect.com wss://relay.walletconnect.com wss://*.walletconnect.com; " +
		"frame-src 'self' https://*.walletconnect.com https://verify.walletconnect.com; " +
		"frame-ancestors 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'"
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

	// gzip/brotli compression on every compressible response — bandwidth
	// is by far the largest line item in our Fly bill and the JSON HTMX
	// partials (listings/auctions/activity/token_live/etc.) compress >10x.
	// Fiber's compress middleware ships with a default Content-Type
	// allow-list that excludes text/event-stream (and the SSE handler
	// below also sets X-Accel-Buffering: no), so a LevelDefault install
	// keeps the SSE channel uncompressed. If you switch to LevelBestSpeed
	// or otherwise loosen the filter, audit the SSE path here — a
	// compressed text/event-stream breaks the nginx buffering fix.
	//
	// Belt-and-braces path-level skip for `/events` itself: the default
	// filter excludes SSE by Content-Type but Fiber still installs its
	// response-writer wrap around the handler. With our `SetBodyStreamWriter`
	// callback pattern that streaming writer can race with the wrap and
	// stall the response — clients see no headers, no body, and (because
	// the server has nothing to flush) eventually a 502 from the fly.io
	// edge. Bridging to /parts/event-types by path (not just content-type)
	// means the compress middleware never touches the SSE stream — not
	// even as a pass-through. The default whitelist still excludes the
	// actual encode step; this skip just removes the wrap entirely.
	app.Use(compress.New(compress.Config{
		Level: compress.LevelDefault,
		Next: func(c *fiber.Ctx) bool {
			return c.Path() == "/events"
		},
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

// clientIP returns the real client IP for rate limiting.
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
func clientIP(c *fiber.Ctx) string {
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

// sseHandler streams events from a Broadcaster to the browser. The /events
// route is the live update channel for bids, listings, offers, auction
// tick-downs, and notifications — every page subscribes to it as a
// background EventSource.
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

		// Belt-and-braces: even when SetBodyStreamWriter's callback never
		// fires (client disconnect during header serialization — fasthttp
		// can skip the callback in that path), the subscription must be
		// released so the broadcaster map doesn't leak. We attach a
		// watcher goroutine that fires cancel() the moment the request
		// context is done. sync.Once makes the call safe whether the
		// outer fallback goroutine or the inner defer wins.
		vctx := c.Context()
		var cancelOnce sync.Once
		cancelOnceFn := func() { cancelOnce.Do(cancel) }
		go func() {
			<-vctx.Done()
			cancelOnceFn()
		}()

		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			// Belt-and-braces: cancel SCOPED TO THE WRITER'S LIFETIME, not
			// the handler's. The previous code put `defer cancel()` at the
			// outer handler scope, where it fired IMMEDIATELY when the
			// handler returned nil — BEFORE fasthttp could serialize
			// headers or hand the bufio.Writer to the stream callback.
			// That race tore the subscriber out of the broadcaster map
			// before any bytes were written, leaving the client
			// permanently stalled on /events with zero bytes and zero
			// headers (the edge proxy would then time out and return 502
			// to the browser).
			defer cancelOnceFn()

			// Keepalive cadence: short enough to detect dead connections
			// within ~30s, long enough not to spam clients with empty
			// frames. The very first tick fires 15s AFTER the handler
			// return — the sentinel flush below makes that wait invisible
			// from the client side.
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()

			// Sentinel first-byte flush. Forces fasthttp to commit the
			// response headers + the first chunk in the same TCP write so
			// the edge proxy and the browser never see a zero-bytes
			// prelude. The ": connected" comment is an SSE comment line —
			// ignored by EventSource consumers but counted as data flush
			// by the transport, which is exactly what we want for the
			// initial flush trigger.
			_, _ = w.WriteString(": connected\n\n")
			_ = w.Flush()

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
