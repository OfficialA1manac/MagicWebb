package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/dataloader"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// Resolver holds shared dependencies injected by NewGraphQLServer.
type Resolver struct {
	q     *db.Q
	bcast *sse.Broadcaster
}

// NewResolver creates a resolver with DB and broadcaster access.
func NewResolver(q *db.Q, bcast *sse.Broadcaster) *Resolver {
	return &Resolver{q: q, bcast: bcast}
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

// Collection fetches a single collection by address.
func (r *queryResolver) Collection(ctx context.Context, address string) (*Collection, error) {
	row, err := r.q.GetCollection(ctx, strings.ToLower(address))
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
	l := 50
	if limit != nil && *limit > 0 && *limit <= 200 {
		l = *limit
	}
	rows, err := r.q.ListCollections(ctx, l)
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
	row, err := r.q.GetListing(ctx, strings.ToLower(collection), tokenID)
	if err != nil {
		return nil, err
	}
	return listingFromRow(row), nil
}

// Listings returns active listings with optional filters.
func (r *queryResolver) Listings(ctx context.Context, collection *string, seller *string, sort *string, limit *int, minPrice *string, maxPrice *string, traits *string) ([]*Listing, error) {
	f := db.ListingsFilter{}
	if collection != nil {
		f.Collection = strings.ToLower(*collection)
	}
	if seller != nil {
		f.Seller = strings.ToLower(*seller)
	}
	if sort != nil {
		f.Sort = *sort
	} else {
		f.Sort = "recent"
	}
	if minPrice != nil {
		f.MinPriceWei = *minPrice
	}
	if maxPrice != nil {
		f.MaxPriceWei = *maxPrice
	}
	if traits != nil && *traits != "" {
		f.Traits = make(map[string]string)
		for _, pair := range strings.Split(*traits, ",") {
			parts := strings.SplitN(pair, ":", 2)
			if len(parts) == 2 {
				f.Traits[parts[0]] = parts[1]
			}
		}
	}
	if limit != nil && *limit > 0 {
		f.Limit = *limit
	} else {
		f.Limit = 50
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
	row, err := r.q.GetAuction(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	return auctionFromRow(row), nil
}

// Auctions returns auctions with optional filters.
func (r *queryResolver) Auctions(ctx context.Context, collection *string, seller *string, status *string, limit *int, minPrice *string, maxPrice *string) ([]*Auction, error) {
	f := db.AuctionsFilter{}
	if collection != nil {
		f.Collection = strings.ToLower(*collection)
	}
	if seller != nil {
		f.Seller = strings.ToLower(*seller)
	}
	if status != nil {
		f.Status = *status
	}
	if minPrice != nil {
		f.MinPriceWei = *minPrice
	}
	if maxPrice != nil {
		f.MaxPriceWei = *maxPrice
	}
	if limit != nil && *limit > 0 {
		f.Limit = *limit
	} else {
		f.Limit = 50
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
	l := 50
	if limit != nil && *limit > 0 {
		l = *limit
	}
	var rows []db.ActivityRow
	var err error

	hasAddr := address != nil && *address != ""
	hasColl := collection != nil && *collection != ""
	hasTok := tokenID != nil && *tokenID != ""

	switch {
	case hasAddr && hasColl && hasTok:
		tokenRows, terr := r.q.GetTokenActivityByAddress(ctx, strings.ToLower(*collection), *tokenID, strings.ToLower(*address), l)
		if terr != nil {
			return nil, terr
		}
		rows = tokenActivityToActivity(tokenRows, *collection, *tokenID)
	case hasColl && hasTok:
		tokenRows, terr := r.q.GetTokenActivity(ctx, strings.ToLower(*collection), *tokenID, l)
		if terr != nil {
			return nil, terr
		}
		rows = tokenActivityToActivity(tokenRows, *collection, *tokenID)
	case hasAddr:
		rows, err = r.q.GetRecentTransactionsByAddress(ctx, strings.ToLower(*address), l)
	default:
		rows, err = r.q.GetRecentTransactions(ctx, l)
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
	row, err := r.q.GetProfile(ctx, strings.ToLower(address))
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
	rows, err := r.q.WalletNFTs(ctx, strings.ToLower(owner))
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
	m, err := r.q.GetMarketMetrics(ctx)
	if err != nil {
		return nil, err
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

