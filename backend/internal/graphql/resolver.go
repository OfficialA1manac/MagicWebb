package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/dataloader"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"

	marketplacev1 "github.com/OfficialA1manac/MagicWebb/backend/internal/connectrpc/marketplacev1"
	marketplacev1connect "github.com/OfficialA1manac/MagicWebb/backend/internal/connectrpc/marketplacev1/marketplacev1connect"
)

// Resolver holds shared dependencies injected by NewGraphQLServer.
// When grpc is non-nil, data-fetching resolvers delegate to the typed
// Connect-RPC service instead of querying the DB directly. This decouples
// the presentation layer from storage and enables future schema stitching.
// All resolvers have been migrated to use grpc (Activity, Offers, WalletNFTs,
// Profile, Search, Metrics) with a DB fallback when grpc is nil.
type Resolver struct {
	q     *db.Q
	bcast *sse.Broadcaster
	grpc  marketplacev1connect.MarketplaceServiceClient
}

// NewResolver creates a resolver with DB, broadcaster, and optional gRPC client.
func NewResolver(q *db.Q, bcast *sse.Broadcaster, grpc marketplacev1connect.MarketplaceServiceClient) *Resolver {
	return &Resolver{q: q, bcast: bcast, grpc: grpc}
}

// drainServerStream reads all items from a server stream and returns them as a
// slice. Used by GraphQL resolvers that delegate to Connect-RPC streaming RPCs
// (GRPC-1). The GraphQL protocol requires complete result sets, so we drain the
// stream before returning.
func drainServerStream[T any](stream *connect.ServerStreamForClient[T]) ([]*T, error) {
	defer stream.Close()
	var out []*T
	for stream.Receive() {
		out = append(out, stream.Msg())
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ── Auction sub-resolver ────────────────────────────────────────────────────

type auctionResolver struct{ *Resolver }

// Bids returns all raw bids for an auction, newest first.
// Uses DataLoader to batch multiple auction bid queries into one DB round-trip.
func (r *auctionResolver) Bids(ctx context.Context, obj *Auction) ([]*Bid, error) {
	loaders := dataloader.FromContext(ctx)
	if loaders == nil {
		// Fallback to direct query if no loaders (e.g., tests)
		rows, err := r.q.GetBidsForAuction(ctx, obj.AuctionID)
		if err != nil {
			return nil, err
		}
		return bidsFromRows(rows), nil
	}
	thunk := loaders.AuctionBids.Load(ctx, obj.AuctionID)
	rows, err := thunk()
	if err != nil {
		return nil, err
	}
	return bidsFromRows(rows), nil
}

// EffectiveBids returns per-bidder cumulative totals, highest first.
// Uses DataLoader to batch multiple auction effective-bid queries into one DB round-trip.
func (r *auctionResolver) EffectiveBids(ctx context.Context, obj *Auction) ([]*EffectiveBid, error) {
	loaders := dataloader.FromContext(ctx)
	if loaders == nil {
		// Fallback to direct query if no loaders
		rows, err := r.q.GetEffectiveBids(ctx, obj.AuctionID)
		if err != nil {
			return nil, err
		}
		return effectiveBidsFromRows(rows), nil
	}
	thunk := loaders.AuctionEffectiveBids.Load(ctx, obj.AuctionID)
	rows, err := thunk()
	if err != nil {
		return nil, err
	}
	return effectiveBidsFromRows(rows), nil
}

// ── Collection sub-resolver ─────────────────────────────────────────────────

type collectionResolver struct{ *Resolver }

// getCollectionStats retrieves stats for a single collection via DataLoader
// when available, falling back to a direct query for non-GraphQL callers.
func (r *collectionResolver) getCollectionStats(ctx context.Context, address string) (db.CollectionStats, error) {
	loaders := dataloader.FromContext(ctx)
	if loaders == nil {
		return r.q.GetCollectionStats(ctx, address)
	}
	thunk := loaders.CollectionStats.Load(ctx, address)
	return thunk()
}

// Stats returns aggregated collection stats (floor, volume, listed count).
// Uses DataLoader — multiple collection stats queries are batched into one DB round-trip.
func (r *collectionResolver) Stats(ctx context.Context, obj *Collection) (*CollectionStats, error) {
	s, err := r.getCollectionStats(ctx, obj.Address)
	if err != nil {
		return nil, err
	}
	return &CollectionStats{
		FloorPriceWei: s.FloorPriceWei,
		Volume24hWei:  s.Volume24hWei,
		ListedCount:   int(s.ListedCount),
	}, nil
}

// FloorPrice returns the floor price wei string.
func (r *collectionResolver) FloorPrice(ctx context.Context, obj *Collection) (string, error) {
	s, err := r.getCollectionStats(ctx, obj.Address)
	if err != nil {
		return "", err
	}
	return s.FloorPriceWei, nil
}

// Volume24h returns 24h volume wei string.
func (r *collectionResolver) Volume24h(ctx context.Context, obj *Collection) (string, error) {
	s, err := r.getCollectionStats(ctx, obj.Address)
	if err != nil {
		return "", err
	}
	return s.Volume24hWei, nil
}

// ListedCount returns the count of active listings for the collection.
func (r *collectionResolver) ListedCount(ctx context.Context, obj *Collection) (int, error) {
	s, err := r.getCollectionStats(ctx, obj.Address)
	if err != nil {
		return 0, err
	}
	return int(s.ListedCount), nil
}

// Listings returns active listings for this collection.
func (r *collectionResolver) Listings(ctx context.Context, obj *Collection, limit *int, sort *string) ([]*Listing, error) {
	return r.Query().Listings(ctx, &obj.Address, nil, sort, limit, nil, nil, nil)
}

// Auctions returns auctions for this collection.
func (r *collectionResolver) Auctions(ctx context.Context, obj *Collection, limit *int, status *string) ([]*Auction, error) {
	return r.Query().Auctions(ctx, &obj.Address, nil, status, limit, nil, nil)
}

// ── Query resolver ──────────────────────────────────────────────────────────

type queryResolver struct{ *Resolver }

// grpcOrDefault returns the gRPC client when available; nil means the caller
// falls back to direct DB access (for migrations not yet complete).
func (r *Resolver) grpcOrDefault() marketplacev1connect.MarketplaceServiceClient {
	return r.grpc
}

// Collection fetches a single collection by address.
func (r *queryResolver) Collection(ctx context.Context, address string) (*Collection, error) {
	addr := strings.ToLower(address)

	if grpc := r.grpcOrDefault(); grpc != nil {
		resp, err := grpc.GetCollection(ctx, connect.NewRequest(&marketplacev1.GetCollectionRequest{Address: addr}))
		if err != nil {
			return nil, err
		}
		c := resp.Msg
		return &Collection{
			Address:     c.Address,
			Name:        c.Name,
			Symbol:      c.Symbol,
			Standard:    c.Standard,
			DeployBlock: int(c.DeployBlock),
			Verified:    c.Verified,
		}, nil
	}

	// Fallback to direct DB access.
	row, err := r.q.GetCollection(ctx, addr)
	if err != nil {
		return nil, err
	}
	return &Collection{
		Address:     row.Address,
		Name:        row.Name,
		Symbol:      row.Symbol,
		Standard:    row.Standard,
		DeployBlock: int(row.DeployBlock),
		Verified:    row.Verified,
	}, nil
}

// Collections lists tracked collections.
func (r *queryResolver) Collections(ctx context.Context, limit *int) ([]*Collection, error) {
	l := int32(50)
	if limit != nil && *limit > 0 && *limit <= 200 {
		l = int32(*limit)
	}

	if grpc := r.grpcOrDefault(); grpc != nil {
		stream, err := grpc.ListCollections(ctx, connect.NewRequest(&marketplacev1.ListCollectionsRequest{Limit: l}))
		if err != nil {
			return nil, err
		}
		items, err := drainServerStream(stream)
		if err != nil {
			return nil, err
		}
		out := make([]*Collection, 0, len(items))
		for _, c := range items {
			out = append(out, collectionFromProto(c))
		}
		return out, nil
	}

	// Fallback to direct DB access.
	fallbackLimit := 50
	if limit != nil && *limit > 0 && *limit <= 200 {
		fallbackLimit = *limit
	}
	rows, err := r.q.ListCollections(ctx, fallbackLimit)
	if err != nil {
		return nil, err
	}
	out := make([]*Collection, 0, len(rows))
	for i := range rows {
		out = append(out, &Collection{
			Address: rows[i].Address, Name: rows[i].Name, Symbol: rows[i].Symbol,
			Standard: rows[i].Standard, DeployBlock: int(rows[i].DeployBlock),
			Verified: rows[i].Verified,
		})
	}
	return out, nil
}

// Listing fetches a single listing by collection+tokenID.
func (r *queryResolver) Listing(ctx context.Context, collection string, tokenID string) (*Listing, error) {
	coll := strings.ToLower(collection)

	if grpc := r.grpcOrDefault(); grpc != nil {
		resp, err := grpc.GetListing(ctx, connect.NewRequest(&marketplacev1.GetListingRequest{Collection: coll, TokenId: tokenID}))
		if err != nil {
			return nil, err
		}
		return listingFromGetResponse(resp.Msg), nil
	}

	// Fallback to direct DB access.
	row, err := r.q.GetListing(ctx, coll, tokenID)
	if err != nil {
		return nil, err
	}
	return listingFromRow(row), nil
}

// Listings returns active listings with optional filters.
func (r *queryResolver) Listings(ctx context.Context, collection *string, seller *string, sort *string, limit *int, minPrice *string, maxPrice *string, traits *string) ([]*Listing, error) {
	req := &marketplacev1.ListListingsRequest{}
	if collection != nil {
		req.Collection = strings.ToLower(*collection)
	}
	if seller != nil {
		req.Seller = strings.ToLower(*seller)
	}
	if sort != nil {
		req.Sort = *sort
	} else {
		req.Sort = "recent"
	}
	if minPrice != nil {
		req.MinPriceWei = *minPrice
	}
	if maxPrice != nil {
		req.MaxPriceWei = *maxPrice
	}
	if traits != nil {
		req.Traits = *traits
	}
	if limit != nil && *limit > 0 && *limit <= 100 {
		req.Limit = int32(*limit)
	} else {
		req.Limit = 50
	}

	if grpc := r.grpcOrDefault(); grpc != nil {
		stream, err := grpc.ListListings(ctx, connect.NewRequest(req))
		if err != nil {
			return nil, err
		}
		items, err := drainServerStream(stream)
		if err != nil {
			return nil, err
		}
		out := make([]*Listing, 0, len(items))
		for _, l := range items {
			out = append(out, listingFromProto(l))
		}
		return out, nil
	}

	// Fallback to direct DB access.
	f := db.ListingsFilter{
		Collection:  req.Collection,
		Seller:      req.Seller,
		Sort:        req.Sort,
		MinPriceWei: req.MinPriceWei,
		MaxPriceWei: req.MaxPriceWei,
		Limit:       int(req.Limit),
	}
	if req.Traits != "" {
		f.Traits = make(map[string]string)
		for _, pair := range strings.Split(req.Traits, ",") {
			parts := strings.SplitN(pair, ":", 2)
			if len(parts) == 2 {
				f.Traits[parts[0]] = parts[1]
			}
		}
	}
	rows, err := r.q.ListActiveListings(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]*Listing, 0, len(rows))
	for i := range rows {
		out = append(out, listingFromRow(&rows[i]))
	}
	return out, nil
}

// Auction fetches a single auction by ID.
func (r *queryResolver) Auction(ctx context.Context, id int) (*Auction, error) {
	if grpc := r.grpcOrDefault(); grpc != nil {
		resp, err := grpc.GetAuction(ctx, connect.NewRequest(&marketplacev1.GetAuctionRequest{AuctionId: int64(id)}))
		if err != nil {
			return nil, err
		}
		return auctionFromGetResponse(resp.Msg), nil
	}

	// Fallback to direct DB access.
	row, err := r.q.GetAuction(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	return auctionFromRow(row), nil
}

// Auctions returns auctions with optional filters.
func (r *queryResolver) Auctions(ctx context.Context, collection *string, seller *string, status *string, limit *int, minPrice *string, maxPrice *string) ([]*Auction, error) {
	req := &marketplacev1.ListAuctionsRequest{}
	if collection != nil {
		req.Collection = strings.ToLower(*collection)
	}
	if seller != nil {
		req.Seller = strings.ToLower(*seller)
	}
	if status != nil {
		req.Status = *status
	}
	if minPrice != nil {
		req.MinPriceWei = *minPrice
	}
	if maxPrice != nil {
		req.MaxPriceWei = *maxPrice
	}
	if limit != nil && *limit > 0 && *limit <= 200 {
		req.Limit = int32(*limit)
	} else {
		req.Limit = 50
	}

	if grpc := r.grpcOrDefault(); grpc != nil {
		stream, err := grpc.ListAuctions(ctx, connect.NewRequest(req))
		if err != nil {
			return nil, err
		}
		items, err := drainServerStream(stream)
		if err != nil {
			return nil, err
		}
		out := make([]*Auction, 0, len(items))
		for _, a := range items {
			out = append(out, auctionFromProto(a))
		}
		return out, nil
	}

	// Fallback to direct DB access.
	f := db.AuctionsFilter{
		Collection:  req.Collection,
		Seller:      req.Seller,
		Status:      req.Status,
		MinPriceWei: req.MinPriceWei,
		MaxPriceWei: req.MaxPriceWei,
		Limit:       int(req.Limit),
	}
	rows, err := r.q.ListAuctions(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]*Auction, 0, len(rows))
	for i := range rows {
		out = append(out, auctionFromRow(&rows[i]))
	}
	return out, nil
}

// Offers returns offers with optional filters.
func (r *queryResolver) Offers(ctx context.Context, collection *string, tokenID *string, bidder *string, owner *string, status *string, limit *int) ([]*Offer, error) {
	if grpc := r.grpcOrDefault(); grpc != nil {
		req := &marketplacev1.ListOffersRequest{}
		if collection != nil {
			req.Collection = strings.ToLower(*collection)
		}
		if tokenID != nil {
			req.TokenId = *tokenID
		}
		if bidder != nil {
			req.Bidder = strings.ToLower(*bidder)
		}
		if owner != nil {
			req.Owner = strings.ToLower(*owner)
		}
		if status != nil {
			req.Status = *status
		}
		if limit != nil && *limit > 0 && *limit <= 100 {
			req.Limit = int32(*limit)
		} else {
			req.Limit = 50
		}
		stream, err := grpc.ListOffers(ctx, connect.NewRequest(req))
		if err != nil {
			return nil, err
		}
		items, err := drainServerStream(stream)
		if err != nil {
			return nil, err
		}
		out := make([]*Offer, 0, len(items))
		for _, o := range items {
			out = append(out, offerFromProto(o))
		}
		return out, nil
	}

	// Fallback to direct DB access.
	f := db.OffersFilter{}
	if collection != nil {
		f.Collection = strings.ToLower(*collection)
	}
	if tokenID != nil {
		f.TokenID = *tokenID
	}
	if bidder != nil {
		f.Bidder = strings.ToLower(*bidder)
	}
	if owner != nil {
		f.Owner = strings.ToLower(*owner)
	}
	if status != nil {
		f.Status = *status
	}
	if limit != nil && *limit > 0 {
		f.Limit = *limit
	} else {
		f.Limit = 50
	}
	rows, err := r.q.ListOffers(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]*Offer, 0, len(rows))
	for i := range rows {
		out = append(out, offerFromRow(&rows[i]))
	}
	return out, nil
}

