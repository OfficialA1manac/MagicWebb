package nonce

import (
	"sync"
	"testing"
	"time"
)

func TestSetGetDelSingleUse(t *testing.T) {
	s := New()
	s.Set("0xabc", "nonce-1", time.Minute)

	v, ok := s.GetDel("0xabc")
	if !ok || v != "nonce-1" {
		t.Fatalf("first GetDel = %q,%v; want nonce-1,true", v, ok)
	}
	// single-use: a second read must fail (replay protection)
	if _, ok := s.GetDel("0xabc"); ok {
		t.Fatal("expected second GetDel to fail (nonce consumed)")
	}
}

func TestGetDelExpired(t *testing.T) {
	s := New()
	s.Set("0xabc", "nonce-1", 20*time.Millisecond)
	time.Sleep(40 * time.Millisecond)
	if _, ok := s.GetDel("0xabc"); ok {
		t.Fatal("expected expired nonce to be rejected")
	}
}

func TestGetDelMissing(t *testing.T) {
	s := New()
	if _, ok := s.GetDel("0xnope"); ok {
		t.Fatal("expected missing key to return false")
	}
}

func TestSetOverwrites(t *testing.T) {
	s := New()
	s.Set("0xabc", "old", time.Minute)
	s.Set("0xabc", "new", time.Minute)
	v, ok := s.GetDel("0xabc")
	if !ok || v != "new" {
		t.Fatalf("GetDel = %q,%v; want new,true", v, ok)
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := New()
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			key := string(rune('A' + i%26))
			s.Set(key, "v", time.Minute)
			s.GetDel(key)
		})
	}
	wg.Wait() // -race will flag any data race
}
