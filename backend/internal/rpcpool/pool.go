// Package rpcpool provides a failover-and-rotation wrapper over multiple Flare
// RPC endpoints. It round-robins calls across endpoints to spread rate-limit
// pressure and automatically fails over to the next endpoint when one errors or
// times out, so a single flaky public RPC never stalls the indexer or keeper.
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
}

// DefaultTimeout bounds each individual RPC attempt before failing over.
const DefaultTimeout = 3 * time.Second

// Pool fans calls out across endpoints with round-robin selection and failover.
type Pool struct {
	nodes   []ethNode
	cur     atomic.Uint64
	timeout time.Duration
}

// New dials every URL and returns a Pool. At least one URL is required; dial
// failures are fatal only if no endpoint comes up.
func New(ctx context.Context, urls []string, timeout time.Duration) (*Pool, error) {
	if len(urls) == 0 {
		return nil, errors.New("rpcpool: no RPC URLs configured")
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	nodes := make([]ethNode, 0, len(urls))
	for _, u := range urls {
		c, err := ethclient.DialContext(ctx, u)
		if err != nil {
			log.Warn().Err(err).Str("url", u).Msg("rpcpool: endpoint dial failed, skipping")
			continue
		}
		nodes = append(nodes, c)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("rpcpool: all %d endpoints failed to dial", len(urls))
	}
	log.Info().Int("endpoints", len(nodes)).Msg("rpcpool: ready")
	return newPoolWithNodes(nodes, timeout), nil
}

func newPoolWithNodes(nodes []ethNode, timeout time.Duration) *Pool {
	return &Pool{nodes: nodes, timeout: timeout}
}

// call runs fn against endpoints in round-robin order, failing over on error or
// timeout until one succeeds or all are exhausted.
func call[T any](p *Pool, ctx context.Context, op string, fn func(context.Context, ethNode) (T, error)) (T, error) {
	var zero T
	var lastErr error
	start := p.cur.Add(1)
	n := uint64(len(p.nodes))
	for i := uint64(0); i < n; i++ {
		node := p.nodes[(start+i)%n]
		cctx, cancel := context.WithTimeout(ctx, p.timeout)
		v, err := fn(cctx, node)
		cancel()
		if err == nil {
			return v, nil
		}
		lastErr = err
		log.Warn().Err(err).Str("op", op).Uint64("endpoint", (start+i)%n).
			Msg("rpcpool: call failed, failing over")
	}
	return zero, fmt.Errorf("rpcpool: %s failed on all %d endpoints: %w", op, n, lastErr)
}

func (p *Pool) BlockNumber(ctx context.Context) (uint64, error) {
	return call(p, ctx, "BlockNumber", func(c context.Context, n ethNode) (uint64, error) {
		return n.BlockNumber(c)
	})
}

func (p *Pool) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	return call(p, ctx, "HeaderByNumber", func(c context.Context, n ethNode) (*types.Header, error) {
		return n.HeaderByNumber(c, number)
	})
}

func (p *Pool) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	return call(p, ctx, "FilterLogs", func(c context.Context, n ethNode) ([]types.Log, error) {
		return n.FilterLogs(c, q)
	})
}

func (p *Pool) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	return call(p, ctx, "CallContract", func(c context.Context, n ethNode) ([]byte, error) {
		return n.CallContract(c, msg, blockNumber)
	})
}

func (p *Pool) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	return call(p, ctx, "PendingNonceAt", func(c context.Context, n ethNode) (uint64, error) {
		return n.PendingNonceAt(c, account)
	})
}

func (p *Pool) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	return call(p, ctx, "SuggestGasPrice", func(c context.Context, n ethNode) (*big.Int, error) {
		return n.SuggestGasPrice(c)
	})
}

func (p *Pool) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	return call(p, ctx, "SuggestGasTipCap", func(c context.Context, n ethNode) (*big.Int, error) {
		return n.SuggestGasTipCap(c)
	})
}

// SendTransaction broadcasts to endpoints until one accepts. Re-broadcasting the
// same signed tx is safe (identical hash); "already known"/"nonce too low" from a
// later endpoint just means a prior endpoint already accepted it, so treat those
// as success rather than failing over forever.
func (p *Pool) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	_, err := call(p, ctx, "SendTransaction", func(c context.Context, n ethNode) (struct{}, error) {
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
	return strings.Contains(s, "already known") ||
		strings.Contains(s, "nonce too low") ||
		strings.Contains(s, "already exists")
}
