// Package cache — tests for the Redis-backed distributed cache (CACHE-1).
//
// These tests use package cache (not cache_test) so they have access to
// unexported symbols like checkHealth() for health-loop testing.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// ── Mock Redis Client ────────────────────────────────────────────────────────

// mockRedisClient implements RedisClient for unit testing. It stores data
// in-memory and supports configurable errors for each method so tests can
// simulate Redis failures, Ping failures, and connection recoveries.
type mockRedisClient struct {
	data     map[string]string
	pingErr  error
	getErr   error
	setErr   error
	closeErr error

	// Counters for verifying method calls.
	pingCalls atomic.Int64
	getCalls  atomic.Int64
	setCalls  atomic.Int64
}

func newMockRedisClient() *mockRedisClient {
	return &mockRedisClient{data: make(map[string]string)}
}

func (m *mockRedisClient) Get(_ context.Context, key string) (string, error) {
	m.getCalls.Add(1)
	if m.getErr != nil {
		return "", m.getErr
	}
	v, ok := m.data[key]
	if !ok {
		return "", errors.New("redis: nil")
	}
	return v, nil
}

func (m *mockRedisClient) Set(_ context.Context, key string, value any, _ time.Duration) error {
	m.setCalls.Add(1)
	if m.setErr != nil {
		return m.setErr
	}
	// JSON marshal returns []byte, but callers may pass strings directly.
	// Accept both so the mock is a faithful stand-in for redis.Client.
	switch v := value.(type) {
	case string:
		m.data[key] = v
	case []byte:
		m.data[key] = string(v)
	default:
		// Should not happen — RedisCache.Set always passes JSON-marshalled []byte.
		m.data[key] = ""
	}
	return nil
}

func (m *mockRedisClient) Ping(_ context.Context) error {
	m.pingCalls.Add(1)
	return m.pingErr
}

func (m *mockRedisClient) Close() error {
	return m.closeErr
}

// ── NewRedisOrMemory — table-driven factory tests ────────────────────────────

func TestNewRedisOrMemory_EmptyURL(t *testing.T) {
	c := NewRedisOrMemory("", 10*time.Second)
	if c == nil {
		t.Fatal("expected non-nil cache")
	}
	if _, ok := c.(*Cache); !ok {
		t.Fatalf("expected *Cache for empty URL, got %T", c)
	}
	if c.Count() != 0 {
		t.Fatalf("expected empty cache, got %d entries", c.Count())
	}
}

func TestNewRedisOrMemory_ConfiguredURL_FallbackToMemory(t *testing.T) {
	// The default build (without -tags redis) always returns *Cache
	// regardless of the URL value, because newRedisClient returns an error.
	// With -tags redis, this test would still pass on connection failure.
	c := NewRedisOrMemory("redis://localhost:6379", 10*time.Second)
	if c == nil {
		t.Fatal("expected non-nil cache")
	}
	if _, ok := c.(*Cache); !ok {
		t.Fatalf("expected *Cache when Redis connection fails or is not compiled in, got %T", c)
	}
}

// ── RedisCache Get / Set ─────────────────────────────────────────────────────

func TestRedisCache_Get_Hit(t *testing.T) {
	mock := newMockRedisClient()
	// Store a JSON value that Redis would return.
	val := map[string]any{"message": "hello"}
	raw, _ := json.Marshal(val)
	mock.data["test:key"] = string(raw)

	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()

	got, ok := rc.Get("test:key")
	if !ok {
		t.Fatal("expected cache hit")
	}
	m, ok := got.(map[string]any)
	if !ok || m["message"] != "hello" {
		t.Fatalf("unexpected value: %v", got)
	}
	if mock.getCalls.Load() != 1 {
		t.Fatalf("expected 1 Get call, got %d", mock.getCalls.Load())
	}
}

func TestRedisCache_Get_Miss(t *testing.T) {
	mock := newMockRedisClient()
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()

	got, ok := rc.Get("nonexistent")
	if ok || got != nil {
		t.Fatalf("expected cache miss, got %v, %v", got, ok)
	}
}

func TestRedisCache_Get_FallbackOnRedisError(t *testing.T) {
	mock := newMockRedisClient()
	mock.getErr = errors.New("connection refused")
	// Also populate the local fallback so we can verify it's consulted.
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()
	rc.local.Set("local:key", "fallback-value")

	got, ok := rc.Get("local:key")
	if !ok || got != "fallback-value" {
		t.Fatalf("expected local fallback hit, got %v, %v", got, ok)
	}
}

func TestRedisCache_Get_FallbackWhenDisabled(t *testing.T) {
	mock := newMockRedisClient()
	mock.data["redis:key"] = `"stale"`
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()

	// Manually disable Redis and seed local cache.
	rc.enabled.Store(false)
	rc.local.Set("redis:key", "local-value")

	got, ok := rc.Get("redis:key")
	if !ok || got != "local-value" {
		t.Fatalf("expected local fallback when disabled, got %v, %v", got, ok)
	}
}