// OfferPositions returns aggregated offer positions for a token.
func (r *queryResolver) OfferPositions(ctx context.Context, collection string, tokenID string) (*OfferSummary, error) {
	rows, err := r.q.GetActiveOffersForToken(ctx, collection, tokenID, 200)
	if err != nil {
		return nil, err
	}
	total := new(big.Int)
	best := "0"
	positions := make([]Offer, 0, len(rows))
	for i := range rows {
		o := offerFromRow(&rows[i])
		positions = append(positions, *o)
		if p, ok := new(big.Int).SetString(o.AmountWei, 10); ok {
			total.Add(total, p)
		}
	}
	if len(positions) > 0 {
		best = positions[0].AmountWei
	}
	return &OfferSummary{
		Collection: collection, TokenID: tokenID, Positions: positions,
		Count: len(positions), Highest: best, TotalWei: total.String(),
		Truncated: len(positions) >= 200,
	}, nil
}

// TokenMeta returns minimal token metadata.
func (r *queryResolver) TokenMeta(ctx context.Context, collection string, tokenID string) (*TokenMeta, error) {
	name, imageURI, err := r.q.GetTokenMeta(ctx, strings.ToLower(collection), tokenID)
	if err != nil {
		return nil, err
	}
	return &TokenMeta{Name: name, ImageURI: imageURI}, nil
}

