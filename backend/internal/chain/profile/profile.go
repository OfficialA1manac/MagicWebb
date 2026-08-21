// Package profile is the per-network tuning table. One process serves one
// chain (config validates CHAIN_ID), and everything that differs between
// Coston2, Songbird and Flare lives here: identity, default RPC set, block
// cadence, finality depth, poll/keeper cadences, getLogs limits, gas caps.
//
// Shared helpers (internal/chain, rpcpool, indexer, keeper) stay common and
// read their knobs from the active profile. Environment variables still
// override individual values (see config.Load) so an operator can tune a
// deployment without a rebuild; the profile is the default, not a cage.
//
// app/src/lib/chains.ts mirrors the identity half of this table for the
// browser; profile_test.go asserts the two stay in sync.
package profile

import (
	"fmt"
	"time"
)

// Profile is the complete per-chain parameter set.
type Profile struct {
	ChainID  uint64
	Key      string // "coston2" | "songbird" | "flare" — matches deployments/<key>.json
	Name     string // display label
	Currency string // native token symbol
	Explorer string // block explorer base URL (no trailing slash)
	// Mainnet means real value is at stake: stricter gas caps, longer upgrade delay.
	Mainnet bool

	// DefaultRPCs is the public endpoint set used when RPC_URL/RPC_URLS are unset.
	DefaultRPCs []string

	// BlockTime is the typical interval between blocks. Drives UI ETAs and the
	// watcher poll interval.
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
	KeeperTick      time.Duration // auction/offer expiry keepers
	RefundTick      time.Duration // loser/offer refund sweeper
	MetadataTick    time.Duration // metadata fetch worker
	OwnershipTick   time.Duration // ownership repair
	FeeSweepTick    time.Duration // fee sweeper
	VerifierTick    time.Duration // collection badge sweeper
	VerifierRecheck time.Duration // how stale a verification may be

	// getLogs limits (public Flare RPCs cap ranges at 30 blocks).
	GetLogsChunk    uint64
	GetLogsBlockCap uint64

	// Gas caps for the keeper (gwei). 0 = no cap.
	MaxFeeCapGwei float64
	MaxTipCapGwei float64

	// MetadataConcurrency bounds concurrent tokenURI fetches.
	MetadataConcurrency int
}

var table = map[uint64]Profile{
	114: {
		ChainID: 114, Key: "coston2", Name: "Flare Coston2", Currency: "C2FLR",
		Explorer: "https://coston2-explorer.flare.network", Mainnet: false,
		DefaultRPCs: []string{
			"https://coston2-api.flare.network/ext/C/rpc",
			"https://coston2.enosys.global/ext/C/rpc",
			"https://rpc.ankr.com/flare_coston2",
		},
		BlockTime: 1800 * time.Millisecond, ReorgSafety: 3, Confirmations: 1,
		PollInterval: 2 * time.Second, KeeperTick: time.Second, RefundTick: 45 * time.Second,
		MetadataTick: 30 * time.Second, OwnershipTick: 60 * time.Second, FeeSweepTick: 5 * time.Minute,
		VerifierTick: 5 * time.Minute, VerifierRecheck: 24 * time.Hour,
		GetLogsChunk: 30, GetLogsBlockCap: 30,
		MaxFeeCapGwei: 100, MaxTipCapGwei: 5, MetadataConcurrency: 3,
	},
	19: {
		ChainID: 19, Key: "songbird", Name: "Songbird", Currency: "SGB",
		Explorer: "https://songbird-explorer.flare.network", Mainnet: true,
		DefaultRPCs: []string{"https://songbird-api.flare.network/ext/C/rpc"},
		BlockTime:   1800 * time.Millisecond, ReorgSafety: 2, Confirmations: 1,
		PollInterval: 2 * time.Second, KeeperTick: 2 * time.Second, RefundTick: 60 * time.Second,
		MetadataTick: 30 * time.Second, OwnershipTick: 90 * time.Second, FeeSweepTick: 10 * time.Minute,
		VerifierTick: 10 * time.Minute, VerifierRecheck: 24 * time.Hour,
		GetLogsChunk: 30, GetLogsBlockCap: 30,
		MaxFeeCapGwei: 60, MaxTipCapGwei: 3, MetadataConcurrency: 3,
	},
	14: {
		ChainID: 14, Key: "flare", Name: "Flare", Currency: "FLR",
		Explorer: "https://flare-explorer.flare.network", Mainnet: true,
		DefaultRPCs: []string{"https://flare-api.flare.network/ext/C/rpc"},
		BlockTime:   1800 * time.Millisecond, ReorgSafety: 2, Confirmations: 1,
		PollInterval: 2 * time.Second, KeeperTick: 2 * time.Second, RefundTick: 60 * time.Second,
		MetadataTick: 30 * time.Second, OwnershipTick: 90 * time.Second, FeeSweepTick: 10 * time.Minute,
		VerifierTick: 10 * time.Minute, VerifierRecheck: 24 * time.Hour,
		GetLogsChunk: 30, GetLogsBlockCap: 30,
		MaxFeeCapGwei: 50, MaxTipCapGwei: 2, MetadataConcurrency: 3,
	},
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
