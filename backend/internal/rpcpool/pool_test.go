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