// TokenFullMetadata returns full token metadata.
func (r *queryResolver) TokenFullMetadata(ctx context.Context, collection string, tokenID string) (*TokenFullMetadata, error) {
	name, desc, image, anim, metaURI, fetchedAt, err := r.q.GetTokenFullMetadata(ctx, strings.ToLower(collection), tokenID)
	if err != nil {
		return nil, err
	}
	return &TokenFullMetadata{
		Name: name, Description: desc, ImageURI: image, AnimationURI: anim,
		MetadataURI: metaURI, FetchedAt: fetchedAt,
	}, nil
}

// TokenAttributes returns token traits.
func (r *queryResolver) TokenAttributes(ctx context.Context, collection string, tokenID string) ([]*Trait, error) {
	traits, err := r.q.GetTokenAttributes(ctx, strings.ToLower(collection), tokenID)
	if err != nil {
		return nil, err
	}
	out := make([]*Trait, 0, len(traits))
	for _, t := range traits {
		out = append(out, &Trait{Type: t.Type, Value: t.Value})
	}
	return out, nil
}

// TokenActivity returns on-chain activity for a token.
func (r *queryResolver) TokenActivity(ctx context.Context, collection string, tokenID string, limit *int) ([]*TokenActivity, error) {
	l := 30
	if limit != nil && *limit > 0 {
		l = *limit
	}
	rows, err := r.q.GetTokenActivity(ctx, strings.ToLower(collection), tokenID, l)
	if err != nil {
		return nil, err
	}
	out := make([]*TokenActivity, 0, len(rows))
	for i := range rows {
		out = append(out, &TokenActivity{
			Type: rows[i].Type, AmountWei: rows[i].AmountWei,
			FromAddr: rows[i].FromAddr, ToAddr: rows[i].ToAddr,
			Timestamp: rows[i].Timestamp, TxHash: rows[i].TxHash,
		})
	}
	return out, nil
}

