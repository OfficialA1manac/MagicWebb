package media

import (
	"context"
	"math/big"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestResolveURI_IPFS(t *testing.T) {
	got := ResolveURI("ipfs://QmTest123", "1")
	want := ipfsGateways[0] + "QmTest123"
	if got != want {
		t.Fatalf("ResolveURI ipfs = %q, want %q", got, want)
	}
}

func TestResolveURI_BareCID(t *testing.T) {
	cid := "QmYwAPJzv5CZsnA625s3Xf2nemtYgPp88kkX5h4N6y3F1"
	got := ResolveURI(cid, "1")
	if got != ipfsGateways[0]+cid {
		t.Fatalf("bare CID = %q, want gateway prefix", got)
	}
}

func TestResolveURI_ERC1155Placeholder(t *testing.T) {
	id := big.NewInt(42)
	padded := make([]byte, 32)
	id.FillBytes(padded)
	got := ResolveURI("ipfs://base/{id}.json", "42")
	if got == "" || got == "ipfs://base/{id}.json" {
		t.Fatalf("placeholder not replaced: %q", got)
	}
}

func TestProxyAllowed_BlocksPrivate(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1/secret",
		"http://10.0.0.1/x",
		"http://172.16.0.1/x",
		"http://192.168.1.1/x",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]/secret",
		"http://[fc00::1]/secret",
		"http://localhost/secret",
		"http://metadata/secret",
		"http://example.local/secret",
	}
	for _, raw := range blocked {
		if ProxyAllowed(raw) {
			t.Fatalf("ProxyAllowed(%q) = true, want false", raw)
		}
	}
	allowed := []string{
		"https://ipfs.io/ipfs/QmTest",
		"http://172.32.0.1/x",
		"http://93.184.216.34/x",
	}
	for _, raw := range allowed {
		if !ProxyAllowed(raw) {
			t.Fatalf("ProxyAllowed(%q) = false, want true", raw)
		}
	}
}

func TestResolveCandidates_MultipleGateways(t *testing.T) {
	cands := ResolveCandidates("ipfs://QmABC", "1")
	if len(cands) < 2 {
		t.Fatalf("expected multiple gateway candidates, got %d", len(cands))
	}
}

func TestSafeDialContext_BlocksLoopbackAndPrivate(t *testing.T) {
	for _, addr := range []string{
		"127.0.0.1:80",
		"10.0.0.1:443",
		"172.16.0.1:443",
		"192.168.1.1:443",
		"169.254.169.254:80",
		"[::1]:443",
		"[fc00::1]:80",
	} {
		if _, err := safeDialContext(context.Background(), "tcp", addr); err == nil {
			t.Fatalf("safeDialContext(%q) must be blocked", addr)
		}
	}
}

func TestSafeDialContext_BlocksLocalhostAndLocalDomains(t *testing.T) {
	for _, addr := range []string{
		"localhost:80",
		"example.local:80",
		"my-service.lan:443",
	} {
		if _, err := safeDialContext(context.Background(), "tcp", addr); err == nil {
			t.Fatalf("safeDialContext(%q) must be blocked", addr)
		}
	}
}

// fakeResolver returns a fixed set of IPs for any host — used to prove that
// safeDialContext re-validates at dial time even after ProxyAllowedContext
// already approved the host.
type fakeResolver struct {
	addrs []netip.Addr
	err   error
}

func (f *fakeResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f.addrs, f.err
}

func TestSafeDialContext_BlocksHostThatResolvesPrivate(t *testing.T) {
	orig := dialResolver
	defer func() { dialResolver = orig }()
	dialResolver = &fakeResolver{addrs: []netip.Addr{
		netip.MustParseAddr("93.184.216.34"),
		netip.MustParseAddr("127.0.0.1"),
	}}
	if _, err := safeDialContext(context.Background(), "tcp", "attacker.example:443"); err == nil {
		t.Fatal("mixed public/private DNS response must be blocked")
	}
}

func TestSafeDialContext_RejectsEmptyDNSResult(t *testing.T) {
	orig := dialResolver
	defer func() { dialResolver = orig }()
	dialResolver = &fakeResolver{addrs: nil, err: nil}
	if _, err := safeDialContext(context.Background(), "tcp", "empty.example:443"); err == nil {
		t.Fatal("an empty DNS response must be refused")
	}
}

func TestSafeDialContext_AllowsResolvedPublicHost(t *testing.T) {
	// Use a TEST-NET-1 (RFC 5737) address: not flagged as private/loopback/etc.
	// by Go's netip, but reserved and unrouteable on the public internet — so
	// we provably do NOT open a real outbound connection in CI.
	orig := dialResolver
	defer func() { dialResolver = orig }()
	dialResolver = &fakeResolver{addrs: []netip.Addr{
		netip.MustParseAddr("192.0.2.1"),
	}}
	tctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := safeDialContext(tctx, "tcp", "good.example:443")
	if err == nil {
		t.Skip("unexpectedly connected to 192.0.2.1 — skipping")
	}
	if strings.Contains(err.Error(), "dial blocked") || strings.Contains(err.Error(), "disallowed") {
		t.Fatalf("non-private IP was mis-blocked: %v", err)
	}
}
