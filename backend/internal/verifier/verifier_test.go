package verifier

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
)

type fakeStore struct {
	addrs   []string
	stamped map[string]bool
	listErr error
}

func (f *fakeStore) ListCollectionsForVerification(context.Context, time.Time, int) ([]string, error) {
	return f.addrs, f.listErr
}

func (f *fakeStore) SetCollectionVerification(_ context.Context, addr string, standardVerified bool) error {
	if f.stamped == nil {
		f.stamped = map[string]bool{}
	}
	f.stamped[addr] = standardVerified
	return nil
}

// fakeCaller answers supportsInterface per-collection so one sweep can mix
// standard contracts, non-standard contracts and unreachable ones.
type fakeCaller struct {
	byAddr map[string]func() ([]byte, error)
}

func (f *fakeCaller) CallContract(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
	// To.Hex() is EIP-55 checksummed; the test's literals are not.
	return f.byAddr[strings.ToLower(msg.To.Hex())]()
}

func (f *fakeCaller) BlockNumber(context.Context) (uint64, error) { return 0, nil }

func trueWord() ([]byte, error) {
	w := make([]byte, 32)
	w[31] = 1
	return w, nil
}

func falseWord() ([]byte, error) { return make([]byte, 32), nil }

const (
	standardColl    = "0x832d74cfbb4617b50c32cd110dfe16837a359b35"
	nonStandardColl = "0x0000000000000000000000000000000000000abc"
	downColl        = "0x0000000000000000000000000000000000000def"
)

func TestSweepOnceStampsOutcomes(t *testing.T) {
	store := &fakeStore{addrs: []string{standardColl, nonStandardColl, downColl}}
	eth := &fakeCaller{byAddr: map[string]func() ([]byte, error){
		standardColl:    trueWord,
		nonStandardColl: func() ([]byte, error) { return nil, errors.New("execution reverted") },
		downColl:        func() ([]byte, error) { return nil, errors.New("dial tcp: connection refused") },
	}}

	r := New(store, eth)
	n, err := r.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Fatalf("stamped %d collections, want 2", n)
	}
	if got, ok := store.stamped[standardColl]; !ok || !got {
		t.Fatalf("standard collection stamped %v (present=%v), want true", got, ok)
	}
	if got, ok := store.stamped[nonStandardColl]; !ok || got {
		t.Fatalf("non-standard collection stamped %v (present=%v), want false", got, ok)
	}
	// An RPC failure must leave the row untouched so the next pass retries it —
	// stamping `false` here would drop badges site-wide during an outage.
	if _, ok := store.stamped[downColl]; ok {
		t.Fatal("unreachable collection was stamped; it must be left for retry")
	}
}

func TestSweepOnceERC1155(t *testing.T) {
	store := &fakeStore{addrs: []string{standardColl}}
	var calls int
	eth := &fakeCaller{byAddr: map[string]func() ([]byte, error){
		standardColl: func() ([]byte, error) {
			calls++
			if calls == 1 { // ERC-721 probe misses
				return falseWord()
			}
			return trueWord()
		},
	}}

	if _, err := New(store, eth).SweepOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !store.stamped[standardColl] {
		t.Fatal("ERC-1155 collection should be standard_verified")
	}
}

func TestSweepOncePropagatesListError(t *testing.T) {
	boom := errors.New("db down")
	_, err := New(&fakeStore{listErr: boom}, &fakeCaller{}).SweepOnce(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}

func TestSweepOnceStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &fakeStore{addrs: []string{standardColl}}
	n, err := New(store, &fakeCaller{}).SweepOnce(ctx)
	if err == nil {
		t.Fatal("want context error")
	}
	if n != 0 {
		t.Fatalf("stamped %d, want 0", n)
	}
}