// Activity returns the marketplace activity feed.
func (r *queryResolver) Activity(ctx context.Context, limit *int, address *string, collection *string, tokenID *string) ([]*Activity, error) {
	l := int32(50)
	if limit != nil && *limit > 0 {
		l = int32(*limit)
	}

	if grpc := r.grpcOrDefault(); grpc != nil {
		req := &marketplacev1.GetActivityRequest{Limit: l}
		if address != nil && *address != "" {
			req.Address = strings.ToLower(*address)
		}
		if collection != nil && *collection != "" {
			req.Collection = strings.ToLower(*collection)
		}
		if tokenID != nil && *tokenID != "" {
			req.TokenId = *tokenID
		}
		resp, err := grpc.GetActivity(ctx, connect.NewRequest(req))
		if err != nil {
			return nil, err
		}
		out := make([]*Activity, 0, len(resp.Msg.Events))
		for _, e := range resp.Msg.Events {
			out = append(out, activityFromProto(e))
		}
		return out, nil
	}

	// Fallback to direct DB access.
	var rows []db.ActivityRow
	var err error

	hasAddr := address != nil && *address != ""
	hasColl := collection != nil && *collection != ""
	hasTok := tokenID != nil && *tokenID != ""

	switch {
	case hasAddr && hasColl && hasTok:
		tokenRows, terr := r.q.GetTokenActivityByAddress(ctx, strings.ToLower(*collection), *tokenID, strings.ToLower(*address), int(l))
		if terr != nil {
			return nil, terr
		}
		rows = tokenActivityToActivity(tokenRows, *collection, *tokenID)
	case hasColl && hasTok:
		tokenRows, terr := r.q.GetTokenActivity(ctx, strings.ToLower(*collection), *tokenID, int(l))
		if terr != nil {
			return nil, terr
		}
		rows = tokenActivityToActivity(tokenRows, *collection, *tokenID)
	case hasAddr:
		rows, err = r.q.GetRecentTransactionsByAddress(ctx, strings.ToLower(*address), int(l))
	default:
		rows, err = r.q.GetRecentTransactions(ctx, int(l))
	}
	if err != nil {
		return nil, err
	}
	out := make([]*Activity, 0, len(rows))
	for i := range rows {
		out = append(out, &Activity{
			Type: rows[i].Type, Collection: rows[i].Collection,
			TokenID: rows[i].TokenID, AmountWei: rows[i].AmountWei,
			Timestamp: rows[i].Timestamp, TxHash: rows[i].TxHash,
		})
	}
	return out, nil
}

// Profile fetches a user profile.
func (r *queryResolver) Profile(ctx context.Context, address string) (*Profile, error) {
	addr := strings.ToLower(address)

	if grpc := r.grpcOrDefault(); grpc != nil {
		resp, err := grpc.GetProfile(ctx, connect.NewRequest(&marketplacev1.GetProfileRequest{Address: addr}))
		if err != nil {
			return nil, err
		}
		return profileFromProto(resp.Msg), nil
	}

	// Fallback to direct DB access.
	row, err := r.q.GetProfile(ctx, addr)
	if err != nil {
		return nil, err
	}
	return &Profile{
		Address: row.Address, DisplayName: row.DisplayName, Bio: row.Bio,
		AvatarURI: row.AvatarURI, BannerURI: row.BannerURI,
		Twitter: row.Twitter, Website: row.Website, Verified: row.Verified,
	}, nil
}

// Notifications returns notifications for a user.
func (r *queryResolver) Notifications(ctx context.Context, address string, limit *int) ([]*Notification, error) {
	l := 50
	if limit != nil && *limit > 0 {
		l = *limit
	}
	rows, err := r.q.ListNotifications(ctx, strings.ToLower(address), l)
	if err != nil {
		return nil, err
	}
	out := make([]*Notification, 0, len(rows))
	for i := range rows {
		n := &rows[i]
		out = append(out, &Notification{
			ID: n.ID, Kind: n.Kind, Title: n.Title,
			Body: n.Body, Link: n.Link, Read: n.Read, CreatedAt: n.CreatedAt,
		})
	}
	return out, nil
}

