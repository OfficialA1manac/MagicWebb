// Package profile is the per-network tuning table. One process serves one
// chain (config validates CHAIN_ID), and everything that differs between
// Coston2, Songbird and Flare lives here: identity, default RPC set, block
// cadence, finality depth, poll/keeper cadences, getLogs limits, gas caps.
//
// Layout (v3.6 wave 6): this file owns the Profile struct, the lookup
// functions and Validate(); each network's values live in their own file
// (coston2.go, songbird.go, flare.go) as a plain `var`. The table below is
// the only place that maps chain id → profile — no init(), no registration.
//
// Shared helpers (internal/chain, rpcpool, indexer, keeper) stay common and
// read their knobs from the active profile. Environment variables still
// override individual values (see config.Load) so an operator can tune a
// deployment without a rebuild; the profile is the default, not a cage.
//
// app/src/lib/chains/{coston2,songbird,flare}.ts mirror the identity half of
// this table for the browser; profile_test.go asserts the two stay in sync.
//
// Rule: any future per-network behavior (gas strategy, explorer API quirks,
// finality tweaks) is a new FIELD in this table, never a forked package or a
// chain-ID branch in shared code. A new field forces a value for all three
// chains at compile time, and read-only networks inherit correct metadata
// for free.
package profile

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Profile is the complete per-chain parameter set.
type Profile struct {
	ChainID  uint64
	Key      string // "coston2" | "songbird" | "flare" — matches deployments/<key>.json
	Name     string // display label
	Currency string // native token symbol
	Explorer string // block explorer base URL (no trailing slash)
	// Mainnet means real value is at stake: stricter gas caps, longer upgrade
	// delay, no faucet, and config.Load refuses RESET_ON_ADDRESS_CHANGE.
	Mainnet bool

	// DefaultRPCs is the public endpoint set used when RPC_URL/RPC_URLS are
	// unset: [0] is the primary, the rest are the rotation fallbacks
	// (deployments/<key>.json rpc.primary + rpc.fallbacks mirror it).
	// Validate() requires at least two on mainnets — one public endpoint
	// rate-limiting us must not stall settlement.
	DefaultRPCs []string

	// BlockTime is the typical interval between blocks. Drives UI ETAs
	// (window.MW_BLOCK_TIME_MS, /api/v1/server-time) and the watcher poll.
	BlockTime time.Duration
	// ReorgSafety is how many blocks behind the head the watcher indexes.
	// Flare-family chains run Snowman consensus with single-slot finality, so
	// this guards against RPC inconsistency between load-balanced nodes, not
	// chain reorganisations. Coston2 testnet nodes are less consistent.
	ReorgSafety uint64
	// Confirmations the UI waits for before calling a receipt final.
	Confirmations uint64

	// Watcher + keeper cadences.
	PollInterval    time.Duration // head poll
	KeeperTick      time.Duration // auction settlement / expired-listing keeper loop
	RefundTick      time.Duration // loser/offer refund sweeper
	MetadataTick    time.Duration // metadata fetch worker
	OwnershipTick   time.Duration // ownership repair
	FeeSweepTick    time.Duration // fee sweeper + keeper balance check
	VerifierTick    time.Duration // collection badge sweeper
	VerifierRecheck time.Duration // how stale a verification may be

	// getLogs limits (public Flare RPCs cap ranges at 30 blocks).
	GetLogsChunk    uint64
	GetLogsBlockCap uint64

	// Gas caps for the keeper (gwei). 0 = no cap. Validate() refuses 0 on
	// every network: an uncapped keeper can drain its wallet in one spike.
	MaxFeeCapGwei float64
	MaxTipCapGwei float64

	// MetadataConcurrency bounds concurrent tokenURI fetches.
	MetadataConcurrency int

	// ProfileSource orders networks for the cross-network profile read-through
	// (api/profiles.go): a wallet with no profile here is looked up on siblings
	// in ascending ProfileSource order. 1 = where users edit profiles today.
	ProfileSource int

	// WSCoalesceMs is the WebSocket write-coalescing window in milliseconds:
	// events arriving within it are batched into one NDJSON frame.
	WSCoalesceMs int
	// ImageProxyConcurrency bounds concurrent outbound fetches through the
	// /api/v1/media proxy (upstream IPFS gateways are slow and rate-limited).
	ImageProxyConcurrency int
	// RateLimitTier names the REST/GraphQL rate-limit posture: "testnet" |
	// "mainnet" (config.APIRateLimitPerMin derives the per-IP budget from it).
	RateLimitTier string
	// ConnectRateTier names the Connect-RPC per-procedure posture, consumed by
	// connectrpc/interceptors.RateLimitsForTier: "testnet" doubles every
	// procedure budget (testers hammer refresh), "mainnet" keeps the base table.
	ConnectRateTier string
	// GraphQLMaxCost is the per-query complexity budget enforced by the
	// GraphQL server (graphql.MaxQueryCost is the compile-time fallback).
	GraphQLMaxCost int
	// FaucetURL is where testers get gas; "" on networks with real value.
	FaucetURL string
	// AuditNote is surfaced to the UI while a network is browse-only; "" when
	// there is nothing to say.
	AuditNote string

	// AllowanceModuleAddr is the Zodiac Allowance Module singleton the fee
	// sweeper drives (keeper_feesweep.go). Deployed at one CREATE2 address on
	// every Flare-family chain today; a per-chain field so a network that
	// ships a different module (or none) is a data change, not a code branch.
	// The sweeper verifies bytecode at this address before its first sweep.
	AllowanceModuleAddr string
}

