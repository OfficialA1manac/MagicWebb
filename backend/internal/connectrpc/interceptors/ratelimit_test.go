package interceptors

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/ratelimit"
)

// newRLRequest creates a connect.AnyRequest for rate limit testing with the
// given HTTP headers. Uses emptypb.Empty as the underlying proto message so
// the connect package can create the request (AnyRequest has unexported methods
// that prevent custom implementations).
func newRLRequest(headers http.Header) *connect.Request[emptypb.Empty] {
	req := connect.NewRequest(&emptypb.Empty{})
	for k, vs := range headers {
		for _, v := range vs {
			req.Header().Add(k, v)
		}
	}
	return req
}

// ── DefaultRateLimits ────────────────────────────────────────────────────────────

func TestDefaultRateLimits_CoversAllProcedures(t *testing.T) {
	tiers := DefaultRateLimits()

	expected := []string{
		"/marketplace.v1.MarketplaceService/GetListing",
		"/marketplace.v1.MarketplaceService/GetAuction",
		"/marketplace.v1.MarketplaceService/GetOffer",
		"/marketplace.v1.MarketplaceService/GetToken",
		"/marketplace.v1.MarketplaceService/ListCollections",
		"/marketplace.v1.MarketplaceService/GetCollection",
		"/marketplace.v1.MarketplaceService/ListListings",
		"/marketplace.v1.MarketplaceService/ListAuctions",
		"/marketplace.v1.MarketplaceService/ListOffers",
		"/marketplace.v1.MarketplaceService/GetActivity",
		"/marketplace.v1.MarketplaceService/GetWalletNFTs",
		"/marketplace.v1.MarketplaceService/GetProfile",
		"/marketplace.v1.MarketplaceService/Search",
		"/marketplace.v1.MarketplaceService/GetMetrics",
	}
	for _, proc := range expected {
		if _, ok := tiers[proc]; !ok {
			t.Errorf("DefaultRateLimits missing procedure %q", proc)
		}
	}
	if len(tiers) != 14 {
		t.Errorf("DefaultRateLimits has %d entries, want 14", len(tiers))
	}
}

func TestDefaultRateLimits_ListEndpointsAre120PerMin(t *testing.T) {
	tiers := DefaultRateLimits()
	listProcs := []string{
		"/marketplace.v1.MarketplaceService/ListCollections",
		"/marketplace.v1.MarketplaceService/ListListings",
		"/marketplace.v1.MarketplaceService/ListAuctions",
		"/marketplace.v1.MarketplaceService/ListOffers",
		"/marketplace.v1.MarketplaceService/GetActivity",
	}
	for _, proc := range listProcs {
		tier := tiers[proc]
		if tier.Limit != 120 {
			t.Errorf("%s limit = %d, want 120", proc, tier.Limit)
		}
		if tier.Window != time.Minute {
			t.Errorf("%s window = %v, want 1m", proc, tier.Window)
		}
	}
}

func TestDefaultRateLimits_SearchIs30PerMin(t *testing.T) {
	tiers := DefaultRateLimits()
	proc := "/marketplace.v1.MarketplaceService/Search"
	tier := tiers[proc]
	if tier.Limit != 30 {
		t.Errorf("Search limit = %d, want 30", tier.Limit)
	}
	if tier.Window != time.Minute {
		t.Errorf("Search window = %v, want 1m", tier.Window)
	}
}

func TestDefaultRateLimits_StandardEndpointsAre60PerMin(t *testing.T) {
	tiers := DefaultRateLimits()
	stdProcs := []string{
		"/marketplace.v1.MarketplaceService/GetListing",
		"/marketplace.v1.MarketplaceService/GetAuction",
		"/marketplace.v1.MarketplaceService/GetOffer",
		"/marketplace.v1.MarketplaceService/GetToken",
		"/marketplace.v1.MarketplaceService/GetCollection",
		"/marketplace.v1.MarketplaceService/GetProfile",
		"/marketplace.v1.MarketplaceService/GetWalletNFTs",
		"/marketplace.v1.MarketplaceService/GetMetrics",
	}
	for _, proc := range stdProcs {
		tier := tiers[proc]
		if tier.Limit != 60 {
			t.Errorf("%s limit = %d, want 60", proc, tier.Limit)
		}
		if tier.Window != time.Minute {
			t.Errorf("%s window = %v, want 1m", proc, tier.Window)
		}
	}
}