func TestRedisCache_Get_InvalidJSON(t *testing.T) {
	mock := newMockRedisClient()
	mock.data["bad:json"] = "{not valid json}"
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()

	got, ok := rc.Get("bad:json")
	if ok || got != nil {
		t.Fatalf("expected miss on invalid JSON, got %v, %v", got, ok)
	}
}

func TestRedisCache_Set_Success(t *testing.T) {
	mock := newMockRedisClient()
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()

	rc.Set("key", "value")

	if _, exists := mock.data["key"]; !exists {
		t.Fatal("expected Set to store value in mock Redis")
	}
	if mock.setCalls.Load() != 1 {
		t.Fatalf("expected 1 Set call, got %d", mock.setCalls.Load())
	}
}

func TestRedisCache_Set_FallbackOnRedisError(t *testing.T) {
	mock := newMockRedisClient()
	mock.setErr = errors.New("connection refused")
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()

	rc.Set("fallback:key", "fallback-value")

	got, ok := rc.local.Get("fallback:key")
	if !ok || got != "fallback-value" {
		t.Fatalf("expected local fallback Set, got %v, %v", got, ok)
	}
}

func TestRedisCache_Set_FallbackWhenDisabled(t *testing.T) {
	mock := newMockRedisClient()
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()
	rc.enabled.Store(false)

	rc.Set("local:key", "local-value")

	got, ok := rc.local.Get("local:key")
	if !ok || got != "local-value" {
		t.Fatalf("expected local Set when disabled, got %v, %v", got, ok)
	}
	if mock.setCalls.Load() != 0 {
		t.Fatal("expected zero Redis Set calls when disabled")
	}
}

// ── Health Loop ──────────────────────────────────────────────────────────────

func TestRedisCache_CheckHealth_DisableOnPingFailure(t *testing.T) {
	mock := newMockRedisClient()
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()

	if !rc.enabled.Load() {
		t.Fatal("expected enabled after construction")
	}

	// Simulate Redis going down.
	mock.pingErr = errors.New("connection refused")
	rc.checkHealth()

	if rc.enabled.Load() {
		t.Fatal("expected disabled after failed health check")
	}
	if mock.pingCalls.Load() != 1 {
		t.Fatalf("expected 1 Ping call from checkHealth, got %d", mock.pingCalls.Load())
	}
}

func TestRedisCache_CheckHealth_ReenableOnRecovery(t *testing.T) {
	mock := newMockRedisClient()
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()

	// First, disable via failed ping.
	mock.pingErr = errors.New("connection refused")
	rc.checkHealth()
	if rc.enabled.Load() {
		t.Fatal("expected disabled after failure")
	}

	// Then, recover.
	mock.pingErr = nil
	rc.checkHealth()

	if !rc.enabled.Load() {
		t.Fatal("expected re-enabled after recovery")
	}
	if mock.pingCalls.Load() != 2 {
		t.Fatalf("expected 2 Ping calls, got %d", mock.pingCalls.Load())
	}
}

func TestRedisCache_CheckHealth_IdempotentOnRepeatedSuccess(t *testing.T) {
	mock := newMockRedisClient()
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()

	for i := 0; i < 5; i++ {
		rc.checkHealth()
		if !rc.enabled.Load() {
			t.Fatalf("expected still enabled after health check %d", i)
		}
	}
}

func TestRedisCache_CheckHealth_IdempotentOnRepeatedFailure(t *testing.T) {
	mock := newMockRedisClient()
	mock.pingErr = errors.New("connection refused")
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()

	for i := 0; i < 5; i++ {
		rc.checkHealth()
		if rc.enabled.Load() {
			t.Fatalf("expected still disabled after health check %d", i)
		}
	}
}

func TestRedisCache_HealthLoop_StopsOnClose(t *testing.T) {
	mock := newMockRedisClient()
	rc := NewRedisCache(mock, 10*time.Second)

	// The health loop is a background goroutine. Close sets closed=true.
	err := rc.Close()
	if err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if rc.enabled.Load() {
		t.Fatal("expected disabled after Close")
	}

	// Verify the closed flag was set (racy, but the healthLoop checks it).
	rc.mu.RLock()
	closed := rc.closed
	rc.mu.RUnlock()
	if !closed {
		t.Fatal("expected closed flag set")
	}
}

// ── Close ────────────────────────────────────────────────────────────────────

func TestRedisCache_Close(t *testing.T) {
	mock := newMockRedisClient()
	rc := NewRedisCache(mock, 10*time.Second)

	err := rc.Close()
	if err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if rc.enabled.Load() {
		t.Fatal("expected disabled after close")
	}

	// After close, Get should use local fallback.
	rc.local.Set("post:close", "still-works")
	got, ok := rc.Get("post:close")
	if !ok || got != "still-works" {
		t.Fatalf("expected local fallback after close, got %v, %v", got, ok)
	}
}

