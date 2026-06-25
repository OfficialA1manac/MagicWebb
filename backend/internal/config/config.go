// Package config loads and validates all environment variables at startup.
// Fast-fail: missing required vars cause immediate os.Exit(1).
package config

import (
	"fmt"
	"math/big"
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
	RPCURL         string   // primary RPC (back-compat / single endpoint)
	RPCURLs        []string // rotation set (RPC_URLS, comma-separated; falls back to [RPCURL])
	ChainID        uint64
	NetworkName    string // EIP-155 chain name (e.g. "Flare Coston2"); surfaced to UI + WC metadata
	NativeCurrency string // EIP-155 native-currency symbol (e.g. "C2FLR"); rendered in user-facing labels
	ExplorerURL    string // block-explorer base URL (e.g. https://coston2-explorer.flare.network)

	// Contract addresses
	MarketplaceAddr string
	AuctionAddr     string
	OfferBookAddr   string
	RoyaltyAddr     string

	// Database
	PostgresURL string

	// Servers
	HTTPAddr string

	// Auth
	SIWEDomain string
	JWTSecret  string
	NonceTTL   time.Duration // TTL for SIWE nonces (in-memory store)

	// Indexer
	IndexFromBlock  uint64 // start block (override for reindex)
	GetLogsChunk    uint64 // getLogs chunk size (Flare public RPC: 30)
	GetLogsBlockCap uint64 // 0 = unlimited (private RPC)

	// Score weights (trending formula)
	ScoreWViews  float64
	ScoreWBids   float64
	ScoreWVolume float64
	ScoreDecay   float64

	// Pinata (IPFS uploads)
	PinataJWT string

	// Keeper bot (optional): hex-encoded ECDSA private key for on-chain auction settlement
	KeeperKey string

	// v29 audit F-03: gas-fee ceilings for the keeper. Public RPCs can spike
	// their suggestions during network congestion; without a cap, a single
	// keeper tx could drain the keeper wallet. 0 = no cap (NOT recommended).
	// Defaults 100/5 gwei leave plenty of headroom on Coston2 and even mainnet.
	MaxFeeCapGwei float64
	MaxTipCapGwei float64

	// Admin token for IndexerService.Reindex (leave empty to disable)
	ServiceToken string

	// FrontendURL is the allowed CORS origin (e.g. https://magicwebb.xyz).
	FrontendURL string

	// WCProjectID enables WalletConnect v2 in the UI (cloud.walletconnect.com).
	// Empty = injected-wallet (MetaMask) only.
	WCProjectID string

	// AdminAllowlist is the set of lowercased addresses permitted to call admin
	// endpoints (e.g. profile verification). Off-chain admin = env allowlist + SIWE JWT.
	AdminAllowlist []string
}

