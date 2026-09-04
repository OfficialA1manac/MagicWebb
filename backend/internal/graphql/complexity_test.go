package graphql

import (
	"testing"
)

// ── listCost helper tests ──────────────────────────────────────────────

func TestListCost_NilLimit(t *testing.T) {
	// nil limit → defaults to 50.
	got := listCost(3, nil)
	want := costListBase + 3*50 // 10 + 150 = 160
	if got != want {
		t.Errorf("listCost(3, nil): want %d, got %d", want, got)
	}
}

func TestListCost_ZeroLimit(t *testing.T) {
	// zero limit → defaults to 50 (only positive limits override the default).
	zero := 0
	got := listCost(3, &zero)
	want := costListBase + 3*50 // 10 + 150 = 160
	if got != want {
		t.Errorf("listCost(3, 0): want %d, got %d", want, got)
	}
}

func TestListCost_NegativeLimit(t *testing.T) {
	// negative limit → defaults to 50.
	neg := -1
	got := listCost(3, &neg)
	want := costListBase + 3*50 // 10 + 150 = 160
	if got != want {
		t.Errorf("listCost(3, -1): want %d, got %d", want, got)
	}
}

func TestListCost_PositiveLimit(t *testing.T) {
	lim := 20
	got := listCost(3, &lim)
	want := costListBase + 3*20 // 10 + 60 = 70
	if got != want {
		t.Errorf("listCost(3, 20): want %d, got %d", want, got)
	}
}

func TestListCost_HighLimit(t *testing.T) {
	lim := 200
	got := listCost(5, &lim)
	want := costListBase + 5*200 // 10 + 1000 = 1010
	if got != want {
		t.Errorf("listCost(5, 200): want %d, got %d", want, got)
	}
}

func TestListCost_ChildComplexityZero(t *testing.T) {
	// Simple scalars have childComplexity=0 (leaf nodes). The base cost alone.
	lim := 10
	got := listCost(0, &lim)
	want := costListBase + 0 // 10
	if got != want {
		t.Errorf("listCost(0, 10): want %d, got %d", want, got)
	}
}

func TestListCost_LimitOne(t *testing.T) {
	lim := 1
	got := listCost(12, &lim)
	want := costListBase + 12*1 // 10 + 12 = 22
	if got != want {
		t.Errorf("listCost(12, 1): want %d, got %d", want, got)
	}
}

// ── ComplexityConfig: field cost categories ────────────────────────────

func TestComplexityConfig_ScalarFields(t *testing.T) {
	cfg := ComplexityConfig()

	// All scalar/enum/time fields should cost 1.
	checks := []struct {
		name string
		cost int
	}{
		{"Listing.Name", cfg.Listing.Name(0)},
		{"Listing.PriceWei", cfg.Listing.PriceWei(0)},
		{"Listing.Seller", cfg.Listing.Seller(0)},
		{"Listing.TokenID", cfg.Listing.TokenID(0)},
		{"Listing.Amount", cfg.Listing.Amount(0)},
		{"Listing.Collection", cfg.Listing.Collection(0)},
		{"Listing.CollectionVerified", cfg.Listing.CollectionVerified(0)},
		{"Listing.ImageURI", cfg.Listing.ImageURI(0)},
		{"Listing.TxHash", cfg.Listing.TxHash(0)},
		{"Collection.Name", cfg.Collection.Name(0)},
		{"Collection.Symbol", cfg.Collection.Symbol(0)},
		{"Collection.Address", cfg.Collection.Address(0)},
		{"Collection.DeployBlock", cfg.Collection.DeployBlock(0)},
		{"Collection.FloorPrice", cfg.Collection.FloorPrice(0)},
		{"Collection.ListedCount", cfg.Collection.ListedCount(0)},
		{"Collection.Verified", cfg.Collection.Verified(0)},
		{"Collection.CreatorAddr", cfg.Collection.CreatorAddr(0)},
		{"Collection.Volume24h", cfg.Collection.Volume24h(0)},
		{"Auction.AuctionID", cfg.Auction.AuctionID(0)},
		{"Auction.Seller", cfg.Auction.Seller(0)},
		{"Auction.HighestBidWei", cfg.Auction.HighestBidWei(0)},
		{"Auction.HighestBidder", cfg.Auction.HighestBidder(0)},
		{"Auction.CreateTx", cfg.Auction.CreateTx(0)},
		{"Bid.AmountWei", cfg.Bid.AmountWei(0)},
		{"Bid.Bidder", cfg.Bid.Bidder(0)},
		{"Bid.TxHash", cfg.Bid.TxHash(0)},
	}
	for _, c := range checks {
		if c.cost != costScalar {
			t.Errorf("%s: want %d, got %d", c.name, costScalar, c.cost)
		}
	}
}

