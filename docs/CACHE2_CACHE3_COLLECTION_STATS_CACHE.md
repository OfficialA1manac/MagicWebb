# CACHE-2 & CACHE-3: Collection Stats Caching & Startup Warming

**Status:** ✅ Implemented | **Commits:** `8818c75`, `90b6e43` | **Category:** Performance / Caching

---

## Overview

CACHE-2 and CACHE-3 work together to eliminate cold-start latency on collection
pages and prevent repeated DB queries for collection statistics.

### CACHE-3: TTL Collection Stats Cache

A 30-second TTL cache for collection statistics (floor price, 24h volume, listed count)
that is shared across all three data paths:

| Consumer | Path | Before CACHE-3 | After CACHE-3 |
|----------|------|---------------|---------------|
| GraphQL DataLoader | `dataloader/loaders.go` | ✅ Had cache | ✅ Same cache |
| gRPC `ListCollections` | `connectrpc/marketplacev1/server.go` | ❌ No cache | ✅ `StatsCache.Get/Set` |
| gRPC `GetCollection` | `connectrpc/marketplacev1/server.go` | ❌ No cache | ✅ `StatsCache.Get/Set` |
| REST collections | `api/collections.go` | ✅ Separate cache | ✅ Separate cache (unchanged) |

### CACHE-2: Startup Cache Warming

On server startup, a background goroutine pre-fills two caches to prevent the first
page load after a deploy from hitting a cold cache:

| Cache | What's warmed | Method |
|-------|--------------|--------|
| **Trending** | 1h, 24h, 7d windows (20 results each) | `GetTrendingCollections()` |
| **Collection stats** | Top 50 collections | `ListCollections(50)` → `GetCollectionStatsBatch()` |

---

## Architecture

### Shared Cache Model

```
┌────────────────────────────────────────────────────┐
│                  dataloader.StatsCache              │
│              (cache.CacheInterface, 30s TTL)         │
└────────┬──────────────┬──────────────┬──────────────┘
         │              │              │
    ┌────▼────┐   ┌─────▼──────┐  ┌───▼──────────┐
    │ GraphQL │   │   gRPC     │  │    gRPC      │
    │Loader   │   │ListColls   │  │ GetCollection│
    └─────────┘   └────────────┘  └──────────────┘
         │              │              │
    loadCollection  ListCollections  GetCollection
    Stats()         RPC handler      RPC handler
```

### Key Design Decisions

1. **Single shared cache**: `dataloader.StatsCache` is a package-level `cache.CacheInterface` variable that all consumers share. This means a gRPC `ListCollections` call that populates the cache benefits subsequent GraphQL queries and vice versa.

2. **30-second TTL**: Collection stats change slowly (only on new listings, bids, or settlements). A 30-second TTL means at most 2 cache misses per minute per collection — eliminating ~98% of DB round-trips for stats queries.

3. **Content-addressed keys**: Cache keys are raw collection addresses (lowercase `0x...`). The value type is `db.CollectionStats` (struct with `FloorPriceWei`, `Volume24hWei`, `ListedCount`).

4. **Cache-bypass on DB error**: When the DB query fails during warming or normal operation, the error is logged but never cached. The next request retries the DB query naturally.

5. **Type assertions**: Cache values are stored as `any` (Go interface) and need a type assertion back to `db.CollectionStats`. The assertion `cached.(db.CollectionStats)` is safe because only `db.CollectionStats` values are stored under address keys.

---

## Startup Warming Flow

```
main() → go warmCriticalCaches(ctx, q, trendingCache)
                              │
                    ┌─────────┴──────────┐
                    ▼                    ▼
            Phase 1: Trending      Phase 2: Stats
                    │                    │
         ┌──────────┼──────────┐   ListCollections(50)
         ▼          ▼          ▼          │
       tr:1h:20  tr:24h:20  tr:7d:20    ▼
         │          │          │    GetCollectionStatsBatch()
         ▼          ▼          ▼          │
      cache.Set   cache.Set   cache.Set   ▼
                                     dataloader.StatsCache.Set()
```

### Properties

- **Non-blocking**: Runs in a background goroutine — server starts serving traffic immediately
- **Best-effort**: Failures are logged as warnings but never prevent startup
- **Timeout-bounded**: Each DB query has a 5-second timeout to prevent hanging
- **Context-aware**: Cancels if the parent context is cancelled (e.g., during shutdown)
- **Independent phases**: Trending failure doesn't block stats warming; nil trending cache doesn't block stats warming

### Warming Logs

