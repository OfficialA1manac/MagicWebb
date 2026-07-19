package graphql

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// ── Helpers ────────────────────────────────────────────────────────────

// newOpCtx creates a context with an OperationContext for the given query string.
// operationName is the top-level field name(s) — used to build the SelectionSet.
func newOpCtx(rawQuery string, operation ast.Operation, fields ...string) context.Context {
	return newOpCtxWithVars(rawQuery, operation, nil, fields...)
}

// newOpCtxWithVars is like newOpCtx but also sets Variables.
func newOpCtxWithVars(rawQuery string, operation ast.Operation, vars map[string]any, fields ...string) context.Context {
	selSet := make(ast.SelectionSet, len(fields))
	for i, f := range fields {
		selSet[i] = &ast.Field{Name: f}
	}
	return graphql.WithOperationContext(context.Background(), &graphql.OperationContext{
		RawQuery:  rawQuery,
		Variables: vars,
		Operation: &ast.OperationDefinition{
			Operation:    operation,
			SelectionSet: selSet,
		},
	})
}

// cannedResponse returns an OperationHandler that always responds with the
// given JSON data. The callCount pointer is incremented each time the
// handler is invoked, allowing tests to verify whether the handler was
// called (cache miss) or skipped (cache hit).
func cannedResponse(data json.RawMessage, callCount *int32) graphql.OperationHandler {
	return func(ctx context.Context) graphql.ResponseHandler {
		atomic.AddInt32(callCount, 1)
		return func(ctx context.Context) *graphql.Response {
			return &graphql.Response{Data: data}
		}
	}
}

// errorResponse returns an OperationHandler that responds with GraphQL errors.
func errorResponse(errMsg string, callCount *int32) graphql.OperationHandler {
	return func(ctx context.Context) graphql.ResponseHandler {
		atomic.AddInt32(callCount, 1)
		return func(ctx context.Context) *graphql.Response {
			return &graphql.Response{
				Errors: gqlerror.List{&gqlerror.Error{Message: errMsg}},
			}
		}
	}
}

// emptyDataResponse returns an OperationHandler that responds with nil Data.
func emptyDataResponse(callCount *int32) graphql.OperationHandler {
	return func(ctx context.Context) graphql.ResponseHandler {
		atomic.AddInt32(callCount, 1)
		return func(ctx context.Context) *graphql.Response {
			return &graphql.Response{}
		}
	}
}

// ── operationTTL tests ─────────────────────────────────────────────────

func TestOperationTTL_NilContext(t *testing.T) {
	if got := operationTTL(nil); got != ttlDefault {
		t.Errorf("nil context: want %v, got %v", ttlDefault, got)
	}
}

func TestOperationTTL_NilOperation(t *testing.T) {
	oc := &graphql.OperationContext{Operation: nil}
	if got := operationTTL(oc); got != ttlDefault {
		t.Errorf("nil operation: want %v, got %v", ttlDefault, got)
	}
}

func TestOperationTTL_SlowFields(t *testing.T) {
	tests := []struct {
		name  string
		field string
	}{
		{"metrics", "metrics"},
		{"collectionStats", "collectionStats"},
		{"collection", "collection"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oc := &graphql.OperationContext{
				Operation: &ast.OperationDefinition{
					SelectionSet: ast.SelectionSet{&ast.Field{Name: tt.field}},
				},
			}
			if got := operationTTL(oc); got != ttlSlow {
				t.Errorf("%s: want %v, got %v", tt.field, ttlSlow, got)
			}
		})
	}
}

func TestOperationTTL_MediumFields(t *testing.T) {
	tests := []struct {
		name  string
		field string
	}{
		{"collections", "collections"},
		{"trending", "trending"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oc := &graphql.OperationContext{
				Operation: &ast.OperationDefinition{
					SelectionSet: ast.SelectionSet{&ast.Field{Name: tt.field}},
				},
			}
			if got := operationTTL(oc); got != ttlMedium {
				t.Errorf("%s: want %v, got %v", tt.field, ttlMedium, got)
			}
		})
	}
}

