// Package health implements the standard gRPC Health Checking Protocol
// (grpc.health.v1.Health) so that Fly.io and other infrastructure can probe
// this instance with a sub-millisecond gRPC health check instead of a full
// HTTP round-trip through /healthz.
//
// The service reports SERVING when both the database and RPC connection are
// healthy. If either probe fails within the configured timeout, it reports
// NOT_SERVING so the orchestrator can replace the instance.
//
// Usage:
//
//	import "google.golang.org/grpc/health/grpc_health_v1"
//	hs := health.New(db, eth, headLag)
//	grpc_health_v1.RegisterHealthServer(grpcSrv, hs)
package health

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// ProbePinger is the subset of the health check that pings the database.
type ProbePinger interface {
	Ping(ctx context.Context) error
}

// ProbeBlockNumber is the subset of the health check that queries the latest
// block number from the chain RPC.
type ProbeBlockNumber interface {
	BlockNumber(ctx context.Context) (uint64, error)
}

// Server implements the standard grpc.health.v1.Health server interface.
// It runs lightweight probes (DB ping + RPC block number) on every Check call
// and caches the result for up to cacheTTL to avoid hammering the backends on
// frequent polling (Fly.io checks every 30s, but Prometheus / kubernetes may
// scrape every 15s).
type Server struct {
	grpc_health_v1.UnimplementedHealthServer
	pinger    ProbePinger
	blockNum  ProbeBlockNumber
	headLag   func() uint64 // optional head-lag callback; nil = skip lag check

	mu     sync.RWMutex
	cached cachedResult
}

type cachedResult struct {
	status   grpc_health_v1.HealthCheckResponse_ServingStatus
	expiresAt time.Time
}

const cacheTTL = 10 * time.Second

// New creates a health server that probes the given DB pinger and RPC
// block-number querier on each Check call and caches the result for cacheTTL.
func New(pinger ProbePinger, blockNum ProbeBlockNumber, headLag func() uint64) *Server {
	return &Server{
		pinger:   pinger,
		blockNum: blockNum,
		headLag:  headLag,
	}
}

// Check implements grpc_health_v1.HealthServer.Check. It probes the DB and
// RPC connections, returning SERVING only when both are healthy and the
// indexer head lag (if configured) is below the warning threshold.
func (s *Server) Check(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	// Service-specific checks: if a service name is requested and it isn't
	// the wildcard "", only respond for services we know about. Currently
	// we only support the overall server health; any named service request
	// returns NOT_FOUND per the spec.
	if req.Service != "" {
		return &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_UNKNOWN,
		}, status.Error(codes.NotFound, "unknown service")
	}

	// Check cache first.
	s.mu.RLock()
	if time.Now().Before(s.cached.expiresAt) {
		cached := s.cached
		s.mu.RUnlock()
		return &grpc_health_v1.HealthCheckResponse{Status: cached.status}, nil
	}
	s.mu.RUnlock()

	// Run probes with a short timeout (3s each, same as /healthz).
	status := s.probe(ctx)

	s.mu.Lock()
	s.cached = cachedResult{status: status, expiresAt: time.Now().Add(cacheTTL)}
	s.mu.Unlock()

	return &grpc_health_v1.HealthCheckResponse{Status: status}, nil
}

// Watch implements grpc_health_v1.HealthServer.Watch (server-side streaming).
// It sends the current health status immediately, then sends updates whenever
// the health status changes. This is optional for the health protocol but
// useful for orchestration systems that want to stream health changes.
func (s *Server) Watch(req *grpc_health_v1.HealthCheckRequest, stream grpc_health_v1.Health_WatchServer) error {
	if req.Service != "" {
		return status.Error(codes.NotFound, "unknown service")
	}

	// Send initial status.
	initialStatus := s.probe(stream.Context())
	if err := stream.Send(&grpc_health_v1.HealthCheckResponse{Status: initialStatus}); err != nil {
		return err
	}

	// Poll every 10s and send updates on status change.
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	lastStatus := initialStatus
	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
			current := s.probe(stream.Context())
			if current != lastStatus {
				if err := stream.Send(&grpc_health_v1.HealthCheckResponse{Status: current}); err != nil {
					return err
				}
				lastStatus = current
			}
		}
	}
}

// forceRefresh clears the cached result so the next Check call re-probes.
// Useful for testing or when an external event (e.g. reconnection) suggests
// the cached status may be stale.
func (s *Server) forceRefresh() {
	s.mu.Lock()
	s.cached = cachedResult{expiresAt: time.Time{}}
	s.mu.Unlock()
}

// probe runs the DB and RPC checks and returns the overall status.
func (s *Server) probe(ctx context.Context) grpc_health_v1.HealthCheckResponse_ServingStatus {
	// DB ping.
	pingCtx, pingCancel := context.WithTimeout(ctx, 3*time.Second)
	defer pingCancel()
	if err := s.pinger.Ping(pingCtx); err != nil {
		log.Warn().Err(err).Msg("health: db ping failed")
		return grpc_health_v1.HealthCheckResponse_NOT_SERVING
	}

	// RPC block number.
	rpcCtx, rpcCancel := context.WithTimeout(ctx, 3*time.Second)
	defer rpcCancel()
	if _, err := s.blockNum.BlockNumber(rpcCtx); err != nil {
		log.Warn().Err(err).Msg("health: rpc block number failed")
		return grpc_health_v1.HealthCheckResponse_NOT_SERVING
	}

	// Indexer head lag check (when callback is configured).
	if s.headLag != nil {
		if lag := s.headLag(); lag > 15 {
			log.Warn().Uint64("lag", lag).Msg("health: indexer head lag exceeds threshold")
			return grpc_health_v1.HealthCheckResponse_NOT_SERVING
		}
	}

	return grpc_health_v1.HealthCheckResponse_SERVING
}