// WalletNFTs returns NFTs owned by a wallet.
func (r *queryResolver) WalletNFTs(ctx context.Context, owner string) ([]*OwnedNFT, error) {
	addr := strings.ToLower(owner)

	if grpc := r.grpcOrDefault(); grpc != nil {
		resp, err := grpc.GetWalletNFTs(ctx, connect.NewRequest(&marketplacev1.GetWalletNFTsRequest{Owner: addr}))
		if err != nil {
			return nil, err
		}
		out := make([]*OwnedNFT, 0, len(resp.Msg.Nfts))
		for _, n := range resp.Msg.Nfts {
			out = append(out, ownedNFTFromProto(n))
		}
		return out, nil
	}

	// Fallback to direct DB access.
	rows, err := r.q.WalletNFTs(ctx, addr)
	if err != nil {
		return nil, err
	}
	out := make([]*OwnedNFT, 0, len(rows))
	for i := range rows {
		n := &rows[i]
		out = append(out, &OwnedNFT{
			Collection: n.Collection, TokenID: n.TokenID, Units: n.Units,
			Standard: n.Standard, Name: n.Name, ImageURI: n.ImageURI,
		})
	}
	return out, nil
}

// Search performs full-text search across NFTs and collections.
func (r *queryResolver) Search(ctx context.Context, query string, limit *int) ([]*SearchResult, error) {
	if grpc := r.grpcOrDefault(); grpc != nil {
		req := &marketplacev1.SearchRequest{Query: query}
		if limit != nil && *limit > 0 && *limit <= 50 {
			req.Limit = int32(*limit)
		} else {
			req.Limit = 20
		}
		resp, err := grpc.Search(ctx, connect.NewRequest(req))
		if err != nil {
			return nil, err
		}
		out := make([]*SearchResult, 0, len(resp.Msg.Results))
		for _, s := range resp.Msg.Results {
			out = append(out, searchResultFromProto(s))
		}
		return out, nil
	}

	// Fallback to direct DB access.
	l := 20
	if limit != nil && *limit > 0 {
		l = *limit
	}
	rows, err := r.q.Search(ctx, query, l)
	if err != nil {
		return nil, err
	}
	out := make([]*SearchResult, 0, len(rows))
	for i := range rows {
		s := &rows[i]
		out = append(out, &SearchResult{
			Kind: s.Kind, Collection: s.Collection, TokenID: s.TokenID,
			Name: s.Name, ImageURI: s.ImageURI,
		})
	}
	return out, nil
}

// Metrics returns aggregate market metrics.
func (r *queryResolver) Metrics(ctx context.Context) (*MarketMetrics, error) {
	if grpc := r.grpcOrDefault(); grpc != nil {
		resp, err := grpc.GetMetrics(ctx, connect.NewRequest(&marketplacev1.GetMetricsRequest{}))
		if err != nil {
			return nil, err
		}
		return metricsFromProto(resp.Msg), nil
	}

	// Fallback to direct DB access.
	m, err := r.q.GetMarketMetrics(ctx)
	if err != nil {
		return nil, err
	}
	if m == nil {
		m = &db.MarketMetrics{}
	}
	return &MarketMetrics{
		TotalActiveListings: int(m.TotalActiveListings),
		TotalSales:          int(m.TotalSales),
		GrossVolumeWei:      m.GrossVolumeWei,
		TotalAuctions:       int(m.TotalAuctions),
		TotalBids:           int(m.TotalBids),
		TotalOffers:         int(m.TotalOffers),
	}, nil
}

// Trending returns trending collections.
func (r *queryResolver) Trending(ctx context.Context, window *string, limit *int) ([]*TrendingScore, error) {
	w := "24h"
	if window != nil && *window != "" {
		w = *window
	}
	l := 20
	if limit != nil && *limit > 0 {
		l = *limit
	}
	rows, err := r.q.GetTrendingCollections(ctx, w, l)
	if err != nil {
		return nil, err
	}
	out := make([]*TrendingScore, 0, len(rows))
	for i := range rows {
		tr := &rows[i]
		volStr := "0"
		if tr.VolumeWei != nil {
			volStr = tr.VolumeWei.String()
		}
		out = append(out, &TrendingScore{
			Collection: tr.Collection, Window: tr.Window, Score: tr.Score,
			Views: int(tr.Views), Bids: int(tr.Bids), VolumeWei: volStr,
		})
	}
	return out, nil
}

// SavedSearches returns saved searches for a user.
func (r *queryResolver) SavedSearches(ctx context.Context, address string, page *string, limit *int) ([]*SavedSearch, error) {
	l := 50
	if limit != nil && *limit > 0 {
		l = *limit
	}
	p := ""
	if page != nil {
		p = *page
	}
	rows, err := r.q.ListSavedSearches(ctx, strings.ToLower(address), l, p)
	if err != nil {
		return nil, err
	}
	out := make([]*SavedSearch, 0, len(rows))
	for i := range rows {
		s := &rows[i]
		out = append(out, &SavedSearch{
			ID: s.ID, UserAddr: s.UserAddr, Name: s.Name,
			Page: s.Page, Params: s.Params, CreatedAt: s.CreatedAt,
		})
	}
	return out, nil
}

// TraitValues returns trait values for a collection (for filtering).
func (r *queryResolver) TraitValues(ctx context.Context, collection string) (map[string]any, error) {
	m, err := r.q.ListTraitValues(ctx, strings.ToLower(collection))
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out, nil
}

// CollectionStats returns stats for a specific collection.
func (r *queryResolver) CollectionStats(ctx context.Context, collection string) (*CollectionStats, error) {
	s, err := r.q.GetCollectionStats(ctx, strings.ToLower(collection))
	if err != nil {
		return nil, err
	}
	return &CollectionStats{
		FloorPriceWei: s.FloorPriceWei,
		Volume24hWei:  s.Volume24hWei,
		ListedCount:   int(s.ListedCount),
	}, nil
}

