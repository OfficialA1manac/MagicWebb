package graphql

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"github.com/99designs/gqlgen/graphql/handler/lru"
)

// ── GQL-1: Build-time persisted queries ──────────────────────────────────
//
// persistedQueries maps SHA-256 hashes to known GraphQL query strings,
// pre-populated at init() time. Clients can send only the hash (no query
// text), reducing request size by ~90%. The hash→query mapping is embedded
// in the binary — no server round-trip needed for the initial registration.
//
// Common queries are registered here with both unparameterized (no variables,
// for CDN caching) and parameterized (with $variables) forms. The hash is
// computed as hex(sha256(queryText)) for Apollo APQ compatibility.
var persistedQueries = map[string]string{}

func init() {
	// ── Unparameterized queries (CDN-cacheable, no variables) ──────────────
	register(`{
  listings(limit: 48, sort: "recent") {
    collection tokenID seller priceWei amount standard
    expiresAt listedAt name imageURI collectionVerified
  }
}`)
	register(`{
  collections(limit: 50) {
    address name symbol standard verified
    stats { floorPriceWei volume24hWei listedCount }
  }
}`)
	register(`{
  trending(window: "24h", limit: 20) {
    collection window score views bids volumeWei
  }
}`)
	register(`{ metrics {
    totalActiveListings totalSales grossVolumeWei
    totalAuctions totalBids totalOffers
  } }`)
	register(`{
  auctions(limit: 50, status: "active") {
    auctionId collection tokenID seller reservePriceWei
    highestBidWei highestBidder status startsAt endsAt
    name imageURI
  }
}`)
	register(`{
  activity(limit: 30) {
    type collection tokenID amountWei timestamp txHash
  }
}`)
	register(`{ offers(limit: 50) {
    offerId bidder collection tokenID amountWei feeWei
    units standard status makeTx expiresAt createdAt
  } }`)
	register(`{
  countActiveListings
  countActiveAuctions
  countCollections
}`)

	// ── Parameterized queries (variables required) ────────────────────────
	register(`query Collection($address: String!) {
  collection(address: $address) {
    address name symbol standard deployBlock verified
    stats { floorPriceWei volume24hWei listedCount }
    listings(limit: 48, sort: "recent") {
      collection tokenID seller priceWei amount standard
      expiresAt listedAt name imageURI collectionVerified
    }
  }
}`)
	register(`query Listings($collection: String, $seller: String, $sort: String, $limit: Int, $minPrice: String, $maxPrice: String, $traits: String) {
  listings(collection: $collection, seller: $seller, sort: $sort, limit: $limit, minPrice: $minPrice, maxPrice: $maxPrice, traits: $traits) {
    collection tokenID seller priceWei amount standard
    expiresAt listedAt name imageURI collectionVerified
  }
}`)
	register(`query Listing($collection: String!, $tokenID: String!) {
  listing(collection: $collection, tokenID: $tokenID) {
    collection tokenID seller priceWei amount standard
    expiresAt listedAt name imageURI collectionVerified
  }
}`)
	register(`query Search($query: String!, $limit: Int) {
  search(query: $query, limit: $limit) {
    kind collection tokenID name imageURI
  }
}`)
	register(`query Token($collection: String!, $tokenID: String!) {
  tokenMeta(collection: $collection, tokenID: $tokenID) { name imageURI }
  tokenFullMetadata(collection: $collection, tokenID: $tokenID) {
    name description imageURI animationURI metadataURI
  }
}`)
	register(`query Profile($address: String!) {
  profile(address: $address) {
    address displayName bio avatarURI bannerURI twitter website verified
  }
}`)
	register(`query Auction($id: Int!) {
  auction(id: $id) {
    auctionId collection tokenID seller reservePriceWei
    highestBidWei highestBidder minIncrementBps status
    startsAt endsAt createTx name imageURI
    bids { bidder amountWei placedAt txHash }
  }
}`)
	register(`query Auctions($collection: String, $seller: String, $status: String, $limit: Int, $minPrice: String, $maxPrice: String) {
  auctions(collection: $collection, seller: $seller, status: $status, limit: $limit, minPrice: $minPrice, maxPrice: $maxPrice) {
    auctionId collection tokenID seller reservePriceWei
    highestBidWei highestBidder status startsAt endsAt
    name imageURI
  }
}`)
	register(`query WalletNFTs($owner: String!) {
  walletNFTs(owner: $owner) {
    collection tokenID units standard name imageURI
  }
}`)
	register(`query Trending($window: String, $limit: Int) {
  trending(window: $window, limit: $limit) {
    collection window score views bids volumeWei
  }
}`)
	register(`query Activity($limit: Int, $address: String, $collection: String, $tokenID: String) {
  activity(limit: $limit, address: $address, collection: $collection, tokenID: $tokenID) {
    type collection tokenID amountWei timestamp txHash
  }
}`)
	register(`query Offers($collection: String, $tokenID: String, $bidder: String, $owner: String, $status: String, $limit: Int) {
  offers(collection: $collection, tokenID: $tokenID, bidder: $bidder, owner: $owner, status: $status, limit: $limit) {
    offerId bidder collection tokenID amountWei feeWei
    units standard status makeTx expiresAt createdAt
  }
}`)
	register(`query SavedSearches($address: String!, $page: String, $limit: Int) {
  savedSearches(address: $address, page: $page, limit: $limit) {
    id userAddr name page params createdAt
  }
}`)
	register(`query Notifications($address: String!, $limit: Int) {
  notifications(address: $address, limit: $limit) {
    id kind title body link read createdAt
  }
}`)
}

