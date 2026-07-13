package graphql

// ── GQL-5: Field-level query cost analysis ─────────────────────────────
//
// Replaces the fixed-complexity limit (extension.FixedComplexityLimit(200))
// with per-field cost weights. The cost model prevents expensive nested
// resolvers from causing DoS while allowing reasonable queries to pass:
//
//   Cost model:
//     Scalar fields  (name, symbol, address, etc.)  = 1
//     Enum fields    (standard, status, etc.)        = 1
//     Time fields    (createdAt, expiresAt, etc.)    = 1
//     Object fields  (stats, etc.)                   = 5
//     List fields    (listings, auctions, etc.)      = 10 + child × limit
//     Connection-style (bids, effectiveBids)          = 5 + child × count
//
//   Max cost per query: 1000 (allows ~10 collections with full stats).
//   Max node count: 500 (limit on total resolved nodes).

const (
	// MaxQueryCost is the maximum total cost a single GraphQL query can incur.
	// Set high enough for legitimate listing pages (48 listings × ~20 cost
	// per listing = 960) but low enough to block deeply nested or unbounded
	// queries (collections { listings { seller { profile { ... } } } }).
	MaxQueryCost = 1000

	// MaxNodeCount is the maximum number of resolved nodes per query.
	// Combined with MaxQueryCost, this provides two-dimensional DoS protection.
	MaxNodeCount = 500

	// Field cost weights.
	costScalar    = 1  // name, symbol, address, amountWei, txHash, etc.
	costEnum      = 1  // standard, status, kind, type
	costTime      = 1  // timestamp, createdAt, expiresAt, etc.
	costObject    = 5  // stats, profile — resolves to a sub-object
	costListBase  = 10 // listings, auctions, offers, collections — DB round-trip
	costConnBase  = 5  // bids, effectiveBids — batched via DataLoader
	costPerItem   = 2  // multiplied by limit/count for list-type fields
)

