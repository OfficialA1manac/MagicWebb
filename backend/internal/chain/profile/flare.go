package profile

import "time"

// flare is the Flare main network (chain 14). Mirror:
// app/src/lib/chains/flare.ts + deployments/flare.json.
var flare = Profile{
	ChainID: 14, Key: "flare", Name: "Flare", Currency: "FLR",
	Explorer: "https://flare-explorer.flare.network", Mainnet: true,
	// Primary + two verified public fallbacks (eth_chainId == 0xe checked
	// 2026-09-06). Validate() requires >= 2 on mainnets.
	DefaultRPCs: []string{
		"https://flare-api.flare.network/ext/C/rpc",
		"https://rpc.ankr.com/flare",
		"https://flare.rpc.thirdweb.com",
	},
	BlockTime: 1800 * time.Millisecond, ReorgSafety: 2, Confirmations: 1,
	PollInterval: 2 * time.Second, KeeperTick: 2 * time.Second, RefundTick: 2 * time.Second,
	MetadataTick: 30 * time.Second, OwnershipTick: 90 * time.Second, FeeSweepTick: 10 * time.Minute,
	VerifierTick: 10 * time.Minute, VerifierRecheck: 24 * time.Hour,
	GetLogsChunk: 30, GetLogsBlockCap: 30,
	// Mainnet gas caps (v3.6): 200 / 20 gwei — same rationale as songbird.go
	// (Coston2 starvation precedent; bounded worst case ≈ 0.03 FLR per settle).
	MaxFeeCapGwei: 200, MaxTipCapGwei: 20, MetadataConcurrency: 3,
	ProfileSource: 2, WSCoalesceMs: 100, ImageProxyConcurrency: 4,
	RateLimitTier: "mainnet", ConnectRateTier: "mainnet", GraphQLMaxCost: 1000,
	FaucetURL: "", AuditNote: "view-only until the security audit finishes",
	AllowanceModuleAddr: allowanceModuleSingleton,
}
