package config

import "testing"

func find(t *testing.T, ns []Network, chainID uint64) Network {
	t.Helper()
	for _, n := range ns {
		if n.ChainID == chainID {
			return n
		}
	}
	t.Fatalf("chain %d missing from switcher", chainID)
	return Network{}
}

// The default an operator gets by setting nothing must tell the truth: only
// Coston2 has contracts deployed, so the other two are destinations that do not
// exist yet and must render disabled rather than linking to a dead app.
func TestBuildNetworksUnsetMarksOthersUnavailable(t *testing.T) {
	ns := buildNetworks("", "", 114, TradingLive)
	if len(ns) != 3 {
		t.Fatalf("got %d networks, want 3", len(ns))
	}
	if c := find(t, ns, 114); !c.Current || !c.Available {
		t.Fatalf("coston2 = %+v, want current and available", c)
	}
	for _, id := range []uint64{19, 14} {
		if n := find(t, ns, id); n.Available {
			t.Fatalf("chain %d = %+v, want unavailable", id, n)
		}
	}
}

// The current network stays available even when NETWORK_URLS omits it — the
// user is already on it, and a missing self-entry is a typo rather than a
// reason to grey out the page being viewed.
func TestBuildNetworksCurrentIsAlwaysAvailable(t *testing.T) {
	ns := buildNetworks("19=https://magicwebb-songbird.fly.dev", "19=browse-only", 114, TradingLive)
	if c := find(t, ns, 114); !c.Available || c.URL != "" {
		t.Fatalf("coston2 = %+v, want available with no URL", c)
	}
	if s := find(t, ns, 19); !s.Available || s.URL != "https://magicwebb-songbird.fly.dev" {
		t.Fatalf("songbird = %+v, want available with its URL", s)
	}
}

func TestBuildNetworksParsing(t *testing.T) {
	ns := buildNetworks(" 114 = https://a.example/ , 19=https://b.example ,19=,bogus,xx=https://c.example ", "", 114, TradingLive)

	// Trailing slash stripped so template concatenation never doubles it.
	if got := find(t, ns, 114).URL; got != "https://a.example" {
		t.Fatalf("coston2 URL = %q, want trailing slash stripped", got)
	}
	// A later empty origin must not erase an earlier valid one.
	if got := find(t, ns, 19).URL; got != "https://b.example" {
		t.Fatalf("songbird URL = %q, want the non-empty entry to win", got)
	}
	// Malformed entries are skipped, not fatal — a typo in one entry must not
	// take the switcher down.
	if n := find(t, ns, 14); n.Available {
		t.Fatalf("flare = %+v, want unavailable", n)
	}
}

// Trading status: the current network reports its own (from
// ContractsDeployed), siblings come from NETWORK_TRADING, anything else is
// unknown (empty) rather than a guess. Unknown values are dropped, not fatal.
func TestBuildNetworksTrading(t *testing.T) {
	ns := buildNetworks("19=https://b.example,14=https://c.example", "114=browse-only,19=browse-only,14=bogus", 114, TradingLive)
	if got := find(t, ns, 114).Trading; got != TradingLive {
		t.Fatalf("current trading = %q, want %q (never from NETWORK_TRADING)", got, TradingLive)
	}
	if got := find(t, ns, 19).Trading; got != TradingBrowseOnly {
		t.Fatalf("songbird trading = %q, want %q", got, TradingBrowseOnly)
	}
	if got := find(t, ns, 14).Trading; got != "" {
		t.Fatalf("flare trading = %q, want empty for an invalid value", got)
	}
}

func TestRateLimitForTier(t *testing.T) {
	if got := rateLimitForTier("testnet"); got != 120 {
		t.Fatalf("testnet = %d, want 120", got)
	}
	for _, tier := range []string{"mainnet", "", "other"} {
		if got := rateLimitForTier(tier); got != 60 {
			t.Fatalf("%q = %d, want 60", tier, got)
		}
	}
}
