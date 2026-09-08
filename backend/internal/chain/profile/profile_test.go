package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestForAndAll(t *testing.T) {
	for _, id := range []uint64{114, 19, 14} {
		p, err := For(id)
		if err != nil {
			t.Fatalf("For(%d): %v", id, err)
		}
		if p.ChainID != id || p.Key == "" || p.Name == "" || p.Currency == "" || len(p.DefaultRPCs) == 0 {
			t.Fatalf("profile %d incomplete: %+v", id, p)
		}
		if p.ReorgSafety == 0 || p.PollInterval == 0 || p.GetLogsBlockCap == 0 || p.KeeperTick == 0 {
			t.Fatalf("profile %d has zero tuning: %+v", id, p)
		}
		if strings.HasSuffix(p.Explorer, "/") {
			t.Fatalf("explorer must not end with /: %s", p.Explorer)
		}
	}
	if _, err := For(1); err == nil {
		t.Fatal("chain 1 must be unsupported")
	}
	if got := All(); len(got) != 3 || got[0].ChainID != 114 || got[1].ChainID != 19 || got[2].ChainID != 14 {
		t.Fatalf("All() order: %+v", got)
	}
}

func TestMainnetsAreStricterThanTestnet(t *testing.T) {
	c2 := MustFor(114)
	for _, id := range []uint64{19, 14} {
		m := MustFor(id)
		if !m.Mainnet || c2.Mainnet {
			t.Fatalf("Mainnet flag wrong: %d=%v coston2=%v", id, m.Mainnet, c2.Mainnet)
		}
		if m.MaxFeeCapGwei >= c2.MaxFeeCapGwei || m.MaxTipCapGwei >= c2.MaxTipCapGwei {
			t.Fatalf("mainnet %d gas caps must be below testnet", id)
		}
		if m.ReorgSafety > c2.ReorgSafety {
			t.Fatalf("mainnet %d has single-slot finality; reorg safety must not exceed testnet", id)
		}
		if m.RateLimitTier != "mainnet" || m.ConnectRateTier != "mainnet" {
			t.Fatalf("mainnet %d tiers must be mainnet: %q/%q", id, m.RateLimitTier, m.ConnectRateTier)
		}
	}
	if c2.RateLimitTier != "testnet" || c2.ConnectRateTier != "testnet" {
		t.Fatalf("coston2 tiers must be testnet: %q/%q", c2.RateLimitTier, c2.ConnectRateTier)
	}
}

// v3.6 wave 6: every shipped profile passes Validate, and the specific
// invariants Validate exists for are enforced (mainnet ≥2 RPCs, non-zero gas
// caps, tier spelling, faucet presence, allowance address shape).
func TestValidateShippedProfiles(t *testing.T) {
	if err := ValidateAll(); err != nil {
		t.Fatalf("ValidateAll: %v", err)
	}
	for _, id := range []uint64{19, 14} {
		if n := len(MustFor(id).DefaultRPCs); n < 2 {
			t.Fatalf("mainnet %d must ship >= 2 RPCs, has %d", id, n)
		}
	}
	// Mainnet gas caps: 2000/200 (2026-09-08). eth_getBlockByNumber reports a
	// 500 gwei base fee and a 150 gwei priority fee on Songbird, Flare AND
	// Coston2; the earlier 200/20 clamped feeCap below the base fee, which is
	// exactly the Coston2 2026-08-31 starvation. 4x headroom over the base fee,
	// still below Coston2's 3000/300 (TestMainnetsAreStricterThanTestnet).
	for _, id := range []uint64{19, 14} {
		if p := MustFor(id); p.MaxFeeCapGwei != 2000 || p.MaxTipCapGwei != 200 {
			t.Fatalf("mainnet %d caps: want 2000/200, got %v/%v", id, p.MaxFeeCapGwei, p.MaxTipCapGwei)
		}
	}
	// One Allowance Module singleton across the family.
	for _, p := range All() {
		if p.AllowanceModuleAddr != allowanceModuleSingleton {
			t.Fatalf("%s allowance module drifted: %s", p.Key, p.AllowanceModuleAddr)
		}
	}
}

