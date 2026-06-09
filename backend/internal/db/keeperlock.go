package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// keeperLockKey is the cluster-wide advisory lock key for keeper single-flight
// ("MagicWeb" as big-endian int64). All instances compete for it; only the
// holder broadcasts keeper transactions, preventing duplicate settles and
// keeper-nonce races in multi-instance deploys.
const keeperLockKey int64 = 0x4D61676963576562

// WaitKeeperLock blocks until this instance wins the keeper advisory lock or
// ctx is cancelled. The lock rides a dedicated pooled connection, so Postgres
// releases it automatically if the process or connection dies — another
// instance then acquires it on its next retry, giving keeper failover with no
// extra infrastructure.
func WaitKeeperLock(ctx context.Context, pool *pgxpool.Pool) (release func(), err error) {
	const retry = 15 * time.Second
	for {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			log.Warn().Err(err).Msg("keeper lock: acquire conn failed, retrying")
		} else {
			var got bool
			if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", keeperLockKey).Scan(&got); err != nil {
				log.Warn().Err(err).Msg("keeper lock: try lock failed, retrying")
				conn.Release()
			} else if got {
				log.Info().Msg("keeper lock: acquired — this instance runs the keepers")
				return func() {
					// Unlock best-effort; releasing the conn closes the session
					// lock anyway if the unlock itself fails.
					_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", keeperLockKey)
					conn.Release()
				}, nil
			} else {
				conn.Release()
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retry):
		}
	}
}
