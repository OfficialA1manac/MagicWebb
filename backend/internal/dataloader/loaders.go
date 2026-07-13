// Package dataloader provides request-scoped DataLoader instances for GraphQL
// resolvers. Each request gets a fresh Loaders struct; loaders batch individual
// DB calls into single round-trips, eliminating the N+1 query problem for
// common nested GraphQL queries (collections→stats, auctions→bids).
package dataloader

import (
	"context"
	"time"

	"github.com/graph-gophers/dataloader/v7"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// CollectionStatsCacheTTL controls how long collection stats are cached in
// process memory. 30 seconds balances freshness (stats change slowly — only
// when new listings/bids arrive) against DB load (stats are fetched on every
// collection page and every listing grid render).
const CollectionStatsCacheTTL = 30 * time.Second

// statsCache is a process-wide TTL cache for collection stats, shared across
// all requests. Since stats are read-heavy and rarely change within 30s,
// this eliminates the DB round-trip for repeat queries. Uses in-memory
// cache by default (package-level init can't access config). When Redis
// is later wired at a higher level, the collections service cache in
// api/rest.go provides cross-instance consistency for the trending path.
var statsCache cache.CacheInterface = cache.New(CollectionStatsCacheTTL)

// Loaders holds all DataLoader instances for a single GraphQL request.
// Created per-request in the GraphQL HTTP handler and injected via context.
type Loaders struct {
	CollectionStats      *dataloader.Loader[string, db.CollectionStats]
	AuctionBids          *dataloader.Loader[int64, []db.BidRow]
	AuctionEffectiveBids *dataloader.Loader[int64, []db.EffectiveBidRow]
}

// New creates a fresh set of DataLoaders backed by the given DB handle.
func New(q *db.Q) *Loaders {
	return &Loaders{
		CollectionStats: dataloader.NewBatchedLoader(func(ctx context.Context, keys []string) []*dataloader.Result[db.CollectionStats] {
			return loadCollectionStats(ctx, q, keys)
		}),
		AuctionBids: dataloader.NewBatchedLoader(func(ctx context.Context, keys []int64) []*dataloader.Result[[]db.BidRow] {
			return loadAuctionBids(ctx, q, keys)
		}),
		AuctionEffectiveBids: dataloader.NewBatchedLoader(func(ctx context.Context, keys []int64) []*dataloader.Result[[]db.EffectiveBidRow] {
			return loadAuctionEffectiveBids(ctx, q, keys)
		}),
	}
}

// ── Batch load functions ────────────────────────────────────────────────────

func loadCollectionStats(ctx context.Context, q *db.Q, addrs []string) []*dataloader.Result[db.CollectionStats] {
	results := make([]*dataloader.Result[db.CollectionStats], len(addrs))

	// CACHE-3: Check the process-wide TTL cache before hitting the DB.
	// Stats change slowly (only on new listings/bids/settlements), so
	// a 30s cache eliminates repeat DB queries for collection pages and
	// listing grids that reference the same collections.
	var uncached []int // indices of addresses not found in cache
	for i, addr := range addrs {
		if cached, ok := statsCache.Get(addr); ok {
			results[i] = &dataloader.Result[db.CollectionStats]{Data: cached.(db.CollectionStats)}
		} else {
			uncached = append(uncached, i)
		}
	}

	if len(uncached) == 0 {
		return results
	}

	// Build list of addresses that need a DB fetch.
	missAddrs := make([]string, len(uncached))
	for j, idx := range uncached {
		missAddrs[j] = addrs[idx]
	}

	statsMap, err := q.GetCollectionStatsBatch(ctx, missAddrs)
	if err != nil {
		for _, idx := range uncached {
			results[idx] = &dataloader.Result[db.CollectionStats]{Error: err}
		}
		return results
	}

	for i, addr := range addrs {
		if results[i] != nil {
			continue // already filled from cache
		}
		s, ok := statsMap[addr]
		if !ok {
			s = db.CollectionStats{FloorPriceWei: "0", Volume24hWei: "0", ListedCount: 0}
		}
		statsCache.Set(addr, s)
		results[i] = &dataloader.Result[db.CollectionStats]{Data: s}
	}
	return results
}

func loadAuctionBids(ctx context.Context, q *db.Q, ids []int64) []*dataloader.Result[[]db.BidRow] {
	results := make([]*dataloader.Result[[]db.BidRow], len(ids))
	bidsMap, err := q.GetBidsForAuctionsBatch(ctx, ids)
	if err != nil {
		for i := range ids {
			results[i] = &dataloader.Result[[]db.BidRow]{Error: err}
		}
		return results
	}
	for i, id := range ids {
		bids, ok := bidsMap[id]
		if !ok {
			bids = []db.BidRow{} // empty, not nil
		}
		results[i] = &dataloader.Result[[]db.BidRow]{Data: bids}
	}
	return results
}

func loadAuctionEffectiveBids(ctx context.Context, q *db.Q, ids []int64) []*dataloader.Result[[]db.EffectiveBidRow] {
	results := make([]*dataloader.Result[[]db.EffectiveBidRow], len(ids))
	effMap, err := q.GetEffectiveBidsBatch(ctx, ids)
	if err != nil {
		for i := range ids {
			results[i] = &dataloader.Result[[]db.EffectiveBidRow]{Error: err}
		}
		return results
	}
	for i, id := range ids {
		eff, ok := effMap[id]
		if !ok {
			eff = []db.EffectiveBidRow{}
		}
		results[i] = &dataloader.Result[[]db.EffectiveBidRow]{Data: eff}
	}
	return results
}

// ── Context key ─────────────────────────────────────────────────────────────

type contextKey struct{}

var loadersKey = contextKey{}

// WithLoaders attaches Loaders to a context for use by GraphQL resolvers.
func WithLoaders(ctx context.Context, l *Loaders) context.Context {
	return context.WithValue(ctx, loadersKey, l)
}

// FromContext extracts Loaders from a context. Returns nil if none attached.
func FromContext(ctx context.Context) *Loaders {
	l, _ := ctx.Value(loadersKey).(*Loaders)
	return l
}
