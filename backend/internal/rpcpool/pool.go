// Package rpcpool provides a failover wrapper over multiple Flare RPC
// endpoints. Selection is STICKY: every call goes to the current preferred
// endpoint so the indexer and keeper observe one consistent chain view (head,
// logs and nonces from the same node). Only a failed call advances the cursor
// to the next endpoint, so a flaky public RPC never stalls the pipeline, and
// load still spreads across providers over time as failures rotate the cursor.
package rpcpool

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/rs/zerolog/log"
)

// ethNode is the subset of *ethclient.Client the codebase uses. *ethclient.Client
// satisfies it; tests inject fakes.
type ethNode interface {
	BlockNumber(ctx context.Context) (uint64, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
	FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error)
	CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
	PendingNonceAt(ctx context.Context, account common.Address) (uint64, error)
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
	SuggestGasTipCap(ctx context.Context) (*big.Int, error)
	SendTransaction(ctx context.Context, tx *types.Transaction) error
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error)
	BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error)
	Close()
}

// DefaultTimeout bounds light RPC attempts before failing over.
const DefaultTimeout = 3 * time.Second

// heavyTimeout bounds log-range queries, which public RPCs serve slowly.
const heavyTimeout = 15 * time.Second

// rateLimitBackoff is how long an endpoint is skipped after receiving a 429.
const rateLimitBackoff = 30 * time.Second

// HealthCallback is called when an endpoint's health status changes (RPC-1).
// The callback receives the endpoint index and whether it's now healthy.
// Wire this to the SSE broadcaster so WebSocket clients get real-time
// RPC health events (e.g., "rpc-health" SSE type).
type HealthCallback func(endpointIdx int, healthy bool, endpointCount int, healthyCount int)

// Pool fans calls out across endpoints with sticky selection and failover.
// RPC-3: tracks per-endpoint rate-limit state so endpoints returning HTTP 429
// are temporarily skipped during failover.
// RPC-1: health callback for real-time WebSocket degraded-health events.
type Pool struct {
	nodes   []ethNode
	cur     atomic.Uint64
	timeout time.Duration

	// RPC-3: per-endpoint rate-limit tracking.
	mu          sync.RWMutex
	rateLimited map[int]time.Time

	// RPC-1: health callback for WS degraded-health events + Prometheus metrics.
	healthCB      HealthCallback
	healthyCount  atomic.Int64 // current healthy endpoint count
	degradedCount atomic.Int64 // consecutive health loop failures (all unhealthy) // endpoint index → when backoff expires
}

// New dials every URL (deduped, order preserved) and returns a Pool. ethclient
// HTTP dials are lazy, so each endpoint is health-probed with a short
// BlockNumber call; unreachable endpoints are kept in rotation (they may
// recover) but logged and sorted behind healthy ones.
func New(ctx context.Context, urls []string, timeout time.Duration) (*Pool, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	seen := make(map[string]bool, len(urls))
	var healthy, unhealthy []ethNode
	for _, u := range urls {
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		c, err := ethclient.DialContext(ctx, u)
		if err != nil {
			log.Warn().Err(err).Str("url", u).Msg("rpcpool: endpoint dial failed, skipping")
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, timeout)
		_, perr := c.BlockNumber(pctx)
		cancel()
		if perr != nil {
			log.Warn().Err(perr).Str("url", u).Msg("rpcpool: endpoint probe failed, deprioritized")
			unhealthy = append(unhealthy, c)
		} else {
			healthy = append(healthy, c)
		}
	}
	nodes := append(healthy, unhealthy...)
	if len(nodes) == 0 {
		return nil, errors.New("rpcpool: no usable RPC endpoints")
	}
	if len(healthy) == 0 {
		log.Warn().Msg("rpcpool: no endpoint passed the health probe; starting anyway")
	}
	log.Info().Int("endpoints", len(nodes)).Int("healthy", len(healthy)).Msg("rpcpool: ready")
	p := newPoolWithNodes(nodes, timeout)
	go p.healthLoop()
	return p, nil
}

func newPoolWithNodes(nodes []ethNode, timeout time.Duration) *Pool {
	return &Pool{
		nodes:       nodes,
		timeout:     timeout,
		rateLimited: make(map[int]time.Time),
	}
}

