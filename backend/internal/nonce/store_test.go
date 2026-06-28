package nonce

import (
	"sync"
	"testing"
	"time"
)

func TestMemStoreSetIfFreeRejectsSecondIssue(t *testing.T) {
	s := New()
	if !s.SetIfFree("0xabc", "n1", time.Minute) {
		t.Fatal("first SetIfFree should succeed")
	}
	if s.SetIfFree("0xabc", "n2", time.Minute) {
		t.Fatal("second SetIfFree must FAIL while first is live")
	}
	// Seed an entry with an already-expired TTL (negative TTL means
	// expiresAt is in the past). Use a different address so the live
	// entry above isn't disturbed — this genuinely exercises the
	// expiry path in SetIfFree, not a manual GetDel clearing.
	s.SetIfFree("0xexpired", "expired", -time.Second)
	if !s.SetIfFree("0xexpired", "n3", time.Minute) {
		t.Fatal("SetIfFree should succeed after prior entry's TTL has expired")
	}
}

func TestMemStoreGetDelIsSingleUse(t *testing.T) {
	s := New()
	s.SetIfFree("0xabc", "n", time.Minute)
	v1, ok := s.GetDel("0xabc")
	if !ok || v1 != "n" {
		t.Fatalf("first GetDel: %q %v", v1, ok)
	}
	v2, ok := s.GetDel("0xabc")
	if ok || v2 != "" {
		t.Fatalf("second GetDel must fail: %q %v", v2, ok)
	}
}


func TestMemStoreSetIfFreeConcurrentRace(t *testing.T) {
	s := New()
	s.SetIfFree("0xrace", "expired", -time.Second)
	s.GetDel("0xrace") // clear the expired entry so goroutines race on a free slot
	var wg sync.WaitGroup
	var mu sync.Mutex
	var successes int
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if s.SetIfFree("0xrace", "n"+string(rune('a'+idx)), time.Minute) {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if successes != 1 { t.Fatalf("expected 1, got %d", successes) }
}