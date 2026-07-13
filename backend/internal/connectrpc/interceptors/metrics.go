// Package interceptors provides Connect-RPC interceptors for metrics,
// rate limiting, auth, and deadline propagation across all gRPC handlers.
package interceptors

import (
	"context"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
)

// Metrics holds atomic counters for gRPC handler observability.
// Exported so the metrics dashboard (/api/v1/metrics) and health
// endpoints can surface gRPC traffic patterns that were previously
// invisible (the gRPC handlers had zero observability before this).
type Metrics struct {
	// Per-method request totals (monotonic counters).
	GetListingRequests      atomic.Int64
	GetAuctionRequests      atomic.Int64
	GetOfferRequests        atomic.Int64
	GetTokenRequests        atomic.Int64
	ListCollectionsRequests atomic.Int64
	GetCollectionRequests   atomic.Int64
	ListListingsRequests    atomic.Int64
	ListAuctionsRequests    atomic.Int64
	ListOffersRequests      atomic.Int64
	GetActivityRequests     atomic.Int64
	GetWalletNFTsRequests   atomic.Int64
	GetProfileRequests      atomic.Int64
	SearchRequests          atomic.Int64
	GetMetricsRequests      atomic.Int64

	// Total request count across all methods.
	TotalRequests atomic.Int64

	// Total error count (connect.Code != OK).
	TotalErrors atomic.Int64

	// Active requests gauge (incremented before handler, decremented after).
	ActiveRequests atomic.Int64

	// Cumulative request latency in microseconds (for computing avg).
	// Divide by TotalRequests to get average latency.
	CumulativeLatencyUs atomic.Int64
}

// GlobalMetrics is the singleton Metrics instance for Connect-RPC handlers.
// Initialized at package load; callers retrieve it via Global().
var GlobalMetrics = &Metrics{}

// Global returns the singleton Metrics instance.
func Global() *Metrics {
	return GlobalMetrics
}

// requestCounter returns the per-method counter for the given RPC procedure.
// Returns nil for unknown procedures — the interceptor still counts totals.
func (m *Metrics) requestCounter(procedure string) *atomic.Int64 {
	switch procedure {
	case "/marketplace.v1.MarketplaceService/GetListing":
		return &m.GetListingRequests
	case "/marketplace.v1.MarketplaceService/GetAuction":
		return &m.GetAuctionRequests
	case "/marketplace.v1.MarketplaceService/GetOffer":
		return &m.GetOfferRequests
	case "/marketplace.v1.MarketplaceService/GetToken":
		return &m.GetTokenRequests
	case "/marketplace.v1.MarketplaceService/ListCollections":
		return &m.ListCollectionsRequests
	case "/marketplace.v1.MarketplaceService/GetCollection":
		return &m.GetCollectionRequests
	case "/marketplace.v1.MarketplaceService/ListListings":
		return &m.ListListingsRequests
	case "/marketplace.v1.MarketplaceService/ListAuctions":
		return &m.ListAuctionsRequests
	case "/marketplace.v1.MarketplaceService/ListOffers":
		return &m.ListOffersRequests
	case "/marketplace.v1.MarketplaceService/GetActivity":
		return &m.GetActivityRequests
	case "/marketplace.v1.MarketplaceService/GetWalletNFTs":
		return &m.GetWalletNFTsRequests
	case "/marketplace.v1.MarketplaceService/GetProfile":
		return &m.GetProfileRequests
	case "/marketplace.v1.MarketplaceService/Search":
		return &m.SearchRequests
	case "/marketplace.v1.MarketplaceService/GetMetrics":
		return &m.GetMetricsRequests
	default:
		return nil
	}
}

// MetricsInterceptor returns a Connect-RPC unary interceptor that records
// request count, latency, error rate, and active requests. Use with
// connect.WithInterceptors() when constructing the handler.
func MetricsInterceptor() connect.UnaryInterceptorFunc {
	m := GlobalMetrics
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()
			m.ActiveRequests.Add(1)
			defer m.ActiveRequests.Add(-1)

			resp, err := next(ctx, req)

			elapsed := time.Since(start)
			m.TotalRequests.Add(1)
			m.CumulativeLatencyUs.Add(elapsed.Microseconds())

			// Per-method counters.
			procedure := req.Spec().Procedure
			if ctr := m.requestCounter(procedure); ctr != nil {
				ctr.Add(1)
			}

			if err != nil {
				m.TotalErrors.Add(1)
			}

			return resp, err
		}
	}
}

// AvgLatencyUs returns the average request latency in microseconds.
// Returns 0 when no requests have been served (avoids divide-by-zero).
func (m *Metrics) AvgLatencyUs() int64 {
	total := m.TotalRequests.Load()
	if total == 0 {
		return 0
	}
	return m.CumulativeLatencyUs.Load() / total
}

// ErrorRate returns the error rate as a percentage (0.0-100.0).
func (m *Metrics) ErrorRate() float64 {
	total := m.TotalRequests.Load()
	if total == 0 {
		return 0
	}
	return float64(m.TotalErrors.Load()) / float64(total) * 100.0
}
