// Package cache provides a simple TTL-based in-memory cache for read-heavy
// API endpoints. It is NOT a general-purpose distributed cache — keys live in
// process memory only and are evicted on TTL expiry (lazy: on read, the
// goroutine that finds a stale entry deletes it).
//
// Use for endpoints where:
//   - stale-by-TTL data is acceptable (trending scores, activity feed)
//   - the DB query is noticeably more expensive than a map lookup + JSON round-trip
//   - per-request consistency with the DB is not required
package cache

import (
	"sync"
	"sync/atomic"
	"time"
)

// entry holds one cached value with its expiry deadline.
type entry struct {
	data      any
	expiresAt time.Time
}

// Cache is a TTL-keyed in-memory cache. Safe for concurrent use.
// Zero value is NOT usable — use New.
type Cache struct {
	mu    sync.RWMutex
	items map[string]*entry
	ttl   time.Duration

	// Prometheus-compatible metrics (atomic counters — no lock contention).
	Hits   atomic.Int64
	Misses atomic.Int64
	Sets   atomic.Int64
	Evictions atomic.Int64
}

// New creates a cache where every entry lives for ttl before becoming
// stale. A zero TTL is allowed but useless (every Get returns nil).
func New(ttl time.Duration) *Cache {
	return &Cache{
		items: make(map[string]*entry),
		ttl:   ttl,
	}
}

// Get returns the cached value for key, or nil + false on miss (or stale).
func (c *Cache) Get(key string) (any, bool) {
	c.mu.RLock()
	e, ok := c.items[key]
	c.mu.RUnlock()
	if !ok {
		c.Misses.Add(1)
		return nil, false
	}
	if time.Now().After(e.expiresAt) {
		// Lazy eviction: delete on read so a burst-write pattern doesn't
		// leave dead entries until the next GC.
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		c.Misses.Add(1)
		c.Evictions.Add(1)
		return nil, false
	}
	c.Hits.Add(1)
	return e.data, true
}

// Set stores data under key with the cache's TTL. Overwrites any existing
// entry for the same key.
func (c *Cache) Set(key string, data any) {
	c.mu.Lock()
	c.items[key] = &entry{data: data, expiresAt: time.Now().Add(c.ttl)}
	c.mu.Unlock()
	c.Sets.Add(1)
}

// Clear removes all entries from the cache. Useful in tests or when
// the underlying data source has changed (e.g. reindex completed).
func (c *Cache) Clear() {
	c.mu.Lock()
	clear(c.items)
	c.mu.Unlock()
}

// Count returns the number of entries currently in the cache (including
// stale entries that have not been lazily evicted yet).
func (c *Cache) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Warm fills the cache with pre-computed values. Called on startup to
// prevent cold-start latency spikes for frequently-accessed keys.
func (c *Cache) Warm(items map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	expiry := time.Now().Add(c.ttl)
	for k, v := range items {
		c.items[k] = &entry{data: v, expiresAt: expiry}
	}
}
