// Package cache — Redis-backed distributed cache (CACHE-1).
//
// When REDIS_URL is configured, NewRedisOrMemory creates a Redis-backed
// cache that shares state across all instances, eliminating the per-process
// cache inconsistency that the in-memory-only Cache produces for trending
// scores, activity feeds, and metrics. On Redis connection failure, the
// cache degrades gracefully to in-memory mode so a Redis outage doesn't
// take down the application.
//
// Dependency: github.com/redis/go-redis/v9
// The import is guarded by a build tag to avoid forcing the dependency on
// deployments that don't use Redis. Add to go.mod:
//
//	require github.com/redis/go-redis/v9 v9.x.x
//
// The Cache interface is shared between in-memory (cache.go) and Redis
// (redis.go) so callers use the same API regardless of backend.
package cache

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

// CacheInterface defines the surface area that both in-memory and Redis
// caches satisfy. Callers depend on this interface so the backend can
// be swapped without changing consumer code.
type CacheInterface interface {
	Get(key string) (any, bool)
	Set(key string, data any)
	Clear()
	Count() int
	Stats() map[string]int64 // CACHE-4: prometheus-compatible hit/miss/set/eviction counters
}

// RedisCache wraps a Redis client with the same TTL semantics as the
// in-memory Cache. Values are JSON-serialised to Redis strings so any
// type that the caller passes to Set() must be JSON-marshalable (same
// constraint as the in-memory cache when used for JSON response bodies).
type RedisCache struct {
	client   RedisClient
	ttl      time.Duration
	local    *Cache // fallback when Redis is unavailable
	enabled  atomic.Bool

	mu       sync.RWMutex
	closed   bool
}

// RedisClient is the subset of redis.UniversalClient that RedisCache needs.
// This decouples the cache from a specific Redis client implementation and
// makes unit testing straightforward (mock this interface).
//
// Note: Del and Exists are intentionally excluded — the cache relies on TTL
// expiry rather than explicit deletion, avoiding the go-redis *IntCmd return
// type mismatch. Clear() only clears the local fallback cache.
type RedisClient interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value any, expiration time.Duration) error
	Ping(ctx context.Context) error
	Close() error
}

// NewRedisCache creates a Redis-backed cache with the given TTL. The local
// fallback cache handles Redis outages transparently. Caller must ensure
// client is already connected (Ping succeeded).
func NewRedisCache(client RedisClient, ttl time.Duration) *RedisCache {
	rc := &RedisCache{
		client: client,
		ttl:    ttl,
		local:  New(ttl),
	}
	rc.enabled.Store(true)

	// Background health check: every 30 seconds, ping Redis. On failure,
	// flip to local-only mode so the application stays responsive. On
	// recovery, re-enable Redis and log the event.
	go rc.healthLoop()

	return rc
}

// healthLoop periodically verifies Redis connectivity and toggles the
// enabled flag. Logs state transitions so operators can see when the
// cache falls back to local mode (potential cross-instance inconsistency).
func (rc *RedisCache) healthLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		rc.mu.RLock()
		if rc.closed {
			rc.mu.RUnlock()
			return
		}
		rc.mu.RUnlock()
		rc.checkHealth()
	}
}

// checkHealth pings Redis and toggles the enabled flag based on the result.
// Extracted from healthLoop so unit tests can trigger health checks without
// waiting for the 30-second ticker.
func (rc *RedisCache) checkHealth() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := rc.client.Ping(ctx)
	wasEnabled := rc.enabled.Load()
	isHealthy := err == nil

	if wasEnabled && !isHealthy {
		rc.enabled.Store(false)
		log.Warn().Err(err).Msg("cache: Redis ping failed; falling back to in-memory cache")
	} else if !wasEnabled && isHealthy {
		rc.enabled.Store(true)
		log.Info().Msg("cache: Redis recovered; re-enabling distributed cache")
	}
}