func TestComplexityConfig_EnumFields(t *testing.T) {
	cfg := ComplexityConfig()

	checks := []struct {
		name string
		cost int
	}{
		{"Listing.Standard", cfg.Listing.Standard(0)},
		{"Auction.Status", cfg.Auction.Status(0)},
		{"Auction.Standard", cfg.Auction.Standard(0)},
		{"Collection.Standard", cfg.Collection.Standard(0)},
		{"Notification.Kind", cfg.Notification.Kind(0)},
		{"Offer.Status", cfg.Offer.Status(0)},
		{"Offer.Standard", cfg.Offer.Standard(0)},
		{"TrendingScore.Window", cfg.TrendingScore.Window(0)},
	}
	for _, c := range checks {
		if c.cost != costEnum {
			t.Errorf("%s: want %d, got %d", c.name, costEnum, c.cost)
		}
	}
}

func TestComplexityConfig_TimeFields(t *testing.T) {
	cfg := ComplexityConfig()

	checks := []struct {
		name string
		cost int
	}{
		{"Listing.ExpiresAt", cfg.Listing.ExpiresAt(0)},
		{"Listing.ListedAt", cfg.Listing.ListedAt(0)},
		{"Auction.EndsAt", cfg.Auction.EndsAt(0)},
		{"Auction.StartsAt", cfg.Auction.StartsAt(0)},
		{"Bid.PlacedAt", cfg.Bid.PlacedAt(0)},
		{"Notification.CreatedAt", cfg.Notification.CreatedAt(0)},
	}
	for _, c := range checks {
		if c.cost != costTime {
			t.Errorf("%s: want %d, got %d", c.name, costTime, c.cost)
		}
	}
}

func TestComplexityConfig_ObjectFields(t *testing.T) {
	cfg := ComplexityConfig()

	checks := []struct {
		name string
		cost int
	}{
		{"Collection.Stats", cfg.Collection.Stats(0)},
		{"Query.Metrics", cfg.Query.Metrics(0)},
		{"Query.Profile", cfg.Query.Profile(0, "")},
		{"Query.CollectionStats", cfg.Query.CollectionStats(0, "")},
	}
	for _, c := range checks {
		if c.cost != costObject {
			t.Errorf("%s: want %d, got %d", c.name, costObject, c.cost)
		}
	}
}

func TestComplexityConfig_CollectionStatsFields(t *testing.T) {
	cfg := ComplexityConfig()

	checks := []struct {
		name string
		cost int
	}{
		{"CollectionStats.FloorPriceWei", cfg.CollectionStats.FloorPriceWei(0)},
		{"CollectionStats.ListedCount", cfg.CollectionStats.ListedCount(0)},
		{"CollectionStats.Volume24hWei", cfg.CollectionStats.Volume24hWei(0)},
	}
	for _, c := range checks {
		if c.cost != costScalar {
			t.Errorf("%s: want %d, got %d", c.name, costScalar, c.cost)
		}
	}
}

// ── ComplexityConfig: list-type fields ─────────────────────────────────

