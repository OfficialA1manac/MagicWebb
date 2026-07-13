//go:build redis

package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// redisClientAdapter wraps *redis.Client to satisfy the RedisClient interface.
// go-redis uses command types (Get returns *StringCmd, Set returns *StatusCmd,
// Ping returns *StatusCmd) but our interface expects plain (string, error) /
// error returns. This adapter calls .Result() / .Err() on each command to
// unwrap the go-redis command abstraction.
type redisClientAdapter struct {
	client *redis.Client
}

func (a *redisClientAdapter) Get(ctx context.Context, key string) (string, error) {
	return a.client.Get(ctx, key).Result()
}

func (a *redisClientAdapter) Set(ctx context.Context, key string, value any, expiration time.Duration) error {
	return a.client.Set(ctx, key, value, expiration).Err()
}

func (a *redisClientAdapter) Ping(ctx context.Context) error {
	return a.client.Ping(ctx).Err()
}

func (a *redisClientAdapter) Close() error {
	return a.client.Close()
}

// newRedisClient creates a real Redis-backed cache when the `redis` build
// tag is active. It parses the REDIS_URL, connects with a 3s timeout, and
// pings to confirm connectivity. On any failure, it returns nil + error
// so NewRedisOrMemory falls back to the in-memory cache.
func newRedisClient(redisURL string, ttl time.Duration) (CacheInterface, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Error().Err(err).Str("redis_url", truncateURL(redisURL)).
			Msg("cache: failed to parse REDIS_URL; falling back to in-memory cache")
		return nil, err
	}

	// Conservative timeouts — Redis should respond in < 100ms on a
	// same-region Fly.io private network. 3s is the circuit-breaker
	// deadline for startup connectivity.
	opts.DialTimeout = 3 * time.Second
	opts.ReadTimeout = 1 * time.Second
	opts.WriteTimeout = 1 * time.Second
	opts.PoolSize = 10
	opts.MinIdleConns = 2

	client := redis.NewClient(opts)
	adapter := &redisClientAdapter{client: client}

	// Verify connectivity before returning so callers don't hit a
	// broken cache on the first request.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := adapter.Ping(ctx); err != nil {
		log.Error().Err(err).Str("redis_url", truncateURL(redisURL)).
			Msg("cache: Redis ping failed; falling back to in-memory cache")
		adapter.Close()
		return nil, err
	}

	log.Info().Str("redis_url", truncateURL(redisURL)).
		Msg("cache: Redis connected — distributed cache active")

	return NewRedisCache(adapter, ttl), nil
}