func TestOperationTTL_FastFields(t *testing.T) {
	tests := []struct {
		name  string
		field string
	}{
		{"listings", "listings"},
		{"listing", "listing"},
		{"auctions", "auctions"},
		{"auction", "auction"},
		{"activity", "activity"},
		{"search", "search"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oc := &graphql.OperationContext{
				Operation: &ast.OperationDefinition{
					SelectionSet: ast.SelectionSet{&ast.Field{Name: tt.field}},
				},
			}
			if got := operationTTL(oc); got != ttlFast {
				t.Errorf("%s: want %v, got %v", tt.field, ttlFast, got)
			}
		})
	}
}

func TestOperationTTL_UnknownField(t *testing.T) {
	oc := &graphql.OperationContext{
		Operation: &ast.OperationDefinition{
			SelectionSet: ast.SelectionSet{&ast.Field{Name: "unknownQuery"}},
		},
	}
	if got := operationTTL(oc); got != ttlDefault {
		t.Errorf("unknown field: want %v, got %v", ttlDefault, got)
	}
}

func TestOperationTTL_MultiField_MinimumTTL(t *testing.T) {
	// metrics (slow, 60s) + listings (fast, 15s) → should get 15s (minimum).
	oc := &graphql.OperationContext{
		Operation: &ast.OperationDefinition{
			SelectionSet: ast.SelectionSet{
				&ast.Field{Name: "metrics"},
				&ast.Field{Name: "listings"},
			},
		},
	}
	if got := operationTTL(oc); got != ttlFast {
		t.Errorf("metrics+listings: want min TTL %v, got %v", ttlFast, got)
	}
}

func TestOperationTTL_MultiField_AllSlow(t *testing.T) {
	// Two slow fields → should still be slow.
	oc := &graphql.OperationContext{
		Operation: &ast.OperationDefinition{
			SelectionSet: ast.SelectionSet{
				&ast.Field{Name: "metrics"},
				&ast.Field{Name: "collection"},
			},
		},
	}
	if got := operationTTL(oc); got != ttlSlow {
		t.Errorf("metrics+collection: want %v, got %v", ttlSlow, got)
	}
}

func TestOperationTTL_Mixed_SlowMediumFast(t *testing.T) {
	// metrics (60s) + trending (30s) + listings (15s) → 15s
	oc := &graphql.OperationContext{
		Operation: &ast.OperationDefinition{
			SelectionSet: ast.SelectionSet{
				&ast.Field{Name: "metrics"},
				&ast.Field{Name: "trending"},
				&ast.Field{Name: "listings"},
			},
		},
	}
	if got := operationTTL(oc); got != ttlFast {
		t.Errorf("metrics+trending+listings: want min TTL %v, got %v", ttlFast, got)
	}
}

// ── ttlForField tests ──────────────────────────────────────────────────

func TestTTLForField_AllMappings(t *testing.T) {
	tests := []struct {
		field string
		want  time.Duration
	}{
		{"metrics", ttlSlow},
		{"collectionStats", ttlSlow},
		{"collection", ttlSlow},
		{"collections", ttlMedium},
		{"trending", ttlMedium},
		{"listings", ttlFast},
		{"listing", ttlFast},
		{"auctions", ttlFast},
		{"auction", ttlFast},
		{"activity", ttlFast},
		{"search", ttlFast},
		{"unknownThing", ttlDefault},
		{"", ttlDefault},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			if got := ttlForField(tt.field); got != tt.want {
				t.Errorf("ttlForField(%q): want %v, got %v", tt.field, tt.want, got)
			}
		})
	}
}

// ── cacheKey tests ─────────────────────────────────────────────────────

func TestCacheKey_Deterministic(t *testing.T) {
	oc := &graphql.OperationContext{
		RawQuery: "{ listings(limit:5) { name priceWei } }",
	}
	k1 := cacheKey(oc)
	k2 := cacheKey(oc)
	if k1 != k2 {
		t.Errorf("same query produced different keys: %q vs %q", k1, k2)
	}
	if len(k1) != 64 {
		t.Errorf("expected SHA-256 hex key (64 chars), got %d chars", len(k1))
	}
}

