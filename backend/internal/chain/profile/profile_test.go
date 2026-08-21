package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
	}
}

// The frontend carries the identity half of this table (app/src/lib/chains.ts)
// and the repo records deployments per key (deployments/<key>.json). Both
// must agree with the Go profile or the network switcher, explorer links and
// currency labels drift between server and browser.
func TestParityWithFrontendAndDeployments(t *testing.T) {
	root := repoRoot(t)
	ts, err := os.ReadFile(filepath.Join(root, "app", "src", "lib", "chains.ts"))
	if err != nil {
		t.Skipf("chains.ts not found: %v", err)
	}
	src := string(ts)
	for _, p := range All() {
		// 114: { id: 114, key: 'coston2', name: 'Flare Coston2', currency: 'C2FLR', explorer: '…', rpc: '…', …
		re := regexp.MustCompile(`(?m)^\s*` + itoa(p.ChainID) + `:\s*\{\s*id:\s*` + itoa(p.ChainID) + `,\s*key:\s*'` + p.Key + `',\s*name:\s*'` + regexp.QuoteMeta(p.Name) + `',\s*currency:\s*'` + p.Currency + `',\s*explorer:\s*'` + regexp.QuoteMeta(p.Explorer) + `',\s*rpc:\s*'` + regexp.QuoteMeta(p.DefaultRPCs[0]) + `'`)
		if !re.MatchString(src) {
			t.Errorf("app/src/lib/chains.ts out of sync for chain %d (%s): expected name=%q currency=%q explorer=%q rpc=%q", p.ChainID, p.Key, p.Name, p.Currency, p.Explorer, p.DefaultRPCs[0])
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
				Primary string `json:"primary"`
			} `json:"rpc"`
		}
		if err := json.Unmarshal(b, &d); err != nil {
			t.Errorf("deployments/%s.json: %v", p.Key, err)
			continue
		}
		if d.ChainID != p.ChainID || d.Explorer != p.Explorer || d.RPC.Primary != p.DefaultRPCs[0] {
			t.Errorf("deployments/%s.json disagrees with profile: %+v vs chainId=%d explorer=%s rpc=%s", p.Key, d, p.ChainID, p.Explorer, p.DefaultRPCs[0])
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
