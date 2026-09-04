// Package config loads and validates all environment variables at startup.
// Fast-fail: missing required vars cause immediate os.Exit(1).
package config

import (
	"encoding/hex"
	"fmt"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/chain/profile"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
)

// C is the global config loaded once at startup via Load().
var C Config

// Network is one entry in the frontend's network switcher.
//
// Availability cannot be derived at runtime: this process only knows about its
// own chain, and asking another network's API whether it is alive would make
// every page render depend on a cross-origin request. It is declared instead,
// through NETWORK_URLS — a network with no URL is one that has no deployment,
// and the switcher renders it disabled rather than linking to a dead app.
type Network struct {
	ChainID   uint64
	Name      string // display label, e.g. "Songbird"
	URL       string // origin of that network's app; empty when not deployed
	Current   bool   // the network this process serves
	Available bool   // has a URL, so it can be navigated to
	// Trading is "live" when that network has marketplace contracts and
	// "browse-only" otherwise. For the current network it is derived from
	// ContractsDeployed; for siblings it is declared through NETWORK_TRADING
	// ("114=live,19=browse-only") because this process cannot see their
	// contracts. Empty when a sibling is not listed there.
	Trading string
}

// TradingLive / TradingBrowseOnly are the two values Network.Trading takes.
const (
	TradingLive       = "live"
	TradingBrowseOnly = "browse-only"
)

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

	// Networks is every chain the frontend switcher offers, in display order,
	// including the one this process serves. Each network is a separate origin
	// with its own API, database and /ws, so switching is a navigation, not a
	// state change — see NETWORK_URLS.
	Networks []Network

	// Profile is the per-chain tuning table entry for ChainID
	// (internal/chain/profile). Env vars override individual values; the
	// profile supplies every default so a new network needs no code change.
	Profile profile.Profile

	// Contract addresses
	MarketplaceAddr        string
	AuctionAddr            string
	OfferBookAddr          string
	NFTAddr                string
	MarketplaceManagerAddr string
	RoyaltyAddr            string

	// Database
	PostgresURL string // primary (read-write) connection
	ReadPoolURL string // optional read-replica connection for query offloading; empty = all reads use PostgresURL
	RedisURL    string // optional Redis URL for distributed cache; empty = in-memory only (CACHE-1)

	// IMG-3: S3-compatible blob store backend. When ImgStoreBackend = "s3", blob
	// bodies are stored in S3/MinIO instead of Postgres BYTEA, saving PG
	// storage and freeing connections. Metadata (hashes, mime, refcounts)
	// remains in nft_image_blobs for dedup and quota enforcement.
	ImgStoreBackend string // "s3" or "" (empty = Postgres BYTEA, default)
	S3Endpoint      string // S3-compatible endpoint (e.g. s3.amazonaws.com, play.min.io:9000)
	S3Bucket        string // bucket name for blob storage
	S3AccessKey     string // S3 access key ID
	S3SecretKey     string // S3 secret access key
	S3UseSSL        bool   // use HTTPS for S3 connections

	// Servers
	HTTPAddr  string
	GRPCPort  int      // gRPC event bridge port (0 = disabled)
	GRPCPeers []string // gRPC peer addresses (host:port) for cross-instance fan-out

	// Auth
	SIWEDomain string
	JWTSecret  string
	NonceTTL   time.Duration // TTL for SIWE nonces (in-memory store); env NONCE_TTL

	// Indexer
	IndexFromBlock  uint64 // start block (override for reindex)
	GetLogsChunk    uint64 // getLogs chunk size (Flare public RPC: 30)
	GetLogsBlockCap uint64 // 0 = unlimited (private RPC)

	// TrackedCollections are NFT contract addresses (ERC-721/ERC-1155) the
	// indexer must watch for Transfer events. Without a tracked_collections
	// row, the indexer's processTransfers never sees Transfer logs from that
	// contract, so nft_ownership stays empty and WalletNFTs returns zero
	// results — even when the wallet legitimately holds NFTs. The indexer
	// auto-seeds tracked_collections when a listing or auction is created
	// for a token from that collection; TRACKED_COLLECTIONS lets operators
	// add contracts that have never been listed/auctioned on the marketplace.
	// Comma-separated, case-insensitive. Seed at startup via EnsureCollection.
	TrackedCollections []string

	// Score weights (trending formula)
	ScoreWViews  float64
	ScoreWBids   float64
	ScoreWVolume float64
	ScoreDecay   float64

	// Metadata worker
	MetadataConcurrency int // concurrent metadata fetches per tick; env METADATA_CONCURRENCY

	// WSCoalesceMs is the WebSocket write-coalescing window (ms); profile
	// default, env WS_COALESCE_MS overrides.
	WSCoalesceMs int
	// ImageProxyConcurrency bounds concurrent outbound /api/v1/media fetches;
	// profile default, env IMAGE_PROXY_CONCURRENCY overrides.
	ImageProxyConcurrency int
	// GraphQLMaxCost is the per-query complexity budget; profile default,
	// env GRAPHQL_MAX_COST overrides.
	GraphQLMaxCost int
	// APIRateLimitPerMin is the per-IP budget for /api/v1 and /graphql.
	// Derived from the profile's RateLimitTier (testnet 120, mainnet 60);
	// env API_RATE_LIMIT_PER_MIN overrides.
	APIRateLimitPerMin int

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

	// Gas alerting webhook — at least one URL must be set for alerts to fire.
	DiscordWebhookURL    string // Discord/Slack webhook URL for gas cost alerts
	PrometheusWebhookURL string // Prometheus Alertmanager-compatible webhook URL
	GasAlertThresholdWei string // minimum total gas cost (wei) in 24h to trigger alert

	// SMTP email alerting — when SMTPHost and EmailTo are set, gas alerts are
	// also sent via email as an alternative to webhook-based notifications.
	SMTPHost  string // SMTP server hostname
	SMTPPort  int    // SMTP server port (default 587)
	SMTPUser  string // SMTP username (usually the email address)
	SMTPPass  string // SMTP password or app-specific token
	EmailFrom string // from-address for alert emails
	EmailTo   string // to-address for alert emails

	// Fee sweep (Zodiac Allowance Module on Gnosis Safe)
	// SafeAddr is the Gnosis Safe multisig used as feeRecipient. When set and
	// KeeperKey is also set, a background sweeper periodically checks the
	// Safe's native balance and pulls fees to PersonalWalletAddr via the
	// Zodiac Allowance Module.
	SafeAddr           string
	PersonalWalletAddr string
	// FeeSweepMinWei is the minimum Safe balance (in wei) that triggers a sweep.
	// Sweeping on every tick wastes gas on dust; sweep only when balance clears
	// a meaningful multiple of current gas cost. Default: 0.1 native token (1e17 wei).
	// Set to "0" to sweep every tick (not recommended).
	FeeSweepMinWei string

	// FrontendURL is the allowed CORS origin (e.g. https://magicwebb.xyz).
	FrontendURL string

	// WCProjectID enables WalletConnect v2 in the UI (cloud.walletconnect.com).
	// Empty = injected-wallet (MetaMask) only.
	WCProjectID string

	// SentryDSN enables error/panic reporting to Sentry. Empty = disabled.
	// When set, the Sentry SDK is initialised at startup and a Fiber recovery
	// middleware captures all panics and sends them to the configured project.
	// The DSN is a secret — set via fly secrets, not fly.toml [env].
	SentryDSN string

	// OTELExporterOTLPEndpoint is the OTLP/gRPC collector endpoint for distributed
	// tracing (e.g. Honeycomb, Grafana Tempo, Jaeger). Empty = tracing disabled.
	// When set, the OpenTelemetry SDK is initialised at startup with a batch
	// span exporter and an otelfiber middleware that creates a span per HTTP
	// request. The endpoint is a secret — set via fly secrets.
	OTELExporterOTLPEndpoint string
}

