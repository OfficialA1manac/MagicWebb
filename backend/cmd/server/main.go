// MagicWebb — single binary: Fiber HTTP server + blockchain indexer goroutine.
// Run: go run ./cmd/server   (no Docker, no Redis, no separate processes)
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/api"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/indexer"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/nonce"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/ratelimit"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/rpcpool"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

func main() {
	config.Load()

	if config.C.Env != "production" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// DB
	if err := db.Migrate(config.C.PostgresURL); err != nil {
		log.Fatal().Err(err).Msg("db migration failed")
	}
	pool, err := db.Connect(ctx, config.C.PostgresURL)
	if err != nil {
		log.Fatal().Err(err).Msg("db connect failed")
	}
	defer pool.Close()
	q := db.New(pool)

	// SSE broadcaster with cross-instance fan-out via Postgres LISTEN/NOTIFY.
	// Degrades to local-only delivery if the listen conn is unavailable.
	bcast := sse.NewBridged(ctx, pool, config.C.PostgresURL)

	// Shared (Postgres) rate limiter + nonce store, so limits and single-use
	// SIWE nonces hold across instances.
	rl := ratelimit.NewPg(pool)
	ns := nonce.NewPg(pool)

	// Ethereum access: rotation + failover across every configured endpoint
	// (RPC_URLS, falling back to RPC_URL). All indexer/keeper reads, writes and
	// log filters go through the pool.
	eth, err := rpcpool.New(ctx, config.C.RPCURLs, rpcpool.DefaultTimeout)
	if err != nil {
		log.Fatal().Err(err).Msg("eth rpc pool init failed")
	}

	// serverTimeMs is updated atomically by the indexer watcher
	var serverTimeMs int64

	// Start indexer in background. Keepers gate on a Postgres advisory lock
	// (dedicated connection — not through the shared pool) so only one instance
	// broadcasts settle/refund txs; the returned lockCtx stops them the moment
	// ownership is lost.
	runner := indexer.New(&config.C, q, bcast, eth, &serverTimeMs).
		WithKeeperGate(func(c context.Context) (context.Context, func(), error) {
			return db.WaitKeeperLock(c, config.C.PostgresURL)
		})
	indexerDone := make(chan struct{})
	go func() {
		defer close(indexerDone)
		log.Info().Msg("indexer starting")
		runner.Run(ctx)
		log.Info().Msg("indexer stopped")
	}()

	// Fiber app. ProxyHeader=Fly-Client-IP plus EnableTrustedProxyCheck=false
	// makes `c.IP()` (and our api.rest clientIP helper) trust Fly.io's
	// reverse-proxy-stamped header — mathematically unspoofable from the
	// outside because Fly's edge strips any inbound copy. Combined with the
	// Forwarded / X-Forwarded-For fallback chain, this fixes the Priority
	// Stack `clientIpSpoof` 🟠 P1 (was: trivially spoofable rightmost-XFF
	// extraction when traffic bypassed the proxy).
	app := fiber.New(fiber.Config{
		ReadTimeout:           5 * time.Second,
		WriteTimeout:          0, // SSE connections need no write timeout
		IdleTimeout:           60 * time.Second,
		DisableStartupMessage: false,
		EnableTrustedProxyCheck: false,
		ProxyHeader:           "Fly-Client-IP",
		// BodyLimit: 1 MB. Prevents oversized payload DoS attacks on JSON
		// endpoints (profile update, reports, auth verify). Fiber's default
		// limit is 4 MB — tightening to 1 MB means a degenerate JSON upload
		// cannot starve the connection pool or the Go GC. The limit applies
		// BEFORE any middleware or route handler touches the body — a 413
		// "Request Entity Too Large" is written directly by the framework.
		BodyLimit: 1 * 1024 * 1024, // 1 MB
	})

	// Mount all REST + SSE routes
	api.Mount(app, q, bcast, rl, &config.C, eth)

	// Auth endpoints with tighter rate limit (20 req/min per IP)
	app.Get("/auth/nonce", nonceHandler(ns, rl))
	app.Post("/auth/verify", verifyHandler(ns, rl))

	// Serve HTMX templates + static files
	mountUI(app, q, &serverTimeMs)

	// Graceful shutdown: SIGINT/SIGTERM stops accepting traffic, then cancels
	// ctx and WAITS for the indexer/keepers to drain so no settle/refund
	// broadcast is cut mid-flight and the advisory lock releases cleanly.
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		s := <-sig
		log.Info().Str("signal", s.String()).Msg("shutting down")
		if err := app.ShutdownWithTimeout(10 * time.Second); err != nil {
			log.Error().Err(err).Msg("http shutdown")
		}
	}()

	log.Info().Str("addr", config.C.HTTPAddr).Msg("server starting")
	if err := app.Listen(config.C.HTTPAddr); err != nil {
		log.Fatal().Err(err).Msg("server failed")
	}

	cancel()
	select {
	case <-indexerDone:
		log.Info().Msg("indexer drained")
	case <-time.After(15 * time.Second):
		log.Warn().Msg("indexer drain timed out")
	}
}

