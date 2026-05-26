// Package ratelimit provides an in-memory sliding-window rate limiter.
package ratelimit

import (
	"sync"
	"time"
)

type entry struct {
	timestamps []int64 // UnixMicro
}

// Limiter is a thread-safe sliding-window rate limiter backed by sync.Map.
type Limiter struct {
	mu      sync.Mutex
	windows map[string]*entry
}

// New creates a Limiter and starts a background cleanup goroutine.
func New() *Limiter {
	l := &Limiter{windows: make(map[string]*entry)}
	go l.cleanup()
	return l
}

// Allow returns true if the caller identified by key is within limit
// requests per window. Thread-safe.
func (l *Limiter) Allow(key string, limit int, window time.Duration) bool {
	now := time.Now().UnixMicro()
	cutoff := time.Now().Add(-window).UnixMicro()

	l.mu.Lock()
	defer l.mu.Unlock()

	e, ok := l.windows[key]
	if !ok {
		e = &entry{}
		l.windows[key] = e
	}

	// drop expired
	fresh := e.timestamps[:0]
	for _, ts := range e.timestamps {
		if ts >= cutoff {
			fresh = append(fresh, ts)
		}
	}
	e.timestamps = fresh

	if len(e.timestamps) >= limit {
		return false
	}
	e.timestamps = append(e.timestamps, now)
	return true
}

func (l *Limiter) cleanup() {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for range t.C {
		cutoff := time.Now().Add(-5 * time.Minute).UnixMicro()
		l.mu.Lock()
		for k, e := range l.windows {
			if len(e.timestamps) == 0 || e.timestamps[len(e.timestamps)-1] < cutoff {
				delete(l.windows, k)
			}
		}
		l.mu.Unlock()
	}
}