// ── clientIPFromRequest ──────────────────────────────────────────────────────────

func TestClientIPFromRequest_FlyClientIP(t *testing.T) {
	req := newRLRequest(http.Header{"Fly-Client-Ip": {"1.2.3.4"}})
	if ip := clientIPFromRequest(req); ip != "1.2.3.4" {
		t.Errorf("Fly-Client-IP = %q, want 1.2.3.4", ip)
	}
}

func TestClientIPFromRequest_Forwarded(t *testing.T) {
	req := newRLRequest(http.Header{"Forwarded": {"for=10.0.0.1:12345;proto=https"}})
	if ip := clientIPFromRequest(req); ip != "10.0.0.1" {
		t.Errorf("Forwarded for= = %q, want 10.0.0.1", ip)
	}
}

func TestClientIPFromRequest_ForwardedQuoted(t *testing.T) {
	req := newRLRequest(http.Header{"Forwarded": {"for=\"192.168.1.1\""}})
	if ip := clientIPFromRequest(req); ip != "192.168.1.1" {
		t.Errorf("Forwarded quoted = %q, want 192.168.1.1", ip)
	}
}

func TestClientIPFromRequest_XForwardedFor(t *testing.T) {
	req := newRLRequest(http.Header{"X-Forwarded-For": {"10.0.0.1, 10.0.0.2, 1.2.3.4"}})
	if ip := clientIPFromRequest(req); ip != "1.2.3.4" {
		t.Errorf("XFF rightmost = %q, want 1.2.3.4", ip)
	}
}

func TestClientIPFromRequest_XForwardedForWithPort(t *testing.T) {
	req := newRLRequest(http.Header{"X-Forwarded-For": {"1.2.3.4:443"}})
	if ip := clientIPFromRequest(req); ip != "1.2.3.4" {
		t.Errorf("XFF with port = %q, want 1.2.3.4", ip)
	}
}

func TestClientIPFromRequest_XRealIP(t *testing.T) {
	req := newRLRequest(http.Header{"X-Real-Ip": {"5.6.7.8"}})
	if ip := clientIPFromRequest(req); ip != "5.6.7.8" {
		t.Errorf("X-Real-IP = %q, want 5.6.7.8", ip)
	}
}

func TestClientIPFromRequest_Empty(t *testing.T) {
	req := newRLRequest(http.Header{})
	if ip := clientIPFromRequest(req); ip != "" {
		t.Errorf("empty headers = %q, want empty string", ip)
	}
}

func TestClientIPFromRequest_Priority(t *testing.T) {
	req := newRLRequest(http.Header{
		"Fly-Client-Ip":   {"1.2.3.4"},
		"Forwarded":       {"for=9.9.9.9"},
		"X-Forwarded-For": {"8.8.8.8"},
		"X-Real-Ip":       {"7.7.7.7"},
	})
	if ip := clientIPFromRequest(req); ip != "1.2.3.4" {
		t.Errorf("priority: Fly-Client-IP should win, got %q", ip)
	}
}

// ── extractRightmostIP ───────────────────────────────────────────────────────────

func TestExtractRightmostIP_Single(t *testing.T) {
	if ip := extractRightmostIP("1.2.3.4"); ip != "1.2.3.4" {
		t.Errorf("single = %q, want 1.2.3.4", ip)
	}
}

func TestExtractRightmostIP_Multiple(t *testing.T) {
	if ip := extractRightmostIP("a, b, c"); ip != "c" {
		t.Errorf("multiple = %q, want c", ip)
	}
}

func TestExtractRightmostIP_WithPort(t *testing.T) {
	if ip := extractRightmostIP("1.2.3.4:8080"); ip != "1.2.3.4" {
		t.Errorf("with port = %q, want 1.2.3.4", ip)
	}
}

func TestExtractRightmostIP_Whitespace(t *testing.T) {
	if ip := extractRightmostIP(" 10.0.0.1 , 10.0.0.2 "); ip != "10.0.0.2" {
		t.Errorf("whitespace = %q, want 10.0.0.2", ip)
	}
}

// ── TieredRateLimitInterceptor ───────────────────────────────────────────────────