// CountActiveListings returns the count of active listings.
func (r *queryResolver) CountActiveListings(ctx context.Context) (int, error) {
	n, err := r.q.CountActiveListings(ctx)
	return int(n), err
}

// CountActiveAuctions returns the count of active auctions.
func (r *queryResolver) CountActiveAuctions(ctx context.Context) (int, error) {
	n, err := r.q.CountActiveAuctions(ctx)
	return int(n), err
}

// CountCollections returns the count of tracked collections.
func (r *queryResolver) CountCollections(ctx context.Context) (int, error) {
	n, err := r.q.CountCollections(ctx)
	return int(n), err
}

// TotalVolume24h returns total 24h volume in wei.
func (r *queryResolver) TotalVolume24h(ctx context.Context) (string, error) {
	return r.q.TotalVolume24hWei(ctx)
}

// ── Subscription resolver ────────────────────────────────────────────────────

type subscriptionResolver struct{ *Resolver }

// ListingUpdated returns a channel that receives listing updates when the
// indexer broadcasts a listing change. The channel is backed by the SSE
// Broadcaster's SubscribeRaw, which fans events to all subscribers.
// Filtering by collection/tokenID is applied server-side for efficiency.
func (r *subscriptionResolver) ListingUpdated(ctx context.Context, collection *string, tokenID *string) (<-chan *Listing, error) {
	if r.bcast == nil {
		return nil, fmt.Errorf("broadcaster not available")
	}
	eventCh, cancel, ok := r.bcast.SubscribeRaw()
	if !ok {
		return nil, fmt.Errorf("too many subscribers")
	}

	ch := make(chan *Listing, 8)
	go func() {
		defer cancel()
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-eventCh:
				if !ok {
					return
				}
				if ev.Type != "listing-updated" {
					continue
				}
				// Marshal event data into a listing for filtering.
				data, err := json.Marshal(ev.Data)
				if err != nil {
					continue
				}
				var row db.ListingRow
				if err := json.Unmarshal(data, &row); err != nil {
					continue
				}
				// Apply collection/tokenID filter.
				if collection != nil && !strings.EqualFold(row.Collection, *collection) {
					continue
				}
				if tokenID != nil && row.TokenID != *tokenID {
					continue
				}
				select {
				case ch <- listingFromRow(&row):
				default:
					// Slow consumer — drop event
				}
			}
		}
	}()

	return ch, nil
}

// AuctionUpdated returns a channel that receives auction updates.
func (r *subscriptionResolver) AuctionUpdated(ctx context.Context, auctionID *int) (<-chan *Auction, error) {
	eventCh, cancel, ok := r.bcast.SubscribeRaw()
	if !ok {
		return nil, fmt.Errorf("too many subscribers")
	}

	ch := make(chan *Auction, 8)
	go func() {
		defer cancel()
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-eventCh:
				if !ok {
					return
				}
				if ev.Type != "auction-updated" {
					continue
				}
				data, err := json.Marshal(ev.Data)
				if err != nil {
					continue
				}
				var row db.AuctionRow
				if err := json.Unmarshal(data, &row); err != nil {
					continue
				}
				if auctionID != nil && row.AuctionID != int64(*auctionID) {
					continue
				}
				select {
				case ch <- auctionFromRow(&row):
				default:
				}
			}
		}
	}()

	return ch, nil
}

// ActivityUpdated returns a channel that receives activity feed updates.
func (r *subscriptionResolver) ActivityUpdated(ctx context.Context) (<-chan *Activity, error) {
	eventCh, cancel, ok := r.bcast.SubscribeRaw()
	if !ok {
		return nil, fmt.Errorf("too many subscribers")
	}

	ch := make(chan *Activity, 8)
	go func() {
		defer cancel()
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-eventCh:
				if !ok {
					return
				}
				if ev.Type != "activity" {
					continue
				}
				data, err := json.Marshal(ev.Data)
				if err != nil {
					continue
				}
				var row db.ActivityRow
				if err := json.Unmarshal(data, &row); err != nil {
					continue
				}
				act := &Activity{
					Type: row.Type, Collection: row.Collection,
					TokenID: row.TokenID, AmountWei: row.AmountWei,
					Timestamp: row.Timestamp, TxHash: row.TxHash,
				}
				select {
				case ch <- act:
				default:
				}
			}
		}
	}()

	return ch, nil
}

// NotificationUpdated returns a channel that receives notifications.
func (r *subscriptionResolver) NotificationUpdated(ctx context.Context) (<-chan *Notification, error) {
	eventCh, cancel, ok := r.bcast.SubscribeRaw()
	if !ok {
		return nil, fmt.Errorf("too many subscribers")
	}

	ch := make(chan *Notification, 8)
	go func() {
		defer cancel()
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-eventCh:
				if !ok {
					return
				}
				if ev.Type != "notification" {
					continue
				}
				data, err := json.Marshal(ev.Data)
				if err != nil {
					continue
				}
				var n db.NotificationRow
				if err := json.Unmarshal(data, &n); err != nil {
					continue
				}
				notif := &Notification{
					ID: n.ID, Kind: n.Kind, Title: n.Title,
					Body: n.Body, Link: n.Link, Read: n.Read, CreatedAt: n.CreatedAt,
				}
				select {
				case ch <- notif:
				default:
				}
			}
		}
	}()

	return ch, nil
}

// ── Proto-to-GraphQL mapping ───────────────────────────────────────────────
// These functions convert from protobuf message types (returned by Connect-RPC)
// to the GraphQL model types defined in models_gen.go. They are the bridge
// between the typed service layer and the presentation layer.
//
// Note: proto messages like GetListingResponse and Listing are structurally
// identical but separate Go types, so each response type needs a dedicated
// mapper.

