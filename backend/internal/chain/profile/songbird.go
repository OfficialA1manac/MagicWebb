package profile

import "time"

// allowanceModuleSingleton is the Zodiac Allowance Module v0.62.0, deployed
// at the same CREATE2 address on Flare (14), Songbird (19) and Coston2 (114).
// Source: https://github.com/gnosisguild/zodiac/blob/master/contracts/allowance/AllowanceModule.sol
// The fee sweeper checks bytecode at this address before its first sweep.
const allowanceModuleSingleton = "0xCFbFaC74C26F8647cBDb8c5caf80BB5b32E43134"

// songbird is Flare's canary network (chain 19). Mirror:
// app/src/lib/chains/songbird.ts + deployments/songbird.json.
var songbird = Profile{
	ChainID: 19, Key: "songbird", Name: "Songbird", Currency: "SGB",
	Explorer: "https://songbird-explorer.flare.network", Mainnet: true,
	// Primary + one verified public fallback (eth_chainId == 0x13 checked
	// 2026-09-06). Validate() requires >= 2 on mainnets.
	DefaultRPCs: []string{
		"https://songbird-api.flare.network/ext/C/rpc",
		"https://rpc.ankr.com/flare_songbird",
	},
	BlockTime: 1800 * time.Millisecond, ReorgSafety: 2, Confirmations: 1,
	PollInterval: 2 * time.Second, KeeperTick: 2 * time.Second, RefundTick: 2 * time.Second,
	MetadataTick: 30 * time.Second, OwnershipTick: 90 * time.Second, FeeSweepTick: 10 * time.Minute,
	VerifierTick: 10 * time.Minute, VerifierRecheck: 24 * time.Hour,
	GetLogsChunk: 30, GetLogsBlockCap: 30,
	// Mainnet gas caps (v3.6): 200 / 20 gwei. The earlier 60 / 3 came from
	// quiet-hour readings; the Coston2 starvation precedent (coston2.go)
	// showed that a cap below the pool's minimum fee makes every keeper tx
	// "underpriced" and silently stops settlement. Songbird's suggested fee
	// sits at 25-50 gwei with spikes past 100, so 200 keeps the keeper
	// mineable through a spike while still bounding the worst case
	// (200 gwei × ~150k gas ≈ 0.03 SGB per settle). The underpriced counter
	// (keeper_gas.go) alerts when the cap is below the suggestion for 3 ticks.
	MaxFeeCapGwei: 200, MaxTipCapGwei: 20, MetadataConcurrency: 3,
	ProfileSource: 3, WSCoalesceMs: 100, ImageProxyConcurrency: 4,
	RateLimitTier: "mainnet", ConnectRateTier: "mainnet", GraphQLMaxCost: 1000,
	FaucetURL: "", AuditNote: "view-only until the security audit finishes",
	AllowanceModuleAddr: allowanceModuleSingleton,
}