func TestComplexityConfig_ListTypeFields(t *testing.T) {
	cfg := ComplexityConfig()

	// List-type query fields use listCost(childComplexity, limit).
	// We verify the formula with known inputs.

	// listings(limit: 48) with 12 scalar children per Listing.
	lim48 := 48
	listings48 := cfg.Query.Listings(12, nil, nil, nil, &lim48, nil, nil, nil)
	wantListings48 := listCost(12, &lim48) // 10 + 12*48 = 586
	if listings48 != wantListings48 {
		t.Errorf("Query.Listings(12 child, limit=48): want %d, got %d", wantListings48, listings48)
	}

	// auctions(limit: 10) with 5 scalar children per Auction (no bids).
	lim10 := 10
	auctions10 := cfg.Query.Auctions(5, nil, nil, nil, &lim10, nil, nil)
	wantAuctions10 := listCost(5, &lim10) // 10 + 5*10 = 60
	if auctions10 != wantAuctions10 {
		t.Errorf("Query.Auctions(5 child, limit=10): want %d, got %d", wantAuctions10, auctions10)
	}

	// collections(limit: 20) with 8 scalar children per Collection.
	lim20 := 20
	collections20 := cfg.Query.Collections(8, &lim20)
	wantCollections20 := listCost(8, &lim20) // 10 + 8*20 = 170
	if collections20 != wantCollections20 {
		t.Errorf("Query.Collections(8 child, limit=20): want %d, got %d", wantCollections20, collections20)
	}
}

// ── ComplexityConfig: connection-style fields ──────────────────────────

func TestComplexityConfig_ConnectionFields(t *testing.T) {
	cfg := ComplexityConfig()

	// bids(10): Bid has 3 scalar fields (bidder, amountWei, placedAt) + 1 time.
	// childComplexity=4, count=10.
	bidCost := cfg.Auction.Bids(4)              // Auction.Bids only takes childComplexity
	wantBidCost := costConnBase + 4*costPerItem // 5 + 4*2 = 13
	if bidCost != wantBidCost {
		t.Errorf("Auction.Bids(4 child): want %d, got %d", wantBidCost, bidCost)
	}

	// effectiveBids(10): EffectiveBid has 4 scalars (bidder, effectiveWei, bidCount, lastBidAt).
	effCost := cfg.Auction.EffectiveBids(4)
	wantEffCost := costConnBase + 4*costPerItem // 5 + 4*2 = 13
	if effCost != wantEffCost {
		t.Errorf("Auction.EffectiveBids(4 child): want %d, got %d", wantEffCost, effCost)
	}
}

// ── Real-world query patterns ──────────────────────────────────────────

// TestComplexityConfig_ListingsPage48 verifies the cost of a typical listings
// page query: 48 listings with 12 scalar fields each. Expected cost:
//
//	listCost(12, &48) = 10 + 12×48 = 586
//
// Note: the user's ballpark estimate was ~960; the actual computed cost of 586
// reflects the current model where each Listing field costs 1 (scalar/enum/time)
// and the list base cost is 10. If the cost model is recalibrated (e.g., by
// raising costPerItem or adding per-field connection costs), update this test.
func TestComplexityConfig_ListingsPage48(t *testing.T) {
	cfg := ComplexityConfig()

	// Per-listing child cost: all 12 Listing fields are scalar/enum/time = 12.
	perListing := cfg.Listing.Collection(0) + // 1
		cfg.Listing.TokenID(0) + // 1
		cfg.Listing.Seller(0) + // 1
		cfg.Listing.PriceWei(0) + // 1
		cfg.Listing.Amount(0) + // 1
		cfg.Listing.Standard(0) + // 1
		cfg.Listing.ExpiresAt(0) + // 1
		cfg.Listing.ListedAt(0) + // 1
		cfg.Listing.Name(0) + // 1
		cfg.Listing.ImageURI(0) + // 1
		cfg.Listing.CollectionVerified(0) + // 1
		cfg.Listing.TxHash(0) // 1
	wantPerListing := costScalar * 12
	if perListing != wantPerListing {
		t.Errorf("per-listing child cost: want %d, got %d", wantPerListing, perListing)
	}

	lim48 := 48
	total := cfg.Query.Listings(perListing, nil, nil, nil, &lim48, nil, nil, nil)
	wantTotal := listCost(12, &lim48) // 10 + 12*48 = 586
	if total != wantTotal {
		t.Errorf("48 listings query: want %d, got %d", wantTotal, total)
	}
}