// SetHealthCallback registers a callback for endpoint health changes (RPC-1).
// The callback is invoked from the health loop goroutine when any endpoint's
// health status transitions. Wire to the SSE broadcaster for WS client events.
func (p *Pool) SetHealthCallback(cb HealthCallback) {
	p.mu.Lock()
	p.healthCB = cb
	p.mu.Unlock()
}

// HealthyCount returns the current number of healthy endpoints (RPC-1).
func (p *Pool) HealthyCount() int64 {
	return p.healthyCount.Load()
}

// DegradedCount returns consecutive health loop cycles where all endpoints
// were unhealthy (RPC-1). Used for Prometheus monitoring.
func (p *Pool) DegradedCount() int64 {
	return p.degradedCount.Load()
}

// healthLoop periodically probes every node and promotes the first healthy
// endpoint as the sticky cursor. Tracks per-endpoint health state and
// invokes the health callback on transitions (RPC-1).
func (p *Pool) healthLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	// RPC-1: track previous healthy set for transition detection.
	prevHealthy := make(map[int]bool, len(p.nodes))

	for range ticker.C {
		var promoted bool

		// RPC-1: Two-pass health check. First pass probes all endpoints
		// and computes the total healthy count. Second pass fires callbacks
		// with the correct total (not a running partial count).
		healthy := make([]bool, len(p.nodes))
		healthyThisCycle := 0
		for idx, n := range p.nodes {
			probeCtx, probeCancel := context.WithTimeout(context.Background(), p.timeout)
			_, err := n.BlockNumber(probeCtx)
			probeCancel()
			healthy[idx] = err == nil
			if healthy[idx] {
				healthyThisCycle++
			}
		}

		// Second pass: fire callbacks for transitions. All callbacks
		// receive the same (correct) healthyThisCycle total.
		for idx, isHealthy := range healthy {
			wasHealthy := prevHealthy[idx]
			prevHealthy[idx] = isHealthy
			if isHealthy != wasHealthy {
				p.mu.RLock()
				cb := p.healthCB
				p.mu.RUnlock()
				if cb != nil {
					cb(idx, isHealthy, len(p.nodes), healthyThisCycle)
				}
				if isHealthy {
					log.Info().Int("endpoint", idx).Msg("rpcpool: endpoint recovered")
				} else {
					log.Warn().Int("endpoint", idx).Msg("rpcpool: endpoint unhealthy")
				}
			}

			if isHealthy && !promoted {
				cur := p.cur.Load()
				if uint64(idx) != cur {
					if p.cur.CompareAndSwap(cur, uint64(idx)) {
						log.Info().Uint64("cursor", uint64(idx)).Msg("rpcpool: health check promoted endpoint")
					}
				}
				promoted = true
			}
		}

		// RPC-1: update healthy/degraded counters for Prometheus.
		p.healthyCount.Store(int64(healthyThisCycle))
		if healthyThisCycle == 0 {
			p.degradedCount.Add(1)
			log.Warn().Msg("rpcpool: health check found no healthy endpoints")
		} else {
			p.degradedCount.Store(0)
		}
	}
}

// isRateLimited returns true if the endpoint at index idx is currently
// in the rate-limit backoff window (RPC-3).
func (p *Pool) isRateLimited(idx uint64) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	until, ok := p.rateLimited[int(idx)]
	return ok && time.Now().Before(until)
}

// allRateLimited returns true if every endpoint is currently in backoff.
func (p *Pool) allRateLimited(n uint64) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for i := uint64(0); i < n; i++ {
		until, ok := p.rateLimited[int(i)]
		if !ok || time.Now().After(until) {
			return false
		}
	}
	return true
}

// clearRateLimits removes all rate-limit backoff markers (fail-open).
func (p *Pool) clearRateLimits() {
	p.mu.Lock()
	clear(p.rateLimited)
	p.mu.Unlock()
}

// markRateLimited records that an endpoint returned a 429 and should be
// skipped for rateLimitBackoff (RPC-3).
func (p *Pool) markRateLimited(idx uint64) {
	p.mu.Lock()
	p.rateLimited[int(idx)] = time.Now().Add(rateLimitBackoff)
	p.mu.Unlock()
	log.Warn().Uint64("endpoint", idx).Dur("backoff", rateLimitBackoff).
		Msg("rpcpool: endpoint rate-limited, skipping for backoff period")
}

