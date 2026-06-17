package media

import (
	"context"
	"errors"
	"math/big"
	"net"
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

// fakeDialer replaces the package dialTCP during a test. It records every
// addr it is asked to connect to. Behaviour:
//   - failAll=true  → every attempt fails (use to test errors.Join aggregation)
//   - failFirst=true → only the FIRST attempt fails; subsequent ones succeed
//     (use to test Happy Eyeballs fallback)
//   - both false    → every attempt succeeds (use to test short-circuit on
//     first-IP success)
//
// Success returns an inertConn so callers see an immediate happy path
// without touching the OS dialer.
type fakeDialer struct {
	attempts  []string
	failAll   bool
	failFirst bool
}

func (f *fakeDialer) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	f.attempts = append(f.attempts, addr)
	if f.failAll {
		return nil, errors.New("simulated always-unreachable")
	}
	if f.failFirst && len(f.attempts) == 1 {
		return nil, errors.New("simulated unreachable")
	}
	return inertConn{r: strings.NewReader(""), w: &strings.Builder{}, addr: addr}, nil
}

type inertConn struct {
	r    *strings.Reader
	w    *strings.Builder
	addr string
}

func (c inertConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c inertConn) Write(p []byte) (int, error) { return c.w.Write(p) }
func (c inertConn) Close() error                { return nil }
func (c inertConn) LocalAddr() net.Addr         { return dummyAddr(c.addr) }
func (c inertConn) RemoteAddr() net.Addr        { return dummyAddr(c.addr) }
func (c inertConn) SetDeadline(time.Time) error { return nil }
func (c inertConn) SetReadDeadline(time.Time) error  { return nil }
func (c inertConn) SetWriteDeadline(time.Time) error { return nil }

type dummyAddr string

func (d dummyAddr) Network() string { return "tcp" }
func (d dummyAddr) String() string  { return string(d) }

func TestSafeDialContext_HappyPathStopsOnFirstSuccess(t *testing.T) {
	// First IP succeeds → the loop short-circuits, only ONE dial attempt.
	// This pins down the "Don't burn a second TCP attempt on a green first
	// IP" optimization the loop must keep.
	origR := dialResolver
	origD := dialTCP
	defer func() { dialResolver = origR; dialTCP = origD }()

	dialResolver = &fakeResolver{addrs: []netip.Addr{
		netip.MustParseAddr("192.0.2.1"),
		netip.MustParseAddr("192.0.2.2"),
	}}
	fd := &fakeDialer{} // succeed every attempt
	dialTCP = fd.dial

	conn, err := safeDialContext(context.Background(), "tcp", "good.example:443")
	if err != nil {
		t.Fatalf("first-IP success path should produce nil err: %v", err)
	}
	if conn == nil {
		t.Fatal("expected non-nil conn from happy path")
	}
	if len(fd.attempts) != 1 {
		t.Fatalf("first-IP success should dial only once, got %d: %v", len(fd.attempts), fd.attempts)
	}
	if fd.attempts[0] != "192.0.2.1:443" {
		t.Fatalf("expected first dial to be 192.0.2.1:443, got: %v", fd.attempts)
	}
}

func TestSafeDialContext_FallsBackAcrossVettedIPs(t *testing.T) {
	origR := dialResolver
	origD := dialTCP
	defer func() { dialResolver = origR; dialTCP = origD }()

	// Two-cap-record set: both TEST-NET-1 (public-by-Go-netip, unrouteable),
	// but fakeDialer succeeds so we don't touch the OS dialer.
	dialResolver = &fakeResolver{addrs: []netip.Addr{
		netip.MustParseAddr("192.0.2.1"),
		netip.MustParseAddr("192.0.2.2"),
	}}
	fd := &fakeDialer{failFirst: true} // first IP "unreachable"; second succeeds
	dialTCP = fd.dial

	conn, err := safeDialContext(context.Background(), "tcp", "good.example:443")
	if err != nil {
		t.Fatalf("two-IP fallback should succeed on second IP: %v", err)
	}
	if conn == nil {
		t.Fatal("expected non-nil conn from second-IP success")
	}
	if len(fd.attempts) != 2 {
		t.Fatalf("expected 2 dial attempts (one per vetted IP), got %d: %v", len(fd.attempts), fd.attempts)
	}
	// Order matters: pre-iteration ordering preserves DNS order, which lets
	// Cloudflare's IPv6-first responses still find v4 if v6 is unreachable.
	if fd.attempts[0] != "192.0.2.1:443" || fd.attempts[1] != "192.0.2.2:443" {
		t.Fatalf("expected dial order 192.0.2.1:443 then 192.0.2.2:443, got: %v", fd.attempts)
	}
}

func TestSafeDialContext_AggregatesErrorsPerIP(t *testing.T) {
	origR := dialResolver
	origD := dialTCP
	defer func() { dialResolver = origR; dialTCP = origD }()

	dialResolver = &fakeResolver{addrs: []netip.Addr{
		netip.MustParseAddr("192.0.2.1"),
		netip.MustParseAddr("192.0.2.2"),
	}}
	fd := &fakeDialer{failAll: true}
	dialTCP = fd.dial

	_, err := safeDialContext(context.Background(), "tcp", "good.example:443")
	if err == nil {
		t.Fatal("expected an aggregate error when both IPs fail")
	}
	if len(fd.attempts) != 2 {
		t.Fatalf("expected both IPs to be tried, got %d attempts: %v", len(fd.attempts), fd.attempts)
	}
	msg := err.Error()
	if !strings.Contains(msg, "192.0.2.1") || !strings.Contains(msg, "192.0.2.2") {
		t.Fatalf("errors.Join result should name every IP attempted, got: %v", err)
	}
}
