package interceptors

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"
)

// newReq creates a connect.AnyRequest for testing. Uses emptypb.Empty as the
// underlying proto message — the smallest possible protobuf message. Since
// emptypb.Empty has no service definition, req.Spec().Procedure will be "".
// The interceptor should still count totals and not panic; per-method counters
// will only increment for known procedures (tested separately via unit tests
// on requestCounter).
func newRequest() *connect.Request[emptypb.Empty] {
	return connect.NewRequest(&emptypb.Empty{})
}

// invoke is a convenience method that wraps a handler through this Metrics
// interceptor and invokes it. Returns the result from the wrapped handler.
func (m *Metrics) invoke(h connect.UnaryFunc) (connect.AnyResponse, error) {
	wrapped := m.interceptor()(h)
	return wrapped(context.Background(), newRequest())
}

// ── requestCounter ─────────────────────────────────────────────────────────────

func TestRequestCounter_KnownProcedures(t *testing.T) {
	m := &Metrics{}
	procedures := []string{
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
	for _, proc := range procedures {
		ctr := m.requestCounter(proc)
		if ctr == nil {
			t.Errorf("requestCounter(%q) = nil, want non-nil", proc)
		}
	}
}

func TestRequestCounter_UnknownProcedure(t *testing.T) {
	m := &Metrics{}
	if ctr := m.requestCounter("/unknown.Service/Foo"); ctr != nil {
		t.Errorf("requestCounter(unknown) = %v, want nil", ctr)
	}
	if ctr := m.requestCounter(""); ctr != nil {
		t.Errorf("requestCounter(\"\") = %v, want nil", ctr)
	}
	if ctr := m.requestCounter("/marketplace.v1.MarketplaceService/NonExistent"); ctr != nil {
		t.Errorf("requestCounter(unregistered) = %v, want nil", ctr)
	}
}

// ── Global ──────────────────────────────────────────────────────────────────────

func TestGlobal_ReturnsSingleton(t *testing.T) {
	g1 := Global()
	g2 := Global()
	if g1 != g2 {
		t.Fatal("Global() returned different instances")
	}
	if g1 != GlobalMetrics {
		t.Fatal("Global() != GlobalMetrics — expected same instance")
	}
}

func TestGlobal_Isolation(t *testing.T) {
	m1 := &Metrics{}
	m2 := &Metrics{}

	m1.TotalRequests.Store(5)
	m2.TotalRequests.Store(10)

	if m1.TotalRequests.Load() != 5 {
		t.Errorf("m1.TotalRequests = %d, want 5", m1.TotalRequests.Load())
	}
	if m2.TotalRequests.Load() != 10 {
		t.Errorf("m2.TotalRequests = %d, want 10", m2.TotalRequests.Load())
	}
}

// ── AvgLatencyUs ────────────────────────────────────────────────────────────────

func TestAvgLatencyUs_ZeroRequests(t *testing.T) {
	m := &Metrics{}
	if avg := m.AvgLatencyUs(); avg != 0 {
		t.Errorf("AvgLatencyUs with 0 requests = %d, want 0", avg)
	}
}

func TestAvgLatencyUs_ComputesCorrectly(t *testing.T) {
	m := &Metrics{}
	m.TotalRequests.Store(4)
	m.CumulativeLatencyUs.Store(1000)

	if avg := m.AvgLatencyUs(); avg != 250 {
		t.Errorf("AvgLatencyUs = %d, want 250", avg)
	}
}

func TestAvgLatencyUs_SingleRequest(t *testing.T) {
	m := &Metrics{}
	m.TotalRequests.Store(1)
	m.CumulativeLatencyUs.Store(42)

	if avg := m.AvgLatencyUs(); avg != 42 {
		t.Errorf("AvgLatencyUs = %d, want 42", avg)
	}
}

// ── ErrorRate ────────────────────────────────────────────────────────────────────

func TestErrorRate_ZeroRequests(t *testing.T) {
	m := &Metrics{}
	if rate := m.ErrorRate(); rate != 0.0 {
		t.Errorf("ErrorRate with 0 requests = %f, want 0.0", rate)
	}
}

func TestErrorRate_AllErrors(t *testing.T) {
	m := &Metrics{}
	m.TotalRequests.Store(10)
	m.TotalErrors.Store(10)

	if rate := m.ErrorRate(); rate != 100.0 {
		t.Errorf("ErrorRate = %f, want 100.0", rate)
	}
}

func TestErrorRate_NoErrors(t *testing.T) {
	m := &Metrics{}
	m.TotalRequests.Store(100)
	m.TotalErrors.Store(0)

	if rate := m.ErrorRate(); rate != 0.0 {
		t.Errorf("ErrorRate = %f, want 0.0", rate)
	}
}

func TestErrorRate_Partial(t *testing.T) {
	m := &Metrics{}
	m.TotalRequests.Store(20)
	m.TotalErrors.Store(5)

	if rate := m.ErrorRate(); rate != 25.0 {
		t.Errorf("ErrorRate = %f, want 25.0", rate)
	}
}

// ── Per-method counter uniqueness ───────────────────────────────────────────────

func TestPerMethodCounters_AreDistinct(t *testing.T) {
	m := &Metrics{}
	m.GetListingRequests.Store(1)
	m.GetAuctionRequests.Store(2)
	m.SearchRequests.Store(3)

	if m.GetListingRequests.Load() != 1 {
		t.Error("GetListingRequests leaked into another counter")
	}
	if m.GetAuctionRequests.Load() != 2 {
		t.Error("GetAuctionRequests leaked into another counter")
	}
	if m.SearchRequests.Load() != 3 {
		t.Error("SearchRequests leaked into another counter")
	}
	if m.GetOfferRequests.Load() != 0 {
		t.Error("GetOfferRequests should be 0")
	}
}

// ── MetricsInterceptor integration ──────────────────────────────────────────────

func TestMetricsInterceptor_RecordsSuccess(t *testing.T) {
	m := &Metrics{}
	// Use a handler with a tiny sleep so CumulativeLatencyUs is always > 0
	// even on platforms with low-resolution clocks (Windows).
	h := func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		time.Sleep(1 * time.Millisecond)
		return connect.NewResponse(&emptypb.Empty{}), nil
	}

	resp, err := m.invoke(h)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	_ = resp

	if m.TotalRequests.Load() != 1 {
		t.Errorf("TotalRequests = %d, want 1", m.TotalRequests.Load())
	}
	if m.TotalErrors.Load() != 0 {
		t.Errorf("TotalErrors = %d, want 0", m.TotalErrors.Load())
	}
	if m.ActiveRequests.Load() != 0 {
		t.Errorf("ActiveRequests = %d, want 0 (decremented after handler)", m.ActiveRequests.Load())
	}
	if m.CumulativeLatencyUs.Load() == 0 {
		t.Error("CumulativeLatencyUs should be > 0")
	}
}