// Load reads environment variables and panics on missing required values.
func Load() {
	C = Config{
		Env:     envOrDefault("ENV", "development"),
		RPCURL:  required("RPC_URL"),
		ChainID: requiredUint64("CHAIN_ID"),

		// Chain-metadata block. Required by the UI for WalletConnect
		// pairing (chains:[1]+optionalChains:[CHAIN_ID]+rpcMap:{CHAIN_ID:RPC_URL}),
		// user-facing labels (toast summaries, ctaLabels, summary rows —
		// wallet.js reads window.MW_NATIVE_CURRENCY/NETWORK_NAME), and
		// explorer <a href="{{$.ExplorerURL}}/tx/..."> links. Defaults are
		// Coston2-specific; a future mainnet promo just sets these in .env
		// (NETWORK_NAME="Flare", NATIVE_CURRENCY="FLR", EXPLORER_URL=
		// "https://flare-explorer.flare.network") and the entire frontend
		// pivots without a redeploy. envOrDefault returns the .env-supplied
		// value if non-empty, else the compile-time default — the deploy
		// defaults are the FAILSAFE for misconfiguration.
		NetworkName:    envOrDefault("NETWORK_NAME", "Flare Coston2"),
		NativeCurrency: envOrDefault("NATIVE_CURRENCY", "C2FLR"),
		ExplorerURL:    envOrDefault("EXPLORER_URL", "https://coston2-explorer.flare.network"),

		MarketplaceAddr: required("MARKETPLACE_ADDR"),
		AuctionAddr:     required("AUCTION_ADDR"),
		OfferBookAddr:   required("OFFERBOOK_ADDR"),
		RoyaltyAddr:     envOrDefault("ROYALTY_ADDR", ""),

		PostgresURL: required("POSTGRES_URL"),

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

		PinataJWT: envOrDefault("PINATA_JWT", ""),

		KeeperKey: envOrDefault("KEEPER_KEY", ""),

		// v29: ceiling on keeper gas pricing. 0 = unbounded (NOT recommended).
		MaxFeeCapGwei: optFloat64("KEEPER_MAX_FEE_CAP_GWEI", 100),
		MaxTipCapGwei: optFloat64("KEEPER_MAX_TIP_CAP_GWEI", 5),

		ServiceToken: envOrDefault("SERVICE_TOKEN", ""),

		FrontendURL: envOrDefault("FRONTEND_URL", "http://localhost:3000"),
		WCProjectID: envOrDefault("WC_PROJECT_ID", ""),

		AdminAllowlist: parseAddrList(envOrDefault("ADMIN_ALLOWLIST", "")),
	}

	C.MarketplaceAddr = strings.ToLower(C.MarketplaceAddr)
	C.AuctionAddr = strings.ToLower(C.AuctionAddr)
	C.OfferBookAddr = strings.ToLower(C.OfferBookAddr)
	C.RoyaltyAddr = strings.ToLower(C.RoyaltyAddr)

	// RPC rotation set: RPC_URLS (comma-separated) plus the required RPC_URL,
	// deduped with the primary first — setting RPC_URLS can only ADD endpoints,
	// never silently drop the primary from rotation.
	C.RPCURLs = []string{C.RPCURL}
	for _, u := range parseURLList(os.Getenv("RPC_URLS")) {
		if u != C.RPCURL {
			C.RPCURLs = append(C.RPCURLs, u)
		}
	}

	if len(C.JWTSecret) < 32 {
		fmt.Fprintln(os.Stderr, "FATAL: JWT_SECRET must be at least 32 characters")
		os.Exit(1)
	}
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

// parseURLList splits a comma-separated URL list, trimming whitespace and
// dropping empties. Case is preserved (URL paths/tokens are case-sensitive).
func parseURLList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseAddrList splits a comma-separated address list and lowercases each entry.
func parseAddrList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// IsAdmin reports whether addr is in the admin allowlist (case-insensitive).
func (c *Config) IsAdmin(addr string) bool {
	addr = strings.ToLower(strings.TrimSpace(addr))
	for _, a := range c.AdminAllowlist {
		if a == addr {
			return true
		}
	}
	return false
}

// MaxFeeCapWei returns the keeper's fee-cap ceiling in wei, or nil when the
// ceiling is disabled (0). v29 audit F-03 — bounded by KEEPER_MAX_FEE_CAP_GWEI.
func (c *Config) MaxFeeCapWei() *big.Int {
	if c.MaxFeeCapGwei <= 0 {
		return nil
	}
	// gwei → wei: 1 gwei = 1e9 wei. Use a fixed-point conversion through
	// float64; the resulting wei value is far below any float precision
	// concern at the 100-gwei magnitude this constant uses.
	return new(big.Int).SetUint64(uint64(c.MaxFeeCapGwei * 1e9))
}

// MaxTipCapWei returns the keeper's tip-cap ceiling in wei, or nil when
// disabled. v29 audit F-03 — bounded by KEEPER_MAX_TIP_CAP_GWEI.
func (c *Config) MaxTipCapWei() *big.Int {
	if c.MaxTipCapGwei <= 0 {
		return nil
	}
	return new(big.Int).SetUint64(uint64(c.MaxTipCapGwei * 1e9))
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