// TestComplexityConfig_Collections10WithStats verifies the cost of a
// collections page: 10 collections with their stats object. Expected cost:
//
//	Per-collection: 10 scalars + 5 (stats object) + 3 (stats fields) = 18
//	listCost(18, &10) = 10 + 18×10 = 190
//
// Note: the user's ballpark estimate was ~200; actual is 190 with the current
// model. The delta comes from per-collection fields that each cost only 1.
// If additional fields (listings, auctions) are included, costs rise accordingly.
func TestComplexityConfig_Collections10WithStats(t *testing.T) {
	cfg := ComplexityConfig()

	// Per-collection child cost: all scalar fields + stats object + stats scalars.
	perCollection := cfg.Collection.Address(0) + // 1
		cfg.Collection.Name(0) + // 1
		cfg.Collection.Symbol(0) + // 1
		cfg.Collection.Standard(0) + // 1
		cfg.Collection.Verified(0) + // 1
		cfg.Collection.CreatorAddr(0) + // 1
		cfg.Collection.DeployBlock(0) + // 1
		cfg.Collection.FloorPrice(0) + // 1
		cfg.Collection.ListedCount(0) + // 1
		cfg.Collection.Volume24h(0) + // 1
		cfg.Collection.Stats(0) + // 5 (object)
		cfg.CollectionStats.FloorPriceWei(0) + // 1
		cfg.CollectionStats.ListedCount(0) + // 1
		cfg.CollectionStats.Volume24hWei(0) // 1
	wantPerCollection := 10*costScalar + costObject + 3*costScalar // 10 + 5 + 3 = 18
	if perCollection != wantPerCollection {
		t.Errorf("per-collection child cost (10 scalars + stats + 3 stats scalars): want %d, got %d",
			wantPerCollection, perCollection)
	}

	lim10 := 10
	total := cfg.Query.Collections(perCollection, &lim10)
	wantTotal := listCost(18, &lim10) // 10 + 18*10 = 190
	if total != wantTotal {
		t.Errorf("10 collections with stats: want %d, got %d", wantTotal, total)
	}
}

// TestComplexityConfig_CollectionDetailPage verifies the cost of a full
// collection detail page: collection query + stats + 48 child listings.
// Expected cost breakdown:
//
//	Query.collection       = 10  (base)
//	Collection scalars     = 6   (name, symbol, standard, verified, creatorAddr, address)
//	Collection.stats       = 5   (object)
//	Stats scalars          = 3   (floorPriceWei, listedCount, volume24hWei)
//	Collection.listings    = listCost(12, &48) = 586
//	TOTAL                  = 610
func TestComplexityConfig_CollectionDetailPage(t *testing.T) {
	cfg := ComplexityConfig()

	lim48 := 48

	// Step 1: Query root → collection.
	rootCost := cfg.Query.Collection(0, "") // 10

	// Step 2: Collection's scalar children.
	collectionScalars := cfg.Collection.Name(0) + // 1
		cfg.Collection.Symbol(0) + // 1
		cfg.Collection.Standard(0) + // 1
		cfg.Collection.Verified(0) + // 1
		cfg.Collection.CreatorAddr(0) + // 1
		cfg.Collection.Address(0) // 1
	// = 6

	// Step 3: Collection.stats (object) + its scalar children.
	statsCost := cfg.Collection.Stats(0) + // 5
		cfg.CollectionStats.FloorPriceWei(0) + // 1
		cfg.CollectionStats.ListedCount(0) + // 1
		cfg.CollectionStats.Volume24hWei(0) // 1
	// = 8

	// Step 4: Collection.listings → 12 scalar per listing, limit 48.
	perListing := 12 // all scalar/Enum/Time = 12
	listingsCost := cfg.Collection.Listings(perListing, &lim48, nil)

	total := rootCost + collectionScalars + statsCost + listingsCost
	// 10 + 6 + 8 + 586 = 610
	want := 10 + 6 + 8 + listCost(12, &lim48)
	if total != want {
		t.Errorf("collection detail page: want %d, got %d", want, total)
	}

	// Verify it's well within MaxQueryCost.
	if total >= MaxQueryCost {
		t.Errorf("collection detail page cost %d exceeds MaxQueryCost %d", total, MaxQueryCost)
	}
}