func TestMetricsInterceptor_RecordsError(t *testing.T) {
	m := &Metrics{}
	testErr := errors.New("something went wrong")
	h := nextHandler(testErr)

	_, err := m.invoke(h)
	if err != testErr {
		t.Fatalf("expected testErr, got %v", err)
	}

	if m.TotalRequests.Load() != 1 {
		t.Errorf("TotalRequests = %d, want 1", m.TotalRequests.Load())
	}
	if m.TotalErrors.Load() != 1 {
		t.Errorf("TotalErrors = %d, want 1 (error was returned)", m.TotalErrors.Load())
	}
}

func TestMetricsInterceptor_ActiveRequestsGauge(t *testing.T) {
	m := &Metrics{}

	// Simulate 5 concurrent requests.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := nextHandler(nil)
			m.invoke(h)
		}()
	}
	wg.Wait()

	if m.ActiveRequests.Load() != 0 {
		t.Errorf("ActiveRequests after concurrent completion = %d, want 0", m.ActiveRequests.Load())
	}
	if m.TotalRequests.Load() != 5 {
		t.Errorf("TotalRequests = %d, want 5", m.TotalRequests.Load())
	}
}

func TestMetricsInterceptor_ConcurrentIncrements(t *testing.T) {
	m := &Metrics{}

	const goroutines = 100
	var wg sync.WaitGroup
	var errCount atomic.Int64

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var handlerErr error
			if idx%3 == 0 {
				handlerErr = errors.New("fail")
			}
			h := nextHandler(handlerErr)
			_, err := m.invoke(h)
			if err != nil {
				errCount.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if m.TotalRequests.Load() != goroutines {
		t.Errorf("TotalRequests = %d, want %d", m.TotalRequests.Load(), goroutines)
	}
	if m.TotalErrors.Load() != errCount.Load() {
		t.Errorf("TotalErrors = %d, want %d", m.TotalErrors.Load(), errCount.Load())
	}
	if m.ActiveRequests.Load() != 0 {
		t.Errorf("ActiveRequests = %d, want 0", m.ActiveRequests.Load())
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────────

// interceptor creates a MetricsInterceptor that writes to this Metrics instance
// (not the global singleton), for test isolation.
func (m *Metrics) interceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()
			m.ActiveRequests.Add(1)
			defer m.ActiveRequests.Add(-1)

			resp, err := next(ctx, req)

			elapsed := time.Since(start)
			m.TotalRequests.Add(1)
			m.CumulativeLatencyUs.Add(elapsed.Microseconds())

			if ctr := m.requestCounter(req.Spec().Procedure); ctr != nil {
				ctr.Add(1)
			}
			if err != nil {
				m.TotalErrors.Add(1)
			}
			return resp, err
		}
	}
}

// nextHandler returns a connect.UnaryFunc that returns the given error.
func nextHandler(wantErr error) connect.UnaryFunc {
	return func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		if wantErr != nil {
			return nil, wantErr
		}
		return connect.NewResponse(&emptypb.Empty{}), nil
	}
}