// ── SIWE auth ────────────────────────────────────────────────────────────────

type nonceResp struct {
	Nonce string `json:"nonce"`
}

type verifyReq struct {
	Address   string `json:"address"`
	Message   string `json:"message"`
	Signature string `json:"signature"`
}

type tokenResp struct {
	Token   string `json:"token"`
	Address string `json:"address"`
}

func nonceHandler(ns nonce.Store, rl *ratelimit.Limiter) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ip := c.IP()
		if !rl.Allow("auth:"+ip, 20, time.Minute) {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "rate limit exceeded"})
		}
		address := strings.ToLower(strings.TrimSpace(c.Query("address")))
		if address == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "address required"})
		}
		if !isValidEthAddr(address) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid address format"})
		}
		// Cryptographically random 16-byte nonce. crypto/rand per RFC 4086.
		var rb [16]byte
		if _, err := rand.Read(rb[:]); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "rng failed"})
		}
		n := hex.EncodeToString(rb[:])
		if !ns.SetIfFree(address, n, 5*time.Minute) {
			// Live nonce exists — caller must consume it first. Rate-limit
			// prevents tight retry loops from a legitimate user.
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "nonce already issued, consume it or wait for expiry"})
		}
		return c.JSON(nonceResp{Nonce: n})
	}
}

