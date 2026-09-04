package health

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/health/grpc_health_v1"
)

// blockingProbe counts Ping calls and can hold every in-flight probe on a
// gate so the test controls when the "slow" refresh completes.
type blockingProbe struct {
	calls atomic.Int32
	gate  chan struct{} // nil = return immediately
	err   error
}

func (p *blockingProbe) Ping(ctx context.Context) error {
	p.calls.Add(1)
	if p.gate != nil {
		select {
		case <-p.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return p.err
}

func (p *blockingProbe) BlockNumber(context.Context) (uint64, error) { return 1, nil }

func newTestServer(p *blockingProbe) *Server { return New(p, p, nil) }

// A burst of concurrent Checks on an expired cache must run exactly one
// probe: the first caller refreshes, the rest wait on refreshMu and take the
// cached result.
func TestCheck_SingleFlightsRefresh(t *testing.T) {
	p := &blockingProbe{gate: make(chan struct{})}
	s := newTestServer(p)

	const n = 8
	var wg sync.WaitGroup
	results := make([]grpc_health_v1.HealthCheckResponse_ServingStatus, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := s.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
			if err != nil {
				t.Errorf("Check %d: %v", i, err)
				return
			}
			results[i] = resp.Status
		}(i)
	}

	// Wait until the first probe is parked on the gate, then give the other
	// goroutines time to pile up behind refreshMu before releasing it.
	deadline := time.Now().Add(2 * time.Second)
	for p.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(p.gate)
	wg.Wait()

	if got := p.calls.Load(); got != 1 {
		t.Fatalf("probe ran %d times for %d concurrent Checks, want 1", got, n)
	}
	for i, st := range results {
		if st != grpc_health_v1.HealthCheckResponse_SERVING {
			t.Errorf("Check %d status = %v, want SERVING", i, st)
		}
	}
	// And a follow-up within the TTL still hits the cache.
	if _, err := s.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := p.calls.Load(); got != 1 {
		t.Fatalf("probe ran %d times after cached follow-up, want 1", got)
	}
}

// A probe aborted by the caller's context must not poison the cache: the
// canceling caller sees NOT_SERVING, but the next caller re-probes and gets
// the real backend answer.
func TestCheck_DoesNotCacheCanceledProbe(t *testing.T) {
	p := &blockingProbe{gate: make(chan struct{})}
	s := newTestServer(p)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resp, err := s.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("canceled Check status = %v, want NOT_SERVING", resp.Status)
	}
	if _, ok := s.cachedStatus(); ok {
		t.Fatal("canceled probe result was cached")
	}

	// Healthy backend, live context: must probe again and report SERVING.
	p.gate = nil
	resp, err = s.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("post-cancel Check status = %v, want SERVING", resp.Status)
	}
	if got := p.calls.Load(); got != 2 {
		t.Fatalf("probe calls = %d, want 2 (canceled + real)", got)
	}
}

// Sanity: a genuine backend failure with a live context IS cached.
func TestCheck_CachesRealFailure(t *testing.T) {
	p := &blockingProbe{err: errors.New("db down")}
	s := newTestServer(p)
	for i := 0; i < 3; i++ {
		resp, err := s.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Status != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
			t.Fatalf("Check %d status = %v, want NOT_SERVING", i, resp.Status)
		}
	}
	if got := p.calls.Load(); got != 1 {
		t.Fatalf("probe calls = %d, want 1 (failure cached)", got)
	}
}