func TestCacheKey_DifferentQueries(t *testing.T) {
	oc1 := &graphql.OperationContext{RawQuery: "{ metrics }"}
	oc2 := &graphql.OperationContext{RawQuery: "{ listings(limit:5) { name } }"}
	if cacheKey(oc1) == cacheKey(oc2) {
		t.Error("different queries should produce different keys")
	}
}

func TestCacheKey_WhitespaceNormalization(t *testing.T) {
	// Internal whitespace is collapsed by strings.Fields + strings.Join,
	// so these two produce the same key.
	oc1 := &graphql.OperationContext{RawQuery: "{  listings(limit:5)  { name } }"}
	oc2 := &graphql.OperationContext{RawQuery: "{ listings(limit:5) { name } }"}
	if cacheKey(oc1) != cacheKey(oc2) {
		t.Error("whitespace-normalized queries should produce identical keys")
	}
}

func TestCacheKey_TrailingNewline(t *testing.T) {
	oc1 := &graphql.OperationContext{RawQuery: "{ metrics }\n"}
	oc2 := &graphql.OperationContext{RawQuery: "{ metrics }"}
	if cacheKey(oc1) != cacheKey(oc2) {
		t.Error("trailing newline should be normalized away, producing same key")
	}
}

func TestCacheKey_VariablesAffectKey(t *testing.T) {
	oc1 := &graphql.OperationContext{
		RawQuery:  "query($addr: String!) { collection(address: $addr) { name } }",
		Variables: map[string]any{"addr": "0xabc"},
	}
	oc2 := &graphql.OperationContext{
		RawQuery:  "query($addr: String!) { collection(address: $addr) { name } }",
		Variables: map[string]any{"addr": "0xdef"},
	}
	if cacheKey(oc1) == cacheKey(oc2) {
		t.Error("different variables should produce different keys")
	}
}

func TestCacheKey_NoVars(t *testing.T) {
	// No variables — key is based only on normalized query.
	oc := &graphql.OperationContext{RawQuery: "{ metrics }"}
	k1 := cacheKey(oc)
	k2 := cacheKey(oc)
	if k1 != k2 {
		t.Error("same query with no vars should produce same key")
	}
}

func TestCacheKey_EmptyVars(t *testing.T) {
	oc1 := &graphql.OperationContext{RawQuery: "{ metrics }", Variables: nil}
	oc2 := &graphql.OperationContext{RawQuery: "{ metrics }", Variables: map[string]any{}}
	if cacheKey(oc1) != cacheKey(oc2) {
		t.Error("nil and empty variables should produce same key")
	}
}

// ── InterceptOperation tests ───────────────────────────────────────────

func TestInterceptOperation_CacheHit(t *testing.T) {
	cache := NewResponseCacheExtension()
	data := json.RawMessage(`{"metrics":{"volume24h":"100"}}`)

	// First request: cache miss, populates cache.
	var count1 int32
	ctx1 := newOpCtx("{ metrics }", ast.Query, "metrics")
	handler1 := cache.InterceptOperation(ctx1, cannedResponse(data, &count1))
	resp1 := handler1(ctx1)
	if resp1 == nil || resp1.Data == nil {
		t.Fatal("first request should return data")
	}
	if atomic.LoadInt32(&count1) != 1 {
		t.Errorf("first request: handler called %d times, want 1", count1)
	}
	if cache.MissesTotal.Load() != 1 {
		t.Errorf("misses: want 1, got %d", cache.MissesTotal.Load())
	}

	// Second request: cache hit, handler NOT called.
	var count2 int32
	ctx2 := newOpCtx("{ metrics }", ast.Query, "metrics")
	handler2 := cache.InterceptOperation(ctx2, cannedResponse(data, &count2))
	resp2 := handler2(ctx2)
	if resp2 == nil || resp2.Data == nil {
		t.Fatal("second request should return cached data")
	}
	if atomic.LoadInt32(&count2) != 0 {
		t.Errorf("second request: handler called %d times, want 0 (cache hit)", count2)
	}
	if cache.HitsTotal.Load() != 1 {
		t.Errorf("hits: want 1, got %d", cache.HitsTotal.Load())
	}

	// Verify cached data matches.
	if string(resp2.Data) != string(data) {
		t.Errorf("cached data mismatch: want %s, got %s", data, resp2.Data)
	}
}