// listingFromProto maps a proto Listing (returned by ListListings) to a
// GraphQL Listing.
func listingFromProto(l *marketplacev1.Listing) *Listing {
	out := &Listing{
		Collection:         l.Collection,
		TokenID:            l.TokenId,
		Seller:             l.Seller,
		PriceWei:           l.PriceWei,
		Amount:             int(l.Amount),
		Standard:           l.Standard,
		TxHash:             l.TxHash,
		Name:               l.Name,
		ImageURI:           l.ImageUri,
		CollectionVerified: l.CollectionVerified,
	}
	if l.ExpiresAtMs != 0 {
		out.ExpiresAt = time.UnixMilli(l.ExpiresAtMs)
	}
	if l.ListedAtMs != 0 {
		out.ListedAt = time.UnixMilli(l.ListedAtMs)
	}
	return out
}

// listingFromGetResponse maps a proto GetListingResponse (returned by
// GetListing) to a GraphQL Listing.
func listingFromGetResponse(l *marketplacev1.GetListingResponse) *Listing {
	out := &Listing{
		Collection:         l.Collection,
		TokenID:            l.TokenId,
		Seller:             l.Seller,
		PriceWei:           l.PriceWei,
		Amount:             int(l.Amount),
		Standard:           l.Standard,
		TxHash:             l.TxHash,
		Name:               l.Name,
		ImageURI:           l.ImageUri,
		CollectionVerified: l.CollectionVerified,
	}
	if l.ExpiresAtMs != 0 {
		out.ExpiresAt = time.UnixMilli(l.ExpiresAtMs)
	}
	if l.ListedAtMs != 0 {
		out.ListedAt = time.UnixMilli(l.ListedAtMs)
	}
	return out
}

// auctionFromProto maps a proto Auction (returned by ListAuctions) to a
// GraphQL Auction.
func auctionFromProto(a *marketplacev1.Auction) *Auction {
	out := &Auction{
		AuctionID:       a.AuctionId,
		Collection:      a.Collection,
		TokenID:         a.TokenId,
		Seller:          a.Seller,
		Standard:        a.Standard,
		ReservePriceWei: a.ReservePriceWei,
		HighestBidWei:   a.HighestBidWei,
		HighestBidder:   a.HighestBidder,
		MinIncrementBps: int(a.MinIncrementBps),
		Status:          a.Status,
		CreateTx:        a.CreateTx,
		Name:            a.Name,
		ImageURI:        a.ImageUri,
	}
	if a.StartsAtMs != 0 {
		out.StartsAt = time.UnixMilli(a.StartsAtMs)
	}
	if a.EndsAtMs != 0 {
		out.EndsAt = time.UnixMilli(a.EndsAtMs)
	}
	return out
}

// auctionFromGetResponse maps a proto GetAuctionResponse (returned by
// GetAuction) to a GraphQL Auction.
func auctionFromGetResponse(a *marketplacev1.GetAuctionResponse) *Auction {
	out := &Auction{
		AuctionID:       a.AuctionId,
		Collection:      a.Collection,
		TokenID:         a.TokenId,
		Seller:          a.Seller,
		Standard:        a.Standard,
		ReservePriceWei: a.ReservePriceWei,
		HighestBidWei:   a.HighestBidWei,
		HighestBidder:   a.HighestBidder,
		MinIncrementBps: int(a.MinIncrementBps),
		Status:          a.Status,
		CreateTx:        a.CreateTx,
		Name:            a.Name,
		ImageURI:        a.ImageUri,
	}
	if a.StartsAtMs != 0 {
		out.StartsAt = time.UnixMilli(a.StartsAtMs)
	}
	if a.EndsAtMs != 0 {
		out.EndsAt = time.UnixMilli(a.EndsAtMs)
	}
	return out
}

// collectionFromProto maps a proto Collection to a GraphQL Collection.
// Stats (floor, volume, listed count) are resolved via the CollectionResolver
// sub-resolver using DataLoader, which batches multiple collection stats
// queries into one DB round-trip.
//
// GQL-3 note: the proto Collection already includes FloorPriceWei, Volume_24HWei,
// and ListedCount fields populated by server.go. To eliminate the DataLoader
// round-trip, the GraphQL Collection type needs these fields added to models.go
// and schema.graphql, so collectionFromProto can populate them directly.
func collectionFromProto(c *marketplacev1.Collection) *Collection {
	return &Collection{
		Address:     c.Address,
		Name:        c.Name,
		Symbol:      c.Symbol,
		Standard:    c.Standard,
		DeployBlock: int(c.DeployBlock),
		Verified:    c.Verified,
	}
}

// activityFromProto maps a proto ActivityEvent (returned by GetActivity) to
// a GraphQL Activity.
func activityFromProto(e *marketplacev1.ActivityEvent) *Activity {
	return &Activity{
		Type:       e.Type,
		Collection: e.Collection,
		TokenID:    e.TokenId,
		AmountWei:  e.AmountWei,
		Timestamp:  time.UnixMilli(e.TimestampMs),
		TxHash:     e.TxHash,
	}
}

// offerFromProto maps a proto Offer (returned by ListOffers) to a GraphQL
// Offer.
func offerFromProto(o *marketplacev1.Offer) *Offer {
	out := &Offer{
		OfferID:    o.OfferId,
		Bidder:     o.Bidder,
		Collection: o.Collection,
		TokenID:    o.TokenId,
		AmountWei:  o.AmountWei,
		FeeWei:     o.FeeWei,
		Units:      int(o.Units),
		Standard:   o.Standard,
		Status:     o.Status,
		MakeTx:     o.MakeTx,
	}
	if o.ExpiresAtMs != 0 {
		out.ExpiresAt = time.UnixMilli(o.ExpiresAtMs)
	}
	if o.CreatedAtMs != 0 {
		out.CreatedAt = time.UnixMilli(o.CreatedAtMs)
	}
	return out
}

