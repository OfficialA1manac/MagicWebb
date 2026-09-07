package profile

import "time"

// coston2 is the Flare test network (chain 114). Mirror:
// app/src/lib/chains/coston2.ts + deployments/coston2.json.
var coston2 = Profile{
	ChainID: 114, Key: "coston2", Name: "Flare Coston2", Currency: "C2FLR",
	Explorer: "https://coston2-explorer.flare.network", Mainnet: false,
	DefaultRPCs: []string{
		"https://coston2-api.flare.network/ext/C/rpc",
		"https://coston2.enosys.global/ext/C/rpc",
		"https://rpc.ankr.com/flare_coston2",
	},
	BlockTime: 1800 * time.Millisecond, ReorgSafety: 3, Confirmations: 1,
	PollInterval: 2 * time.Second, KeeperTick: time.Second, RefundTick: 2 * time.Second,
	MetadataTick: 30 * time.Second, OwnershipTick: 60 * time.Second, FeeSweepTick: 5 * time.Minute,
	VerifierTick: 5 * time.Minute, VerifierRecheck: 24 * time.Hour,
	GetLogsChunk: 30, GetLogsBlockCap: 30,
	// Coston2's gas market runs HOT for a testnet: observed 2026-08-31, the
	// RPC pool minimum fee cap was 500 gwei and eth_gasPrice suggested
	// ~1450 gwei, so the old 100-gwei ceiling made every keeper tx
	// underpriced-rejected — auction 1 sat unsettled for hours behind
	// "have gas fee cap (100000000000) < pool minimum fee cap
	// (500000000000)". C2FLR is a faucet token; a generous cap costs
	// nothing real, while a starved cap silently halts settlement. The
	// mainnet profiles keep tighter caps on purpose (see songbird.go).
	MaxFeeCapGwei: 3000, MaxTipCapGwei: 300, MetadataConcurrency: 3,
	ProfileSource: 1, WSCoalesceMs: 50, ImageProxyConcurrency: 8,
	RateLimitTier: "testnet", ConnectRateTier: "testnet", GraphQLMaxCost: 1000,
	FaucetURL: "https://faucet.flare.network/coston2", AuditNote: "",
	AllowanceModuleAddr: allowanceModuleSingleton,
}