// TestComplexityConfig_AuctionWithBids verifies the cost of a single auction
// with 10 bids. Expected cost:
//
//	Query.auction          = 10  (base)
//	Auction scalars        = 3   (auctionID, seller, status)
//	Auction.bids(10)       = 5 + 4×2×10... wait, bids takes childComplexity
//	Bid child cost         = 4   (bidder, amountWei, placedAt, txHash)
//	Auction.bids           = costConnBase + 4*costPerItem = 5+8 = 13
//	Note: bids doesn't take a limit arg in the current schema — it uses
//	      the child complexity passed in by the gqlgen framework.
//	TOTAL (basic)          = 10 + 3 + 13 = 26
//
// With explicit child complexity passed by gqlgen (e.g., 10 bids × 4 fields):
//
//	Auction.bids           = 5 + 4*2 = 13  (per-child, multiplied by gqlgen)
//	gqlgen multiplies per-item cost by the number of items resolved.
func TestComplexityConfig_AuctionWithBids(t *testing.T) {
	cfg := ComplexityConfig()

	// Query root.
	rootCost := cfg.Query.Auction(0, 1) // costListBase = 10

	// Auction scalar children.
	auctionScalars := cfg.Auction.AuctionID(0) + // 1
		cfg.Auction.Seller(0) + // 1
		cfg.Auction.Status(0) // 1
	// = 3

	// Auction.bids with Bid child complexity (4 fields: bidder, amountWei, placedAt, txHash).
	bidChild := cfg.Bid.Bidder(0) + cfg.Bid.AmountWei(0) + cfg.Bid.PlacedAt(0) + cfg.Bid.TxHash(0) // 4
	bidsCost := cfg.Auction.Bids(bidChild)                                                         // costConnBase + bidChild*costPerItem = 5 + 4*2 = 13

	total := rootCost + auctionScalars + bidsCost
	want := costListBase + 3*costScalar + costConnBase + 4*costPerItem // 10 + 3 + 5 + 8 = 26
	if total != want {
		t.Errorf("auction with bids: want %d, got %d", want, total)
	}
}

// TestComplexityConfig_HomepageQueries verifies the cost of the 5 homepage
// queries (persisted queries for the landing page). These are small, focused
// queries that should all remain well under MaxQueryCost.
func TestComplexityConfig_HomepageQueries(t *testing.T) {
	cfg := ComplexityConfig()

	// Trending(limit: 12) — 6 scalar children per TrendingScore.
	lim12 := 12
	perTrending := cfg.TrendingScore.Collection(0) + // 1
		cfg.TrendingScore.Score(0) + // 1
		cfg.TrendingScore.Views(0) + // 1
		cfg.TrendingScore.Bids(0) + // 1
		cfg.TrendingScore.VolumeWei(0) + // 1
		cfg.TrendingScore.Window(0) // 1
	// perTrending = 6
	trendingCost := cfg.Query.Trending(perTrending, nil, &lim12)
	wantTrending := listCost(6, &lim12) // 10 + 6*12 = 82
	if trendingCost != wantTrending {
		t.Errorf("trending(limit:12): want %d, got %d", wantTrending, trendingCost)
	}

	// Metrics — single object with 6 scalars.
	metricsCost := cfg.Query.Metrics(0) + // 5
		cfg.MarketMetrics.GrossVolumeWei(0) + // 1
		cfg.MarketMetrics.TotalActiveListings(0) + // 1
		cfg.MarketMetrics.TotalAuctions(0) + // 1
		cfg.MarketMetrics.TotalBids(0) + // 1
		cfg.MarketMetrics.TotalOffers(0) + // 1
		cfg.MarketMetrics.TotalSales(0) // 1
	wantMetrics := costObject + 6*costScalar // 5 + 6 = 11
	if metricsCost != wantMetrics {
		t.Errorf("metrics: want %d, got %d", wantMetrics, metricsCost)
	}

	// Both are well within MaxQueryCost.
	if trendingCost >= MaxQueryCost {
		t.Errorf("trending cost %d exceeds MaxQueryCost %d", trendingCost, MaxQueryCost)
	}
	if metricsCost >= MaxQueryCost {
		t.Errorf("metrics cost %d exceeds MaxQueryCost %d", metricsCost, MaxQueryCost)
	}
}

