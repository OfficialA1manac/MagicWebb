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

// fakeNode is an ethNode whose BlockNumber either fails or returns its id, and
// counts how many times it was called — enough to assert round-robin + failover.
type fakeNode struct {
	id    uint64
	fail  bool
	calls atomic.Int64
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
func (f *fakeNode) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	f.calls.Add(1)
	if f.fail {
		return errors.New("rpc down")
	}
	return nil
}

func TestPoolRoundRobin(t *testing.T) {
	a, b := &fakeNode{id: 1}, &fakeNode{id: 2}
	p := newPoolWithNodes([]ethNode{a, b}, time.Second)

	// Three calls across two healthy nodes should spread, not hammer one.
	for i := 0; i < 4; i++ {
		if _, err := p.BlockNumber(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if a.calls.Load() == 0 || b.calls.Load() == 0 {
		t.Fatalf("round-robin uneven: a=%d b=%d", a.calls.Load(), b.calls.Load())
	}
}

func TestPoolFailover(t *testing.T) {
	down, up := &fakeNode{id: 1, fail: true}, &fakeNode{id: 2}
	p := newPoolWithNodes([]ethNode{down, up}, time.Second)

	// Even if the cursor lands on the dead node first, the call must succeed via
	// the healthy one.
	got, err := p.BlockNumber(context.Background())
	if err != nil {
		t.Fatalf("failover did not recover: %v", err)
	}
	if got != 2 {
		t.Fatalf("got %d, want 2 (served by healthy node)", got)
	}
}

func TestPoolAllDownReturnsError(t *testing.T) {
	p := newPoolWithNodes([]ethNode{&fakeNode{fail: true}, &fakeNode{fail: true}}, time.Second)
	_, err := p.BlockNumber(context.Background())
	if err == nil {
		t.Fatal("want error when every endpoint is down")
	}
}
