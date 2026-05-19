package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	auctionv1 "github.com/OfficialA1manac/MagicWebb/backend/gen/auction/v1"
	indexerv1 "github.com/OfficialA1manac/MagicWebb/backend/gen/indexer/v1"
	mktv1 "github.com/OfficialA1manac/MagicWebb/backend/gen/marketplace/v1"
	offersv1 "github.com/OfficialA1manac/MagicWebb/backend/gen/offers/v1"
	apiPkg "github.com/OfficialA1manac/MagicWebb/backend/internal/api"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/service"
)

func main() {
	config.Load()

	if config.C.Env != "production" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// DB migrations + pool.
	if err := db.Migrate(config.C.PostgresURL); err != nil {
		log.Fatal().Err(err).Msg("db migration failed")
	}
	pool, err := db.Connect(ctx, config.C.PostgresURL)
	if err != nil {
		log.Fatal().Err(err).Msg("db connect failed")
	}
	defer pool.Close()

	// Redis.
	rdb, err := cache.Connect(ctx, config.C.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("redis connect failed")
	}
	defer rdb.Close()

	// Ethereum client (for IndexerService chain queries).
	var ethCli *ethclient.Client
	if ethCli, err = ethclient.DialContext(ctx, config.C.RPCURL); err != nil {
		log.Warn().Err(err).Msg("eth client connect failed — IndexerService will run degraded")
	}

	// Services.
	q := db.New(pool)
	mktSvc := service.NewMarketplaceService(q, rdb,
		config.C.ScoreWViews, config.C.ScoreWBids, config.C.ScoreWVolume, config.C.ScoreDecay)
	auctionSvc := service.NewAuctionService(q, rdb)
	offersSvc := service.NewOffersService(q, rdb, &config.C)
	indexerSvc := service.NewIndexerService(q, ethCli, &config.C)

	// gRPC server with interceptor chain.
	grpcSrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			auth.RecoveryUnaryInterceptor,
			auth.UnaryInterceptor(config.C.JWTSecret),
		),
		grpc.ChainStreamInterceptor(
			auth.RecoveryStreamInterceptor,
			auth.StreamInterceptor(config.C.JWTSecret),
		),
	)
	mktv1.RegisterMarketplaceServiceServer(grpcSrv, mktSvc)
	auctionv1.RegisterAuctionServiceServer(grpcSrv, auctionSvc)
	offersv1.RegisterOffersServiceServer(grpcSrv, offersSvc)
	indexerv1.RegisterIndexerServiceServer(grpcSrv, indexerSvc)
	reflection.Register(grpcSrv) // enable grpcurl/Postman tooling

	// Start gRPC listener.
	lis, err := net.Listen("tcp", config.C.GRPCAddr)
	if err != nil {
		log.Fatal().Err(err).Str("addr", config.C.GRPCAddr).Msg("grpc listen failed")
	}
	go func() {
		log.Info().Str("addr", config.C.GRPCAddr).Msg("gRPC server listening")
		if err := grpcSrv.Serve(lis); err != nil {
			log.Error().Err(err).Msg("gRPC serve error")
		}
	}()

	// HTTP mux: health, readiness, SIWE auth, REST API, SSE.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "db unhealthy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// Auth endpoints with rate limiting (20 req/min per IP).
	authRL := newAuthRateLimiter()
	mux.HandleFunc("/auth/nonce", authRL.wrap(handleNonce(rdb)))
	mux.HandleFunc("/auth/verify", authRL.wrap(handleVerify(rdb)))

	// REST API + SSE (handles /api/v1/* and /events).
	apiRouter := apiPkg.NewRouter(q, rdb, &config.C)
	mux.Handle("/api/", apiRouter)
	mux.Handle("/events", apiRouter)

	httpSrv := &http.Server{
		Addr:         config.C.HTTPAddr,
		Handler:      corsMiddlewareWithURL(config.C.FrontendURL, mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	go func() {
		log.Info().Str("addr", config.C.HTTPAddr).Msg("HTTP server listening")
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("HTTP serve error")
		}
	}()

	// Graceful shutdown on context cancel (triggered by OS signal upstream).
	<-ctx.Done()
	log.Info().Msg("api: shutting down")
	grpcSrv.GracefulStop()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	_ = httpSrv.Shutdown(shutCtx)
	log.Info().Msg("api: shutdown complete")
}

// ── SIWE auth handlers ────────────────────────────────────────────────────────

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

func handleNonce(rdb *cache.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		address := strings.ToLower(r.URL.Query().Get("address"))
		if address == "" {
			http.Error(w, "address required", http.StatusBadRequest)
			return
		}
		nonce := fmt.Sprintf("%x", crypto.Keccak256([]byte(address+fmt.Sprint(time.Now().UnixNano())))[:8])
		if err := rdb.SetNonce(r.Context(), address, nonce, config.C.NonceTTL); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, nonceResp{Nonce: nonce})
	}
}

func handleVerify(rdb *cache.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req verifyReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		addr := strings.ToLower(req.Address)

		// Verify nonce exists in Redis.
		nonce, found, err := rdb.GetNonce(r.Context(), addr)
		if err != nil || !found {
			http.Error(w, "nonce not found or expired", http.StatusUnauthorized)
			return
		}
		// Nonce must appear in the SIWE message.
		if !strings.Contains(req.Message, nonce) {
			http.Error(w, "nonce mismatch", http.StatusUnauthorized)
			return
		}

		// EIP-191 signature verification.
		ok, err := verifyEIP191(req.Message, req.Signature, req.Address)
		if err != nil || !ok {
			http.Error(w, "signature verification failed", http.StatusUnauthorized)
			return
		}

		// Issue JWT (24h TTL).
		token, err := auth.Issue(req.Address, config.C.JWTSecret, 24*time.Hour)
		if err != nil {
			http.Error(w, "token issue failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, tokenResp{Token: token, Address: req.Address})
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

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// corsMiddlewareWithURL is an explicit-allowlist CORS middleware.
// It allows the configured frontendURL plus localhost variants for dev.
func corsMiddlewareWithURL(frontendURL string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == frontendURL ||
			strings.HasPrefix(origin, "http://localhost:") ||
			strings.HasPrefix(origin, "http://127.0.0.1:") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── Auth rate limiter (20 req/min per IP) ─────────────────────────────────────

const (
	authRateLimit  = 20
	authRateWindow = time.Minute
)

type authRateLimiterState struct {
	mu      sync.Mutex
	buckets map[string]*authBucket
}

type authBucket struct {
	count   int
	resetAt time.Time
}

func newAuthRateLimiter() *authRateLimiterState {
	return &authRateLimiterState{buckets: make(map[string]*authBucket)}
}

func (rl *authRateLimiterState) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.buckets[ip]
	if !ok || now.After(b.resetAt) {
		rl.buckets[ip] = &authBucket{count: 1, resetAt: now.Add(authRateWindow)}
		return true
	}
	if b.count >= authRateLimit {
		return false
	}
	b.count++
	return true
}

func (rl *authRateLimiterState) wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIPFromRequest(r)
		if !rl.allow(ip) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

func clientIPFromRequest(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i != -1 {
		return addr[:i]
	}
	return addr
}