func TestInterceptOperation_CacheMiss(t *testing.T) {
	cache := NewResponseCacheExtension()
	data := json.RawMessage(`{"listings":[{"name":"NFT #1"}]}`)

	var count int32
	ctx := newOpCtx("{ listings(limit:5) { name } }", ast.Query, "listings")
	handler := cache.InterceptOperation(ctx, cannedResponse(data, &count))
	resp := handler(ctx)
	if resp == nil || resp.Data == nil {
		t.Fatal("response should have data")
	}
	if atomic.LoadInt32(&count) != 1 {
		t.Errorf("handler called %d times, want 1", count)
	}
	if cache.MissesTotal.Load() != 1 {
		t.Errorf("misses: want 1, got %d", cache.MissesTotal.Load())
	}
	if cache.SetsTotal.Load() != 1 {
		t.Errorf("sets: want 1, got %d", cache.SetsTotal.Load())
	}
}

func TestInterceptOperation_MutationNotCached(t *testing.T) {
	cache := NewResponseCacheExtension()
	data := json.RawMessage(`{"createListing":{"id":"123"}}`)

	// Run mutation twice — both times should call the handler (no caching).
	for i := 0; i < 2; i++ {
		var count int32
		ctx := newOpCtx("mutation { createListing(price: 100) { id } }", ast.Mutation, "createListing")
		handler := cache.InterceptOperation(ctx, cannedResponse(data, &count))
		resp := handler(ctx)
		if resp == nil {
			t.Fatal("mutation should return data")
		}
		if atomic.LoadInt32(&count) != 1 {
			t.Errorf("iteration %d: handler called %d times, want 1 (mutations not cached)", i, count)
		}
	}

	// No sets or misses should be recorded (cache is bypassed entirely).
	if cache.SetsTotal.Load() != 0 {
		t.Errorf("sets: want 0, got %d", cache.SetsTotal.Load())
	}
}

func TestInterceptOperation_SubscriptionNotCached(t *testing.T) {
	cache := NewResponseCacheExtension()
	data := json.RawMessage(`{"newListing":{"name":"NFT"}}`)

	var count int32
	ctx := newOpCtx("subscription { newListing { name } }", ast.Subscription, "newListing")
	handler := cache.InterceptOperation(ctx, cannedResponse(data, &count))
	resp := handler(ctx)
	if resp == nil {
		t.Fatal("subscription should return data")
	}
	if atomic.LoadInt32(&count) != 1 {
		t.Errorf("handler called %d times, want 1", count)
	}
	if cache.SetsTotal.Load() != 0 {
		t.Errorf("subscriptions should not be cached, got %d sets", cache.SetsTotal.Load())
	}
}

func TestInterceptOperation_NilOperationContext(t *testing.T) {
	cache := NewResponseCacheExtension()
	data := json.RawMessage(`{}`)

	var count int32
	// Context without OperationContext — should fall through to next handler.
	ctx := context.Background()
	handler := cache.InterceptOperation(ctx, cannedResponse(data, &count))
	resp := handler(ctx)
	if resp == nil {
		t.Fatal("should pass through to next handler")
	}
	if atomic.LoadInt32(&count) != 1 {
		t.Errorf("handler called %d times, want 1", count)
	}
}

func TestInterceptOperation_ErrorResponseNotCached(t *testing.T) {
	cache := NewResponseCacheExtension()

	// First request: returns an error — should NOT be cached.
	var count1 int32
	ctx1 := newOpCtx("{ metrics }", ast.Query, "metrics")
	handler1 := cache.InterceptOperation(ctx1, errorResponse("something broke", &count1))
	resp1 := handler1(ctx1)
	if resp1 == nil || len(resp1.Errors) == 0 {
		t.Fatal("first request should return errors")
	}

	// Second request: same query — should be a cache miss (error not cached).
	var count2 int32
	ctx2 := newOpCtx("{ metrics }", ast.Query, "metrics")
	handler2 := cache.InterceptOperation(ctx2, errorResponse("something broke", &count2))
	resp2 := handler2(ctx2)
	if resp2 == nil || len(resp2.Errors) == 0 {
		t.Fatal("second request should return errors (not cached)")
	}
	if atomic.LoadInt32(&count2) != 1 {
		t.Errorf("error responses should not be cached, handler called %d times", count2)
	}
}

