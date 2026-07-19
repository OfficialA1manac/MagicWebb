package graphql

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
)

// ── GQL-2: Apollo-style tiered response cache ─────────────────────────
//
// Caches complete GraphQL JSON responses with operation-aware TTLs:
// queries that access slow-changing data (metrics, collection stats) are
// cached for 60s; medium-change data (trending, collections list) for 30s;
// volatile data (listings, auctions, activity) for 15s. Mutations and
// subscriptions are never cached.
//
// Cache entries are keyed by sha256(rawQuery + stableJSON(variables)).
// On cache hit, the DB and all resolvers are skipped entirely, reducing
// latency to near-zero. On miss, the response is captured after execution
// and cached for its tiered TTL.
//
// Thread-safety: sync.RWMutex protects the store map. LRU tracking uses
// per-entry lastAccessed timestamps; eviction removes the least-recently-
// accessed entry when at capacity (500 entries).

const (
	// GQL-2: Tiered cache TTLs based on data volatility.
	ttlSlow     = 60 * time.Second // metrics, collection(address:) — change rarely
	ttlMedium   = 30 * time.Second // trending, collections list
	ttlFast     = 15 * time.Second // listings, auctions, activity
	ttlDefault  = 15 * time.Second // unknown queries

	maxCacheEntries = 500
)

// operationTTL returns the cache TTL for a GraphQL query based on its
// top-level field names. Slow-changing operations (metrics, collection
// stats) get longer TTLs; volatile data (listings, activity) gets shorter.
// For multi-field queries, the MINIMUM TTL across all fields is used —
// conservative: never serve stale data when fast + slow fields are batched.
func operationTTL(oc *graphql.OperationContext) time.Duration {
	if oc == nil || oc.Operation == nil {
		return ttlDefault
	}
	minTTL := ttlSlow // start with the longest, tighten as we find faster fields
	for _, sel := range oc.Operation.SelectionSet {
		field, ok := sel.(*ast.Field)
		if !ok {
			continue
		}
		ttl := ttlForField(field.Name)
		if ttl < minTTL {
			minTTL = ttl
		}
	}
	return minTTL
}

// ttlForField returns the cache TTL for a single top-level field name.
func ttlForField(name string) time.Duration {
	switch name {
	case "metrics", "collectionStats", "collection":
		return ttlSlow // stats change rarely
	case "collections", "trending":
		return ttlMedium // list changes periodically
	case "listings", "listing", "auctions", "auction", "activity", "search":
		return ttlFast // volatile data
	default:
		return ttlDefault
	}
}

// ── Cache data structures ─────────────────────────────────────────────

type cacheEntry struct {
	data      []byte
	expiresAt time.Time
	// lastAccessedNanos is an atomic UnixNano timestamp for LRU eviction.
	// ALL reads and writes use sync/atomic to avoid data races between
	// the cache-hit path (no lock, atomic write) and evictOneLocked
	// (write lock, but Go requires atomic reads when paired with atomic
	// writes on the same variable).
	lastAccessedNanos int64
}

// ResponseCacheExtension implements graphql.OperationInterceptor to cache
// GraphQL responses with tiered TTLs and LRU eviction.
type ResponseCacheExtension struct {
	mu    sync.RWMutex
	store map[string]*cacheEntry

	// GQL-2: Per-operation cache metrics (atomic counters).
	HitsTotal   atomic.Int64
	MissesTotal atomic.Int64
	SetsTotal   atomic.Int64
	Evictions   atomic.Int64
}

// NewResponseCacheExtension creates a tiered response cache with
// background eviction every 60s.
func NewResponseCacheExtension() *ResponseCacheExtension {
	ce := &ResponseCacheExtension{
		store: make(map[string]*cacheEntry),
	}
	go func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for range t.C {
			ce.evict()
		}
	}()
	return ce
}

// ExtensionName returns the extension name for gqlgen registration.
func (c *ResponseCacheExtension) ExtensionName() string {
	return "ResponseCache"
}

// Validate is a no-op for the response cache.
func (c *ResponseCacheExtension) Validate(s graphql.ExecutableSchema) error {
	return nil
}

