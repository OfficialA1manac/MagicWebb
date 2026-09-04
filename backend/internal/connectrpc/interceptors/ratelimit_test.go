package interceptors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/connectrpc/marketplacev1"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/connectrpc/marketplacev1/marketplacev1connect"
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

// Client-controlled forwarding headers must be ignored — rotating them
// previously minted a fresh rate-limit bucket per request (spoof bypass).
func TestClientIPFromRequest_IgnoresClientControlledHeaders(t *testing.T) {
	for name, hdr := range map[string]http.Header{
		"Forwarded":       {"Forwarded": {"for=10.0.0.1:12345;proto=https"}},
		"ForwardedQuoted": {"Forwarded": {"for=\"192.168.1.1\""}},
		"XForwardedFor":   {"X-Forwarded-For": {"10.0.0.1, 10.0.0.2, 1.2.3.4"}},
		"XRealIP":         {"X-Real-Ip": {"5.6.7.8"}},
	} {
		req := newRLRequest(hdr)
		if ip := clientIPFromRequest(req); ip != "" {
			t.Errorf("%s: spoofable header trusted, got %q, want empty (peer fallback)", name, ip)
		}
	}
}

func TestClientIPFromRequest_RejectsNonIPFlyClientIP(t *testing.T) {
	req := newRLRequest(http.Header{"Fly-Client-Ip": {"not-an-ip"}})
	if ip := clientIPFromRequest(req); ip != "" {
		t.Errorf("non-IP Fly-Client-IP = %q, want empty", ip)
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

// ── TieredRateLimitInterceptor ───────────────────────────────────────────────────

// getListingStub is a MarketplaceService whose GetListing always succeeds,
// so the interceptor is the only thing that can fail a call.
type getListingStub struct {
	marketplacev1connect.UnimplementedMarketplaceServiceHandler
}

func (getListingStub) GetListing(context.Context, *connect.Request[marketplacev1.GetListingRequest]) (*connect.Response[marketplacev1.GetListingResponse], error) {
	return connect.NewResponse(&marketplacev1.GetListingResponse{}), nil
}

// Exercises the GetListing tier through the real mounted handler, so the
// procedure name the interceptor keys on is the one Connect stamps on the
// request — not the empty procedure a hand-built connect.Request carries.
func TestTieredRateLimitInterceptor_GetListingTier(t *testing.T) {
	rl := ratelimit.New()
	tiers := map[string]RateLimitTier{
		marketplacev1connect.MarketplaceServiceGetListingProcedure: {Limit: 5, Window: time.Minute},
	}
	mux := http.NewServeMux()
	mux.Handle(marketplacev1connect.NewMarketplaceServiceHandler(getListingStub{},
		connect.WithInterceptors(TieredRateLimitInterceptor(rl, tiers))))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := marketplacev1connect.NewMarketplaceServiceClient(srv.Client(), srv.URL)
	call := func(ip string) error {
		req := connect.NewRequest(&marketplacev1.GetListingRequest{Collection: "0x0", TokenId: "1"})
		req.Header().Set("Fly-Client-Ip", ip)
		_, err := client.GetListing(context.Background(), req)
		return err
	}

	for i := 1; i <= 5; i++ {
		if err := call("10.0.0.1"); err != nil {
			t.Fatalf("GetListing %d should succeed, got: %v", i, err)
		}
	}
	err := call("10.0.0.1")
	if err == nil {
		t.Fatal("GetListing 6 should be rate limited")
	}
	if code := connect.CodeOf(err); code != connect.CodeResourceExhausted {
		t.Fatalf("GetListing 6 code = %v, want CodeResourceExhausted (%v)", code, err)
	}
	// The tier is per-IP: a different client still has budget.
	if err := call("10.0.0.2"); err != nil {
		t.Fatalf("other IP should succeed, got: %v", err)
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