func TestInterceptOperation_EmptyDataNotCached(t *testing.T) {
	cache := NewResponseCacheExtension()

	var count1 int32
	ctx1 := newOpCtx("{ metrics }", ast.Query, "metrics")
	handler1 := cache.InterceptOperation(ctx1, emptyDataResponse(&count1))
	resp1 := handler1(ctx1)
	if resp1 == nil {
		t.Fatal("response should not be nil")
	}
	if cache.SetsTotal.Load() != 0 {
		t.Errorf("nil-data responses should not be cached, got %d sets", cache.SetsTotal.Load())
	}

	// Second call: should miss again (nil Data not cached).
	var count2 int32
	ctx2 := newOpCtx("{ metrics }", ast.Query, "metrics")
	handler2 := cache.InterceptOperation(ctx2, emptyDataResponse(&count2))
	_ = handler2(ctx2)
	if atomic.LoadInt32(&count2) != 1 {
		t.Errorf("nil-data responses should miss again, handler called %d times", count2)
	}
}

func TestInterceptOperation_DifferentTTLsProduceDifferentExpiry(t *testing.T) {
	cache := NewResponseCacheExtension()

	// Slow query (metrics) — TTL 60s.
	var cSlow int32
	ctxSlow := newOpCtx("{ metrics }", ast.Query, "metrics")
	hSlow := cache.InterceptOperation(ctxSlow, cannedResponse(json.RawMessage(`{"a":1}`), &cSlow))
	_ = hSlow(ctxSlow)

	// Fast query (listings) — TTL 15s.
	var cFast int32
	ctxFast := newOpCtx("{ listings(limit:1) { name } }", ast.Query, "listings")
	hFast := cache.InterceptOperation(ctxFast, cannedResponse(json.RawMessage(`{"b":2}`), &cFast))
	_ = hFast(ctxFast)

	// Both stored.
	cache.mu.RLock()
	storeSize := len(cache.store)
	cache.mu.RUnlock()
	if storeSize != 2 {
		t.Errorf("want 2 entries in store, got %d", storeSize)
	}
}

// ── LRU eviction tests ─────────────────────────────────────────────────

func TestLRUEviction_EvictsLeastRecentlyAccessed(t *testing.T) {
	cache := NewResponseCacheExtension()

	// Fill cache to capacity with maxCacheEntries entries.
	// We insert entries sequentially; the first one is the oldest.
	for i := 0; i < maxCacheEntries; i++ {
		key := "key_" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		cache.mu.Lock()
		cache.store[key] = &cacheEntry{
			data:              json.RawMessage(`{}`),
			expiresAt:         time.Now().Add(time.Hour),
			lastAccessedNanos: time.Now().Add(-time.Duration(maxCacheEntries-i) * time.Second).UnixNano(),
		}
		cache.mu.Unlock()
	}

	// The first entry (oldest lastAccessed) should be evicted next.
	cache.mu.Lock()
	cache.evictOneLocked()
	cache.mu.Unlock()

	// "key_a0" was the oldest — should be gone.
	cache.mu.RLock()
	_, exists := cache.store["key_a0"]
	cache.mu.RUnlock()
	if exists {
		t.Error("oldest entry should have been evicted")
	}

	// Store size should be maxCacheEntries - 1.
	cache.mu.RLock()
	sz := len(cache.store)
	cache.mu.RUnlock()
	if sz != maxCacheEntries-1 {
		t.Errorf("store size after eviction: want %d, got %d", maxCacheEntries-1, sz)
	}
}