// InterceptOperation checks the cache and returns a cached response on hit.
// On miss, executes the operation and caches the result.
func (c *ResponseCacheExtension) InterceptOperation(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	oc := getOperationContextSafe(ctx)
	if oc == nil || oc.Operation == nil {
		return next(ctx)
	}

	// Only cache query operations.
	if oc.Operation.Operation != astOperationQuery {
		return next(ctx)
	}

	// Build deterministic cache key from raw query + stable-encoded variables.
	key := cacheKey(oc)

	// Check cache.
	c.mu.RLock()
	entry, ok := c.store[key]
	c.mu.RUnlock()
	if ok && time.Now().Before(entry.expiresAt) {
		// Cache hit — update LRU timestamp lock-free and return cached data.
		now := time.Now().UnixNano()
		atomic.StoreInt64(&entry.lastAccessedNanos, now)
		c.HitsTotal.Add(1)
		return func(ctx context.Context) *graphql.Response {
			return &graphql.Response{Data: entry.data}
		}
	}

	// Cache miss — execute normally and cache the result.
	c.MissesTotal.Add(1)
	return func(ctx context.Context) *graphql.Response {
		handler := next(ctx)
		resp := handler(ctx)
		if resp != nil && len(resp.Errors) == 0 && resp.Data != nil {
			ttl := operationTTL(oc)
			now := time.Now()
			entry := &cacheEntry{
				data:      resp.Data,
				expiresAt: now.Add(ttl),
			}
			// Atomic store to avoid data race with cache-hit path's StoreInt64.
			atomic.StoreInt64(&entry.lastAccessedNanos, now.UnixNano())

			c.mu.Lock()
			if len(c.store) >= maxCacheEntries {
				c.evictOneLocked()
				c.Evictions.Add(1)
			}
			c.store[key] = entry
			c.mu.Unlock()
			c.SetsTotal.Add(1)
		}
		return resp
	}
}

// evict removes all expired entries.
func (c *ResponseCacheExtension) evict() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, v := range c.store {
		if now.After(v.expiresAt) {
			delete(c.store, k)
		}
	}
}

// evictOneLocked removes the least-recently-accessed entry. Caller must
// hold c.mu (write lock). lastAccessedNanos is read atomically (no further
// lock needed since the write lock already excludes concurrent writers).
// When the store is empty, this is a no-op.
func (c *ResponseCacheExtension) evictOneLocked() {
	var oldestKey string
	var oldestNanos int64
	for k, v := range c.store {
		// Atomic load to avoid data race with cache-hit path's StoreInt64.
		nanos := atomic.LoadInt64(&v.lastAccessedNanos)
		if oldestKey == "" || nanos < oldestNanos {
			oldestKey = k
			oldestNanos = nanos
		}
	}
	if oldestKey != "" {
		delete(c.store, oldestKey)
	}
}

// Stats returns Prometheus-compatible cache metrics.
func (c *ResponseCacheExtension) Stats() map[string]int64 {
	c.mu.RLock()
	size := int64(len(c.store))
	c.mu.RUnlock()
	return map[string]int64{
		"graphql_cache_hits":      c.HitsTotal.Load(),
		"graphql_cache_misses":    c.MissesTotal.Load(),
		"graphql_cache_sets":      c.SetsTotal.Load(),
		"graphql_cache_evictions": c.Evictions.Load(),
		"graphql_cache_size":      size,
	}
}

// ── Helpers ────────────────────────────────────────────────────────────

// astOperationQuery is the string constant gqlparser uses for query operations.
// gqlparser defines ast.Query = "query" (lowercase).
const astOperationQuery = "query"

// getOperationContextSafe wraps graphql.GetOperationContext with a panic
// recovery. gqlgen's GetOperationContext panics when called outside the
// middleware chain (e.g., in tests with bare context.Background()). This
// wrapper returns nil in that case, allowing graceful fallthrough.
func getOperationContextSafe(ctx context.Context) *graphql.OperationContext {
	defer func() { recover() }()
	return graphql.GetOperationContext(ctx)
}

// cacheKey builds a deterministic SHA-256 key from the raw query text and
// stably-serialized variables. Two queries with the same logical operation
// but different whitespace produce different keys — the PersistedQueryCache
// (GQL-1) handles normalization at the APQ layer.
//
// We strip leading/trailing whitespace to reduce cache fragmentation from
// clients that send the same query with different indentation.
func cacheKey(oc *graphql.OperationContext) string {
	h := sha256.New()
	// Normalize whitespace: strip leading/trailing, collapse internal runs.
	q := strings.Join(strings.Fields(oc.RawQuery), " ")
	h.Write([]byte(q))
	if len(oc.Variables) > 0 {
		// json.Marshal produces stable, sorted output for maps.
		vars, _ := json.Marshal(oc.Variables)
		h.Write(vars)
	}
	return hex.EncodeToString(h.Sum(nil))
}
