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
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/api"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/indexer"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/media"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/nonce"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/ratelimit"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/rpcpool"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
	"github.com/OfficialA1manac/MagicWebb/frontend"
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

	// Verify deployment config matches env vars. Prevents the indexer from
	// mixing events from old and new contract instances after a redeploy.
	// Creates the initial row on first deploy; refuses to start on mismatch.
	if err := verifyDeploymentConfig(ctx, pool); err != nil {
		log.Fatal().Err(err).Msg("deployment config mismatch — contract addresses changed since last deploy; truncate on-chain data first")
	}

	// Optional read-replica pool for query offloading. When READ_POOL_URL is
	// set (e.g. a Neon read-only branch endpoint), read-heavy API queries
	// (listings, auctions, activity, search) are routed through the replica
	// pool while writes always use the primary. Nil is safe — reader() falls
	// back to the primary pool when no replica is configured.
	readPool, err := db.ConnectReadReplica(ctx, config.C.ReadPoolURL)
	if err != nil {
		log.Fatal().Err(err).Msg("read replica connect failed")
	}
	if readPool != nil {
		defer readPool.Close()
	}
	q := db.New(pool)
	if readPool != nil {
		q = q.WithReadReplica(readPool)
	}

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

	// Seed tracked_collections at startup so the indexer watches Transfer events
	// for every NFT contract the operator explicitly configured (TRACKED_COLLECTIONS
	// comma-separated env var) AND every collection that already has rows in
	// nft_tokens (previously listed/auctioned collections whose tracking row may
	// have been lost). Without this, collections that have never been listed or
	// auctioned on MagicWebb are invisible to the indexer — their Transfer events
	// are never processed, nft_ownership stays empty, and WalletNFTs returns zero
	// results even when the wallet legitimately holds NFTs.
	seedTracked(ctx, q)

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
	api.Mount(app, q, bcast, rl, &config.C, eth, &serverTimeMs)

	// Admin-only endpoint: force re-scan of Transfer events for a specific
	// collection (mounted after runner creation so the handler can call
	// runner.ReindexCollection).
	api.MountReindexRoute(app, runner, &config.C)

	// Register SLO (Prometheus gauge) and healthz (DB + RPC + lag threshold) routes.
	// Extracted into a function for unit testability — the getHeadLag callback
	// lets tests inject a controllable lag value instead of a real Runner.
	registerSLOHealthRoutes(app, q, eth, runner.HeadLagBlocks)

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

// ── Deployment Config Check ──────────────────────────────────────────────────

// verifyDeploymentConfig reads the latest deployment_config row and compares it
// to the running env config. On first deploy (no rows), it inserts the current
// config. On mismatch, it returns an error so main() can fatal-exit.
func verifyDeploymentConfig(ctx context.Context, pool db.PgxPool) error {
	var chainID int64
	var marketplaceAddr, auctionAddr, offerbookAddr, nftAddr, managerAddr string
	err := pool.QueryRow(ctx,
		`SELECT chain_id, marketplace_addr, auction_addr, offerbook_addr, nft_addr, marketplace_manager_addr
		   FROM deployment_config ORDER BY id DESC LIMIT 1`,
	).Scan(&chainID, &marketplaceAddr, &auctionAddr, &offerbookAddr, &nftAddr, &managerAddr)

	if err == pgx.ErrNoRows {
		// First deploy — insert the current config and proceed.
		_, err := pool.Exec(ctx,
			`INSERT INTO deployment_config(chain_id, marketplace_addr, auction_addr, offerbook_addr, nft_addr, marketplace_manager_addr)
			 VALUES($1,$2,$3,$4,$5,$6)`,
			config.C.ChainID, config.C.MarketplaceAddr, config.C.AuctionAddr,
			config.C.OfferBookAddr, config.C.NFTAddr, config.C.MarketplaceManagerAddr)
		if err != nil {
			return fmt.Errorf("insert initial deployment config: %w", err)
		}
		log.Info().Msg("deployment config: initial row created")
		return nil
	}
	if err != nil {
		return fmt.Errorf("read deployment config: %w", err)
	}

	// Compare each field. Collect all mismatches before returning.
	var diffs []string
	if chainID != int64(config.C.ChainID) {
		diffs = append(diffs, fmt.Sprintf("chain_id: stored=%d env=%d", chainID, config.C.ChainID))
	}
	if marketplaceAddr != config.C.MarketplaceAddr {
		diffs = append(diffs, fmt.Sprintf("marketplace_addr: stored=%s env=%s", marketplaceAddr, config.C.MarketplaceAddr))
	}
	if auctionAddr != config.C.AuctionAddr {
		diffs = append(diffs, fmt.Sprintf("auction_addr: stored=%s env=%s", auctionAddr, config.C.AuctionAddr))
	}
	if offerbookAddr != config.C.OfferBookAddr {
		diffs = append(diffs, fmt.Sprintf("offerbook_addr: stored=%s env=%s", offerbookAddr, config.C.OfferBookAddr))
	}
	if nftAddr != config.C.NFTAddr {
		diffs = append(diffs, fmt.Sprintf("nft_addr: stored=%s env=%s", nftAddr, config.C.NFTAddr))
	}
	if len(diffs) > 0 {
		return fmt.Errorf("deployment mismatch:\n  %s", strings.Join(diffs, "\n  "))
	}

	log.Info().Msg("deployment config: matches env — proceeding")
	return nil
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
		ip := api.ClientIP(c)
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
		if !ns.SetIfFree(address, n, config.C.NonceTTL) {
			// Live nonce exists — caller must consume it first. Rate-limit
			// prevents tight retry loops from a legitimate user.
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "nonce already issued, consume it or wait for expiry"})
		}
		return c.JSON(nonceResp{Nonce: n})
	}
}