func TestValidateRejectsBadProfiles(t *testing.T) {
	base := MustFor(19)
	cases := map[string]func(p *Profile){
		"single RPC on mainnet":  func(p *Profile) { p.DefaultRPCs = p.DefaultRPCs[:1] },
		"no RPCs":                func(p *Profile) { p.DefaultRPCs = nil },
		"http RPC":               func(p *Profile) { p.DefaultRPCs = []string{"http://a", "https://b"} },
		"hostless RPC":           func(p *Profile) { p.DefaultRPCs = []string{"https://", "https://b"} },
		"bare host explorer":     func(p *Profile) { p.Explorer = "songbird-explorer.flare.network" },
		"zero fee cap":           func(p *Profile) { p.MaxFeeCapGwei = 0 },
		"zero tip cap":           func(p *Profile) { p.MaxTipCapGwei = 0 },
		"fee cap below tip cap":  func(p *Profile) { p.MaxFeeCapGwei = 1; p.MaxTipCapGwei = 2 },
		"zero keeper tick":       func(p *Profile) { p.KeeperTick = 0 },
		"zero block time":        func(p *Profile) { p.BlockTime = 0 },
		"zero confirmations":     func(p *Profile) { p.Confirmations = 0 },
		"bad rate tier":          func(p *Profile) { p.RateLimitTier = "prod" },
		"bad connect tier":       func(p *Profile) { p.ConnectRateTier = "" },
		"faucet on mainnet":      func(p *Profile) { p.FaucetURL = "https://faucet" },
		"trailing slash explorer": func(p *Profile) { p.Explorer += "/" },
		"empty currency":         func(p *Profile) { p.Currency = "" },
		"bad allowance address":  func(p *Profile) { p.AllowanceModuleAddr = "0x123" },
		"zero graphql cost":      func(p *Profile) { p.GraphQLMaxCost = 0 },
	}
	for name, mut := range cases {
		p := base
		p.DefaultRPCs = append([]string(nil), base.DefaultRPCs...)
		mut(&p)
		if err := p.Validate(); err == nil {
			t.Errorf("%s: Validate should fail", name)
		}
	}
	// Testnet without a faucet is also wrong.
	tn := MustFor(114)
	tn.FaucetURL = ""
	if err := tn.Validate(); err == nil {
		t.Error("testnet without faucet: Validate should fail")
	}
	// Sanity: the base passes and a tick edit that stays > 0 passes.
	ok := base
	ok.KeeperTick = 3 * time.Second
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid tweak rejected: %v", err)
	}
}

// The frontend carries the identity half of this table, one file per network
// (app/src/lib/chains/<key>.ts), and the repo records deployments per key
// (deployments/<key>.json). Both must agree with the Go profile or the network
// switcher, explorer links, currency labels and RPC lists drift between server
// and browser.
func TestParityWithFrontendAndDeployments(t *testing.T) {
	root := repoRoot(t)
	for _, p := range All() {
		ts, err := os.ReadFile(filepath.Join(root, "app", "src", "lib", "chains", p.Key+".ts"))
		if err != nil {
			t.Errorf("app/src/lib/chains/%s.ts missing: %v", p.Key, err)
			continue
		}
		src := string(ts)
		want := map[string]string{
			`id:\s*` + itoa(p.ChainID):                                      "id",
			`key:\s*'` + p.Key + `'`:                                         "key",
			`name:\s*'` + regexp.QuoteMeta(p.Name) + `'`:                     "name",
			`currency:\s*'` + p.Currency + `'`:                               "currency",
			`explorer:\s*'` + regexp.QuoteMeta(p.Explorer) + `'`:             "explorer",
			`rpc:\s*'` + regexp.QuoteMeta(p.DefaultRPCs[0]) + `'`:            "rpc",
			`blockTimeMs:\s*` + itoa(uint64(p.BlockTime/time.Millisecond)):   "blockTimeMs",
			`confirmations:\s*` + itoa(p.Confirmations):                      "confirmations",
			`testnet:\s*` + map[bool]string{true: "false", false: "true"}[p.Mainnet]: "testnet",
		}
		for re, field := range want {
			if !regexp.MustCompile(re).MatchString(src) {
				t.Errorf("app/src/lib/chains/%s.ts: field %s out of sync with the Go profile (want /%s/)", p.Key, field, re)
			}
		}
		for _, fb := range p.DefaultRPCs[1:] {
			if !strings.Contains(src, "'"+fb+"'") {
				t.Errorf("app/src/lib/chains/%s.ts: rpcFallbacks missing %s", p.Key, fb)
			}
		}

		b, err := os.ReadFile(filepath.Join(root, "deployments", p.Key+".json"))
		if err != nil {
			t.Errorf("deployments/%s.json missing: %v", p.Key, err)
			continue
		}
		var d struct {
			ChainID  uint64 `json:"chainId"`
			Explorer string `json:"explorer"`
			RPC      struct {
				Primary   string   `json:"primary"`
				Fallbacks []string `json:"fallbacks"`
			} `json:"rpc"`
		}
		if err := json.Unmarshal(b, &d); err != nil {
			t.Errorf("deployments/%s.json: %v", p.Key, err)
			continue
		}
		if d.ChainID != p.ChainID || d.Explorer != p.Explorer || d.RPC.Primary != p.DefaultRPCs[0] {
			t.Errorf("deployments/%s.json disagrees with profile: %+v vs chainId=%d explorer=%s rpc=%s", p.Key, d, p.ChainID, p.Explorer, p.DefaultRPCs[0])
		}
		if strings.Join(d.RPC.Fallbacks, ",") != strings.Join(p.DefaultRPCs[1:], ",") {
			t.Errorf("deployments/%s.json rpc.fallbacks %v != profile fallbacks %v", p.Key, d.RPC.Fallbacks, p.DefaultRPCs[1:])
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for range 6 {
		if _, err := os.Stat(filepath.Join(dir, "deployments", "schema.json")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Skip("repo root not found")
	return ""
}

func itoa(u uint64) string {
	b, _ := json.Marshal(u)
	return string(b)
}
