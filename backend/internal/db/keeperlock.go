package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// keeperLockKey is the cluster-wide advisory lock key for keeper single-flight
// ("MagicWeb" as big-endian int64). All instances compete for it; only the
// holder broadcasts keeper transactions, preventing duplicate settles and
// keeper-nonce races in multi-instance deploys.
const keeperLockKey int64 = 0x4D61676963576562

// WaitKeeperLock blocks until this instance wins the keeper advisory lock or
// ctx is cancelled.
//
// The lock is held on a DEDICATED session connection dialed via SessionDSN —
// never through the shared pgxpool. Session advisory locks are meaningless
// through Supabase's transaction-mode pooler (:6543): the lock would attach to
// an arbitrary pooled server connection and leak there, permanently blocking
// every instance. SessionDSN re-points :6543 → :5432 exactly as the SSE bridge
// does.
//
// On success it returns a context derived from ctx that is cancelled the
// moment lock ownership can no longer be proven (a periodic ping on the lock
// connection fails — Postgres releases the lock server-side at that point and
// another instance may take over). Keeper goroutines must run under lockCtx
// and re-enter this function to re-acquire, which closes the split-brain
// window where two instances would broadcast with the same keeper key.
func WaitKeeperLock(ctx context.Context, dsn string) (lockCtx context.Context, release func(), err error) {
	const retry = 15 * time.Second
	const pingEvery = 10 * time.Second
	sessionDSN := SessionDSN(dsn)

	for {
		conn, derr := pgx.Connect(ctx, sessionDSN)
		if derr != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			log.Warn().Err(derr).Msg("keeper lock: session dial failed, retrying")
		} else {
			var got bool
			if qerr := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", keeperLockKey).Scan(&got); qerr != nil {
				log.Warn().Err(qerr).Msg("keeper lock: try lock failed, retrying")
				_ = conn.Close(context.Background())
			} else if !got {
				_ = conn.Close(context.Background())
			} else {
				log.Info().Msg("keeper lock: acquired — this instance runs the keepers")
				lctx, cancel := context.WithCancel(ctx)

				// Liveness monitor: lock ownership is only as alive as this
				// session. A failed ping means Postgres has (or may have)
				// released the lock — stop the keepers immediately.
				go func() {
					t := time.NewTicker(pingEvery)
					defer t.Stop()
					for {
						select {
						case <-lctx.Done():
							return
						case <-t.C:
							pctx, pcancel := context.WithTimeout(lctx, 5*time.Second)
							perr := conn.Ping(pctx)
							pcancel()
							if perr != nil && lctx.Err() == nil {
								log.Warn().Err(perr).Msg("keeper lock: lost (ping failed) — stopping keepers")
								cancel()
								return
							}
						}
					}
				}()

				rel := func() {
					cancel()
					// Best-effort unlock; closing the session releases the
					// lock server-side regardless.
					uctx, ucancel := context.WithTimeout(context.Background(), 5*time.Second)
					_, _ = conn.Exec(uctx, "SELECT pg_advisory_unlock($1)", keeperLockKey)
					ucancel()
					_ = conn.Close(context.Background())
				}
				return lctx, rel, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(retry):
		}
	}
}
