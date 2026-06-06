package ratelimit

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAllowUnderLimit(t *testing.T) {
	l := New()
	for i := range 5 {
		if !l.Allow("k", 5, time.Minute) {
			t.Fatalf("request %d should be allowed", i)
		}
	}
}

func TestAllowBlocksAtLimit(t *testing.T) {
	l := New()
	// each Allow mutates the window, so these are distinct requests
	if !l.Allow("k", 2, time.Minute) {
		t.Fatal("first request should be allowed")
	}
	if !l.Allow("k", 2, time.Minute) {
		t.Fatal("second request should be allowed")
	}
	if l.Allow("k", 2, time.Minute) {
		t.Fatal("third request should be blocked")
	}
}

func TestWindowSlides(t *testing.T) {
	l := New()
	window := 40 * time.Millisecond
	if !l.Allow("k", 1, window) {
		t.Fatal("first request should be allowed")
	}
	if l.Allow("k", 1, window) {
		t.Fatal("second request within window should be blocked")
	}
	time.Sleep(60 * time.Millisecond) // let the window slide past
	if !l.Allow("k", 1, window) {
		t.Fatal("request after window should be allowed again")
	}
}

func TestPerKeyIsolation(t *testing.T) {
	l := New()
	if !l.Allow("a", 1, time.Minute) {
		t.Fatal("key a first request should pass")
	}
	if !l.Allow("b", 1, time.Minute) {
		t.Fatal("key b must have its own independent window")
	}
	if l.Allow("a", 1, time.Minute) {
		t.Fatal("key a second request should be blocked")
	}
}

func TestConcurrentAllowRespectsLimit(t *testing.T) {
	l := New()
	const limit = 50
	var allowed int64
	var wg sync.WaitGroup
	for range 200 {
		wg.Go(func() {
			if l.Allow("shared", limit, time.Minute) {
				atomic.AddInt64(&allowed, 1)
			}
		})
	}
	wg.Wait()
	if allowed != limit {
		t.Fatalf("allowed = %d, want exactly %d (no over-admission under race)", allowed, limit)
	}
}
