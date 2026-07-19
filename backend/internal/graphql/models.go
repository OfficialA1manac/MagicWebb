package graphql

import "time"

// ── GraphQL model types ──────────────────────────────────────────────────────
// These types match the GraphQL schema fields exactly (camelCase).
// gqlgen auto-binds GraphQL types to Go types with matching names.
// Explicit mapping in gqlgen.yml overrides where needed.

type Collection struct {
	Address     string          `json:"address"`
	Name        string          `json:"name"`
	Symbol      string          `json:"symbol"`
	Standard    string          `json:"standard"`
	DeployBlock int             `json:"deployBlock"`
	Verified    bool            `json:"verified"`

	// GQL-3: Preloaded stats from the proto Collection message.
	// When set (server.go::ListCollections populates these via
	// GetCollectionStatsBatch), the CollectionResolver sub-resolvers
	// skip DataLoader and return these directly, saving 1 DB round-trip
	// per collection in list responses.
	StatsPreloaded bool   `json:"-"`
	PreloadedFloor string `json:"-"`
	PreloadedVol   string `json:"-"`
	PreloadedCount int    `json:"-"`
}

type CollectionStats struct {
	FloorPriceWei string `json:"floorPriceWei"`
	Volume24hWei  string `json:"volume24hWei"`
	ListedCount   int    `json:"listedCount"`
}

type Listing struct {
	Collection         string    `json:"collection"`
	TokenID            string    `json:"tokenID"`
	Seller             string    `json:"seller"`
	PriceWei           string    `json:"priceWei"`
	Amount             int       `json:"amount"`
	Standard           string    `json:"standard"`
	ExpiresAt          time.Time `json:"expiresAt"`
	ListedAt           time.Time `json:"listedAt"`
	TxHash             string    `json:"txHash"`
	Name               string    `json:"name"`
	ImageURI           string    `json:"imageURI"`
	CollectionVerified bool      `json:"collectionVerified"`
}

type Auction struct {
	AuctionID       int64     `json:"auctionID"`
	Collection      string    `json:"collection"`
	TokenID         string    `json:"tokenID"`
	Seller          string    `json:"seller"`
	Standard        string    `json:"standard"`
	ReservePriceWei string    `json:"reservePriceWei"`
	HighestBidWei   string    `json:"highestBidWei"`
	HighestBidder   string    `json:"highestBidder"`
	MinIncrementBps int       `json:"minIncrementBps"`
	StartsAt        time.Time `json:"startsAt"`
	EndsAt          time.Time `json:"endsAt"`
	Status          string    `json:"status"`
	CreateTx        string    `json:"createTx"`
	Name            string    `json:"name"`
	ImageURI        string    `json:"imageURI"`
}

type Bid struct {
	Bidder    string    `json:"bidder"`
	AmountWei string    `json:"amountWei"`
	TxHash    string    `json:"txHash"`
	PlacedAt  time.Time `json:"placedAt"`
}

type EffectiveBid struct {
	Bidder       string    `json:"bidder"`
	EffectiveWei string    `json:"effectiveWei"`
	BidCount     int       `json:"bidCount"`
	LastBidAt    time.Time `json:"lastBidAt"`
}

type Offer struct {
	OfferID    string    `json:"offerID"`
	Bidder     string    `json:"bidder"`
	Collection string    `json:"collection"`
	TokenID    string    `json:"tokenID"`
	AmountWei  string    `json:"amountWei"`
	FeeWei     string    `json:"feeWei"`
	Units      int       `json:"units"`
	Standard   string    `json:"standard"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Status     string    `json:"status"`
	MakeTx     string    `json:"makeTx"`
	CreatedAt  time.Time `json:"createdAt"`
}

type TokenMeta struct {
	Name     string `json:"name"`
	ImageURI string `json:"imageURI"`
}

type TokenFullMetadata struct {
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	ImageURI     string    `json:"imageURI"`
	AnimationURI string    `json:"animationURI"`
	MetadataURI  string    `json:"metadataURI"`
	FetchedAt    time.Time `json:"fetchedAt"`
}

type Trait struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type Activity struct {
	Type       string    `json:"type"`
	Collection string    `json:"collection"`
	TokenID    string    `json:"tokenID"`
	AmountWei  string    `json:"amountWei"`
	Timestamp  time.Time `json:"timestamp"`
	TxHash     string    `json:"txHash"`
}

type TokenActivity struct {
	Type      string    `json:"type"`
	AmountWei string    `json:"amountWei"`
	FromAddr  string    `json:"fromAddr"`
	ToAddr    string    `json:"toAddr"`
	Timestamp time.Time `json:"timestamp"`
	TxHash    string    `json:"txHash"`
}

type Profile struct {
	Address     string `json:"address"`
	DisplayName string `json:"displayName"`
	Bio         string `json:"bio"`
	AvatarURI   string `json:"avatarURI"`
	BannerURI   string `json:"bannerURI"`
	Twitter     string `json:"twitter"`
	Website     string `json:"website"`
	Verified    bool   `json:"verified"`
}

type Notification struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Link      string    `json:"link"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"createdAt"`
}

type MarketMetrics struct {
	TotalActiveListings int    `json:"totalActiveListings"`
	TotalSales          int    `json:"totalSales"`
	GrossVolumeWei      string `json:"grossVolumeWei"`
	TotalAuctions       int    `json:"totalAuctions"`
	TotalBids           int    `json:"totalBids"`
	TotalOffers         int    `json:"totalOffers"`
}

type TrendingScore struct {
	Collection string  `json:"collection"`
	Window     string  `json:"window"`
	Score      float64 `json:"score"`
	Views      int     `json:"views"`
	Bids       int     `json:"bids"`
	VolumeWei  string  `json:"volumeWei"`
}

type SearchResult struct {
	Kind       string `json:"kind"`
	Collection string `json:"collection"`
	TokenID    string `json:"tokenID"`
	Name       string `json:"name"`
	ImageURI   string `json:"imageURI"`
}

type OwnedNFT struct {
	Collection string `json:"collection"`
	TokenID    string `json:"tokenID"`
	Units      string `json:"units"`
	Standard   string `json:"standard"`
	Name       string `json:"name"`
	ImageURI   string `json:"imageURI"`
}

type SavedSearch struct {
	ID        int64     `json:"id"`
	UserAddr  string    `json:"userAddr"`
	Name      string    `json:"name"`
	Page      string    `json:"page"`
	Params    string    `json:"params"`
	CreatedAt time.Time `json:"createdAt"`
}

type OfferSummary struct {
	Collection string  `json:"collection"`
	TokenID    string  `json:"tokenID"`
	Positions  []Offer `json:"positions"`
	Count      int     `json:"count"`
	Highest    string  `json:"highest"`
	TotalWei   string  `json:"totalWei"`
	Truncated  bool    `json:"truncated"`
}