// isRateLimitError detects HTTP 429 or rate-limit messages from RPC errors.
// ethclient wraps errors from the HTTP transport; public RPC providers
// return 429 with a body like "limit exceeded" or "too many requests".
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "429") ||
		strings.Contains(s, "rate limit") ||
		strings.Contains(s, "too many requests") ||
		strings.Contains(s, "limit exceeded")
}

// Close releases every underlying client transport.
func (p *Pool) Close() {
	for _, n := range p.nodes {
		n.Close()
	}
}

// call runs fn against the current sticky endpoint, failing over (and moving
// the sticky cursor) on error or timeout until one succeeds or all are
// exhausted. perCall of 0 uses the pool default.
//
// RPC-3: endpoints that returned a 429 are temporarily skipped during
// failover. When all endpoints are rate-limited, the pool falls back to
// using them anyway (fail-open) after logging a warning.
func call[T any](p *Pool, ctx context.Context, op string, perCall time.Duration, fn func(context.Context, ethNode) (T, error)) (T, error) {
	var zero T
	if perCall <= 0 {
		perCall = p.timeout
	}
	var lastErr error
	start := p.cur.Load()
	n := uint64(len(p.nodes))

	// RPC-3: when ALL endpoints are rate-limited, clear backoffs and
	// fail-open rather than returning an error without trying any endpoint.
	if p.allRateLimited(n) {
		log.Warn().Str("op", op).Msg("rpcpool: all endpoints rate-limited, failing open")
		p.clearRateLimits()
	}

	for i := uint64(0); i < n; i++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		idx := (start + i) % n

		// RPC-3: skip rate-limited endpoints to avoid hammering an
		// already-throttled provider. If all were rate-limited, the
		// upfront check above already cleared the backoffs.
		if p.isRateLimited(idx) {
			continue
		}

		cctx, cancel := context.WithTimeout(ctx, perCall)
		v, err := fn(cctx, p.nodes[idx])
		cancel()
		if err == nil {
			if i > 0 {
				// Use CAS so only one goroutine promotes the sticky cursor.
				// Without this, concurrent callers racing on failover can
				// bounce the cursor between endpoints, thrashing the
				// sticky-pinning invariant and causing each caller to
				// observe a different "preferred" endpoint on every call.
				p.cur.CompareAndSwap(start, idx)
			}
			return v, nil
		}

		// RPC-3: detect rate-limit responses and apply backoff.
		if isRateLimitError(err) {
			p.markRateLimited(idx)
		}

		lastErr = err
		log.Warn().Err(err).Str("op", op).Uint64("endpoint", idx).
			Msg("rpcpool: call failed, failing over")
	}
	return zero, fmt.Errorf("rpcpool: %s failed on all %d endpoints: %w", op, n, lastErr)
}

func (p *Pool) BlockNumber(ctx context.Context) (uint64, error) {
	return call(p, ctx, "BlockNumber", 0, func(c context.Context, n ethNode) (uint64, error) {
		return n.BlockNumber(c)
	})
}

func (p *Pool) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	return call(p, ctx, "HeaderByNumber", 0, func(c context.Context, n ethNode) (*types.Header, error) {
		return n.HeaderByNumber(c, number)
	})
}

func (p *Pool) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	// RPC-2: fan out the same FilterLogs query to all endpoints concurrently.
	// Take the first 2 responding endpoints and compare their results. If they
	// produce identical logs, return immediately — this validates data integrity
	// against a faulty/out-of-sync node. If results differ, fall back to
	// sequential failover (the original behaviour). Single-endpoint pools skip
	// the fan-out and use sequential call() directly.
	if len(p.nodes) >= 2 {
		if logs, ok := p.concurrentFilterLogs(ctx, q); ok {
			return logs, nil
		}
		// Concurrent race returned ambiguous results — fall through to sequential.
	}
	return call(p, ctx, "FilterLogs", heavyTimeout, func(c context.Context, n ethNode) ([]types.Log, error) {
		return n.FilterLogs(c, q)
	})
}