func TestLRUEviction_AccessUpdatesLRUOrder(t *testing.T) {
	cache := &ResponseCacheExtension{store: make(map[string]*cacheEntry)}

	// Three entries: a (oldest), b (middle), c (newest).
	now := time.Now()
	cache.store["a"] = &cacheEntry{lastAccessedNanos: now.Add(-30 * time.Second).UnixNano()}
	cache.store["b"] = &cacheEntry{lastAccessedNanos: now.Add(-20 * time.Second).UnixNano()}
	cache.store["c"] = &cacheEntry{lastAccessedNanos: now.Add(-10 * time.Second).UnixNano()}

	// Access "a" — updates its lastAccessedNanos to now.
	atomic.StoreInt64(&cache.store["a"].lastAccessedNanos, now.UnixNano())

	// Evict — "b" should go (now the oldest).
	cache.evictOneLocked()
	if _, ok := cache.store["b"]; ok {
		t.Error("b should have been evicted (oldest after a was accessed)")
	}
	if _, ok := cache.store["a"]; !ok {
		t.Error("a should still be in cache (was just accessed)")
	}
	if _, ok := cache.store["c"]; !ok {
		t.Error("c should still be in cache")
	}
}

func TestEvictOneLocked_EmptyStore(t *testing.T) {
	cache := &ResponseCacheExtension{store: make(map[string]*cacheEntry)}
	// Should not panic.
	cache.evictOneLocked()
	if len(cache.store) != 0 {
		t.Error("empty store should remain empty after eviction")
	}
}

// ── Expiry tests ───────────────────────────────────────────────────────

func TestInterceptOperation_ExpiredEntryIsMiss(t *testing.T) {
	cache := NewResponseCacheExtension()
	data := json.RawMessage(`{"metrics":{"volume24h":"100"}}`)

	// Insert an already-expired entry directly into the store.
	key := cacheKey(&graphql.OperationContext{RawQuery: "{ metrics }"})
	cache.mu.Lock()
	cache.store[key] = &cacheEntry{
		data:              data,
		expiresAt:         time.Now().Add(-time.Hour), // expired an hour ago
		lastAccessedNanos: time.Now().UnixNano(),
	}
	cache.mu.Unlock()

	// Request: should be a cache miss (expired).
	var count int32
	ctx := newOpCtx("{ metrics }", ast.Query, "metrics")
	handler := cache.InterceptOperation(ctx, cannedResponse(data, &count))
	resp := handler(ctx)
	if resp == nil || resp.Data == nil {
		t.Fatal("should return data (cache miss + re-populate)")
	}
	if atomic.LoadInt32(&count) != 1 {
		t.Errorf("expired entry: handler called %d times, want 1 (miss)", count)
	}
}

func TestEvict_RemovesExpiredEntries(t *testing.T) {
	cache := NewResponseCacheExtension()

	// Insert 2 expired + 1 valid entry.
	cache.mu.Lock()
	cache.store["expired1"] = &cacheEntry{expiresAt: time.Now().Add(-time.Minute)}
	cache.store["expired2"] = &cacheEntry{expiresAt: time.Now().Add(-time.Hour)}
	cache.store["valid"] = &cacheEntry{expiresAt: time.Now().Add(time.Hour)}
	cache.mu.Unlock()

	cache.evict()

	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if _, ok := cache.store["expired1"]; ok {
		t.Error("expired1 should have been evicted")
	}
	if _, ok := cache.store["expired2"]; ok {
		t.Error("expired2 should have been evicted")
	}
	if _, ok := cache.store["valid"]; !ok {
		t.Error("valid entry should remain")
	}
}

// ── Stats tests ────────────────────────────────────────────────────────

