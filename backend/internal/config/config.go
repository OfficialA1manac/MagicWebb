// Package config loads and validates all environment variables at startup.
// Fast-fail: missing required vars cause immediate os.Exit(1).
package config

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
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
	NonceTTL   time.Duration // TTL for SIWE nonces (in-memory store); env NONCE_TTL

	// Indexer
	IndexFromBlock  uint64 // start block (override for reindex)
	GetLogsChunk    uint64 // getLogs chunk size (Flare public RPC: 30)
	GetLogsBlockCap uint64 // 0 = unlimited (private RPC)

	// Score weights (trending formula)
	ScoreWViews  float64
	ScoreWBids   float64
	ScoreWVolume float64
	ScoreDecay   float64


	// Metadata worker
	MetadataConcurrency int // concurrent metadata fetches per tick; env METADATA_CONCURRENCY

	// Keeper bot (optional): hex-encoded ECDSA private key for on-chain auction settlement
	KeeperKey string

	// v29 audit F-03: gas-fee ceilings for the keeper. Public RPCs can spike
	// their suggestions during network congestion; without a cap, a single
	// keeper tx could drain the keeper wallet. 0 = no cap (NOT recommended).
	// Defaults 100/5 gwei leave plenty of headroom on Coston2.
	MaxFeeCapGwei float64
	MaxTipCapGwei float64

	// Phase 4 V4.1: minimum keeper wallet balance (in wei). The keeper emits a
	// warning at startup when its balance is below this threshold. Default 0.1
	// FLR (0.1 × 1e18 wei) ensures ~20-50 settlements on Coston2 at 5 gwei.
	// Set to 0 to disable the balance check.
	KeeperMinBalanceWei string

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
		// Coston2-specific. envOrDefault returns the .env-supplied value if
		// non-empty, else the compile-time default — the deploy defaults are
		// the FAILSAFE for misconfiguration. This build targets Coston2 (chain
		// 114) exclusively; NETWORK_NAME, NATIVE_CURRENCY, and EXPLORER_URL
		// allow operators to customise display labels WITHOUT changing the
		// underlying chain or the chain-ID validation below.
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
		NonceTTL:   optDuration("NONCE_TTL", 5*time.Minute),

		IndexFromBlock:       optUint64("INDEX_FROM_BLOCK", 0),
		GetLogsChunk:         optUint64("GETLOGS_CHUNK", 30),
		GetLogsBlockCap:      optUint64("GETLOGS_BLOCK_CAP", 30),
		MetadataConcurrency:  optInt("METADATA_CONCURRENCY", 3),

		ScoreWViews:  optFloat64("SCORE_W_VIEWS", 0.3),
		ScoreWBids:   optFloat64("SCORE_W_BIDS", 0.5),
		ScoreWVolume: optFloat64("SCORE_W_VOLUME", 0.2),
		ScoreDecay:   optFloat64("SCORE_DECAY", 0.05),


		KeeperKey: envOrDefault("KEEPER_KEY", ""),

		// v29: ceiling on keeper gas pricing. 0 = unbounded (NOT recommended).
		MaxFeeCapGwei: optFloat64("KEEPER_MAX_FEE_CAP_GWEI", 100),
		MaxTipCapGwei: optFloat64("KEEPER_MAX_TIP_CAP_GWEI", 5),

		// Phase 4 V4.1: minimum keeper wallet balance. Default 0.1 FLR.
		// Env: KEEPER_MIN_BALANCE_WEI (empty = 100000000000000000)
		KeeperMinBalanceWei: envOrDefault("KEEPER_MIN_BALANCE_WEI", "100000000000000000"),

		ServiceToken: envOrDefault("SERVICE_TOKEN", ""),

		FrontendURL: envOrDefault("FRONTEND_URL", "http://localhost:3000"),
		WCProjectID: envOrDefault("WC_PROJECT_ID", ""),

		AdminAllowlist: parseAddrList(envOrDefault("ADMIN_ALLOWLIST", "")),
	}

	C.MarketplaceAddr = strings.ToLower(C.MarketplaceAddr)
	C.AuctionAddr = strings.ToLower(C.AuctionAddr)
	C.OfferBookAddr = strings.ToLower(C.OfferBookAddr)
	C.RoyaltyAddr = strings.ToLower(C.RoyaltyAddr)

	// Chain metadata validation — only Coston2 (chain 114) is supported.
	// Any unrecognised chain ID is a fatal error: silently using Coston2
	// defaults on a misconfigured deploy would produce incorrect labels
	// and broken wallet pairing. Operators deploying to a different testnet
	// must set NETWORK_NAME, NATIVE_CURRENCY, EXPLORER_URL explicitly.
	switch C.ChainID {
	case 114:
		// Coston2 — defaults are correct; no action.
	default:
		fmt.Fprintf(os.Stderr, "FATAL: unsupported CHAIN_ID=%d; this deployment targets Coston2 (chain 114) only.\n", C.ChainID)
		os.Exit(1)
	}

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

	// v35: KEEPER_KEY validation — when set, parse as ECDSA private key.
	// An invalid key silently disabled the keeper subsystem before; now
	// it fails fast with a clear error so operators catch typos at startup.
	if C.KeeperKey != "" {
		pkBytes, err := hex.DecodeString(strings.TrimPrefix(C.KeeperKey, "0x"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "FATAL: KEEPER_KEY is not valid hex\n")
			os.Exit(1)
		}
		if _, err := crypto.ToECDSA(pkBytes); err != nil {
			fmt.Fprintf(os.Stderr, "FATAL: KEEPER_KEY is not a valid ECDSA private key: %v\n", err)
			os.Exit(1)
		}
	}

	// v35: SERVICE_TOKEN minimum length — consistent with JWT_SECRET ≥32.
	if C.ServiceToken != "" && len(C.ServiceToken) < 32 {
		fmt.Fprintf(os.Stderr, "FATAL: SERVICE_TOKEN must be at least 32 characters when set\n")
		os.Exit(1)
	}

	// Phase 4 V4.1: validate KeeperMinBalanceWei at startup. A typo like
	// "0.1" (missing wei conversion — should be 100000000000000000) would
	// silently skip the balance check with only a log line. Fail fast here
	// so operators catch misconfiguration at deploy time.
	if C.KeeperMinBalanceWei != "" {
		minWei, ok := new(big.Int).SetString(C.KeeperMinBalanceWei, 10)
		if !ok || minWei.Sign() < 0 {
			fmt.Fprintf(os.Stderr, "FATAL: KEEPER_MIN_BALANCE_WEI=%q is not a valid non-negative decimal integer\n", C.KeeperMinBalanceWei)
			os.Exit(1)
		}
	}

	// v35: ADMIN_ALLOWLIST entry validation. Each entry must be a well-formed
	// Ethereum address; malformed entries produce a clear startup failure.
	for _, addr := range C.AdminAllowlist {
		if !isValidEthAddr(addr) {
			fmt.Fprintf(os.Stderr, "FATAL: ADMIN_ALLOWLIST contains invalid address: %q\n", addr)
			os.Exit(1)
		}
	}

	// v35: production guard — empty ADMIN_ALLOWLIST in production is a
	// misconfiguration (no admin can verify collections or manage the platform).
	if C.Env == "production" && len(C.AdminAllowlist) == 0 {
		fmt.Fprintln(os.Stderr, "WARN: ADMIN_ALLOWLIST is empty in production; no admin can verify collections or manage the platform")
	}

	// v35: contract address validation — MARKETPLACE_ADDR, AUCTION_ADDR,
	// OFFERBOOK_ADDR must be well-formed Ethereum addresses. Previously
	// they were only lowercased; a typo in .env would deploy a broken site.
	for _, pair := range [][2]string{
		{"MARKETPLACE_ADDR", C.MarketplaceAddr},
		{"AUCTION_ADDR", C.AuctionAddr},
		{"OFFERBOOK_ADDR", C.OfferBookAddr},
	} {
		if !isValidEthAddr(pair[1]) {
			fmt.Fprintf(os.Stderr, "FATAL: %s is not a valid Ethereum address: %q\n", pair[0], pair[1])
			os.Exit(1)
		}
	}

	// v35: production SIWE guard — SIWE_DOMAIN=localhost in production
	// means wallet sign-ins will fail because the signed message domain
	// won't match. Fail fast to prevent broken sign-in in production.
	if C.Env == "production" && C.SIWEDomain == "localhost" {
		fmt.Fprintln(os.Stderr, "FATAL: SIWE_DOMAIN is still 'localhost' in production; set it to your public domain (e.g. magicwebb.fly.dev)")
		os.Exit(1)
	}
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
		// v35: log a clear warning on parse errors instead of silently
		// returning the default. GETLOGS_BLOCK_CAP is safety-critical —
		// a misconfigured cap silently falling back to 30 could mask
		// a production misconfiguration that leaves the indexer stuck.
		fmt.Fprintf(os.Stderr, "WARN: %s=%q is not a valid uint64 (using default %d): %v\n", key, v, def, err)
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
		// v35: log a clear warning on parse errors (see optUint64).
		fmt.Fprintf(os.Stderr, "WARN: %s=%q is not a valid float64 (using default %f): %v\n", key, v, def, err)
		return def
	}
	return f
}

func optDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: %s=%q is not a valid duration (using default %v): %v\n", key, v, def, err)
		return def
	}
	return d
}

func optInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: %s=%q is not a valid int (using default %d): %v\n", key, v, def, err)
		return def
	}
	if n < 1 {
		fmt.Fprintf(os.Stderr, "WARN: %s=%d is < 1, clamping to default %d\n", key, n, def)
		return def
	}
	return n
}