func TestTieredRateLimitInterceptor_AllowsWithinLimit(t *testing.T) {
	rl := ratelimit.New()
	tiers := map[string]RateLimitTier{
		"/marketplace.v1.MarketplaceService/GetListing": {Limit: 5, Window: time.Minute},
	}
	interceptor := TieredRateLimitInterceptor(rl, tiers)

	h := func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&emptypb.Empty{}), nil
	}

	req := newRLRequest(http.Header{"Fly-Client-Ip": {"10.0.0.1"}})

	for i := 0; i < 5; i++ {
		resp, err := interceptor(h)(context.Background(), req)
		if err != nil {
			t.Fatalf("request %d should succeed, got: %v", i+1, err)
		}
		_ = resp
	}
}

func TestTieredRateLimitInterceptor_BlocksWhenExceeded(t *testing.T) {
	rl := ratelimit.New()
	// Key "" matches emptypb.Empty's Spec().Procedure. See Allow() call in
	// ratelimit.go: the key is ip+"|"+req.Spec().Procedure.
	tiers := map[string]RateLimitTier{
		"": {Limit: 2, Window: time.Minute},
	}
	interceptor := TieredRateLimitInterceptor(rl, tiers)

	h := func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&emptypb.Empty{}), nil
	}

	req := newRLRequest(http.Header{"Fly-Client-Ip": {"10.0.0.2"}})

	interceptor(h)(context.Background(), req)
	interceptor(h)(context.Background(), req)

	_, err := interceptor(h)(context.Background(), req)
	if err == nil {
		t.Fatal("expected rate limit error after 2 requests")
	}
	cerr, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("expected *connect.Error, got %T", err)
	}
	if cerr.Code() != connect.CodeResourceExhausted {
		t.Errorf("code = %v, want ResourceExhausted", cerr.Code())
	}
}

func TestTieredRateLimitInterceptor_DefaultTier(t *testing.T) {
	rl := ratelimit.New()
	interceptor := TieredRateLimitInterceptor(rl, nil)

	h := func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&emptypb.Empty{}), nil
	}

	req := newRLRequest(http.Header{"Fly-Client-Ip": {"10.0.0.3"}})

	for i := 0; i < 10; i++ {
		_, err := interceptor(h)(context.Background(), req)
		if err != nil {
			t.Fatalf("request %d with default tier should succeed, got: %v", i+1, err)
		}
	}
}

func TestTieredRateLimitInterceptor_DifferentIPsSeparate(t *testing.T) {
	rl := ratelimit.New()
	tiers := map[string]RateLimitTier{
		"": {Limit: 1, Window: time.Minute},
	}
	interceptor := TieredRateLimitInterceptor(rl, tiers)

	h := func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&emptypb.Empty{}), nil
	}

	// IP A exhausts.
	reqA := newRLRequest(http.Header{"Fly-Client-Ip": {"10.0.0.1"}})
	interceptor(h)(context.Background(), reqA)

	// IP B should still succeed.
	reqB := newRLRequest(http.Header{"Fly-Client-Ip": {"10.0.0.2"}})
	_, err := interceptor(h)(context.Background(), reqB)
	if err != nil {
		t.Fatalf("different IP should not be blocked: %v", err)
	}
}

func TestTieredRateLimitInterceptor_NilTiersUsesDefaults(t *testing.T) {
	rl := ratelimit.New()
	interceptor := TieredRateLimitInterceptor(rl, nil)

	h := func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&emptypb.Empty{}), nil
	}
	req := newRLRequest(http.Header{"Fly-Client-Ip": {"10.0.0.9"}})

	for i := 0; i < 5; i++ {
		_, err := interceptor(h)(context.Background(), req)
		if err != nil {
			t.Fatalf("nil tiers default: request %d should succeed, got: %v", i+1, err)
		}
	}
}

func TestTieredRateLimitInterceptor_UnknownIP(t *testing.T) {
	rl := ratelimit.New()
	tiers := map[string]RateLimitTier{
		"": {Limit: 1, Window: time.Minute},
	}
	interceptor := TieredRateLimitInterceptor(rl, tiers)

	h := func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&emptypb.Empty{}), nil
	}
	req := newRLRequest(http.Header{})

	interceptor(h)(context.Background(), req)
	_, err := interceptor(h)(context.Background(), req)
	if err == nil {
		t.Fatal("expected rate limit for unknown IP")
	}
}
