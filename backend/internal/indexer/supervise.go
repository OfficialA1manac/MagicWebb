package indexer

import (
	"context"
	"runtime/debug"
	"time"

	"github.com/rs/zerolog/log"
)

// workerRestartDelay is the pause between a worker panicking and being
// restarted. Long enough that a worker panicking on every tick cannot spin the
// CPU, short enough that a transient fault (an unexpected RPC response shape,
// a nil map entry) costs seconds of keeper latency rather than requiring a
// process restart.
const workerRestartDelay = 5 * time.Second

// supervise runs fn until ctx is cancelled, restarting it if it panics.
//
// Every long-lived worker in this package runs inside the single server
// process — the same process serving HTTP, SSE, WebSocket and GraphQL. An
// unrecovered panic in any one of them takes all of that down with it, so a
// nil dereference while decoding one malformed log would be a full outage.
// Panics are logged with their stack and the worker comes back; a clean return
// (fn deciding it has nothing to do, e.g. a missing keeper key) is final and
// does not restart.
func supervise(ctx context.Context, name string, fn func(context.Context)) {
	for ctx.Err() == nil {
		if !runGuarded(ctx, name, fn) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(workerRestartDelay):
		}
	}
}

// runGuarded calls fn and reports whether it panicked.
func runGuarded(ctx context.Context, name string, fn func(context.Context)) (panicked bool) {
	defer func() {
		if rec := recover(); rec != nil {
			panicked = true
			log.Error().Str("worker", name).Interface("panic", rec).
				Bytes("stack", debug.Stack()).
				Msg("indexer worker panicked — restarting")
		}
	}()
	fn(ctx)
	return false
}
