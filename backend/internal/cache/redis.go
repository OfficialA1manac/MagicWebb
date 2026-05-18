// Package cache wraps go-redis with typed helpers for WebbPlace use-cases.
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// Client wraps the Redis connection.
type Client struct{ rdb *redis.Client }

// Connect parses the Redis URL and pings the server.
func Connect(ctx context.Context, url string) (*Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("cache: parse URL: %w", err)
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("cache: ping: %w", err)
	}
	log.Info().Str("addr", opts.Addr).Msg("redis connected")
	return &Client{rdb}, nil
}

func (c *Client) Close() { c.rdb.Close() }

// ── SIWE nonce ────────────────────────────────────────────────────────────

const noncePrefix = "siwe:nonce:"

// SetNonce stores a SIWE nonce for `address` with the given TTL.
func (c *Client) SetNonce(ctx context.Context, address, nonce string, ttl time.Duration) error {
	return c.rdb.Set(ctx, noncePrefix+address, nonce, ttl).Err()
}

// GetNonce retrieves and DELETES the nonce (single-use).
// Returns ("", false, nil) if not found.
func (c *Client) GetNonce(ctx context.Context, address string) (nonce string, found bool, err error) {
	val, err := c.rdb.GetDel(ctx, noncePrefix+address).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

// ── Server time cache ─────────────────────────────────────────────────────

// SetServerTime caches the latest block timestamp (used for countdown sync).
func (c *Client) SetServerTime(ctx context.Context, unixMs int64) error {
	return c.rdb.Set(ctx, "server:time", unixMs, 5*time.Second).Err()
}

func (c *Client) GetServerTime(ctx context.Context) (int64, error) {
	return c.rdb.Get(ctx, "server:time").Int64()
}

// ── Generic helpers ───────────────────────────────────────────────────────

func (c *Client) Set(ctx context.Context, key string, val any, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, val, ttl).Err()
}

func (c *Client) Get(ctx context.Context, key string) (string, error) {
	return c.rdb.Get(ctx, key).Result()
}

func (c *Client) Del(ctx context.Context, keys ...string) error {
	return c.rdb.Del(ctx, keys...).Err()
}

// Pub/Sub (used by WebSocket hub)

func (c *Client) Publish(ctx context.Context, channel string, msg any) error {
	return c.rdb.Publish(ctx, channel, msg).Err()
}

func (c *Client) Subscribe(ctx context.Context, channels ...string) *redis.PubSub {
	return c.rdb.Subscribe(ctx, channels...)
}