func verifyHandler(ns nonce.Store, rl *ratelimit.Limiter) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ip := c.IP()
		if !rl.Allow("auth:"+ip, 20, time.Minute) {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "rate limit exceeded"})
		}
		var req verifyReq
		if err := json.Unmarshal(c.Body(), &req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "bad request"})
		}
		addr := strings.ToLower(req.Address)
		if !isValidEthAddr(addr) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid address format"})
		}
		n, found := ns.GetDel(addr)
		if !found || !strings.Contains(req.Message, n) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "nonce not found or expired"})
		}
		// Bind the signed message to our domain so a signature obtained for another
		// site cannot be replayed here (SIWE domain binding).
		// R-07 fix: use structured EIP-4361 parsing instead of substring match.
		// The previous `strings.Contains(req.Message, d)` was vulnerable to
		// cross-application replay: an attacker could trick a user into signing
		// a SIWE message for attacker.com with the target domain embedded in
		// the Statement or URI fields, and the substring check would pass.
		// The fix extracts the domain from the EIP-4361 `domain` line (the
		// first line of the message, before " wants you to sign in...") and
		// requires an exact match. Falls back to substring match if the message
		// doesn't follow EIP-4361 format (legacy compatibility).
		if d := config.C.SIWEDomain; d != "" {
			if !siweDomainMatches(req.Message, d) {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "domain mismatch"})
			}
		}
		// v29 audit F-01: bind the signature to the running chain. Without
		// this, a Coston2-signed payload replays as valid on mainnet because
		// the (message, signature, address) tuple is identical except for the
		// chainId line. The wallet.js SIWE template now includes
		// `Chain ID: N`; we require N == config.C.ChainID. Reject before
		// EIP-191 so a forged-claim signature can't even burn signature-verify
		// cycles.
		// R-09 fix: use structured EIP-4361 line parsing instead of substring
		// match. The previous `strings.Contains(req.Message, wantSubstr)` was
		// vulnerable to cross-chain replay: an attacker could trick a user
		// into signing a SIWE message for chain 1 with "Chain ID: 114"
		// embedded in the URI or Statement field, and the substring check
		// would pass. The fix extracts the Chain ID from the EIP-4361
		// `Chain ID: N` line and requires an exact integer match. Falls back
		// to substring match if the message doesn't follow EIP-4361 format
		// (legacy compatibility).
		if want := config.C.ChainID; want != 0 {
			if !siweChainIDMatches(req.Message, want) {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "chain id mismatch"})
			}
		}
		ok, err := verifyEIP191(req.Message, req.Signature, req.Address)
		if err != nil || !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "signature verification failed"})
		}
		token, err := auth.Issue(addr, config.C.JWTSecret, auth.DefaultAudience, config.C.NonceTTL*24)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "token issuance failed"})
		}
		// Set an HttpOnly, address-bound, SameSite=Strict session cookie so
		// the SPA can authenticate via cookie (XSS exfiltration safe). The
		// cookie name is `mw_s_<addr-prefix>` so a wallet switch forces
		// re-auth and a stolen cookie can't be replayed against a different
		// user's session.
		setSessionCookie(c, addr, token)
		return c.JSON(tokenResp{Token: token, Address: addr})
	}
}

// setSessionCookie writes the auth cookie with hardening defaults:
// HttpOnly (no JS access), Secure (always-on in production; in dev, only if
// the request itself was HTTPS — fiber's c.Protocol() is forwarded-scheme-
// aware), SameSite=Strict (no cross-site send), Path=/, Max-Age=24h
// (matches JWT TTL). The cookie name itself encodes the wallet so multiple
// wallets coexisting in one browser resolve cleanly.
//
// The previous implementation keyed Secure on `c.Protocol() != "http"`,
// which is wrong behind a misconfigured TLS-terminating proxy that forwards
// the wrong scheme. Force Secure=true whenever ENV=production so a downgrade
// to plaintext auth is impossible even if the proxy is buggy.
func setSessionCookie(c *fiber.Ctx, address, token string) {
	c.Cookie(&fiber.Cookie{
		Name:     auth.CookieName(address),
		Value:    token,
		Path:     "/",
		HTTPOnly: true,
		Secure:   config.C.Env == "production" || c.Protocol() == "https",
		// v22 audit: dropped from "Strict" to "Lax". Strict blocks the auth
		// cookie on cross-origin top-level GET navigations — the user-visible
		// symptom: anyone arriving from Twitter / Discord / Telegram is
		// silently signed-out on first page load and has to reconnect. Lax
		// still defends against CSRF on cross-origin state-changing POSTs
		// (browser does NOT send Lax cookies on cross-site POSTs); JWT gate
		// on every mutating endpoint is the real defence. SameSite=Lax is
		// the explicit web standard for session cookies.
		SameSite: "Lax",
		MaxAge:   int((24 * time.Hour).Seconds()),
	})
}

