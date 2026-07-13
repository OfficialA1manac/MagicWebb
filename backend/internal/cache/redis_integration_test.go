//go:build redis

// Package cache — Redis integration tests using testcontainers-go.
//
// These tests start a real Redis container and verify end-to-end
// connectivity, Set/Get round-trips, TTL expiry, and fallback behavior.
// On machines without Docker, tests skip automatically.
//
// Run with:
//
//	go test -tags redis -run Integration ./internal/cache/... -v -count=1
package cache

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// redisContainer starts a Redis testcontainer and returns the connection URL.
// Caller must defer cleanup. Skips the test if Docker is not available.
func redisContainer(t *testing.T) (string, func()) {
	t.Helper()

	ctx := context.Background()

	container, err := tcredis.Run(ctx,
		"redis:7-alpine",
		testcontainers.WithStartupTimeout(30*time.Second),
	)
	if err != nil {
		t.Skipf("testcontainers: cannot start Redis container (Docker not available?): %v", err)
	}

	connStr, err := container.ConnectionString(ctx)
	if err != nil {
		container.Terminate(ctx)
		t.Skipf("testcontainers: cannot get Redis connection string: %v", err)
	}

	cleanup := func() {
		container.Terminate(ctx)
	}

	return connStr, cleanup
}

// ── Integration tests ────────────────────────────────────────────────────────

func TestIntegration_RedisConnectAndPing(t *testing.T) {
	redisURL, cleanup := redisContainer(t)
	defer cleanup()

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	client := redis.NewClient(opts)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Redis ping failed: %v", err)
	}
	t.Log("Redis connected and pinged successfully")
}

func TestIntegration_RedisCache_SetAndGet(t *testing.T) {
	redisURL, cleanup := redisContainer(t)
	defer cleanup()

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	client := redis.NewClient(opts)
	defer client.Close()

	adapter := &redisClientAdapter{client: client}
	rc := NewRedisCache(adapter, 30*time.Second)
	defer rc.Close()

	// Set → Get round-trip with a complex JSON value.
	type user struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	rc.Set("user:1", user{Name: "Alice", Email: "alice@example.com"})

	got, ok := rc.Get("user:1")
	if !ok {
		t.Fatal("expected cache hit after Set")
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", got)
	}
	if m["name"] != "Alice" || m["email"] != "alice@example.com" {
		t.Fatalf("unexpected value: %v", m)
	}
	t.Logf("Set → Get round-trip: %v", m)
}

func TestIntegration_RedisCache_TTLExpiry(t *testing.T) {
	redisURL, cleanup := redisContainer(t)
	defer cleanup()

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	client := redis.NewClient(opts)
	defer client.Close()

	adapter := &redisClientAdapter{client: client}

	// Use a short TTL so expiry happens quickly. A 2s sleep against
	// a 1s TTL gives plenty of margin for slow CI runners.
	rc := NewRedisCache(adapter, 1*time.Second)
	defer rc.Close()

	rc.Set("ephemeral", "short-lived")

	// Immediate Get should hit.
	if _, ok := rc.Get("ephemeral"); !ok {
		t.Fatal("expected immediate hit after Set")
	}

	// Wait for TTL to expire.
	time.Sleep(2 * time.Second)

	// After TTL, Redis should return nil, and we fall back to local
	// (which doesn't have the key either).
	got, ok := rc.Get("ephemeral")
	if ok {
		t.Fatalf("expected miss after TTL expiry, got %v", got)
	}
	t.Log("TTL expiry confirmed — key no longer retrievable")
}

func TestIntegration_RedisCache_FallbackOnDisconnect(t *testing.T) {
	redisURL, cleanup := redisContainer(t)
	defer cleanup()

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	client := redis.NewClient(opts)

	adapter := &redisClientAdapter{client: client}
	rc := NewRedisCache(adapter, 30*time.Second)

	// Seed data in Redis and in local fallback.
	rc.Set("shared:key", "redis-value")
	rc.local.Set("shared:key", "local-fallback")

	// Verify Redis is working.
	if got, ok := rc.Get("shared:key"); !ok || got != "redis-value" {
		t.Fatalf("expected Redis hit, got %v, %v", got, ok)
	}

	// Disable Redis (simulating connection failure).
	rc.enabled.Store(false)

	// Should now return the local fallback value.
	got, ok := rc.Get("shared:key")
	if !ok || got != "local-fallback" {
		t.Fatalf("expected local fallback after disabling Redis, got %v, %v", got, ok)
	}
	t.Log("Fallback to local cache works after disabling Redis")

	// Re-enable and verify Redis works again.
	rc.enabled.Store(true)
	if got, ok := rc.Get("shared:key"); !ok || got != "redis-value" {
		t.Fatalf("expected Redis hit after re-enabling, got %v, %v", got, ok)
	}
	t.Log("Re-enabled Redis — data restored")

	rc.Close()
}

func TestIntegration_RedisCache_HealthCheckRecovery(t *testing.T) {
	redisURL, cleanup := redisContainer(t)
	defer cleanup()

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	client := redis.NewClient(opts)

	adapter := &redisClientAdapter{client: client}
	rc := NewRedisCache(adapter, 30*time.Second)
	defer rc.Close()

	// Healthy initially.
	rc.checkHealth()
	if !rc.enabled.Load() {
		t.Fatal("expected enabled after healthy ping")
	}

	// Simulate Redis failure — close the underlying client so Ping fails.
	client.Close()

	rc.checkHealth()
	if rc.enabled.Load() {
		t.Fatal("expected disabled after failed ping")
	}
	t.Log("Health check correctly disabled Redis on failure")

	// Reconnect with a fresh client to simulate recovery.
	newClient := redis.NewClient(opts)
	newAdapter := &redisClientAdapter{client: newClient}
	// No defer newClient.Close() — rc.Close() already closes rc.client
	// which we're about to swap to newAdapter.

	// Swap the client (white-box: directly assign for test).
	rc.client = newAdapter

	rc.checkHealth()
	if !rc.enabled.Load() {
		t.Fatal("expected re-enabled after recovery ping")
	}
	t.Log("Health check correctly re-enabled Redis after recovery")
}

func TestIntegration_RedisCache_NewRedisOrMemory_Factory(t *testing.T) {
	redisURL, cleanup := redisContainer(t)
	defer cleanup()

	// newRedisClient is build-tag-gated; with -tags redis it creates
	// a real Redis connection. Test that the factory works end-to-end.
	c := NewRedisOrMemory(redisURL, 30*time.Second)

	// With a valid Redis URL and -tags redis, we expect a *RedisCache.
	if _, ok := c.(*RedisCache); !ok {
		t.Fatalf("expected *RedisCache with valid Redis URL, got %T", c)
	}

	c.Set("factory:key", "from-factory")
	got, ok := c.Get("factory:key")
	if !ok || got != "from-factory" {
		t.Fatalf("factory round-trip failed: %v, %v", got, ok)
	}
	t.Log("NewRedisOrMemory factory round-trip succeeded")

	if rc, ok := c.(*RedisCache); ok {
		rc.Close()
	}
}

func TestIntegration_RedisCache_EmptyURLReturnsInMemory(t *testing.T) {
	// Empty URL should always return *Cache even with -tags redis.
	c := NewRedisOrMemory("", 10*time.Second)
	if _, ok := c.(*Cache); !ok {
		t.Fatalf("expected *Cache for empty URL, got %T", c)
	}
}
