// MagicWebb — single binary: Fiber HTTP server + blockchain indexer goroutine.
// Run: go run ./cmd/server   (no Docker, no Redis, no separate processes)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
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

	// SSE broadcaster (replaces Redis pub/sub)
	bcast := sse.New()

	// Rate limiter + nonce store (in-memory, replaces Redis)
	rl := ratelimit.New()
	ns := nonce.New()

	// Ethereum client
	eth, err := ethclient.DialContext(ctx, config.C.RPCURL)
	if err != nil {
		log.Fatal().Err(err).Msg("eth client connect failed")
	}
	defer eth.Close()

	// serverTimeMs is updated atomically by the indexer watcher
	var serverTimeMs int64

	// Start indexer in background
	runner := indexer.New(&config.C, q, bcast, eth, &serverTimeMs)
	go func() {
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
	api.Mount(app, q, bcast, rl, &config.C)

	// Auth endpoints with tighter rate limit (20 req/min per IP)
	authRL := ratelimit.New()
	app.Get("/auth/nonce", nonceHandler(ns, authRL))
	app.Post("/auth/verify", verifyHandler(ns, authRL))

	// Serve HTMX templates + static files
	mountUI(app, q, &serverTimeMs)

	log.Info().Str("addr", config.C.HTTPAddr).Msg("server starting")
	if err := app.Listen(config.C.HTTPAddr); err != nil {
		log.Fatal().Err(err).Msg("server failed")
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

func nonceHandler(ns *nonce.Store, rl *ratelimit.Limiter) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ip := c.IP()
		if !rl.Allow("auth:"+ip, 20, time.Minute) {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "rate limit exceeded"})
		}
		address := strings.ToLower(c.Query("address"))
		if address == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "address required"})
		}
		n := fmt.Sprintf("%x", crypto.Keccak256([]byte(address+fmt.Sprint(time.Now().UnixNano())))[:8])
		ns.Set(address, n, 5*time.Minute)
		return c.JSON(nonceResp{Nonce: n})
	}
}

func verifyHandler(ns *nonce.Store, rl *ratelimit.Limiter) fiber.Handler {
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
		ok, err := verifyEIP191(req.Message, req.Signature, req.Address)
		if err != nil || !ok {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "signature verification failed"})
		}
		token, err := auth.Issue(addr, config.C.JWTSecret, config.C.NonceTTL*24)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "token issuance failed"})
		}
		return c.JSON(tokenResp{Token: token, Address: req.Address})
	}
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

	// HTMX partials (return HTML fragments for hx-get)
	app.Get("/partials/listings", partialListings(q))
	app.Get("/partials/auctions", partialAuctions(q))
	app.Get("/partials/activity", partialActivity(q))

	// Server-time (used by auction countdown)
	app.Get("/api/v1/server-time", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"unix_ms": atomic.LoadInt64(serverTimeMs)})
	})
}
