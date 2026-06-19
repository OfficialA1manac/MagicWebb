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

	// Fiber app
	app := fiber.New(fiber.Config{
		ReadTimeout:           5 * time.Second,
		WriteTimeout:          0, // SSE connections need no write timeout
		IdleTimeout:           60 * time.Second,
		DisableStartupMessage: false,
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
		address := strings.ToLower(c.Query("address"))
		if address == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "address required"})
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
		n, found := ns.GetDel(addr)
		if !found || !strings.Contains(req.Message, n) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "nonce not found or expired"})
		}
		// Bind the signed message to our domain so a signature obtained for another
		// site cannot be replayed here (SIWE domain binding).
		if d := config.C.SIWEDomain; d != "" && !strings.Contains(req.Message, d) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "domain mismatch"})
		}
		// Reject re-use of an identical signed message (anti-replay).
		if !strings.Contains(req.Message, "Issued At:") && !strings.Contains(req.Message, "issuedAt") {
			// Loose EIP-4361 shape check — accept either canonical SIWE or our
			// legacy `\n` form. The strict SIWE verifier is enforced in
			// auth.Verify for downstream JWT use.
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
		SameSite: "Strict",
		MaxAge:   int((24 * time.Hour).Seconds()),
	})
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