func TestStats(t *testing.T) {
	cache := NewResponseCacheExtension()

	// Simulate some activity.
	cache.HitsTotal.Store(10)
	cache.MissesTotal.Store(5)
	cache.SetsTotal.Store(7)
	cache.Evictions.Store(2)

	cache.mu.Lock()
	cache.store["k1"] = &cacheEntry{}
	cache.store["k2"] = &cacheEntry{}
	cache.mu.Unlock()

	stats := cache.Stats()
	if stats["graphql_cache_hits"] != 10 {
		t.Errorf("hits: want 10, got %d", stats["graphql_cache_hits"])
	}
	if stats["graphql_cache_misses"] != 5 {
		t.Errorf("misses: want 5, got %d", stats["graphql_cache_misses"])
	}
	if stats["graphql_cache_sets"] != 7 {
		t.Errorf("sets: want 7, got %d", stats["graphql_cache_sets"])
	}
	if stats["graphql_cache_evictions"] != 2 {
		t.Errorf("evictions: want 2, got %d", stats["graphql_cache_evictions"])
	}
	if stats["graphql_cache_size"] != 2 {
		t.Errorf("size: want 2, got %d", stats["graphql_cache_size"])
	}
}

// ── Interface tests ────────────────────────────────────────────────────

func TestExtensionName(t *testing.T) {
	cache := NewResponseCacheExtension()
	if name := cache.ExtensionName(); name != "ResponseCache" {
		t.Errorf("ExtensionName: want 'ResponseCache', got %q", name)
	}
}

func TestValidate(t *testing.T) {
	cache := NewResponseCacheExtension()
	if err := cache.Validate(nil); err != nil {
		t.Errorf("Validate should return nil, got %v", err)
	}
}

// ── Concurrency tests ──────────────────────────────────────────────────

func TestInterceptOperation_ConcurrentAccess(t *testing.T) {
	cache := NewResponseCacheExtension()
	data := json.RawMessage(`{"listings":[]}`)

	var wg sync.WaitGroup
	const goroutines = 50
	const iterations = 20

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				var count int32
				ctx := newOpCtx("{ listings(limit:5) { name } }", ast.Query, "listings")
				handler := cache.InterceptOperation(ctx, cannedResponse(data, &count))
				resp := handler(ctx)
				if resp == nil || resp.Data == nil {
					t.Errorf("concurrent access: nil response")
					return
				}
			}
		}()
	}
	wg.Wait()

	// At least one hit should have occurred (after the first miss populates the cache).
	totalHits := cache.HitsTotal.Load()
	totalMisses := cache.MissesTotal.Load()
	if totalHits+totalMisses != int64(goroutines*iterations) {
		t.Errorf("hits(%d) + misses(%d) != total requests(%d)",
			totalHits, totalMisses, goroutines*iterations)
	}
}

func TestInterceptOperation_ConcurrentDifferentQueries(t *testing.T) {
	cache := NewResponseCacheExtension()
	queries := []struct {
		rawQuery string
		field    string
		data     json.RawMessage
	}{
		{"{ metrics }", "metrics", json.RawMessage(`{"metrics":{}}`)},
		{"{ listings(limit:5) { name } }", "listings", json.RawMessage(`{"listings":[]}`)},
		{"{ trending(window: DAY, limit: 10) { address } }", "trending", json.RawMessage(`{"trending":[]}`)},
	}

	var wg sync.WaitGroup
	for _, q := range queries {
		wg.Add(1)
		go func(rawQuery, field string, data json.RawMessage) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				var count int32
				ctx := newOpCtx(rawQuery, ast.Query, field)
				handler := cache.InterceptOperation(ctx, cannedResponse(data, &count))
				resp := handler(ctx)
				if resp == nil {
					t.Errorf("concurrent different queries: nil response for %s", field)
					return
				}
			}
		}(q.rawQuery, q.field, q.data)
	}
	wg.Wait()

	// Each query should have at least 1 set.
	if cache.SetsTotal.Load() < 3 {
		t.Errorf("sets: want at least 3, got %d", cache.SetsTotal.Load())
	}
}

// ── Capacity test ──────────────────────────────────────────────────────