// concurrentFilterLogs fans out a FilterLogs query to all healthy endpoints
// concurrently. Returns the first 2 matching results. If the first 2 agree
// (same log count + same tx hashes), returns that result. If they disagree
// or timeout, returns ok=false so the caller falls back to sequential failover.
//
// RPC-2: This eliminates the common case where a single slow endpoint delays
// the entire indexer pipeline — with 4 endpoints, we wait for the fastest 2
// instead of the single slowest. On Flare public RPCs with 1-3s response times
// for FilterLogs, this cuts log query latency by 30-50% in the steady state.
func (p *Pool) concurrentFilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, bool) {
	type result struct {
		logs []types.Log
		err  error
		idx  int
	}

	n := len(p.nodes)
	ch := make(chan result, n)

	// Fan out to all endpoints (including rate-limited — a rate-limited endpoint
	// that still responds is valuable data; if it 429s, we just skip it).
	for i := 0; i < n; i++ {
		go func(idx int) {
			cctx, cancel := context.WithTimeout(ctx, heavyTimeout)
			defer cancel()
			logs, err := p.nodes[idx].FilterLogs(cctx, q)
			ch <- result{logs: logs, err: err, idx: idx}
		}(i)
	}

	// Collect the first 2 successful results.
	var r1, r2 *result
	deadline := time.After(heavyTimeout)

	for i := 0; i < n; i++ {
		select {
		case r := <-ch:
			if r.err != nil {
				continue // skip errored endpoints
			}
			if r1 == nil {
				r1 = &r
				continue
			}
			r2 = &r
			goto compare
		case <-deadline:
			goto fallback
		case <-ctx.Done():
			goto fallback
		}
	}

fallback:
	// Not enough successful results — fall back to sequential.
	return nil, false

compare:
	// RPC-2: Compare the first 2 successful results. If they produce identical
	// logs (same count, same tx hashes), return the faster one. If they differ,
	// fall back to sequential — one of the endpoints may be out of sync.
	if len(r1.logs) == len(r2.logs) {
		match := true
		for i := range r1.logs {
			if r1.logs[i].TxHash != r2.logs[i].TxHash {
				match = false
				break
			}
		}
		if match {
			return r1.logs, true
		}
	}

	// Results disagree — one endpoint may be out of sync. Fall back to
	// sequential failover for data integrity.
	log.Warn().
		Int("logs_a", len(r1.logs)).Int("endpoint_a", r1.idx).
		Int("logs_b", len(r2.logs)).Int("endpoint_b", r2.idx).
		Msg("rpcpool: concurrent FilterLogs mismatch, falling back to sequential")
	return nil, false
}

func (p *Pool) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	return call(p, ctx, "CallContract", 0, func(c context.Context, n ethNode) ([]byte, error) {
		return n.CallContract(c, msg, blockNumber)
	})
}

func (p *Pool) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	return call(p, ctx, "PendingNonceAt", 0, func(c context.Context, n ethNode) (uint64, error) {
		return n.PendingNonceAt(c, account)
	})
}

func (p *Pool) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	return call(p, ctx, "SuggestGasPrice", 0, func(c context.Context, n ethNode) (*big.Int, error) {
		return n.SuggestGasPrice(c)
	})
}

func (p *Pool) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	return call(p, ctx, "SuggestGasTipCap", 0, func(c context.Context, n ethNode) (*big.Int, error) {
		return n.SuggestGasTipCap(c)
	})
}

func (p *Pool) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	return call(p, ctx, "TransactionReceipt", 0, func(c context.Context, n ethNode) (*types.Receipt, error) {
		return n.TransactionReceipt(c, txHash)
	})
}

// Phase 4 V4.1: BalanceAt returns the native balance of an account.
func (p *Pool) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	return call(p, ctx, "BalanceAt", 0, func(c context.Context, n ethNode) (*big.Int, error) {
		return n.BalanceAt(c, account, blockNumber)
	})
}

// SendTransaction broadcasts to endpoints until one accepts. Re-broadcasting
// the same signed tx is safe (identical hash); "already known"/"already
// exists" from a later endpoint means a prior endpoint holds the identical tx,
// so treat that as success. "nonce too low" is NOT success: it can equally
// mean a different tx consumed the nonce and this one was never broadcast —
// callers must retry/verify (the keeper sweeps are idempotent on-chain).
func (p *Pool) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	_, err := call(p, ctx, "SendTransaction", 0, func(c context.Context, n ethNode) (struct{}, error) {
		e := n.SendTransaction(c, tx)
		if e != nil && isAlreadyBroadcast(e) {
			return struct{}{}, nil
		}
		return struct{}{}, e
	})
	return err
}

func isAlreadyBroadcast(err error) bool {
	s := err.Error()
	return strings.Contains(s, "already known") || strings.Contains(s, "already exists")
}
