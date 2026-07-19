package rpcpool

import (
	"context"
	"errors"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// fakeNode is an ethNode whose calls either fail or return its id, counting
// invocations — enough to assert sticky selection + failover semantics.
type fakeNode struct {
	id      uint64
	fail    bool
	sendErr error // overrides fail for SendTransaction when set
	calls   atomic.Int64
}

func (f *fakeNode) BlockNumber(ctx context.Context) (uint64, error) {
	f.calls.Add(1)
	if f.fail {
		return 0, errors.New("rpc down")
	}
	return f.id, nil
}
func (f *fakeNode) HeaderByNumber(ctx context.Context, n *big.Int) (*types.Header, error) {
	return nil, nil
}
func (f *fakeNode) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	return nil, nil
}
func (f *fakeNode) CallContract(ctx context.Context, m ethereum.CallMsg, n *big.Int) ([]byte, error) {
	return nil, nil
}
func (f *fakeNode) PendingNonceAt(ctx context.Context, a common.Address) (uint64, error) {
	return 0, nil
}
func (f *fakeNode) SuggestGasPrice(ctx context.Context) (*big.Int, error)  { return nil, nil }
func (f *fakeNode) SuggestGasTipCap(ctx context.Context) (*big.Int, error) { return nil, nil }
func (f *fakeNode) TransactionReceipt(ctx context.Context, h common.Hash) (*types.Receipt, error) {
	return nil, nil
}
func (f *fakeNode) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	return big.NewInt(0), nil
}
func (f *fakeNode) Close() {}
func (f *fakeNode) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	f.calls.Add(1)
	if f.sendErr != nil {
		return f.sendErr
	}
	if f.fail {
		return errors.New("rpc down")
	}
	return nil
}