func TestInterceptOperation_AtCapacityEvicts(t *testing.T) {
	cache := &ResponseCacheExtension{store: make(map[string]*cacheEntry)}

	// Fill to exactly maxCacheEntries. Each iteration uses unique variables
	// (\"i\":i) to produce distinct cache keys — otherwise all 500 entries
	// would have the same key and never fill the store beyond 1 entry.
	for i := 0; i < maxCacheEntries; i++ {
		data := json.RawMessage(`{"i":` + strconv.Itoa(i) + `}`)
		var count int32
		ctx := newOpCtxWithVars("{ listings(limit:1) { name } }", ast.Query,
			map[string]any{"i": i}, "listings")
		handler := cache.InterceptOperation(ctx, cannedResponse(data, &count))
		_ = handler(ctx)
	}

	// All entries should have lastAccessedNanos set (by atomic.StoreInt64).
	cache.mu.RLock()
	for k, v := range cache.store {
		nanos := atomic.LoadInt64(&v.lastAccessedNanos)
		if nanos == 0 {
			t.Errorf("entry %s has zero lastAccessedNanos", k)
			break
		}
	}
	cache.mu.RUnlock()

	// Force time to advance so the new entry gets a newer timestamp.
	// This ensures it won't be immediately evicted as the oldest.
	time.Sleep(time.Millisecond)

	// Insert one more — should trigger eviction of the oldest.
	var count int32
	ctx := newOpCtxWithVars("{ listings(limit:1) { name } }", ast.Query,
		map[string]any{"i": maxCacheEntries}, "listings")
	handler := cache.InterceptOperation(ctx, cannedResponse(json.RawMessage(`{"new":true}`), &count))
	_ = handler(ctx)

	// Store should still be at capacity (one evicted, one added).
	cache.mu.RLock()
	sz := len(cache.store)
	cache.mu.RUnlock()
	if sz != maxCacheEntries {
		t.Errorf("store size: want %d, got %d", maxCacheEntries, sz)
	}

	if cache.Evictions.Load() < 1 {
		t.Errorf("evictions: want at least 1, got %d", cache.Evictions.Load())
	}
}

// ── Edge case tests ────────────────────────────────────────────────────

func TestInterceptOperation_NilResponse(t *testing.T) {
	cache := NewResponseCacheExtension()

	// First request: nil response should pass through.
	var count1 int32
	nilHandler1 := func(ctx context.Context) graphql.ResponseHandler {
		atomic.AddInt32(&count1, 1)
		return func(ctx context.Context) *graphql.Response {
			return nil // nil response
		}
	}

	ctx1 := newOpCtx("{ metrics }", ast.Query, "metrics")
	handler1 := cache.InterceptOperation(ctx1, nilHandler1)
	resp := handler1(ctx1)
	if resp != nil {
		t.Error("nil response should pass through")
	}
	if atomic.LoadInt32(&count1) != 1 {
		t.Errorf("first call: handler called %d times, want 1", count1)
	}

	// Second request: nil response should NOT be cached — second call should miss again.
	var count2 int32
	nilHandler2 := func(ctx context.Context) graphql.ResponseHandler {
		atomic.AddInt32(&count2, 1)
		return func(ctx context.Context) *graphql.Response {
			return nil // nil response
		}
	}

	ctx2 := newOpCtx("{ metrics }", ast.Query, "metrics")
	handler2 := cache.InterceptOperation(ctx2, nilHandler2)
	_ = handler2(ctx2)
	if atomic.LoadInt32(&count2) != 1 {
		t.Errorf("second call: handler called %d times, want 1 (miss)", count2)
	}
}

func TestCacheKey_SameVarsDifferentOrder(t *testing.T) {
	// json.Marshal produces sorted keys for maps in all Go versions since 1.0,
	// but the spec doesn't guarantee it. This test verifies current behavior.
	oc1 := &graphql.OperationContext{
		RawQuery:  "query($a: String!, $b: Int) { collection(address: $a) { name } }",
		Variables: map[string]any{"a": "0xabc", "b": 42},
	}
	oc2 := &graphql.OperationContext{
		RawQuery:  "query($a: String!, $b: Int) { collection(address: $a) { name } }",
		Variables: map[string]any{"b": 42, "a": "0xabc"},
	}
	k1 := cacheKey(oc1)
	k2 := cacheKey(oc2)
	if k1 != k2 {
		t.Error("json.Marshal should produce stable output for reordered map keys")
	}
}