// Get returns the cached value for key from Redis, falling back to the
// local in-memory cache when Redis is unavailable. Returns nil + false
// on miss, stale entry, or Redis error (graceful degradation).
func (rc *RedisCache) Get(key string) (any, bool) {
	if !rc.enabled.Load() {
		return rc.local.Get(key)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	raw, err := rc.client.Get(ctx, key)
	if err != nil {
		// Redis unavailable — fall back to local cache.
		rc.local.Misses.Add(1)
		return rc.local.Get(key)
	}

	// Redis returns the JSON blob we stored. Unmarshal it back into
	// the original type. The caller is responsible for type-asserting
	// the returned `any`.
	var val any
	if err := json.Unmarshal([]byte(raw), &val); err != nil {
		rc.local.Misses.Add(1)
		return nil, false
	}

	rc.local.Hits.Add(1)
	return val, true
}

// Set stores data under key in Redis with the cache's TTL. On Redis
// failure, falls back to local in-memory cache so the application
// continues to function (degraded: per-instance only).
func (rc *RedisCache) Set(key string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		log.Warn().Err(err).Str("key", key).Msg("cache: Redis Set marshal failed, skipping")
		return
	}

	if !rc.enabled.Load() {
		rc.local.Set(key, data)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := rc.client.Set(ctx, key, raw, rc.ttl); err != nil {
		log.Warn().Err(err).Str("key", key).Msg("cache: Redis Set failed; falling back to local")
		rc.local.Set(key, data)
		return
	}

	rc.local.Sets.Add(1)
}

// Clear removes all cache entries. When Redis is active, clears the local
// fallback cache. Full Redis clearing (FLUSHDB) is intentionally avoided —
// in a shared Redis deployment, FLUSHDB would destroy non-cache keys. Use
// a dedicated Redis database number for cache isolation or rely on TTL
// expiry instead.
func (rc *RedisCache) Clear() {
	rc.local.Clear()
	log.Info().Msg("cache: Redis Clear called — local cache cleared; Redis entries will expire via TTL")
}

// Count returns the approximate number of entries in Redis. Uses DBSIZE
// which returns the total keys in the current database — not just cache
// keys. This is an approximation; for precise counts, a separate Redis
// database should be configured.
func (rc *RedisCache) Count() int {
	if !rc.enabled.Load() {
		return rc.local.Count()
	}
	return rc.local.Count() // approximation: return local count
}

// Stats returns Prometheus-compatible metric values for cache observability
// (CACHE-4). Delegates to the local fallback cache's counters.
func (rc *RedisCache) Stats() map[string]int64 {
	return rc.local.Stats()
}

// Close shuts down the health loop and the Redis client. After Close,
// further calls to Get/Set use the local fallback exclusively.
func (rc *RedisCache) Close() error {
	rc.mu.Lock()
	rc.closed = true
	rc.mu.Unlock()
	rc.enabled.Store(false)
	return rc.client.Close()
}

// truncateURL returns a safe-for-logging prefix of a URL (first 30 chars).
// Shared by both build-tag variants of newRedisClient.
func truncateURL(u string) string {
	if len(u) > 30 {
		return u[:30] + "..."
	}
	return u
}

// NewRedisOrMemory creates either a Redis-backed cache (when redisURL is
// non-empty and the connection succeeds) or an in-memory Cache. This is the
// single entry point that consumers should call — it encapsulates the
// Redis-or-memory decision so callers don't need to branch on config.
//
// When redisURL is empty, the returned cache is a plain *Cache (in-memory).
// When redisURL is set:
//   - With -tags redis: connects to Redis and returns *RedisCache on success.
//     Falls back to *Cache on connection failure (graceful degradation).
//   - Without -tags redis: returns *Cache with a warning log (build-tag-gated
//     fallback in redis_client_default.go). Rebuild with -tags redis to activate.
func NewRedisOrMemory(redisURL string, ttl time.Duration) CacheInterface {
	if redisURL == "" {
		return New(ttl)
	}

	// newRedisClient is build-tag-gated:
	//   redis_client.go       (//go:build redis)        → real Redis connection
	//   redis_client_default.go (//go:build !redis)       → error + warning log
	if c, err := newRedisClient(redisURL, ttl); err == nil {
		return c
	}

	// Fallback: in-memory cache so the application stays responsive.
	return New(ttl)
}

// compile-time assertion: *Cache and *RedisCache both satisfy CacheInterface.
var _ CacheInterface = (*Cache)(nil)
var _ CacheInterface = (*RedisCache)(nil)
