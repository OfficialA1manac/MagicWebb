// Package ratelimit provides a rate limiter. In-memory (sliding window,
// per-instance) by default; Postgres-backed (fixed window, shared across
// instances) when created with NewPg.
package ratelimit

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

type entry struct {
	timestamps []int64 // UnixMicro
}

// Limiter is a thread-safe rate limiter. When pool != nil, Allow uses a shared
// Postgres fixed-window counter; otherwise an in-memory sliding window.
type Limiter struct {
	mu      sync.Mutex
	windows map[string]*entry
	pool    *pgxpool.Pool
}

// New creates an in-memory Limiter and starts background cleanup.
func New() *Limiter {
	l := &Limiter{windows: make(map[string]*entry)}
	go l.cleanup()
	return l
}

// NewPg creates a Postgres-backed (shared) Limiter and starts a background
// sweep of expired windows.
func NewPg(pool *pgxpool.Pool) *Limiter {
	l := &Limiter{windows: make(map[string]*entry), pool: pool}
	go l.sweepPg()
	return l
}

// Allow returns true if the caller identified by key is within limit requests
// per window.
func (l *Limiter) Allow(key string, limit int, window time.Duration) bool {
	if l.pool != nil {
		return l.allowPg(key, limit, window)
	}
	return l.allowMem(key, limit, window)
}

// allowPg is an atomic fixed-window counter: one round-trip UPSERT…RETURNING.
// Fail-open on DB error (availability over strictness for a rate limiter).
func (l *Limiter) allowPg(key string, limit int, window time.Duration) bool {
	windowStart := time.Now().Truncate(window)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var count int
	err := l.pool.QueryRow(ctx,
		`INSERT INTO rate_limits(rl_key, window_start, count) VALUES($1,$2,1)
		 ON CONFLICT(rl_key, window_start) DO UPDATE SET count = rate_limits.count + 1
		 RETURNING count`,
		key, windowStart,
	).Scan(&count)
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("ratelimit: pg allow failed (fail-open)")
		return true
	}
	return count <= limit
}

func (l *Limiter) sweepPg() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = l.pool.Exec(ctx, `DELETE FROM rate_limits WHERE window_start < now() - interval '1 hour'`)
		cancel()
	}
}

func (l *Limiter) allowMem(key string, limit int, window time.Duration) bool {
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