// ownedNFTFromProto maps a proto OwnedNFT (returned by GetWalletNFTs) to a
// GraphQL OwnedNFT.
func ownedNFTFromProto(n *marketplacev1.OwnedNFT) *OwnedNFT {
	return &OwnedNFT{
		Collection: n.Collection,
		TokenID:    n.TokenId,
		Units:      n.Units,
		Standard:   n.Standard,
		Name:       n.Name,
		ImageURI:   n.ImageUri,
	}
}

// profileFromProto maps a proto GetProfileResponse (returned by GetProfile)
// to a GraphQL Profile.
func profileFromProto(p *marketplacev1.GetProfileResponse) *Profile {
	return &Profile{
		Address:     p.Address,
		DisplayName: p.DisplayName,
		Bio:         p.Bio,
		AvatarURI:   p.AvatarUri,
		BannerURI:   p.BannerUri,
		Twitter:     p.Twitter,
		Website:     p.Website,
		Verified:    p.Verified,
	}
}

// searchResultFromProto maps a proto SearchResult (returned by Search) to a
// GraphQL SearchResult.
func searchResultFromProto(s *marketplacev1.SearchResult) *SearchResult {
	return &SearchResult{
		Kind:       s.Kind,
		Collection: s.Collection,
		TokenID:    s.TokenId,
		Name:       s.Name,
		ImageURI:   s.ImageUri,
	}
}

// metricsFromProto maps a proto GetMetricsResponse (returned by GetMetrics)
// to a GraphQL MarketMetrics.
func metricsFromProto(m *marketplacev1.GetMetricsResponse) *MarketMetrics {
	return &MarketMetrics{
		TotalActiveListings: int(m.TotalActiveListings),
		TotalSales:          int(m.TotalSales),
		GrossVolumeWei:      m.GrossVolumeWei,
		TotalAuctions:       int(m.TotalAuctions),
		TotalBids:           int(m.TotalBids),
		TotalOffers:         int(m.TotalOffers),
	}
}

// ── Type-assertion helpers ──────────────────────────────────────────────────

// Auction returns AuctionResolver.
func (r *Resolver) Auction() AuctionResolver { return &auctionResolver{r} }

// Collection returns CollectionResolver.
func (r *Resolver) Collection() CollectionResolver { return &collectionResolver{r} }

// Query returns QueryResolver.
func (r *Resolver) Query() QueryResolver { return &queryResolver{r} }

// Subscription returns SubscriptionResolver.
func (r *Resolver) Subscription() SubscriptionResolver { return &subscriptionResolver{r} }

// ── Row-to-model helpers ────────────────────────────────────────────────────

func listingFromRow(row *db.ListingRow) *Listing {
	return &Listing{
		Collection:         row.Collection,
		TokenID:            row.TokenID,
		Seller:             row.Seller,
		PriceWei:           row.PriceWei,
		Amount:             int(row.Amount),
		Standard:           row.Standard,
		ExpiresAt:          row.ExpiresAt,
		ListedAt:           row.ListedAt,
		TxHash:             row.TxHash,
		Name:               row.Name,
		ImageURI:           row.ImageURI,
		CollectionVerified: row.CollectionVerified,
	}
}

func auctionFromRow(row *db.AuctionRow) *Auction {
	return &Auction{
		AuctionID:       row.AuctionID,
		Collection:      row.Collection,
		TokenID:         row.TokenID,
		Seller:          row.Seller,
		Standard:        row.Standard,
		ReservePriceWei: row.ReservePriceWei,
		HighestBidWei:   row.HighestBidWei,
		HighestBidder:   row.HighestBidder,
		MinIncrementBps: row.MinIncrementBps,
		StartsAt:        row.StartsAt,
		EndsAt:          row.EndsAt,
		Status:          row.Status,
		CreateTx:        row.CreateTx,
		Name:            row.Name,
		ImageURI:        row.ImageURI,
	}
}

func offerFromRow(row *db.OfferRow) *Offer {
	return &Offer{
		OfferID:    row.OfferID,
		Bidder:     row.Bidder,
		Collection: row.Collection,
		TokenID:    row.TokenID,
		AmountWei:  row.AmountWei,
		FeeWei:     row.FeeWei,
		Units:      int(row.Units),
		Standard:   row.Standard,
		ExpiresAt:  row.ExpiresAt,
		Status:     row.Status,
		MakeTx:     row.MakeTx,
		CreatedAt:  row.CreatedAt,
	}
}

func bidsFromRows(rows []db.BidRow) []*Bid {
	out := make([]*Bid, len(rows))
	for i := range rows {
		out[i] = &Bid{
			Bidder:    rows[i].Bidder,
			AmountWei: rows[i].AmountWei,
			TxHash:    rows[i].TxHash,
			PlacedAt:  rows[i].PlacedAt,
		}
	}
	return out
}

func effectiveBidsFromRows(rows []db.EffectiveBidRow) []*EffectiveBid {
	out := make([]*EffectiveBid, len(rows))
	for i := range rows {
		out[i] = &EffectiveBid{
			Bidder:       rows[i].Bidder,
			EffectiveWei: rows[i].EffectiveWei,
			BidCount:     int(rows[i].BidCount),
			LastBidAt:    rows[i].LastBidAt,
		}
	}
	return out
}

func tokenActivityToActivity(rows []db.TokenActivityRow, coll, tokID string) []db.ActivityRow {
	out := make([]db.ActivityRow, len(rows))
	for i, tr := range rows {
		out[i] = db.ActivityRow{
			Type: tr.Type, Collection: coll, TokenID: tokID,
			AmountWei: tr.AmountWei, Timestamp: tr.Timestamp, TxHash: tr.TxHash,
		}
	}
	return out
}