func TestPoolSticky(t *testing.T) {
	a, b := &fakeNode{id: 1}, &fakeNode{id: 2}
	p := newPoolWithNodes([]ethNode{a, b}, time.Second)

	// A healthy preferred endpoint serves every call — the indexer must see one
	// consistent chain view, so reads never rotate on success.
	for i := 0; i < 4; i++ {
		got, err := p.BlockNumber(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got != 1 {
			t.Fatalf("call %d served by node %d, want sticky node 1", i, got)
		}
	}
	if b.calls.Load() != 0 {
		t.Fatalf("sticky violated: secondary saw %d calls", b.calls.Load())
	}
}

func TestPoolFailoverMovesCursor(t *testing.T) {
	down, up := &fakeNode{id: 1, fail: true}, &fakeNode{id: 2}
	p := newPoolWithNodes([]ethNode{down, up}, time.Second)

	got, err := p.BlockNumber(context.Background())
	if err != nil {
		t.Fatalf("failover did not recover: %v", err)
	}
	if got != 2 {
		t.Fatalf("got %d, want 2 (served by healthy node)", got)
	}
	// Cursor must now stick to the healthy node — no repeated probing of the
	// dead one on subsequent calls.
	downCalls := down.calls.Load()
	if _, err := p.BlockNumber(context.Background()); err != nil {
		t.Fatal(err)
	}
	if down.calls.Load() != downCalls {
		t.Fatal("cursor did not move off the failed endpoint")
	}
}

func TestPoolAllDownReturnsError(t *testing.T) {
	p := newPoolWithNodes([]ethNode{&fakeNode{fail: true}, &fakeNode{fail: true}}, time.Second)
	_, err := p.BlockNumber(context.Background())
	if err == nil {
		t.Fatal("want error when every endpoint is down")
	}
}

func TestPoolCancelledContextStopsFailover(t *testing.T) {
	a := &fakeNode{fail: true}
	b := &fakeNode{fail: true}
	p := newPoolWithNodes([]ethNode{a, b}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.BlockNumber(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if a.calls.Load() != 0 || b.calls.Load() != 0 {
		t.Fatal("failover loop ran despite cancelled context")
	}
}

func TestSendTransactionAlreadyKnownIsSuccess(t *testing.T) {
	n := &fakeNode{sendErr: errors.New("already known")}
	p := newPoolWithNodes([]ethNode{n}, time.Second)
	if err := p.SendTransaction(context.Background(), nil); err != nil {
		t.Fatalf("'already known' must be success (same-hash duplicate), got %v", err)
	}
}

func TestSendTransactionNonceTooLowIsError(t *testing.T) {
	// "nonce too low" can mean a DIFFERENT tx consumed the nonce and this one
	// was never broadcast — it must surface as an error so keepers retry.
	n := &fakeNode{sendErr: errors.New("nonce too low")}
	p := newPoolWithNodes([]ethNode{n}, time.Second)
	if err := p.SendTransaction(context.Background(), nil); err == nil {
		t.Fatal("'nonce too low' must not be reported as success")
	}
}

// ── RPC-2: Concurrent FilterLogs tests ────────────────────────────────────

// fakeLogNode allows custom FilterLogs responses for concurrent path testing.
type fakeLogNode struct {
	fakeNode
	logs []types.Log
	err  error
}

func (f *fakeLogNode) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return f.logs, nil
}

func makeLogs(count int, baseTx string) []types.Log {
	out := make([]types.Log, count)
	for i := 0; i < count; i++ {
		out[i] = types.Log{TxHash: common.HexToHash(baseTx + string(rune('0'+i)))}
	}
	return out
}

func TestConcurrentFilterLogsMatchingResults(t *testing.T) {
	// Two endpoints return identical logs → concurrent path succeeds.
	logs := makeLogs(3, "0xabc")
	a := &fakeLogNode{logs: logs}
	b := &fakeLogNode{logs: logs}
	c := &fakeLogNode{logs: logs} // third endpoint — slower, result ignored

	p := newPoolWithNodes([]ethNode{a, b, c}, time.Second)

	prevAttempts := ConcurrentFilterLogsAttempts.Load()
	prevSuccesses := ConcurrentFilterLogsSuccesses.Load()

	got, err := p.FilterLogs(context.Background(), ethereum.FilterQuery{})
	if err != nil {
		t.Fatalf("concurrent FilterLogs failed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 logs, got %d", len(got))
	}

	// At least 2 endpoints should have been called (first 2 responding).
	totalCalls := a.calls.Load() + b.calls.Load() + c.calls.Load()
	if totalCalls < 2 {
		t.Fatalf("want at least 2 FilterLogs calls, got %d", totalCalls)
	}

	// Metrics: this call must have incremented Attempts and Successes.
	if ConcurrentFilterLogsAttempts.Load() <= prevAttempts {
		t.Error("ConcurrentFilterLogsAttempts not incremented by this call")
	}
	if ConcurrentFilterLogsSuccesses.Load() <= prevSuccesses {
		t.Error("ConcurrentFilterLogsSuccesses not incremented by this call")
	}
}

func TestConcurrentFilterLogsMismatchingResults(t *testing.T) {
	// Two endpoints disagree → falls back to sequential failover.
	a := &fakeLogNode{logs: makeLogs(3, "0xabc")}
	b := &fakeLogNode{logs: makeLogs(4, "0xdef")}

	p := newPoolWithNodes([]ethNode{a, b}, time.Second)

	prevFallbacks := ConcurrentFilterLogsFallbacks.Load()

	got, err := p.FilterLogs(context.Background(), ethereum.FilterQuery{})
	if err != nil {
		t.Fatalf("sequential fallback FilterLogs failed: %v", err)
	}
	// Sequential fallback uses the first successful response (a = 3 logs).
	if len(got) != 3 {
		t.Fatalf("want 3 logs from sequential fallback, got %d", len(got))
	}

	// Metrics: this call must have incremented Fallbacks.
	if ConcurrentFilterLogsFallbacks.Load() <= prevFallbacks {
		t.Error("ConcurrentFilterLogsFallbacks not incremented on mismatch")
	}
}

func TestConcurrentFilterLogsSingleEndpoint(t *testing.T) {
	// Single endpoint → concurrent path not used, goes straight to sequential.
	a := &fakeLogNode{logs: makeLogs(2, "0xabc")}
	p := newPoolWithNodes([]ethNode{a}, time.Second)

	prevAttempts := ConcurrentFilterLogsAttempts.Load()

	got, err := p.FilterLogs(context.Background(), ethereum.FilterQuery{})
	if err != nil {
		t.Fatalf("FilterLogs failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 logs, got %d", len(got))
	}

	// Single endpoint must NOT trigger concurrent path.
	if ConcurrentFilterLogsAttempts.Load() != prevAttempts {
		t.Error("single endpoint triggered concurrent FilterLogs path")
	}
}

func TestConcurrentFilterLogsOneEndpointErrors(t *testing.T) {
	// One endpoint errors, other succeeds → concurrent path succeeds with 2
	// matching results (the errored endpoint is skipped).
	logs := makeLogs(5, "0xabc")
	a := &fakeLogNode{logs: logs}
	b := &fakeLogNode{err: errors.New("rpc down")}
	c := &fakeLogNode{logs: logs}

	p := newPoolWithNodes([]ethNode{a, b, c}, time.Second)

	got, err := p.FilterLogs(context.Background(), ethereum.FilterQuery{})
	if err != nil {
		t.Fatalf("concurrent FilterLogs failed despite healthy endpoints: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("want 5 logs, got %d", len(got))
	}
}

func TestConcurrentFilterLogsAllError(t *testing.T) {
	// All endpoints error → concurrent returns nothing, sequential also fails.
	a := &fakeLogNode{err: errors.New("rpc down")}
	b := &fakeLogNode{err: errors.New("rpc down")}

	p := newPoolWithNodes([]ethNode{a, b}, time.Second)

	_, err := p.FilterLogs(context.Background(), ethereum.FilterQuery{})
	if err == nil {
		t.Fatal("want error when all endpoints fail")
	}
}
