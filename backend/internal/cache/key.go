package cache

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

// Key builds a cache key namespaced by chain: mw:<chainID>:<parts joined by ":">.
//
// Every Redis-backed cache in the process MUST key through here. Each network
// is a separate deployment with its own database, and a Redis shared between
// two of them (or reused after a rebind) would otherwise serve Songbird's
// listings to a Coston2 page under an identical "ls:..." key. The prefix makes
// such a collision impossible even before BindChain refuses the connection.
func Key(chainID uint64, parts ...string) string {
	var b strings.Builder
	b.Grow(8 + 20 + len(parts)*16)
	b.WriteString("mw:")
	b.WriteString(strconv.FormatUint(chainID, 10))
	for _, p := range parts {
		b.WriteByte(':')
		b.WriteString(p)
	}
	return b.String()
}

// bootChainKey is the marker every process SETs (NX) on boot. Its value is the
// chain id the Redis instance is bound to. No TTL: the binding is permanent —
// re-pointing an app at another network's Redis is exactly the mistake this
// exists to catch.
const bootChainKey = "mw:boot:chain"

// ChainBinder is the subset of a Redis client BindChain needs. Kept tiny so
// unit tests can supply an in-memory fake.
type ChainBinder interface {
	// SetNX sets key to value only when absent; ok=true when the write happened.
	SetNX(ctx context.Context, key, value string) (ok bool, err error)
	Get(ctx context.Context, key string) (string, error)
}

// ErrRedisChainMismatch is returned (wrapped, with both chain ids) when a Redis
// instance already carries another network's boot marker.
var ErrRedisChainMismatch = errors.New("REDIS_URL is bound to another chain")

// BindChain claims the Redis instance for chainID or verifies the existing
// claim. It returns ErrRedisChainMismatch (wrapped) when the instance is
// already bound to a different chain, nil when this chain owns it (fresh or
// already ours), and any other error when Redis could not be reached — the
// caller decides whether unreachability is fatal (it is not: the cache layer
// degrades to memory).
func BindChain(ctx context.Context, rc ChainBinder, chainID uint64) error {
	want := strconv.FormatUint(chainID, 10)
	ok, err := rc.SetNX(ctx, bootChainKey, want)
	if err != nil {
		return fmt.Errorf("cache: set boot marker: %w", err)
	}
	if ok {
		return nil
	}
	have, err := rc.Get(ctx, bootChainKey)
	if err != nil {
		return fmt.Errorf("cache: read boot marker: %w", err)
	}
	if strings.TrimSpace(have) != want {
		return fmt.Errorf("%w: REDIS_URL is bound to chain %s, this app is chain %s — never share Redis across networks", ErrRedisChainMismatch, strings.TrimSpace(have), want)
	}
	return nil
}

// BindChainURL dials redisURL and runs BindChain. It is the boot-time entry
// point: an empty URL is a no-op (in-memory caches), an unreachable Redis is
// logged and tolerated (the caches fall back to memory exactly as
// NewRedisOrMemory does), and a chain mismatch is returned for the caller to
// treat as fatal.
func BindChainURL(ctx context.Context, redisURL string, chainID uint64) error {
	if redisURL == "" {
		return nil
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Error().Err(err).Str("redis_url", truncateURL(redisURL)).Msg("cache: REDIS_URL unparsable; skipping chain binding")
		return nil
	}
	opts.DialTimeout = 3 * time.Second
	opts.ReadTimeout = time.Second
	opts.WriteTimeout = time.Second
	client := redis.NewClient(opts)
	defer client.Close()

	err = BindChain(ctx, goRedisBinder{client}, chainID)
	if err == nil {
		log.Info().Uint64("chain", chainID).Str("redis_url", truncateURL(redisURL)).Msg("cache: Redis bound to this chain")
		return nil
	}
	if errors.Is(err, ErrRedisChainMismatch) {
		return err
	}
	log.Warn().Err(err).Str("redis_url", truncateURL(redisURL)).Msg("cache: Redis unreachable at boot; chain binding deferred, caches run in memory until it recovers")
	return nil
}

// goRedisBinder adapts *redis.Client to ChainBinder.
type goRedisBinder struct{ c *redis.Client }

func (g goRedisBinder) SetNX(ctx context.Context, key, value string) (bool, error) {
	return g.c.SetNX(ctx, key, value, 0).Result()
}

func (g goRedisBinder) Get(ctx context.Context, key string) (string, error) {
	return g.c.Get(ctx, key).Result()
}
