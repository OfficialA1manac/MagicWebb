// Package verifier recomputes the collection `verified` badge from on-chain
// facts. It replaces the admin curation deleted in 3d2010d: a collection earns
// the badge by declaring ERC-721 or ERC-1155 through ERC-165 supportsInterface
// AND having resolved its token metadata at least once.
//
// The probe runs here rather than inside the indexer's event handlers for two
// reasons: an eth_call on the hot path would cost an RPC round-trip per event,
// and a handler-driven check would never reach collections already sitting in
// the database.
package verifier

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/chain"
)

// Store is the slice of *db.Q the sweeper needs.
type Store interface {
	ListCollectionsForVerification(ctx context.Context, staleBefore time.Time, limit int) ([]string, error)
	SetCollectionVerification(ctx context.Context, addr string, standardVerified bool) error
}

// Defaults chosen to stay invisible next to the indexer's RPC load: 25 calls
// every 5 minutes is under 0.1 req/s even when every probe misses its cache.
const (
	DefaultInterval = 5 * time.Minute
	DefaultRecheck  = 24 * time.Hour
	DefaultBatch    = 25
)

type Runner struct {
	q   Store
	eth chain.Caller

	// Interval between sweeps. Recheck is how old a verification may get
	// before it is probed again — it must be finite because the metadata half
	// of the badge lands after a collection is first seen, so the first pass
	// over a fresh collection legitimately says "not yet".
	Interval time.Duration
	Recheck  time.Duration
	Batch    int
}

func New(q Store, eth chain.Caller) *Runner {
	return &Runner{
		q:        q,
		eth:      eth,
		Interval: DefaultInterval,
		Recheck:  DefaultRecheck,
		Batch:    DefaultBatch,
	}
}

// Run sweeps until ctx is cancelled. The first pass runs immediately so a fresh
// deploy does not wait a full interval before any badge appears.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(r.Interval)
	defer t.Stop()

	for {
		if n, err := r.SweepOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Warn().Err(err).Msg("verifier sweep failed")
		} else if n > 0 {
			log.Debug().Int("checked", n).Msg("verifier sweep")
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SweepOnce probes one batch and returns how many collections were stamped.
// A collection whose probe failed on the transport is left unstamped so the
// next pass retries it — recording "unverified" because an RPC blipped would
// drop badges across the site during an outage.
func (r *Runner) SweepOnce(ctx context.Context) (int, error) {
	addrs, err := r.q.ListCollectionsForVerification(ctx, time.Now().Add(-r.Recheck), r.Batch)
	if err != nil {
		return 0, err
	}

	var stamped int
	for _, addr := range addrs {
		if ctx.Err() != nil {
			return stamped, ctx.Err()
		}
		std, err := chain.DetectStandard(ctx, r.eth, addr)
		if err != nil {
			log.Debug().Err(err).Str("collection", addr).Msg("verifier probe unreachable")
			continue
		}
		if err := r.q.SetCollectionVerification(ctx, addr, std != ""); err != nil {
			log.Warn().Err(err).Str("collection", addr).Msg("verifier write failed")
			continue
		}
		stamped++
	}
	return stamped, nil
}
