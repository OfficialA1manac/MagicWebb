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
	Close()
}

// DefaultTimeout bounds light RPC attempts before failing over.
const DefaultTimeout = 3 * time.Second

// heavyTimeout bounds log-range queries, which public RPCs serve slowly.
const heavyTimeout = 15 * time.Second

// Pool fans calls out across endpoints with sticky selection and failover.
type Pool struct {
	nodes   []ethNode
	cur     atomic.Uint64
	timeout time.Duration
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
	return newPoolWithNodes(nodes, timeout), nil
}

func newPoolWithNodes(nodes []ethNode, timeout time.Duration) *Pool {
	return &Pool{nodes: nodes, timeout: timeout}
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
func call[T any](p *Pool, ctx context.Context, op string, perCall time.Duration, fn func(context.Context, ethNode) (T, error)) (T, error) {
	var zero T
	if perCall <= 0 {
		perCall = p.timeout
	}
	var lastErr error
	start := p.cur.Load()
	n := uint64(len(p.nodes))
	for i := uint64(0); i < n; i++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		idx := (start + i) % n
		cctx, cancel := context.WithTimeout(ctx, perCall)
		v, err := fn(cctx, p.nodes[idx])
		cancel()
		if err == nil {
			if i > 0 {
				p.cur.Store(idx) // stick to the endpoint that worked
			}
			return v, nil
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
	return call(p, ctx, "FilterLogs", heavyTimeout, func(c context.Context, n ethNode) ([]types.Log, error) {
		return n.FilterLogs(c, q)
	})
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