// table is the ONLY chain id → profile mapping. Values live per network in
// coston2.go / songbird.go / flare.go.
var table = map[uint64]Profile{
	114: coston2,
	19:  songbird,
	14:  flare,
}

// For returns the profile for a chain id, or an error listing the supported ones.
func For(chainID uint64) (Profile, error) {
	p, ok := table[chainID]
	if !ok {
		return Profile{}, fmt.Errorf("unsupported CHAIN_ID=%d; supported chains: 14 (Flare), 19 (Songbird), 114 (Coston2)", chainID)
	}
	return p, nil
}

// MustFor is For for callers that have already validated the chain id.
func MustFor(chainID uint64) Profile {
	p, err := For(chainID)
	if err != nil {
		panic(err)
	}
	return p
}

// All returns every supported profile, stable order by chain id descending
// (114, 19, 14) — the order the network switcher lists them.
func All() []Profile {
	return []Profile{table[114], table[19], table[14]}
}

// Supported reports whether a chain id has a profile.
func Supported(chainID uint64) bool { _, ok := table[chainID]; return ok }

var hexAddrRE = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// Validate checks the invariants every profile must hold before a process
// boots on it. config.Load calls it and exits with the joined error, so a
// bad edit to a per-network file fails the deploy instead of starving the
// keeper at 3am:
//   - identity fields present, explorer without a trailing slash;
//   - at least one RPC, and at least TWO on mainnets (rotation fallback);
//   - non-zero gas caps (0 = uncapped = a spike can drain the keeper);
//   - every cadence and getLogs limit non-zero;
//   - tiers are "testnet" | "mainnet"; testnets carry a faucet, mainnets none;
//   - the Allowance Module address is a well-formed address.
func (p Profile) Validate() error {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf("profile %s (%d): "+format, append([]any{p.Key, p.ChainID}, a...)...)) }

	if p.ChainID == 0 || p.Key == "" || p.Name == "" || p.Currency == "" || p.Explorer == "" {
		fail("identity fields (ChainID, Key, Name, Currency, Explorer) must all be set")
	}
	if strings.HasSuffix(p.Explorer, "/") {
		fail("Explorer must not end with '/'")
	}
	if !validHTTPSURL(p.Explorer) {
		fail("Explorer=%q must be an https URL with a host", p.Explorer)
	}
	if len(p.DefaultRPCs) == 0 {
		fail("DefaultRPCs must list at least one endpoint")
	}
	if p.Mainnet && len(p.DefaultRPCs) < 2 {
		fail("mainnet needs >= 2 DefaultRPCs for rotation fallback, have %d", len(p.DefaultRPCs))
	}
	for i, u := range p.DefaultRPCs {
		if !validHTTPSURL(u) {
			fail("DefaultRPCs[%d]=%q must be an https URL with a host", i, u)
		}
	}
	if p.MaxFeeCapGwei <= 0 || p.MaxTipCapGwei <= 0 {
		fail("gas caps must be > 0 (MaxFeeCapGwei=%v MaxTipCapGwei=%v)", p.MaxFeeCapGwei, p.MaxTipCapGwei)
	}
	if p.MaxFeeCapGwei < p.MaxTipCapGwei {
		fail("MaxFeeCapGwei (%v) must be >= MaxTipCapGwei (%v) (EIP-1559 invariant)", p.MaxFeeCapGwei, p.MaxTipCapGwei)
	}
	for name, d := range map[string]time.Duration{
		"BlockTime": p.BlockTime, "PollInterval": p.PollInterval, "KeeperTick": p.KeeperTick, "RefundTick": p.RefundTick,
		"MetadataTick": p.MetadataTick, "OwnershipTick": p.OwnershipTick, "FeeSweepTick": p.FeeSweepTick,
		"VerifierTick": p.VerifierTick, "VerifierRecheck": p.VerifierRecheck,
	} {
		if d <= 0 {
			fail("%s must be > 0", name)
		}
	}
	if p.ReorgSafety == 0 || p.Confirmations == 0 || p.GetLogsChunk == 0 || p.GetLogsBlockCap == 0 {
		fail("ReorgSafety, Confirmations, GetLogsChunk and GetLogsBlockCap must be > 0")
	}
	if p.MetadataConcurrency <= 0 || p.ImageProxyConcurrency <= 0 || p.WSCoalesceMs <= 0 || p.GraphQLMaxCost <= 0 || p.ProfileSource <= 0 {
		fail("MetadataConcurrency, ImageProxyConcurrency, WSCoalesceMs, GraphQLMaxCost and ProfileSource must be > 0")
	}
	for name, tier := range map[string]string{"RateLimitTier": p.RateLimitTier, "ConnectRateTier": p.ConnectRateTier} {
		if tier != "testnet" && tier != "mainnet" {
			fail("%s=%q must be \"testnet\" or \"mainnet\"", name, tier)
		}
	}
	if p.Mainnet && p.FaucetURL != "" {
		fail("mainnet must not advertise a faucet")
	}
	if !p.Mainnet && p.FaucetURL == "" {
		fail("testnet must advertise a faucet")
	}
	if !hexAddrRE.MatchString(p.AllowanceModuleAddr) {
		fail("AllowanceModuleAddr=%q is not a 0x-prefixed 20-byte address", p.AllowanceModuleAddr)
	}
	return errors.Join(errs...)
}

// validHTTPSURL parses s and requires the https scheme and a non-empty host
// ("https://" alone, or a bare hostname, is rejected).
func validHTTPSURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// ValidateAll runs Validate on every profile in the table.
func ValidateAll() error {
	var errs []error
	for _, p := range All() {
		if err := p.Validate(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
