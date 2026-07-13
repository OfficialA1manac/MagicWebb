package interceptors

import (
	"context"
	"time"

	"connectrpc.com/connect"
)

// DeadlineInterceptor propagates an HTTP request deadline to the gRPC
// context. Without this, gRPC handlers run indefinitely — a slow DB query
// from a timed-out HTTP request wastes resources. This interceptor checks
// the incoming context for an existing deadline and, if none is set, adds
// a default timeout to prevent runaway queries.
//
// defaultTimeout is applied when the incoming context has no deadline.
// A zero defaultTimeout means no default is applied (deadline-less
// contexts stay deadline-less).
func DeadlineInterceptor(defaultTimeout time.Duration) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// If the context already has a deadline (set by the HTTP server
			// or an upstream interceptor), use it as-is.
			if _, hasDeadline := ctx.Deadline(); hasDeadline {
				return next(ctx, req)
			}

			// No deadline — apply the default timeout to prevent runaway
			// queries. This is especially important for list endpoints
			// (ListCollections, Search) which can scan large tables.
			if defaultTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
				defer cancel()
			}

			return next(ctx, req)
		}
	}
}
