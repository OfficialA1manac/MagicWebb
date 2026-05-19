// Package config loads and validates all environment variables at startup.
// Fast-fail: missing required vars cause immediate os.Exit(1).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// C is the global config loaded once at startup via Load().
var C Config

type Config struct {
	// Runtime
	Env string // "development" | "production"

	// Network
	RPCURL  string
	ChainID uint64

	// Contract addresses
	MarketplaceAddr string
	AuctionAddr     string
	OfferBookAddr   string
	RoyaltyAddr     string

	// Database
	PostgresURL string

	// Redis
	RedisURL string

	// Servers
	GRPCAddr string
	HTTPAddr string

	// Auth
	SIWEDomain string
	JWTSecret  string
	NonceTTL   time.Duration // Redis TTL for SIWE nonces

	// Indexer
	IndexFromBlock  uint64 // start block (override for reindex)
	GetLogsChunk    uint64 // getLogs chunk size (Flare public RPC: 30)
	GetLogsBlockCap uint64 // 0 = unlimited (private RPC)

	// Score weights (trending formula)
	ScoreWViews  float64
	ScoreWBids   float64
	ScoreWVolume float64
	ScoreDecay   float64

	// Observability
	SentryDSN        string
	OtelEndpoint     string

	// Pinata (IPFS uploads)
	PinataJWT string

	// Keeper bot (optional): hex-encoded ECDSA private key for on-chain auction settlement
	KeeperKey string

	// Admin token for IndexerService.Reindex (leave empty to disable)
	ServiceToken string

	// FrontendURL is the allowed CORS origin (e.g. https://webbplace.xyz).
	FrontendURL string
}

// Load reads environment variables and panics on missing required values.
func Load() {
	C = Config{
		Env:     envOrDefault("ENV", "development"),
		RPCURL:  required("RPC_URL"),
		ChainID: requiredUint64("CHAIN_ID"),

		MarketplaceAddr: required("MARKETPLACE_ADDR"),
		AuctionAddr:     required("AUCTION_ADDR"),
		OfferBookAddr:   required("OFFERBOOK_ADDR"),
		RoyaltyAddr:     envOrDefault("ROYALTY_ADDR", ""),

		PostgresURL: required("POSTGRES_URL"),
		RedisURL:    required("REDIS_URL"),

		GRPCAddr: envOrDefault("GRPC_ADDR", ":9090"),
		HTTPAddr: envOrDefault("HTTP_ADDR", ":8080"),

		SIWEDomain: envOrDefault("SIWE_DOMAIN", "localhost"),
		JWTSecret:  required("JWT_SECRET"),
		NonceTTL:   5 * time.Minute,

		IndexFromBlock:  optUint64("INDEX_FROM_BLOCK", 0),
		GetLogsChunk:    optUint64("GETLOGS_CHUNK", 30),
		GetLogsBlockCap: optUint64("GETLOGS_BLOCK_CAP", 30),

		ScoreWViews:  optFloat64("SCORE_W_VIEWS", 0.3),
		ScoreWBids:   optFloat64("SCORE_W_BIDS", 0.5),
		ScoreWVolume: optFloat64("SCORE_W_VOLUME", 0.2),
		ScoreDecay:   optFloat64("SCORE_DECAY", 0.05),

		SentryDSN:    envOrDefault("SENTRY_DSN", ""),
		OtelEndpoint: envOrDefault("OTEL_EXPORTER_OTLP_ENDPOINT", ""),

		PinataJWT: envOrDefault("PINATA_JWT", ""),

		KeeperKey:    envOrDefault("KEEPER_KEY", ""),
		ServiceToken: envOrDefault("SERVICE_TOKEN", ""),

		FrontendURL: envOrDefault("FRONTEND_URL", "http://localhost:3000"),
	}

	C.MarketplaceAddr = strings.ToLower(C.MarketplaceAddr)
	C.AuctionAddr     = strings.ToLower(C.AuctionAddr)
	C.OfferBookAddr   = strings.ToLower(C.OfferBookAddr)
	C.RoyaltyAddr     = strings.ToLower(C.RoyaltyAddr)
}

func required(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "FATAL: required env var %q is not set\n", key)
		os.Exit(1)
	}
	return v
}

func requiredUint64(key string) uint64 {
	v := required(key)
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: env var %q must be a uint64, got %q\n", key, v)
		os.Exit(1)
	}
	return n
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func optUint64(key string, def uint64) uint64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

func optFloat64(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}