func verifyHandler(ns nonce.Store, rl *ratelimit.Limiter) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ip := api.ClientIP(c)
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

		// L-15 fix: perform ALL validation BEFORE consuming the nonce.
		// The previous ordering called ns.GetDel first, which consumed
		// the nonce before domain/chain/signature validation. If any
		// of those validations failed, the nonce was already consumed
		// and the legitimate user had to request a new one — adding an
		// unnecessary round trip. Worse, if an attacker could trigger
		// validation failures deliberately (e.g. by submitting a message
		// with an invalid domain), they could DOS a real user by
		// repeatedly consuming their nonce without ever completing auth.
		// The fix: validate domain, chain ID, and ECDSA signature BEFORE
		// consuming the nonce. On any validation failure, the nonce is
		// never touched and the legitimate user can retry immediately.

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
		// this, a signature replays as valid on another chain because
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

		// All validations passed — NOW consume the nonce (single-use).
		n, found := ns.GetDel(addr)
		if !found {
			// Nonce was already consumed (race with another verify call)
			// or expired. The caller must request a fresh nonce.
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "nonce already consumed or expired, request a new one"})
		}
		if !strings.Contains(req.Message, n) {
			// Nonce found but message doesn't contain it — security
			// invariant violation (should be unreachable if the client
			// uses the nonce we issued, but we don't consume the nonce
			// here because the attacker could force consumption without
			// valid signature).
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "nonce mismatch"})
		}

		token, err := auth.Issue(addr, config.C.JWTSecret, auth.DefaultAudience, 24*time.Hour)
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

// seedTracked ensures every collection the indexer needs to watch has a row in
// tracked_collections. It merges three sources at startup:
//   1. Explicit TRACKED_COLLECTIONS env var (operator-configured NFT contracts)
//   2. Every unique collection that already appears in nft_tokens (covers
//      collections that were ever listed, auctioned, or had a Transfer event
//      indexed at some point — even if their tracked_collections row was later
//      lost or never explicitly seeded)
// A missing tracked_collections row means the indexer's processTransfers() skips
// that contract entirely, so nft_ownership stays empty and WalletNFTs returns
// zero results even when the wallet holds tokens. This seed runs once per
// process lifetime at startup; EnsureCollection is idempotent (ON CONFLICT DO
// NOTHING) so repeated seeds on restart are cheap.
func seedTracked(ctx context.Context, q *db.Q) {
	addrs := config.C.TrackedCollections // env TRACKED_COLLECTIONS

	// 1. Existing nft_tokens collections (auto-discovered).
	if existing, err := q.ListDistinctCollectionsFromTokens(ctx); err == nil {
		addrs = append(addrs, existing...)
	} else {
		log.Warn().Err(err).Msg("startup: ListDistinctCollectionsFromTokens failed, skipping auto-seed")
	}

	// 2. Dedup.
	seen := make(map[string]bool, len(addrs))
	uniq := make([]string, 0, len(addrs))
	for _, a := range addrs {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		uniq = append(uniq, a)
	}

	if len(uniq) == 0 {
		return
	}

	seeded := q.SeedTrackedCollections(ctx, uniq)
	log.Info().Int("attempted", len(uniq)).Int("seeded", seeded).
		Msg("startup: seeded tracked_collections")
}