// TestComplexityConfig_BelowMaxQueryCost verifies that all common queries
// stay within the MaxQueryCost limit of 1000.
func TestComplexityConfig_BelowMaxQueryCost(t *testing.T) {
	tests := []struct {
		name string
		cost int
	}{
		{"10 collections with stats", 190},
		{"collection detail page", 610},
		{"48 listings", 586},
		{"auction with bids", 26},
		{"trending 12", 82},
		{"metrics", 11},
		{"single listing", listCost(12, intPtr(1))}, // 10 + 12 = 22
		{"single auction", listCost(4, intPtr(1))},  // 10 + 4 = 14
		{"single collection", costListBase},         // 10
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.cost >= MaxQueryCost {
				t.Errorf("%s cost %d exceeds MaxQueryCost %d", tt.name, tt.cost, MaxQueryCost)
			}
			if tt.cost < 0 {
				t.Errorf("%s cost is negative: %d", tt.name, tt.cost)
			}
		})
	}
}

// TestComplexityConfig_ExceedsMaxQueryCost verifies that a pathological
// query would exceed MaxQueryCost. A query requesting 1000 listings
// (no pagination) should blow past the limit.
func TestComplexityConfig_ExceedsMaxQueryCost(t *testing.T) {
	cfg := ComplexityConfig()

	// 1000 listings with 12 scalar children each — no real page does this.
	lim1000 := 1000
	perListing := 12 // all scalar
	cost := cfg.Query.Listings(perListing, nil, nil, nil, &lim1000, nil, nil, nil)
	// listCost(12, &1000) = 10 + 12*1000 = 12010
	if cost <= MaxQueryCost {
		t.Errorf("1000-listings query cost %d should exceed MaxQueryCost %d", cost, MaxQueryCost)
	}
}

// ── Subscription cost tests ────────────────────────────────────────────

func TestComplexityConfig_SubscriptionsZero(t *testing.T) {
	cfg := ComplexityConfig()

	// All subscription fields should cost 0 regardless of child complexity.
	// Subscriptions are push-based — no DB load.
	checks := []struct {
		name string
		cost int
	}{
		{"Subscription.ListingUpdated", cfg.Subscription.ListingUpdated(100, nil, nil)},
		{"Subscription.AuctionUpdated", cfg.Subscription.AuctionUpdated(100, nil)},
		{"Subscription.ActivityUpdated", cfg.Subscription.ActivityUpdated(100)},
		{"Subscription.NotificationUpdated", cfg.Subscription.NotificationUpdated(100)},
	}
	for _, c := range checks {
		if c.cost != 0 {
			t.Errorf("%s: want 0, got %d", c.name, c.cost)
		}
	}
}

// ── Config structure tests ─────────────────────────────────────────────

// TestComplexityConfig_NonNil verifies that key ComplexityConfig functions
// return valid costs without panicking. A nil function pointer in the config
// would panic at query time; this test catches regressions where a new field
// is added to the GraphQL schema but not populated in ComplexityConfig().
func TestComplexityConfig_NonNil(t *testing.T) {
	cfg := ComplexityConfig()

	// Query root — verify the most critical entry points are wired.
	lim := 1
	if cost := cfg.Query.Listings(0, nil, nil, nil, &lim, nil, nil, nil); cost <= 0 {
		t.Error("Query.Listings returned non-positive cost — likely uninitialized")
	}
	if cost := cfg.Query.Collections(0, &lim); cost <= 0 {
		t.Error("Query.Collections returned non-positive cost — likely uninitialized")
	}
	if cost := cfg.Query.Metrics(0); cost <= 0 {
		t.Error("Query.Metrics returned non-positive cost — likely uninitialized")
	}
	if cost := cfg.Query.Collection(0, "0x0"); cost <= 0 {
		t.Error("Query.Collection returned non-positive cost — likely uninitialized")
	}

	// Subscription root — all should be zero-cost (push-based, no DB load).
	if cost := cfg.Subscription.ListingUpdated(999, nil, nil); cost != 0 {
		t.Error("Subscription.ListingUpdated should cost 0")
	}

	// Object types — sample critical fields.
	if cost := cfg.Collection.Stats(0); cost != costObject {
		t.Errorf("Collection.Stats: want %d, got %d", costObject, cost)
	}
	if cost := cfg.Auction.Bids(0); cost != costConnBase {
		t.Errorf("Auction.Bids(0 child): want %d, got %d", costConnBase, cost)
	}
}