// register hashes a GraphQL query and stores it in the persisted query map.
// The key is hex(sha256(queryText)) — the same format used by Apollo APQ.
func register(query string) {
	h := sha256.Sum256([]byte(query))
	key := hex.EncodeToString(h[:])
	persistedQueries[key] = query
}

// LookupPersistedQuery returns the query text for a given SHA-256 hash.
// Returns the query and true when the hash is registered at build time;
// returns ("", false) when the hash is unknown.
func LookupPersistedQuery(hash string) (string, bool) {
	q, ok := persistedQueries[hash]
	return q, ok
}

// ── PersistedQueryCache ──────────────────────────────────────────────────
//
// Implements graphql.Cache[string] (gqlgen's APQ Cache interface) with
// two tiers:
//   1. Build-time registry (persistedQueries map) — pre-loaded, read-only
//   2. Runtime LRU cache — for queries discovered after build time
//
// This eliminates the "PersistedQueryNotFound" round-trip for the most
// common queries: the client sends just the hash and the server already
// has the query text loaded in the binary. Unknown hashes fall through
// to the LRU cache so the APQ protocol works for all queries.

// PersistedQueryCache wraps a pre-populated query map with an LRU fallback.
// Safe for concurrent use.
type PersistedQueryCache struct {
	mu  sync.RWMutex
	lru *lru.LRU[string]
}

// NewPersistedQueryCache creates a cache with the given LRU capacity.
// Build-time registered queries are always available regardless of capacity.
func NewPersistedQueryCache(lruCapacity int) *PersistedQueryCache {
	return &PersistedQueryCache{
		lru: lru.New[string](lruCapacity),
	}
}

// Get looks up a query by its SHA-256 hash. Checks the build-time registry
// first, then falls back to the runtime LRU cache.
func (c *PersistedQueryCache) Get(ctx context.Context, key string) (value string, ok bool) {
	// Tier 1: build-time registry (no lock — read-only after init).
	if q, ok := persistedQueries[key]; ok {
		return q, true
	}
	// Tier 2: runtime LRU cache.
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lru.Get(ctx, key)
}

// Add stores a query hash→text mapping in the runtime LRU cache.
// Build-time entries cannot be overwritten (they're in a separate map).
func (c *PersistedQueryCache) Add(ctx context.Context, key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lru.Add(ctx, key, value)
}