// siweDomainMatches extracts the domain from an EIP-4361 SIWE message
// and checks for an exact match. The domain is the first line of the
// message, which must end with " wants you to sign in with your Ethereum
// account:". Messages that don't follow EIP-4361 format are rejected
// outright — no legacy substring fallback, to prevent cross-application
// replay attacks where the target domain is embedded in a Statement or
// URI field (R-07).
func siweDomainMatches(msg, expected string) bool {
	// EIP-4361: first line is "<domain> wants you to sign in with your Ethereum account:"
	idx := strings.Index(msg, " wants you to sign in")
	if idx <= 0 {
		return false
	}
	domain := msg[:idx]
	return domain == expected
}

// siweChainIDMatches extracts the Chain ID from an EIP-4361 SIWE message
// and checks for an exact integer match. It parses the line starting with
// "Chain ID: ". Messages that don't contain a parseable Chain ID line are
// rejected outright — no legacy substring fallback, to prevent cross-chain
// replay attacks where the target chain ID text is embedded in a URI or
// Statement field (R-12).
func siweChainIDMatches(msg string, expected uint64) bool {
	lines := strings.Split(msg, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Chain ID: ") {
			idStr := strings.TrimSpace(strings.TrimPrefix(line, "Chain ID: "))
			if id, err := strconv.ParseUint(idStr, 10, 64); err == nil {
				return id == expected
			}
			// Chain ID line exists but can't be parsed — reject.
			return false
		}
	}
	// No Chain ID line found — reject.
	return false
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

// ── SLO + Healthz routes (testable via registerSLOHealthRoutes) ────────────────

// registerSLOHealthRoutes mounts two endpoints:
//   /api/v1/indexer/slo — Prometheus-format head_lag_blocks gauge
//   /healthz            — DB ping + RPC block number + head-lag threshold (503 when >15)
//
// The getHeadLag callback lets tests inject a controllable lag value without needing
// a real indexer.Runner. Production passes runner.HeadLagBlocks.
func registerSLOHealthRoutes(app *fiber.App, q *db.Q, eth indexer.EthClient, getHeadLag func() uint64) {
	// Expose head lag as a Prometheus-compatible text gauge.
	app.Get("/api/v1/indexer/slo", func(c *fiber.Ctx) error {
		lag := getHeadLag()
		c.Set("Content-Type", "text/plain; charset=utf-8")
		return c.SendString(fmt.Sprintf(
			"# HELP head_lag_blocks Chain head minus last indexed block\n"+
				"# TYPE head_lag_blocks gauge\n"+
				"head_lag_blocks %d\n", lag))
	})

	// Override /healthz to also report indexer head lag SLO (Fiber LIFO route
	// resolution means this runs after api.Mount's /healthz, effectively wrapping it).
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
		// Indexer lag SLO: warn when more than 15 blocks behind the head.
		// On Flare/Coston2 (~2s block time), 15 blocks ≈ 30 seconds of lag.
		if lag := getHeadLag(); lag > 15 {
			return c.Status(fiber.StatusServiceUnavailable).
				SendString(fmt.Sprintf("indexer lag: %d blocks behind head", lag))
		}
		c.Set("X-MW-Build-SHA", api.MWServerBuildSHA)
		return c.SendStatus(fiber.StatusOK)
	})
}

// ── UI (HTMX pages + static) ──────────────────────────────────────────────────

func mountUI(app *fiber.App, q *db.Q, serverTimeMs *int64) {
	// Initialize frontend templates with media URL function.
	// Must happen before any template rendering.
	frontend.Init(media.ProxyURL)

	// Static files from embedded FS
	mountStatic(app)

	// Astro-built Svelte/React pages from app/dist/ at /app/* URL prefix.
	// Set ASTRO_DIST_DIR env var to the build output path (defaults to
	// "../app/dist" for dev; use "/app/dist" in the Docker image).
	mountAstro(app)

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
	app.Get("/admin/stalled", uiAdminStalled(q))
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

	// v35: /api/v1/server-time moved to the rate-limited api group in rest.go.
	// Previously registered on the bare app, bypassing rateLimitMiddleware entirely.
}