// Load reads environment variables and panics on missing required values.
func Load() {
	prof := profileFor(requiredUint64("CHAIN_ID"))
	C = Config{
		Env:     envOrDefault("ENV", "development"),
		RPCURL:  required("RPC_URL"),
		ChainID: requiredUint64("CHAIN_ID"),

		// Chain identity. Required by the UI for WalletConnect pairing,
		// user-facing labels (window.MW_NATIVE_CURRENCY / MW_NETWORK_NAME) and
		// explorer links. The profile supplies the defaults for every
		// supported chain; NETWORK_NAME, NATIVE_CURRENCY and EXPLORER_URL let
		// an operator customise display labels without changing the chain.
		NetworkName:    envOrDefault("NETWORK_NAME", prof.Name),
		NativeCurrency: envOrDefault("NATIVE_CURRENCY", prof.Currency),
		ExplorerURL:    envOrDefault("EXPLORER_URL", prof.Explorer),
		Profile:        prof,

		// Contract addresses may all be empty: that is "read-only network
		// mode" (see ContractsDeployed) used while Songbird/Flare have no
		// contracts yet. Partially set is a configuration error.
		MarketplaceAddr:        envOrDefault("MARKETPLACE_ADDR", ""),
		AuctionAddr:            envOrDefault("AUCTION_ADDR", ""),
		OfferBookAddr:          envOrDefault("OFFERBOOK_ADDR", ""),
		NFTAddr:                envOrDefault("NFT_ADDR", ""),
		MarketplaceManagerAddr: envOrDefault("MARKETPLACE_MANAGER_ADDR", ""),
		RoyaltyAddr:            envOrDefault("ROYALTY_ADDR", ""),

		PostgresURL: required("POSTGRES_URL"),
		ReadPoolURL: envOrDefault("READ_POOL_URL", ""),
		RedisURL:    envOrDefault("REDIS_URL", ""),

		ImgStoreBackend: envOrDefault("IMG_STORE_BACKEND", ""),
		S3Endpoint:      envOrDefault("S3_ENDPOINT", ""),
		S3Bucket:        envOrDefault("S3_BUCKET", ""),
		S3AccessKey:     envOrDefault("S3_ACCESS_KEY", ""),
		S3SecretKey:     envOrDefault("S3_SECRET_KEY", ""),
		S3UseSSL:        os.Getenv("S3_USE_SSL") == "true",

		HTTPAddr:  envOrDefault("HTTP_ADDR", ":8080"),
		GRPCPort:  optInt("GRPC_PORT", 0),
		GRPCPeers: parseURLList(os.Getenv("GRPC_PEERS")),

		SIWEDomain: envOrDefault("SIWE_DOMAIN", "localhost"),
		JWTSecret:  required("JWT_SECRET"),
		NonceTTL:   optDuration("NONCE_TTL", 5*time.Minute),

		IndexFromBlock:        optUint64("INDEX_FROM_BLOCK", 0),
		GetLogsChunk:          optUint64("GETLOGS_CHUNK", prof.GetLogsChunk),
		GetLogsBlockCap:       optUint64("GETLOGS_BLOCK_CAP", prof.GetLogsBlockCap),
		MetadataConcurrency:   optInt("METADATA_CONCURRENCY", prof.MetadataConcurrency),
		WSCoalesceMs:          optInt("WS_COALESCE_MS", prof.WSCoalesceMs),
		ImageProxyConcurrency: optInt("IMAGE_PROXY_CONCURRENCY", prof.ImageProxyConcurrency),
		GraphQLMaxCost:        optInt("GRAPHQL_MAX_COST", prof.GraphQLMaxCost),
		APIRateLimitPerMin:    optInt("API_RATE_LIMIT_PER_MIN", rateLimitForTier(prof.RateLimitTier)),
		TrackedCollections:    parseAddrList(envOrDefault("TRACKED_COLLECTIONS", "")),

		ScoreWViews:  optFloat64("SCORE_W_VIEWS", 0.3),
		ScoreWBids:   optFloat64("SCORE_W_BIDS", 0.5),
		ScoreWVolume: optFloat64("SCORE_W_VOLUME", 0.2),
		ScoreDecay:   optFloat64("SCORE_DECAY", 0.05),

		KeeperKey: envOrDefault("KEEPER_KEY", ""),

		SafeAddr:           envOrDefault("SAFE_ADDR", ""),
		PersonalWalletAddr: envOrDefault("PERSONAL_WALLET_ADDR", ""),
		FeeSweepMinWei:     envOrDefault("FEE_SWEEP_MIN_WEI", "100000000000000000"), // 0.1 native token

		// v29: ceiling on keeper gas pricing. 0 = unbounded (NOT recommended).
		MaxFeeCapGwei: optFloat64("KEEPER_MAX_FEE_CAP_GWEI", prof.MaxFeeCapGwei),
		MaxTipCapGwei: optFloat64("KEEPER_MAX_TIP_CAP_GWEI", prof.MaxTipCapGwei),

		// Phase 4 V4.1: minimum keeper wallet balance. Default 0.1 FLR.
		// Env: KEEPER_MIN_BALANCE_WEI (empty = 100000000000000000)
		KeeperMinBalanceWei: envOrDefault("KEEPER_MIN_BALANCE_WEI", "100000000000000000"),

		DiscordWebhookURL:    envOrDefault("DISCORD_WEBHOOK_URL", ""),
		PrometheusWebhookURL: envOrDefault("PROMETHEUS_WEBHOOK_URL", ""),
		GasAlertThresholdWei: envOrDefault("GAS_ALERT_THRESHOLD_WEI", "500000000000000000"), // 0.5 native token default

		SMTPHost:  envOrDefault("SMTP_HOST", ""),
		SMTPPort:  optInt("SMTP_PORT", 587),
		SMTPUser:  envOrDefault("SMTP_USER", ""),
		SMTPPass:  envOrDefault("SMTP_PASS", ""),
		EmailFrom: envOrDefault("EMAIL_FROM", ""),
		EmailTo:   envOrDefault("EMAIL_TO", ""),

		FrontendURL: envOrDefault("FRONTEND_URL", "http://localhost:3000"),
		WCProjectID: envOrDefault("WC_PROJECT_ID", ""),

		SentryDSN:                envOrDefault("SENTRY_DSN", ""),
		OTELExporterOTLPEndpoint: envOrDefault("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
	}

	C.MarketplaceAddr = strings.ToLower(C.MarketplaceAddr)
	C.AuctionAddr = strings.ToLower(C.AuctionAddr)
	C.OfferBookAddr = strings.ToLower(C.OfferBookAddr)
	C.NFTAddr = strings.ToLower(C.NFTAddr)
	C.MarketplaceManagerAddr = strings.ToLower(C.MarketplaceManagerAddr)
	C.RoyaltyAddr = strings.ToLower(C.RoyaltyAddr)
	C.SafeAddr = strings.ToLower(C.SafeAddr)
	C.PersonalWalletAddr = strings.ToLower(C.PersonalWalletAddr)

	// All-or-nothing on the three cores. An operator who sets one address but
	// not the others has a broken deploy, not a read-only one.
	set := 0
	for _, a := range []string{C.MarketplaceAddr, C.AuctionAddr, C.OfferBookAddr} {
		if a != "" {
			set++
		}
	}
	if set != 0 && set != 3 {
		fmt.Fprintln(os.Stderr, "FATAL: MARKETPLACE_ADDR, AUCTION_ADDR and OFFERBOOK_ADDR must be set together (or all unset for read-only network mode)")
		os.Exit(1)
	}
	if set == 0 {
		fmt.Fprintf(os.Stderr, "WARN: no contract addresses for chain %d — read-only network mode: API and UI serve, indexer/keepers/verifier idle (see deployments/%s.json)\n", C.ChainID, C.Profile.Key)
	}

	// The switcher catalogue needs ContractsDeployed (above) to label this
	// network's own trading status.
	C.Networks = buildNetworks(os.Getenv("NETWORK_URLS"), os.Getenv("NETWORK_TRADING"), C.ChainID, C.TradingStatus())

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

	// Validate GasAlertThresholdWei at startup. Must be a valid non-negative
	// decimal integer (wei value). A typo like "0.5" (missing wei conversion)
	// is caught here with a clear error rather than silently disabling alerts.
	if C.GasAlertThresholdWei != "" {
		thresh, ok := new(big.Int).SetString(C.GasAlertThresholdWei, 10)
		if !ok || thresh.Sign() < 0 {
			fmt.Fprintf(os.Stderr, "FATAL: GAS_ALERT_THRESHOLD_WEI=%q is not a valid non-negative decimal integer\n", C.GasAlertThresholdWei)
			os.Exit(1)
		}
	}

	// v35: TRACKED_COLLECTIONS entry validation. Invalid entries are a WARN
	// (not fatal) because a typo in one collection doesn't break the rest.
	// The indexer skips malformed addresses at seed time via SeedTrackedCollections,
	// so the operator sees zero results for that collection with no clue WHY —
	// the startup warning closes that gap.
	for _, addr := range C.TrackedCollections {
		if !isValidEthAddr(addr) {
			fmt.Fprintf(os.Stderr, "WARN: TRACKED_COLLECTIONS contains invalid address (skipped): %q\n", addr)
		}
	}

	// v35: empty TRACKED_COLLECTIONS in production means the indexer only
	// watches collections auto-discovered from nft_tokens (i.e. collections
	// that were ever listed or auctioned). Legitimate NFT holders whose
	// collections have never been traded on MagicWebb will see zero results
	// in WalletNFTs until someone creates a listing — the operator should
	// explicitly add every deployed NFT contract to TRACKED_COLLECTIONS.
	if C.Env == "production" && len(C.TrackedCollections) == 0 {
		fmt.Fprintln(os.Stderr, "WARN: TRACKED_COLLECTIONS is empty in production; the indexer will only watch collections auto-discovered from nft_tokens. Add your NFT contracts to TRACKED_COLLECTIONS.")
	}

	// v35: contract address validation — MARKETPLACE_ADDR, AUCTION_ADDR,
	// OFFERBOOK_ADDR must be well-formed Ethereum addresses. Previously
	// they were only lowercased; a typo in .env would deploy a broken site.
	// Skipped entirely in read-only network mode: the all-or-nothing check
	// above already guaranteed the three cores are either all set or all
	// empty, and empty is a supported state, not a typo.
	if C.ContractsDeployed() {
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
	}

	// Ancillary contract addresses: whenever one is set it must be well-formed,
	// and it may only be set when the core contracts are deployed — a lone
	// NFT_ADDR on a read-only network is a misconfigured deploy, not a
	// read-only one.
	for _, pair := range [][2]string{
		{"NFT_ADDR", C.NFTAddr},
		{"MARKETPLACE_MANAGER_ADDR", C.MarketplaceManagerAddr},
		{"ROYALTY_ADDR", C.RoyaltyAddr},
	} {
		if pair[1] == "" {
			continue
		}
		if !C.ContractsDeployed() {
			fmt.Fprintf(os.Stderr, "FATAL: %s is set but the core contract addresses are empty (read-only network mode); unset it or configure all core addresses\n", pair[0])
			os.Exit(1)
		}
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

// buildNetworks resolves the switcher's entries from NETWORK_URLS, a
// comma-separated list of `chainID=origin` pairs:
//
//	NETWORK_URLS="114=https://magicwebb.fly.dev,19=https://magicwebb-songbird.fly.dev"
//
// and NETWORK_TRADING, the same shape with a trading status per sibling:
//
//	NETWORK_TRADING="114=live,19=browse-only,14=browse-only"
//
// The catalogue itself is chain/profile.All() — the one per-network table —
// so a chain the backend cannot run never appears as a destination.
//
// A chain absent from NETWORK_URLS, or listed with an empty origin, is marked
// unavailable. That is the correct default rather than a degraded one: at the
// time of writing only Coston2 has contracts deployed, so an operator who sets
// nothing gets a switcher that tells the truth.
//
// The current network is always available regardless of the list — the user is
// already looking at it, and a missing self-entry is a config typo, not a
// reason to grey out the page they are on. Its trading status comes from
// currentTrading (ContractsDeployed), never from NETWORK_TRADING.
func buildNetworks(networkURLs, networkTrading string, currentChainID uint64, currentTrading string) []Network {
	urls := parseChainMap(networkURLs, "NETWORK_URLS")
	for id, origin := range urls {
		urls[id] = strings.TrimRight(origin, "/")
	}
	trading := parseChainMap(networkTrading, "NETWORK_TRADING")

	all := profile.All()
	out := make([]Network, 0, len(all))
	for _, p := range all {
		n := Network{ChainID: p.ChainID, Name: p.Name}
		n.URL = urls[n.ChainID]
		n.Current = n.ChainID == currentChainID
		n.Available = n.URL != "" || n.Current
		if n.Current {
			n.Trading = currentTrading
		} else {
			switch t := trading[n.ChainID]; t {
			case TradingLive, TradingBrowseOnly:
				n.Trading = t
			case "":
			default:
				fmt.Fprintf(os.Stderr, "WARN: NETWORK_TRADING for chain %d is %q; expected live|browse-only, ignoring\n", n.ChainID, t)
			}
		}
		out = append(out, n)
	}
	return out
}

// parseChainMap parses a comma-separated `chainID=value` list. Malformed
// entries are skipped with a warning naming the env var; a later empty value
// never erases an earlier non-empty one.
func parseChainMap(list, envName string) map[uint64]string {
	out := map[uint64]string{}
	for _, pair := range strings.Split(list, ",") {
		id, val, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok {
			continue
		}
		chainID, err := strconv.ParseUint(strings.TrimSpace(id), 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: %s entry %q has a non-numeric chain id; ignoring\n", envName, pair)
			continue
		}
		if val = strings.TrimSpace(val); val != "" {
			out[chainID] = val
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

// profileFor resolves the per-chain profile or exits with the same FATAL line
// the old chain switch printed, so operators keep a familiar error.
func profileFor(chainID uint64) profile.Profile {
	p, err := profile.For(chainID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v.\n", err)
		os.Exit(1)
	}
	return p
}

// rateLimitForTier maps a profile's RateLimitTier onto the per-IP /api/v1
// budget per minute. Testnets are generous (testers hammer refresh);
// mainnets keep the historical 60/min.
func rateLimitForTier(tier string) int {
	if tier == "testnet" {
		return 120
	}
	return 60
}

// TradingStatus is "live" when this network has marketplace contracts and
// "browse-only" otherwise (read-only network mode). Surfaced to the UI as
// window.MW_TRADING and in the network switcher.
func (c *Config) TradingStatus() string {
	if c.ContractsDeployed() {
		return TradingLive
	}
	return TradingBrowseOnly
}

// ContractsDeployed reports whether this network has marketplace contracts.
// False = read-only network mode: the process serves the UI and API (so the
// network switcher, docs and health checks work) but runs no indexer,
// keepers or verifier because there is nothing on chain to watch.
func (c *Config) ContractsDeployed() bool {
	return c.MarketplaceAddr != "" && c.AuctionAddr != "" && c.OfferBookAddr != ""
}
