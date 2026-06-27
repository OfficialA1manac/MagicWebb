// Package api wires all REST handlers and SSE using Go Fiber.
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/cors"
	flog "github.com/gofiber/fiber/v2/middleware/logger"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/chain"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
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
		// Self-hosted JS bundles (htmx/ethers/alpinejs/walletconnect-sdk)
		// all live under /static, served same-origin. The self-hosted
		// wc-bundle.js is the fallback; the Reown AppKit bridge
		// (appkit-bridge.js) loads the official Reown SDK from esm.sh
		// as an ES module — esm.sh is allow-listed in script-src for
		// this single module import. All transitive imports resolve
		// against esm.sh's origin, not the page origin, so a compromised
		// esm.sh could inject JS — the self-hosted WC bundle is the
		// fallback when esm.sh is unreachable.
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
		// self-hosted Alpine + dynamic injection.
	"script-src 'self' 'unsafe-inline' 'unsafe-eval' https://esm.sh; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
		"font-src 'self' https://fonts.gstatic.com https://fonts.reown.com; " +
		"img-src 'self' data: blob: https: ipfs:; " +
		// v24.1 — Reown AppKit CSP: AppKit (the Reown SDK used via appkit-bridge.js)
		// needs access to its own config API, wallet SDK domains, and relay endpoints.
		// The previous CSP blocked api.web3modal.org (project config) and
		// cca-lite.coinbase.com (Coinbase Wallet SDK), which silently prevented
		// the AppKit modal from initialising — users only saw the raw WC URI fallback.
		//
		// connect-src additions:
		//   * api.web3modal.org — AppKit remote project config (required for init)
		//   * cca-lite.coinbase.com — Coinbase Wallet SDK amp endpoint
		//   * *.reown.com — Reown CDN / API (fonts, assets, future endpoints)
		//   * rpc.walletconnect.com — WalletConnect RPC proxy
	"connect-src 'self' https://coston2-api.flare.network https://ipfs.io https://dweb.link https://gateway.pinata.cloud https://api.reown.com https://esm.sh https://api.web3modal.org https://cca-lite.coinbase.com https://rpc.walletconnect.com https://*.walletconnect.com https://*.walletconnect.org https://*.reown.com wss://relay.walletconnect.com wss://*.walletconnect.com wss://relay.walletconnect.org wss://*.walletconnect.org wss://www.walletlink.org; " +
		// worker-src: blob workers needed by WalletConnect SDK crypto relay.
	"worker-src 'self' blob:; " +
		// frame-src: WalletConnect + Reown verify iframes + explorer panel.
	"frame-src 'self' https://*.walletconnect.com https://*.walletconnect.org https://verify.walletconnect.com https://verify.walletconnect.org https://*.reown.com; " +
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

