package graphql

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/99designs/gqlgen/graphql"
)

// ── GQL-2: Response cache for read-heavy GraphQL queries ────────────
//
// Caches complete GraphQL responses for deterministic read-heavy queries
// (collection, metrics, trending) using the query hash + serialized
// variables as the cache key. In-process TTL cache avoids repeated DB
// round-trips for queries that produce the same result across requests.
//
// Cache duration: 30s (balances freshness with load reduction). Longer
// TTLs would serve stale data; shorter TTLs provide less benefit.
//
// Thread-safety: sync.RWMutex protects the map. Cache hits are read-locked;
// misses are promoted to write-lock only for the store operation.

const responseCacheTTL = 30 * time.Second
const maxCacheEntries = 200

type cacheEntry struct {
	data      []byte
	expiresAt time.Time
}

// ResponseCacheExtension implements graphql.OperationInterceptor to cache
// GraphQL responses. Only caches queries (not mutations or subscriptions).
type ResponseCacheExtension struct {
	mu    sync.RWMutex
	store map[string]*cacheEntry
}

// NewResponseCacheExtension creates a response cache with background eviction.
func NewResponseCacheExtension() *ResponseCacheExtension {
	ce := &ResponseCacheExtension{
		store: make(map[string]*cacheEntry),
	}
	// Background eviction every 60s.
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

// InterceptOperation checks the cache for an existing response and returns
// it on hit. On miss, the operation proceeds normally and the result is
// cached after execution (via InterceptResponse).
func (c *ResponseCacheExtension) InterceptOperation(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	oc := graphql.GetOperationContext(ctx)
	if oc == nil || oc.Operation == nil {
		return next(ctx)
	}

	// Only cache query operations (not mutations or subscriptions).
	if oc.Operation.Operation != "Query" {
		return next(ctx)
	}

	// Build cache key from the raw query and variables.
	key := cacheKey(oc)

	// Check cache.
	c.mu.RLock()
	entry, ok := c.store[key]
	c.mu.RUnlock()
	if ok && time.Now().Before(entry.expiresAt) {
		// Cache hit — return cached response directly.
		return func(ctx context.Context) *graphql.Response {
			return &graphql.Response{
				Data:   entry.data,
				Errors: nil,
			}
		}
	}

	// Cache miss — execute normally and cache the result.
	return func(ctx context.Context) *graphql.Response {
		handler := next(ctx)
		resp := handler(ctx)
		if resp != nil && len(resp.Errors) == 0 && resp.Data != nil {
			c.mu.Lock()
			if len(c.store) >= maxCacheEntries {
				// Evict oldest entry to prevent unbounded growth.
				for k := range c.store {
					delete(c.store, k)
					break
				}
			}
			c.store[key] = &cacheEntry{
				data:      resp.Data,
				expiresAt: time.Now().Add(responseCacheTTL),
			}
			c.mu.Unlock()
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

// cacheKey builds a deterministic key from query text and variables.
func cacheKey(oc *graphql.OperationContext) string {
	h := sha256.New()
	h.Write([]byte(oc.RawQuery))
	if len(oc.Variables) > 0 {
		// Marshal to stable JSON for deterministic hashing.
		vars, _ := json.Marshal(oc.Variables)
		h.Write(vars)
	}
	return hex.EncodeToString(h.Sum(nil))
}