// isValidEthAddr validates a lowercase Ethereum address: 0x + 40 lowercase hex chars.
func isValidEthAddr(s string) bool {
	if len(s) != 42 || s[:2] != "0x" {
		return false
	}
	for i := 2; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// siweDomainMatches extracts the domain from an EIP-4361 SIWE message
// and checks for an exact match. The domain is the first line of the
// message, which must end with " wants you to sign in with your Ethereum
// account:". If the message doesn't follow EIP-4361 format, falls back
// to substring match for legacy compatibility.
func siweDomainMatches(msg, expected string) bool {
	// EIP-4361: first line is "<domain> wants you to sign in with your Ethereum account:"
	idx := strings.Index(msg, " wants you to sign in")
	if idx > 0 {
		domain := msg[:idx]
		return domain == expected
	}
	// Legacy fallback: substring match for non-EIP-4361 messages.
	return strings.Contains(msg, expected)
}

// siweChainIDMatches extracts the Chain ID from an EIP-4361 SIWE message
// and checks for an exact integer match. It parses the line starting with
// "Chain ID: ". If the field is not found or cannot be parsed, it falls
// back to a substring match for legacy compatibility.
//
// R-09 fix: the previous `strings.Contains` check was vulnerable to
// cross-chain replay — an attacker could embed the target chain ID text
// in the URI or Statement field while signing for a different chain.
// Structured line parsing eliminates this attack vector entirely.
func siweChainIDMatches(msg string, expected uint64) bool {
	lines := strings.Split(msg, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Chain ID: ") {
			idStr := strings.TrimSpace(strings.TrimPrefix(line, "Chain ID: "))
			if id, err := strconv.ParseUint(idStr, 10, 64); err == nil {
				return id == expected
			}
		}
	}
	// Legacy fallback: substring match for non-EIP-4361 messages.
	wantSubstr := fmt.Sprintf("Chain ID: %d", expected)
	return strings.Contains(msg, wantSubstr)
}

func verifyEIP191(message, sigHex, address string) (bool, error) {
	msg := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)
	hash := crypto.Keccak256Hash([]byte(msg))
	sigBytes, err := hexutil.Decode(sigHex)
	if err != nil || len(sigBytes) != 65 {
		return false, fmt.Errorf("invalid signature")
	}
	sig := make([]byte, 65)
	copy(sig, sigBytes)
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	pubKey, err := crypto.SigToPub(hash.Bytes(), sig)
	if err != nil {
		return false, err
	}
	recovered := crypto.PubkeyToAddress(*pubKey)
	return strings.EqualFold(recovered.Hex(), address), nil
}

// ── UI (HTMX pages + static) ──────────────────────────────────────────────────

func mountUI(app *fiber.App, q *db.Q, serverTimeMs *int64) {
	// Static files from embedded FS
	mountStatic(app)

	// HTMX pages — server-rendered HTML
	app.Get("/", uiHome(q))
	app.Get("/listings", uiListings(q))
	app.Get("/auctions", uiAuctions(q))
	app.Get("/auction/:id", uiAuctionDetail(q))
	app.Get("/offers", uiOffers(q))
	app.Get("/profile/:addr", uiProfile(q))
	// /profile (no addr) — rescue route. Resolves to /profile/<own addr>
	// when a valid SIWE session cookie is present, else /listings. See
	// uiProfileRedirect for the security rationale.
	app.Get("/profile", uiProfileRedirect)
	app.Get("/collection/:addr", uiCollection(q))
	app.Get("/token/:addr/:id", uiToken(q))
	app.Get("/search", uiSearch(q))
	app.Get("/metrics", uiMetrics(q))
	app.Get("/docs", uiDocsIndex())
	app.Get("/docs/:slug", uiDoc())

	// HTMX partials (return HTML fragments for hx-get). New per-page live
	// partials render the same data shape as their page handler so htmx
	// can swap them into `[data-live]` outerHTML every 1s OR on the SSE
	// event types each page subscribes to.
	app.Get("/partials/listings", partialListings(q))
	app.Get("/partials/auctions", partialAuctions(q))
	app.Get("/partials/activity", partialActivity(q))
	app.Get("/partials/token/:addr/:id", partialToken(q))
	app.Get("/partials/auction/:id", partialAuctionDetail(q))
	app.Get("/partials/offers", partialOffers(q))
	app.Get("/partials/profile/:addr", partialProfile(q))

	// Server-time (used by auction countdown)
	app.Get("/api/v1/server-time", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"unix_ms": atomic.LoadInt64(serverTimeMs)})
	})
}