// ComplexityConfig returns the ComplexityRoot populated with per-field
// cost functions. This replaces the FixedComplexityLimit(200) extension
// with field-aware cost calculation (GQL-5).
//
// Usage in handler.go:
//
//	srv.Use(extension.ComplexityLimitFunc(MaxQueryCost, ComplexityConfig()))
//	// Keep node limit as a secondary defense:
//	srv.Use(extension.FixedComplexityLimit(MaxNodeCount))
func ComplexityConfig() ComplexityRoot {
	return ComplexityRoot{
		// ── Scalar/enum/time costs = 1 ──────────────────────────
		Activity: struct {
			AmountWei  func(childComplexity int) int
			Collection func(childComplexity int) int
			Timestamp  func(childComplexity int) int
			TokenID    func(childComplexity int) int
			TxHash     func(childComplexity int) int
			Type       func(childComplexity int) int
		}{
			AmountWei:  func(c int) int { return costScalar },
			Collection: func(c int) int { return costScalar },
			Timestamp:  func(c int) int { return costTime },
			TokenID:    func(c int) int { return costScalar },
			TxHash:     func(c int) int { return costScalar },
			Type:       func(c int) int { return costEnum },
		},

		Auction: struct {
			AuctionID       func(childComplexity int) int
			Bids            func(childComplexity int) int
			Collection      func(childComplexity int) int
			CreateTx        func(childComplexity int) int
			EffectiveBids   func(childComplexity int) int
			EndsAt          func(childComplexity int) int
			HighestBidWei   func(childComplexity int) int
			HighestBidder   func(childComplexity int) int
			ImageURI        func(childComplexity int) int
			MinIncrementBps func(childComplexity int) int
			Name            func(childComplexity int) int
			ReservePriceWei func(childComplexity int) int
			Seller          func(childComplexity int) int
			Standard        func(childComplexity int) int
			StartsAt        func(childComplexity int) int
			Status          func(childComplexity int) int
			TokenID         func(childComplexity int) int
		}{
			AuctionID:       func(c int) int { return costScalar },
			Bids:            func(c int) int { return costConnBase + c*costPerItem }, // DB query per auction
			Collection:      func(c int) int { return costScalar },
			CreateTx:        func(c int) int { return costScalar },
			EffectiveBids:   func(c int) int { return costConnBase + c*costPerItem },
			EndsAt:          func(c int) int { return costTime },
			HighestBidWei:   func(c int) int { return costScalar },
			HighestBidder:   func(c int) int { return costScalar },
			ImageURI:        func(c int) int { return costScalar },
			MinIncrementBps: func(c int) int { return costScalar },
			Name:            func(c int) int { return costScalar },
			ReservePriceWei: func(c int) int { return costScalar },
			Seller:          func(c int) int { return costScalar },
			Standard:        func(c int) int { return costEnum },
			StartsAt:        func(c int) int { return costTime },
			Status:          func(c int) int { return costEnum },
			TokenID:         func(c int) int { return costScalar },
		},

		Bid: struct {
			AmountWei func(childComplexity int) int
			Bidder    func(childComplexity int) int
			PlacedAt  func(childComplexity int) int
			TxHash    func(childComplexity int) int
		}{
			AmountWei: func(c int) int { return costScalar },
			Bidder:    func(c int) int { return costScalar },
			PlacedAt:  func(c int) int { return costTime },
			TxHash:    func(c int) int { return costScalar },
		},

		Collection: struct {
			Address     func(childComplexity int) int
			Auctions    func(childComplexity int, limit *int, status *string) int
			DeployBlock func(childComplexity int) int
			FloorPrice  func(childComplexity int) int
			ListedCount func(childComplexity int) int
			Listings    func(childComplexity int, limit *int, sort *string) int
			Name        func(childComplexity int) int
			Standard    func(childComplexity int) int
			Stats       func(childComplexity int) int
			Symbol      func(childComplexity int) int
			Verified    func(childComplexity int) int
			Volume24h   func(childComplexity int) int
		}{
			Address:     func(c int) int { return costScalar },
			Auctions:    func(c int, limit *int, _ *string) int { return listCost(c, limit) },
			DeployBlock: func(c int) int { return costScalar },
			FloorPrice:  func(c int) int { return costScalar },
			ListedCount: func(c int) int { return costScalar },
			Listings:    func(c int, limit *int, _ *string) int { return listCost(c, limit) },
			Name:        func(c int) int { return costScalar },
			Standard:    func(c int) int { return costEnum },
			Stats:       func(c int) int { return costObject },
			Symbol:      func(c int) int { return costScalar },
			Verified:    func(c int) int { return costScalar },
			Volume24h:   func(c int) int { return costScalar },
		},

		CollectionStats: struct {
			FloorPriceWei func(childComplexity int) int
			ListedCount   func(childComplexity int) int
			Volume24hWei  func(childComplexity int) int
		}{
			FloorPriceWei: func(c int) int { return costScalar },
			ListedCount:   func(c int) int { return costScalar },
			Volume24hWei:  func(c int) int { return costScalar },
		},

		EffectiveBid: struct {
			BidCount     func(childComplexity int) int
			Bidder       func(childComplexity int) int
			EffectiveWei func(childComplexity int) int
			LastBidAt    func(childComplexity int) int
		}{
			BidCount:     func(c int) int { return costScalar },
			Bidder:       func(c int) int { return costScalar },
			EffectiveWei: func(c int) int { return costScalar },
			LastBidAt:    func(c int) int { return costTime },
		},

		Listing: struct {
			Amount             func(childComplexity int) int
			Collection         func(childComplexity int) int
			CollectionVerified func(childComplexity int) int
			ExpiresAt          func(childComplexity int) int
			ImageURI           func(childComplexity int) int
			ListedAt           func(childComplexity int) int
			Name               func(childComplexity int) int
			PriceWei           func(childComplexity int) int
			Seller             func(childComplexity int) int
			Standard           func(childComplexity int) int
			TokenID            func(childComplexity int) int
			TxHash             func(childComplexity int) int
		}{
			Amount:             func(c int) int { return costScalar },
			Collection:         func(c int) int { return costScalar },
			CollectionVerified: func(c int) int { return costScalar },
			ExpiresAt:          func(c int) int { return costTime },
			ImageURI:           func(c int) int { return costScalar },
			ListedAt:           func(c int) int { return costTime },
			Name:               func(c int) int { return costScalar },
			PriceWei:           func(c int) int { return costScalar },
			Seller:             func(c int) int { return costScalar },
			Standard:           func(c int) int { return costEnum },
			TokenID:            func(c int) int { return costScalar },
			TxHash:             func(c int) int { return costScalar },
		},

		MarketMetrics: struct {
			GrossVolumeWei      func(childComplexity int) int
			TotalActiveListings func(childComplexity int) int
			TotalAuctions       func(childComplexity int) int
			TotalBids           func(childComplexity int) int
			TotalOffers         func(childComplexity int) int
			TotalSales          func(childComplexity int) int
		}{
			GrossVolumeWei:      func(c int) int { return costScalar },
			TotalActiveListings: func(c int) int { return costScalar },
			TotalAuctions:       func(c int) int { return costScalar },
			TotalBids:           func(c int) int { return costScalar },
			TotalOffers:         func(c int) int { return costScalar },
			TotalSales:          func(c int) int { return costScalar },
		},

		Notification: struct {
			Body      func(childComplexity int) int
			CreatedAt func(childComplexity int) int
			ID        func(childComplexity int) int
			Kind      func(childComplexity int) int
			Link      func(childComplexity int) int
			Read      func(childComplexity int) int
			Title     func(childComplexity int) int
		}{
			Body:      func(c int) int { return costScalar },
			CreatedAt: func(c int) int { return costTime },
			ID:        func(c int) int { return costScalar },
			Kind:      func(c int) int { return costEnum },
			Link:      func(c int) int { return costScalar },
			Read:      func(c int) int { return costScalar },
			Title:     func(c int) int { return costScalar },
		},

		Offer: struct {
			AmountWei  func(childComplexity int) int
			Bidder     func(childComplexity int) int
			Collection func(childComplexity int) int
			CreatedAt  func(childComplexity int) int
			ExpiresAt  func(childComplexity int) int
			FeeWei     func(childComplexity int) int
			MakeTx     func(childComplexity int) int
			OfferID    func(childComplexity int) int
			Standard   func(childComplexity int) int
			Status     func(childComplexity int) int
			TokenID    func(childComplexity int) int
			Units      func(childComplexity int) int
		}{
			AmountWei:  func(c int) int { return costScalar },
			Bidder:     func(c int) int { return costScalar },
			Collection: func(c int) int { return costScalar },
			CreatedAt:  func(c int) int { return costTime },
			ExpiresAt:  func(c int) int { return costTime },
			FeeWei:     func(c int) int { return costScalar },
			MakeTx:     func(c int) int { return costScalar },
			OfferID:    func(c int) int { return costScalar },
			Standard:   func(c int) int { return costEnum },
			Status:     func(c int) int { return costEnum },
			TokenID:    func(c int) int { return costScalar },
			Units:      func(c int) int { return costScalar },
		},

		OfferSummary: struct {
			Collection func(childComplexity int) int
			Count      func(childComplexity int) int
			Highest    func(childComplexity int) int
			Positions  func(childComplexity int) int
			TokenID    func(childComplexity int) int
			TotalWei   func(childComplexity int) int
			Truncated  func(childComplexity int) int
		}{
			Collection: func(c int) int { return costScalar },
			Count:      func(c int) int { return costScalar },
			Highest:    func(c int) int { return costScalar },
			Positions:  func(c int) int { return costConnBase + c*costPerItem },
			TokenID:    func(c int) int { return costScalar },
			TotalWei:   func(c int) int { return costScalar },
			Truncated:  func(c int) int { return costScalar },
		},

		OwnedNFT: struct {
			Collection func(childComplexity int) int
			ImageURI   func(childComplexity int) int
			Name       func(childComplexity int) int
			Standard   func(childComplexity int) int
			TokenID    func(childComplexity int) int
			Units      func(childComplexity int) int
		}{
			Collection: func(c int) int { return costScalar },
			ImageURI:   func(c int) int { return costScalar },
			Name:       func(c int) int { return costScalar },
			Standard:   func(c int) int { return costEnum },
			TokenID:    func(c int) int { return costScalar },
			Units:      func(c int) int { return costScalar },
		},

		Profile: struct {
			Address     func(childComplexity int) int
			AvatarURI   func(childComplexity int) int
			BannerURI   func(childComplexity int) int
			Bio         func(childComplexity int) int
			DisplayName func(childComplexity int) int
			Twitter     func(childComplexity int) int
			Verified    func(childComplexity int) int
			Website     func(childComplexity int) int
		}{
			Address:     func(c int) int { return costScalar },
			AvatarURI:   func(c int) int { return costScalar },
			BannerURI:   func(c int) int { return costScalar },
			Bio:         func(c int) int { return costScalar },
			DisplayName: func(c int) int { return costScalar },
			Twitter:     func(c int) int { return costScalar },
			Verified:    func(c int) int { return costScalar },
			Website:     func(c int) int { return costScalar },
		},

		SavedSearch: struct {
			CreatedAt func(childComplexity int) int
			ID        func(childComplexity int) int
			Name      func(childComplexity int) int
			Page      func(childComplexity int) int
			Params    func(childComplexity int) int
			UserAddr  func(childComplexity int) int
		}{
			CreatedAt: func(c int) int { return costTime },
			ID:        func(c int) int { return costScalar },
			Name:      func(c int) int { return costScalar },
			Page:      func(c int) int { return costScalar },
			Params:    func(c int) int { return costScalar },
			UserAddr:  func(c int) int { return costScalar },
		},

		SearchResult: struct {
			Collection func(childComplexity int) int
			ImageURI   func(childComplexity int) int
			Kind       func(childComplexity int) int
			Name       func(childComplexity int) int
			TokenID    func(childComplexity int) int
		}{
			Collection: func(c int) int { return costScalar },
			ImageURI:   func(c int) int { return costScalar },
			Kind:       func(c int) int { return costEnum },
			Name:       func(c int) int { return costScalar },
			TokenID:    func(c int) int { return costScalar },
		},

		TokenActivity: struct {
			AmountWei func(childComplexity int) int
			FromAddr  func(childComplexity int) int
			Timestamp func(childComplexity int) int
			ToAddr    func(childComplexity int) int
			TxHash    func(childComplexity int) int
			Type      func(childComplexity int) int
		}{
			AmountWei: func(c int) int { return costScalar },
			FromAddr:  func(c int) int { return costScalar },
			Timestamp: func(c int) int { return costTime },
			ToAddr:    func(c int) int { return costScalar },
			TxHash:    func(c int) int { return costScalar },
			Type:      func(c int) int { return costEnum },
		},

		TokenFullMetadata: struct {
			AnimationURI func(childComplexity int) int
			Description  func(childComplexity int) int
			FetchedAt    func(childComplexity int) int
			ImageURI     func(childComplexity int) int
			MetadataURI  func(childComplexity int) int
			Name         func(childComplexity int) int
		}{
			AnimationURI: func(c int) int { return costScalar },
			Description:  func(c int) int { return costScalar },
			FetchedAt:    func(c int) int { return costTime },
			ImageURI:     func(c int) int { return costScalar },
			MetadataURI:  func(c int) int { return costScalar },
			Name:         func(c int) int { return costScalar },
		},

		TokenMeta: struct {
			ImageURI func(childComplexity int) int
			Name     func(childComplexity int) int
		}{
			ImageURI: func(c int) int { return costScalar },
			Name:     func(c int) int { return costScalar },
		},

		Trait: struct {
			Type  func(childComplexity int) int
			Value func(childComplexity int) int
		}{
			Type:  func(c int) int { return costScalar },
			Value: func(c int) int { return costScalar },
		},

		TrendingScore: struct {
			Bids       func(childComplexity int) int
			Collection func(childComplexity int) int
			Score      func(childComplexity int) int
			Views      func(childComplexity int) int
			VolumeWei  func(childComplexity int) int
			Window     func(childComplexity int) int
		}{
			Bids:       func(c int) int { return costScalar },
			Collection: func(c int) int { return costScalar },
			Score:      func(c int) int { return costScalar },
			Views:      func(c int) int { return costScalar },
			VolumeWei:  func(c int) int { return costScalar },
			Window:     func(c int) int { return costEnum },
		},

		// ── Query root costs ──────────────────────────────────────────
		Query: struct {
			Activity            func(childComplexity int, limit *int, address *string, collection *string, tokenID *string) int
			Auction             func(childComplexity int, id int) int
			Auctions            func(childComplexity int, collection *string, seller *string, status *string, limit *int, minPrice *string, maxPrice *string) int
			Collection          func(childComplexity int, address string) int
			CollectionStats     func(childComplexity int, collection string) int
			Collections         func(childComplexity int, limit *int) int
			CountActiveAuctions func(childComplexity int) int
			CountActiveListings func(childComplexity int) int
			CountCollections    func(childComplexity int) int
			Listing             func(childComplexity int, collection string, tokenID string) int
			Listings            func(childComplexity int, collection *string, seller *string, sort *string, limit *int, minPrice *string, maxPrice *string, traits *string) int
			Metrics             func(childComplexity int) int
			Notifications       func(childComplexity int, address string, limit *int) int
			OfferPositions      func(childComplexity int, collection string, tokenID string) int
			Offers              func(childComplexity int, collection *string, tokenID *string, bidder *string, owner *string, status *string, limit *int) int
			Profile             func(childComplexity int, address string) int
			SavedSearches       func(childComplexity int, address string, page *string, limit *int) int
			Search              func(childComplexity int, query string, limit *int) int
			TokenActivity       func(childComplexity int, collection string, tokenID string, limit *int) int
			TokenAttributes     func(childComplexity int, collection string, tokenID string) int
			TokenFullMetadata   func(childComplexity int, collection string, tokenID string) int
			TokenMeta           func(childComplexity int, collection string, tokenID string) int
			TotalVolume24h      func(childComplexity int) int
			TraitValues         func(childComplexity int, collection string) int
			Trending            func(childComplexity int, window *string, limit *int) int
			WalletNFTs          func(childComplexity int, owner string) int
		}{
			Activity:            func(c int, limit *int, _, _, _ *string) int { return listCost(c, limit) },
			Auction:             func(c int, _ int) int { return costListBase },
			Auctions:            func(c int, _, _, _ *string, limit *int, _, _ *string) int { return listCost(c, limit) },
			Collection:          func(c int, _ string) int { return costListBase },
			CollectionStats:     func(c int, _ string) int { return costObject },
			Collections:         func(c int, limit *int) int { return listCost(c, limit) },
			CountActiveAuctions: func(c int) int { return costScalar },
			CountActiveListings: func(c int) int { return costScalar },
			CountCollections:    func(c int) int { return costScalar },
			Listing:             func(c int, _, _ string) int { return costListBase },
			Listings:            func(c int, _, _, _ *string, limit *int, _, _, _ *string) int { return listCost(c, limit) },
			Metrics:             func(c int) int { return costObject },
			Notifications:       func(c int, _ string, limit *int) int { return listCost(c, limit) },
			OfferPositions:      func(c int, _, _ string) int { return costConnBase + c*costPerItem },
			Offers:              func(c int, _, _, _, _, _ *string, limit *int) int { return listCost(c, limit) },
			Profile:             func(c int, _ string) int { return costObject },
			SavedSearches:       func(c int, _ string, _ *string, limit *int) int { return listCost(c, limit) },
			Search:              func(c int, _ string, limit *int) int { return listCost(c, limit) },
			TokenActivity:       func(c int, _, _ string, limit *int) int { return listCost(c, limit) },
			TokenAttributes:     func(c int, _, _ string) int { return costObject },
			TokenFullMetadata:   func(c int, _, _ string) int { return costObject },
			TokenMeta:           func(c int, _, _ string) int { return costScalar },
			TotalVolume24h:      func(c int) int { return costScalar },
			TraitValues:         func(c int, _ string) int { return costObject },
			Trending:            func(c int, _ *string, limit *int) int { return listCost(c, limit) },
			WalletNFTs:          func(c int, _ string) int { return listCost(c, nil) },
		},

		// ── Subscription costs = 0 (no DB load — push-based) ──────────
		Subscription: struct {
			ListingUpdated      func(childComplexity int, collection *string, tokenID *string) int
			AuctionUpdated      func(childComplexity int, auctionID *int) int
			ActivityUpdated     func(childComplexity int) int
			NotificationUpdated func(childComplexity int) int
		}{
			ListingUpdated:      func(_ int, _, _ *string) int { return 0 },
			AuctionUpdated:      func(_ int, _ *int) int { return 0 },
			ActivityUpdated:     func(_ int) int { return 0 },
			NotificationUpdated: func(_ int) int { return 0 },
		},
	}
}

// listCost computes the cost of a list-type field. The formula accounts
// for the base cost of the DB query plus the child complexity multiplied
// by the number of items requested (defaulting to 50 when no limit specified).
func listCost(childComplexity int, limit *int) int {
	n := 50 // default limit
	if limit != nil && *limit > 0 {
		n = *limit
	}
	return costListBase + childComplexity*n
}
