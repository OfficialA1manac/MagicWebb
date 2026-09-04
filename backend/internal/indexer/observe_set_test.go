package indexer

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

func hashN(i int) common.Hash {
	var h common.Hash
	binary.BigEndian.PutUint64(h[:8], uint64(i))
	return h
}

// Marking more than observedMaxEntries fresh hashes inside one TTL window
// must still leave the set capped — the age sweep frees nothing, so the
// hard cap has to evict the oldest.
func TestObservedSet_HardCapUnderBurst(t *testing.T) {
	var s observedSet
	const n = observedMaxEntries * 2
	for i := 0; i < n; i++ {
		s.mark(hashN(i))
	}
	if got := len(s.m); got > observedMaxEntries {
		t.Fatalf("len = %d, want <= %d", got, observedMaxEntries)
	}
	// Newest survives; the very first is gone.
	if !s.seen(hashN(n - 1)) {
		t.Fatal("newest hash evicted")
	}
	if s.seen(hashN(0)) {
		t.Fatal("oldest hash survived the cap")
	}
}

// Stale entries are swept by age before the cap has to evict live ones.
func TestObservedSet_AgeSweepFirst(t *testing.T) {
	s := observedSet{m: make(map[common.Hash]time.Time)}
	stale := time.Now().Add(-2 * observedTTL)
	for i := 0; i < observedMaxEntries; i++ {
		s.m[hashN(i)] = stale
	}
	s.mark(hashN(observedMaxEntries)) // tips len over the cap
	if got := len(s.m); got != 1 {
		t.Fatalf("len = %d after age sweep, want 1", got)
	}
	if !s.seen(hashN(observedMaxEntries)) {
		t.Fatal("fresh mark lost")
	}
}
