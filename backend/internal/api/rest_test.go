package api

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// clearSseConns resets the package-level sseConns map between tests so each
// test starts from a clean IP-connection slate. sync.Map lacks a Clear method,
// so we iterate and delete every key.
func clearSseConns() {
	sseConns.Range(func(k, _ any) bool {
		sseConns.Delete(k)
		return true
	})
}

// sseTestHarness bundles a running Fiber SSE server on a random port plus
// a base URL the tests can issue HTTP requests against. Close() shuts down
// the server gracefully.
type sseTestHarness struct {
	ln  net.Listener
	URL string // http://127.0.0.1:<port>
}

// newSseTestServer starts a Fiber app with the SSE handler on a random
// loopback port. Uses a real TCP server (not app.Test) because the SSE
// stream stays open forever — app.Test blocks until the body closes, which
// never happens. A real HTTP client returns response headers immediately.
func newSseTestServer(t *testing.T) *sseTestHarness {
	t.Helper()
	bcast := sse.New()
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/events", sseHandler(bcast))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		_ = app.Listener(ln)
	}()

	return &sseTestHarness{
		ln:  ln,
		URL: fmt.Sprintf("http://%s", ln.Addr().String()),
	}
}

func (h *sseTestHarness) Close() { h.ln.Close() }

// sseGet sends a GET /events to the test server with an optional
// Fly-Client-IP header and returns the response. The caller must close
// resp.Body to release the connection (which triggers the stream writer's
// error path and decrements the per-IP counter).
func sseGet(t *testing.T, h *sseTestHarness, ip string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.URL+"/events", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if ip != "" {
		req.Header.Set("Fly-Client-IP", ip)
	}
	// Timeout so the test doesn't hang if the server blocks forever.
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	return resp
}

// readClose reads the response body (up to 4KB) and closes it, returning
// the trimmed string. Only safe for synchronous responses (429, 503) —
// do NOT call on 200-OK SSE responses where the body stream never ends;
// use drainClose instead.
func readClose(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// drainClose reads a small prefix then closes the response body.
// Used for 200-OK SSE responses where the body stream stays open forever.
// The io.ReadAll error is intentionally discarded — body content is
// irrelevant for these tests; we only need status code + headers.
func drainClose(t *testing.T, resp *http.Response) {
	t.Helper()
	_ = resp.Body.Close()
}

// ── SSE per-IP cap integration tests ─────────────────────────────────────

func TestSsePerIpCapUnderLimit(t *testing.T) {
	clearSseConns()
	srv := newSseTestServer(t)
	defer srv.Close()

	resp := sseGet(t, srv, "10.0.0.1")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", cc)
	}
}

func TestSsePerIpCapExceeded(t *testing.T) {
	clearSseConns()

	// Seed 20 connections for this IP so the next request trips the cap.
	var twenty int64 = 20
	sseConns.Store("10.0.0.2", &twenty)

	srv := newSseTestServer(t)
	defer srv.Close()

	resp := sseGet(t, srv, "10.0.0.2")
	body := readClose(t, resp)

	if resp.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("Status = %d, want 429 (body=%q)", resp.StatusCode, body)
	}
	if !strings.Contains(body, "too many connections") {
		t.Fatalf("body = %q, want 'too many connections' message", body)
	}

	// Verify the counter was NOT permanently altered — the handler
	// increments then decrements on cap-exceeded, so it should still be 20.
	raw, ok := sseConns.Load("10.0.0.2")
	if !ok {
		t.Fatal("IP entry missing from sseConns after cap-exceeded request")
	}
	if n := atomic.LoadInt64(raw.(*int64)); n != 20 {
		t.Fatalf("counter = %d after cap-exceeded, want 20 (handler must roll back)", n)
	}
}

func TestSsePerIpCapDifferentIPs(t *testing.T) {
	clearSseConns()

	// Saturate IP A.
	var twenty int64 = 20
	sseConns.Store("10.0.0.10", &twenty)

	srv := newSseTestServer(t)
	defer srv.Close()

	// IP B (different IP) should succeed.
	resp := sseGet(t, srv, "10.0.0.20")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Status = %d, want 200 for different IP", resp.StatusCode)
	}

	// Confirm IP A is unchanged.
	rawA, _ := sseConns.Load("10.0.0.10")
	if n := atomic.LoadInt64(rawA.(*int64)); n != 20 {
		t.Fatalf("IP A counter = %d, want 20 (must be unmodified)", n)
	}
}