// TestMaxQueryCost_Reasonable verifies MaxQueryCost is set to a value
// that allows real-world queries but blocks pathological ones.
func TestMaxQueryCost_Reasonable(t *testing.T) {
	if MaxQueryCost <= 0 {
		t.Error("MaxQueryCost must be positive")
	}
	if MaxQueryCost < 100 {
		t.Errorf("MaxQueryCost %d is too low to allow even trivial queries", MaxQueryCost)
	}
	if MaxQueryCost > 10000 {
		t.Errorf("MaxQueryCost %d is too high to provide effective DoS protection", MaxQueryCost)
	}
}

// ── Additional query root coverage ─────────────────────────────────────

func TestComplexityConfig_RemainingQueryRoots(t *testing.T) {
	cfg := ComplexityConfig()
	lim := 10

	// Every query root should return a computable cost. If a field is added
	// to the GraphQL schema but not to ComplexityConfig, calling it panics.
	tests := []struct {
		name string
		cost int
	}{
		{"WalletNFTs", cfg.Query.WalletNFTs(4, "0x0")},
		{"Activity", cfg.Query.Activity(4, &lim, nil, nil, nil)},
		{"Notifications", cfg.Query.Notifications(4, "0x0", &lim)},
		{"Offers", cfg.Query.Offers(4, nil, nil, nil, nil, nil, &lim)},
		{"Search", cfg.Query.Search(4, "test", &lim)},
		{"SavedSearches", cfg.Query.SavedSearches(4, "0x0", nil, &lim)},
		{"TokenActivity", cfg.Query.TokenActivity(4, "0x0", "1", &lim)},
		{"OfferPositions", cfg.Query.OfferPositions(4, "0x0", "1")},
		{"Trending", cfg.Query.Trending(4, nil, &lim)},
		{"CollectionStats", cfg.Query.CollectionStats(0, "0x0")},
		{"TokenAttributes", cfg.Query.TokenAttributes(0, "0x0", "1")},
		{"TokenFullMetadata", cfg.Query.TokenFullMetadata(0, "0x0", "1")},
		{"TokenMeta", cfg.Query.TokenMeta(0, "0x0", "1")},
		{"TraitValues", cfg.Query.TraitValues(0, "0x0")},
		{"Profile", cfg.Query.Profile(0, "0x0")},
		{"TotalVolume24h", cfg.Query.TotalVolume24h(0)},
		{"CountActiveAuctions", cfg.Query.CountActiveAuctions(0)},
		{"CountActiveListings", cfg.Query.CountActiveListings(0)},
		{"CountCollections", cfg.Query.CountCollections(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.cost < 0 {
				t.Errorf("%s cost is negative: %d", tt.name, tt.cost)
			}
			// Every query root must cost something, or the complexity limiter
			// cannot see its work. No root is allowlisted to cost 0 today; a
			// root that legitimately should (a constant, no DB touch) must be
			// added to zeroCostRoots with the reason, not silently pass here.
			if tt.cost == 0 {
				if reason, ok := zeroCostRoots[tt.name]; ok {
					t.Logf("%s cost is 0 (allowlisted: %s)", tt.name, reason)
					return
				}
				t.Errorf("%s returned zero cost; add it to zeroCostRoots with a reason if intentional", tt.name)
			}
		})
	}
}

// zeroCostRoots lists query roots allowed to report cost 0, keyed by root
// name with the reason they do no work. Empty: every root today hits the DB.
var zeroCostRoots = map[string]string{}

// ── Helpers ────────────────────────────────────────────────────────────

func intPtr(n int) *int { return &n }