func TestRedisCache_Close_PropagatesClientError(t *testing.T) {
	mock := newMockRedisClient()
	mock.closeErr = errors.New("close failed")
	rc := NewRedisCache(mock, 10*time.Second)

	err := rc.Close()
	if err == nil {
		t.Fatal("expected Close to propagate client.Close error")
	}
}

// ── Clear ────────────────────────────────────────────────────────────────────

func TestRedisCache_Clear(t *testing.T) {
	mock := newMockRedisClient()
	mock.data["should:persist"] = `"important"`
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()

	// Seed local cache.
	rc.local.Set("local:key", "local-value")
	if rc.local.Count() != 1 {
		t.Fatal("expected 1 local entry before Clear")
	}

	// Clear only affects local cache — Redis keys are NOT flushed.
	rc.Clear()

	if rc.local.Count() != 0 {
		t.Fatal("expected local cache cleared")
	}
	if _, exists := mock.data["should:persist"]; !exists {
		t.Fatal("expected Redis key to survive Clear")
	}
}

// ── Count ────────────────────────────────────────────────────────────────────

func TestRedisCache_Count_ReturnsLocalCount(t *testing.T) {
	mock := newMockRedisClient()
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()

	rc.local.Set("a", 1)
	rc.local.Set("b", 2)
	rc.local.Set("c", 3)

	if rc.Count() != 3 {
		t.Fatalf("expected Count=3, got %d", rc.Count())
	}
}

func TestRedisCache_Count_WhenDisabled(t *testing.T) {
	mock := newMockRedisClient()
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()
	rc.enabled.Store(false)

	rc.local.Set("only", "one")
	if rc.Count() != 1 {
		t.Fatalf("expected Count=1 when disabled, got %d", rc.Count())
	}
}

// ── Full round-trip: Set → Get with JSON values ─────────────────────────────

func TestRedisCache_RoundTrip_JSON(t *testing.T) {
	mock := newMockRedisClient()
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()

	type testPayload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	rc.Set("json:key", testPayload{Name: "test", Count: 42})

	got, ok := rc.Get("json:key")
	if !ok {
		t.Fatal("expected cache hit")
	}
	// The return value is the JSON-unmarshalled map.
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", got)
	}
	if m["name"] != "test" || int(m["count"].(float64)) != 42 {
		t.Fatalf("unexpected payload: %v", m)
	}
}

// ── CacheInterface compliance ────────────────────────────────────────────────

func TestCacheInterface_Compliance(t *testing.T) {
	// Compile-time assertions already in redis.go:
	//   var _ CacheInterface = (*Cache)(nil)
	//   var _ CacheInterface = (*RedisCache)(nil)
	//
	// This test ensures runtime behavior also matches the interface contract.
	tests := []struct {
		name string
		c    CacheInterface
	}{
		{"in-memory Cache", New(10 * time.Second)},
		{"RedisCache", func() CacheInterface {
			mock := newMockRedisClient()
			rc := NewRedisCache(mock, 10*time.Second)
			t.Cleanup(func() { rc.Close() })
			return rc
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set → Get
			tt.c.Set("test", "hello")
			v, ok := tt.c.Get("test")
			if !ok {
				t.Fatal("expected Get to return true after Set")
			}
			if v != "hello" {
				t.Fatalf("expected 'hello', got %v", v)
			}

			// Get miss on unknown key
			_, ok = tt.c.Get("nonexistent")
			if ok {
				t.Fatal("expected miss on unknown key")
			}

			// Clear — for RedisCache this only clears the local fallback.
			// Disable Redis first so Get falls through to the (now-cleared)
			// local cache, verifying the local-clear behavior.
			if rc, isRC := tt.c.(*RedisCache); isRC {
				rc.enabled.Store(false)
				// Seed local cache so Clear has something to remove.
				rc.local.Set("test", "local-hello")
			}
			tt.c.Clear()
			_, ok = tt.c.Get("test")
			if ok {
				t.Fatal("expected miss after Clear")
			}
		})
	}
}

// ── Edge cases ──────────────────────────────────────────────────────────────

func TestRedisCache_Get_ContextTimeout(t *testing.T) {
	// This test verifies that Get handles context properly when Redis
	// times out. The mock doesn't block, so we just verify the error path.
	mock := newMockRedisClient()
	mock.getErr = context.DeadlineExceeded
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()

	got, ok := rc.Get("timeout:key")
	if ok || got != nil {
		t.Fatalf("expected miss on context timeout, got %v, %v", got, ok)
	}
}

func TestRedisCache_Set_MarshalError(t *testing.T) {
	// JSON cannot marshal channels — Set should log and skip without panicking.
	mock := newMockRedisClient()
	rc := NewRedisCache(mock, 10*time.Second)
	defer rc.Close()

	// func values and channels cannot be JSON-marshalled.
	rc.Set("bad:key", make(chan int))
	if mock.setCalls.Load() != 0 {
		t.Fatal("expected zero Set calls on marshal failure")
	}
}