// Mount registers all REST + SSE routes on the Fiber app.
// serverTimeMs is updated atomically by the indexer; the /api/v1/server-time
// endpoint reads it under the rate-limited api group.
func Mount(app *fiber.App, q *db.Q, bcast *sse.Broadcaster, rl *ratelimit.Limiter, cfg *config.Config, eth chain.Caller, serverTimeMs *int64) {
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

	app.Get("/events", sseHandler(bcast))

	api := app.Group("/api/v1", rateLimitMiddleware(rl))

	// Domain-specific route registrations.
	NewListingsService(q, eth).RegisterRoutes(api)
	NewAuctionsService(q).RegisterRoutes(api)
	NewOffersService(q).RegisterRoutes(api)
	NewCollectionsService(q).RegisterRoutes(api)
	ms := NewMediaService(q, eth, rl)
	ms.RegisterRoutes(api)
	NewWalletService(q).RegisterRoutes(api)
	NewNotificationsService(q).RegisterRoutes(api, cfg)
	NewProfilesService(q).RegisterRoutes(api, cfg)
	NewAdminService(q, cfg).RegisterRoutes(api, cfg)
	NewSearchService(q).RegisterRoutes(api)
	NewSavedSearchesService(q).RegisterRoutes(api, cfg)
	NewMetricsService(q).RegisterRoutes(api)
	NewIndexerService(q, cfg.ChainID).RegisterRoutes(api)

	// Image-by-hash route registered under /api/v1 so it shares the
	// rateLimitMiddleware applied to the api router — the old app-level
	// registration bypassed rate limiting entirely.
	api.Get("/img/:sha256", ms.HandleImageByHash())

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
		if !rl.Allow(ClientIP(c), 60, time.Minute) {
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

// ── SSE handler ───────────────────────────────────────────────────────────────

// ssePerIPLimit is the maximum concurrent SSE connections per IP.
// /events serves only public market data (listings, auctions, offers,
// activity) — no auth, no PII — but a single IP holding thousands of
// connections can exhaust the 10k subscriber pool. Capping at 20 per IP
// leaves plenty of headroom for legitimate browser tabs while preventing
// a single actor from dominating the pool.
const ssePerIPLimit = 20

// sseConns tracks active SSE connections per IP. Writes happen on
// subscribe/cancel (low frequency — at most one per tab open/close).
// sync.Map chosen over RWMutex+map because subscribe is called during
// request setup (not in the stream-write hot path) and the map grows
// slowly (one entry per unique IP).
var sseConns sync.Map // IP string → *int64

// sseHandler streams events from a Broadcaster to the browser. The /events
// route is the live update channel for bids, listings, offers, auction
// tick-downs, and notifications — every page subscribes to it as a
// background EventSource.
//
// Per-IP connection cap (v35): before subscribing, increments an atomic
// counter keyed on ClientIP. Returns 429 if the IP exceeds ssePerIPLimit.
// Decrements on cancel/disconnect so the slot is released when the tab
// closes. The cap prevents a single IP from exhausting the global 10k
// subscriber pool.
//
// Cleanup contract (the fix this revision introduces):
//   • `cancel()` is deferred INSIDE the SetBodyStreamWriter callback, not
//     at the outer handler scope. Putting `defer cancel()` on the outer
//     scope fired the instant the handler returned `nil` — BEFORE fasthttp
//     could serialise headers or hand the bufio.Writer to the stream
//     callback — tearing the subscriber out before a single byte was
//     written and leaving the browser stalled on /events with zero
//     headers and zero body.
//   • Every `w.WriteString` and `w.Flush` is now error-checked; on error
//     (which happens when the TCP socket / fasthttp response closes
//     because the client disconnected, the edge proxy timed out, or the
//     broadcaster cancelled the channel) we `return` from the callback
//     so the deferred `cancel()` reliably releases the subscriber slot.
//   • Earlier revisions spawned a `go func() { <-c.Context().Done(); ... }`
//     watcher to "belt-and-braces" the lifecycle, but `fasthttp`'s
//     `RequestCtx.Done()` closes immediately when the request handler
//     returns, not when the TCP socket actually drops — so that
//     goroutine fired the cancel atomically, also tearing the
//     subscriber out before any stream, AND racing with `RequestCtx`
//     state mutation (a documented fasthttp gotcha). It is gone. The
//     write/flush error path inside the callback is the only reliable
//     signal we have on the fiber side, and when combined with the
//     deferred `cancel()` it covers every disconnect path we care about.
func sseHandler(bcast *sse.Broadcaster) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")
		c.Set("X-Accel-Buffering", "no")

		// Per-IP concurrent connection cap. The increment happens here
		// (pre-stream), but the DEFERMENT is INSIDE the stream callback —
		// the outer handler returns nil immediately after SetBodyStreamWriter,
		// so a defer here would fire before the stream is active.
		ip := ClientIP(c)
		raw, _ := sseConns.LoadOrStore(ip, new(int64))
		cnt := raw.(*int64)
		if n := atomic.AddInt64(cnt, 1); n > ssePerIPLimit {
			atomic.AddInt64(cnt, -1)
			return c.Status(fiber.StatusTooManyRequests).SendString("too many connections from this IP")
		}

		ch, cancel, ok := bcast.Subscribe()
		if !ok {
			atomic.AddInt64(cnt, -1)
			return c.Status(fiber.StatusServiceUnavailable).SendString("too many subscribers")
		}

		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			defer cancel()
			// Decrement the per-IP counter when the stream ends (client
			// disconnect, TCP close, or broadcaster cancel). This is
			// INSIDE the stream callback, not the outer handler, because
			// the outer handler returns nil immediately (before the
			// stream is active) — a defer there would fire instantly.
			defer atomic.AddInt64(cnt, -1)

			// Keepalive cadence: short enough to detect dead connections
			// within ~30s, long enough not to spam clients with empty
			// frames. Until the ticker fires, the sentinel flush below
			// is what keeps the edge proxy from holding buffered bytes.
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()

			// Sentinel first-byte flush. Forces fasthttp to commit the
			// response headers + the first chunk in the same TCP write
			// so the edge proxy and the browser never see a zero-bytes
			// prelude. ": connected" is an SSE comment line (ignored by
			// EventSource consumers but transported identically to a
			// data frame). On error, return immediately — the deferred
			// `cancel()` releases the subscriber.
			if _, err := w.WriteString(": connected\n\n"); err != nil {
				return
			}
			if err := w.Flush(); err != nil {
				return
			}

			for {
				select {
				case msg, ok := <-ch:
					if !ok {
						return
					}
					if _, err := w.WriteString(msg); err != nil {
						return
					}
					if err := w.Flush(); err != nil {
						return
					}
				case <-ticker.C:
					if _, err := w.WriteString(": keepalive\n\n"); err != nil {
						return
					}
					if err := w.Flush(); err != nil {
						return
					}
				}
			}
		})
		return nil
	}
}
