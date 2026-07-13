//go:build !redis

package cache

import (
	"errors"
	"time"

	"github.com/rs/zerolog/log"
)

// newRedisClient is the default (non-Redis) build. It returns an error
// so that NewRedisOrMemory falls back to the in-memory cache. Rebuild
// with -tags redis to activate the real Redis client.
func newRedisClient(redisURL string, ttl time.Duration) (CacheInterface, error) {
	log.Warn().Str("redis_url", truncateURL(redisURL)).
		Msg("cache: REDIS_URL configured but Redis support not compiled in; using in-memory cache. Rebuild with -tags redis.")
	return nil, errors.New("redis not compiled in")
}