```
startup: trending cache warm complete  window=1h  rows=20
startup: trending cache warm complete  window=24h rows=20
startup: trending cache warm complete  window=7d  rows=20
startup: collection stats cache warm complete  collections=50  stats_warmed=50
```

---

## Implementation Details

### Files Modified

| File | Change | Commit |
|------|--------|--------|
| `dataloader/loaders.go` | Exported `statsCache` → `StatsCache` (was private) | `8818c75` |
| `connectrpc/marketplacev1/server.go` | `ListCollections`: split cache check + DB query for uncached only | `8818c75` |
| `connectrpc/marketplacev1/server.go` | `GetCollection`: cache-first, DB-fallback with cache population | `8818c75` |
| `cmd/server/main.go` | Renamed `warmTrendingCache` → `warmCriticalCaches` | `90b6e43` |
| `cmd/server/main.go` | Added Phase 2: collection stats warming via `ListCollections` + `GetCollectionStatsBatch` | `90b6e43` |
| `cmd/server/main.go` | Added `dataloader` import for `dataloader.StatsCache` | `90b6e43` |

### `ListCollections` gRPC Cache Logic

```go
// Check cache first for each address
cachedStats := make(map[string]db.CollectionStats)
var uncached []string
for _, addr := range addrs {
    if cached, ok := dataloader.StatsCache.Get(addr); ok {
        cachedStats[addr] = cached.(db.CollectionStats)
    } else {
        uncached = append(uncached, addr)
    }
}

// Only query DB for uncached addresses
if len(uncached) > 0 {
    if stats, err := s.q.GetCollectionStatsBatch(ctx, uncached); err == nil {
        for addr, s := range stats {
            cachedStats[addr] = s
            dataloader.StatsCache.Set(addr, s)
        }
    }
}

// Fill stats from merged cache+DB result
for i, c := range cols {
    if s, ok := cachedStats[c.Address]; ok {
        cols[i].FloorPriceWei = s.FloorPriceWei
        cols[i].Volume_24HWei = s.Volume24hWei
        cols[i].ListedCount = int32(s.ListedCount)
    }
}
```

### `GetCollection` gRPC Cache Logic

```go
// Cache-first, DB-fallback with population
if cached, ok := dataloader.StatsCache.Get(address); ok {
    s := cached.(db.CollectionStats)
    res.FloorPriceWei = s.FloorPriceWei
    res.Volume_24HWei = s.Volume24hWei
    res.ListedCount = int32(s.ListedCount)
} else {
    if stats, err := s.q.GetCollectionStats(ctx, address); err == nil {
        res.FloorPriceWei = stats.FloorPriceWei
        res.Volume_24HWei = stats.Volume24hWei
        res.ListedCount = int32(stats.ListedCount)
        dataloader.StatsCache.Set(address, stats)
    }
}
```

---

## Cache Observability (CACHE-4)

The underlying `cache.Cache` implementation includes Prometheus-compatible hit/miss/set/eviction
counters exposed via the `/metrics` endpoint:

```
# HELP magicwebb_cache_hits_total Total cache hits across all REST in-memory caches.
# TYPE magicwebb_cache_hits_total counter
magicwebb_cache_hits_total <N>

# HELP magicwebb_cache_misses_total Total cache misses across all REST in-memory caches.
# TYPE magicwebb_cache_misses_total counter
magicwebb_cache_misses_total <N>
```

The `dataloader.StatsCache` inherits these counters automatically since it's backed by
`cache.New(30*time.Second)`.

---

## Edge Cases & Behavior

| Scenario | Behavior |
|----------|----------|
| **All addresses in cache** | Zero DB queries for stats — all served from cache |
| **All addresses miss cache** | Single `GetCollectionStatsBatch()` call, results cached for next requests |
| **Partial hits** | Only uncached addresses trigger a DB batch query |
| **DB query fails** | Error logged, zero-valued stats returned (no caching of failures) |
| **Nil trending cache** | Phase 1 skipped, Phase 2 (stats warming) still runs |
| **Context cancelled during warming** | Warming goroutine returns immediately, no partial results cached |
| **Cold start (first deploy)** | `ListCollections` returns empty, stats warming is a no-op (no crash) |
| **Type assertion panics** | Not possible — only `db.CollectionStats` values are stored under address keys |

---

## Related Features

- **CACHE-1**: Redis-backed distributed cache (`cache.NewRedisOrMemory`) — used by REST trending/activity caches
- **CACHE-4**: Prometheus cache metrics (`cache.Cache.Stats()`) — hit/miss/set/eviction counters
- **GQL-2**: GraphQL response cache — separate tiered cache for complete GraphQL responses
- **GQL-3**: DataLoader stats preloading — eliminates duplicate DB queries when proto already has stats