func TestSsePerIpCapTwentyOneSequential(t *testing.T) {
	clearSseConns()
	srv := newSseTestServer(t)
	defer srv.Close()

	// Simulate 20 sequential connections from the same IP —
	// each increments the atomic counter. Because the SSE stream
	// writer blocks on the Broadcaster channel (no messages published
	// in tests), the deferred counter-decrement never runs. The 21st
	// request should therefore trip the cap at 21 > 20.
	for i := 0; i < ssePerIPLimit; i++ {
		resp := sseGet(t, srv, "10.0.1.1")
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("request %d: Status = %d, want 200", i+1, resp.StatusCode)
		}
		drainClose(t, resp)
	}

	// 21st request — cap hit.
	resp := sseGet(t, srv, "10.0.1.1")
	body := readClose(t, resp)
	if resp.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("request %d: Status = %d, want 429 (body=%q)", ssePerIPLimit+1, resp.StatusCode, body)
	}
}

func TestSsePerIpCapSaturatesAtLimit(t *testing.T) {
	clearSseConns()
	srv := newSseTestServer(t)
	defer srv.Close()

	// Exactly at the limit (no seeding).
	raw, _ := sseConns.LoadOrStore("10.0.2.1", new(int64))
	cnt := raw.(*int64)
	atomic.StoreInt64(cnt, ssePerIPLimit)

	resp := sseGet(t, srv, "10.0.2.1")
	body := readClose(t, resp)

	if resp.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("at-limit Status = %d, want 429 (body=%q)", resp.StatusCode, body)
	}
}

func TestSsePerIpCapOneBelowLimit(t *testing.T) {
	clearSseConns()
	srv := newSseTestServer(t)
	defer srv.Close()

	// One below the limit.
	raw, _ := sseConns.LoadOrStore("10.0.3.1", new(int64))
	cnt := raw.(*int64)
	atomic.StoreInt64(cnt, ssePerIPLimit-1)

	resp := sseGet(t, srv, "10.0.3.1")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("one-below Status = %d, want 200", resp.StatusCode)
	}
}

func TestSsePerIpCapNoHeadersUsesCIP(t *testing.T) {
	clearSseConns()
	srv := newSseTestServer(t)
	defer srv.Close()

	// Without any IP headers, ClientIP() falls back to c.IP() which
	// returns the loopback address. The first request must succeed.
	resp := sseGet(t, srv, "") // no Fly-Client-IP header
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no-header Status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
}

func TestSsePerIpCapHeaderHierarchy(t *testing.T) {
	clearSseConns()

	// When Fly-Client-IP is absent but X-Forwarded-For is present,
	// ClientIP() picks the rightmost XFF entry. Saturate that IP.
	var twenty int64 = 20
	sseConns.Store("192.168.1.1", &twenty)

	srv := newSseTestServer(t)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/events", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Forwarded-For", "10.0.0.1, 192.168.1.1")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	body := readClose(t, resp)

	if resp.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("Status = %d, want 429 via XFF rightmost (body=%q)", resp.StatusCode, body)
	}
}

func TestSsePerIpCapFlyClientIpBeatsXFF(t *testing.T) {
	clearSseConns()

	// Saturate via Fly-Client-IP.
	var twenty int64 = 20
	sseConns.Store("172.16.0.1", &twenty)

	srv := newSseTestServer(t)
	defer srv.Close()

	// Sending both headers: Fly-Client-IP takes priority per the
	// trust hierarchy, so the cap should trigger on 172.16.0.1
	// even though the rightmost XFF is a fresh IP.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/events", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Fly-Client-IP", "172.16.0.1")
	req.Header.Set("X-Forwarded-For", "fresh-ip-only")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	body := readClose(t, resp)

	if resp.StatusCode != fiber.StatusTooManyRequests {
		t.Fatalf("Status = %d, want 429 (Fly-Client-IP must beat XFF) (body=%q)", resp.StatusCode, body)
	}
}
