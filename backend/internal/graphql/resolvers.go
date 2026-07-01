package graphql

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// resolver implements the QueryResolver interface.
type resolver struct {
	q     *db.Q
	bcast *sse.Broadcaster
}

// Interface compliance check.
var _ QueryResolver = (*resolver)(nil)

// ── Core marketplace ──────────────────────────────────────────────────────────

func (r *resolver) Collection(ctx context.Context, address string) (*Collection, error) {
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

func (r *resolver) Collections(ctx context.Context, limit *int) ([]*Collection, error) {
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
		c := &Collection{
			Address: rows[i].Address, Name: rows[i].Name, Symbol: rows[i].Symbol,
			Standard: rows[i].Standard, DeployBlock: int(rows[i].DeployBlock),
			Verified: rows[i].Verified,
		}
		out = append(out, c)
	}
	return out, nil
}

func (r *resolver) Listing(ctx context.Context, collection string, tokenID string) (*Listing, error) {
	row, err := r.q.GetListing(ctx, strings.ToLower(collection), tokenID)
	if err != nil {
		return nil, err
	}
	return listingFromRow(row), nil
}

func (r *resolver) Listings(ctx context.Context, collection, seller, sort *string, limit *int, minPrice, maxPrice, traits *string) ([]*Listing, error) {
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

func listingFromRow(row *db.ListingRow) *Listing {
	return &Listing{
		Collection: row.Collection, TokenID: row.TokenID, Seller: row.Seller,
		PriceWei: row.PriceWei, Amount: int(row.Amount), Standard: row.Standard,
		ExpiresAt: row.ExpiresAt, ListedAt: row.ListedAt, TxHash: row.TxHash,
		Name: row.Name, ImageURI: row.ImageURI, CollectionVerified: row.CollectionVerified,
	}
}

func (r *resolver) Auction(ctx context.Context, id int) (*Auction, error) {
	row, err := r.q.GetAuction(ctx, int64(id))
	if err != nil {
		return nil, err
	}
	return auctionFromRow(row), nil
}

func (r *resolver) Auctions(ctx context.Context, collection, seller, status *string, limit *int, minPrice, maxPrice *string) ([]*Auction, error) {
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

func auctionFromRow(row *db.AuctionRow) *Auction {
	return &Auction{
		AuctionID: row.AuctionID, Collection: row.Collection, TokenID: row.TokenID,
		Seller: row.Seller, Standard: row.Standard, ReservePriceWei: row.ReservePriceWei,
		HighestBidWei: row.HighestBidWei, HighestBidder: row.HighestBidder,
		MinIncrementBps: int(row.MinIncrementBps), StartsAt: row.StartsAt, EndsAt: row.EndsAt,
		Status: row.Status, CreateTx: row.CreateTx, Name: row.Name, ImageURI: row.ImageURI,
	}
}

func (r *resolver) Offers(ctx context.Context, collection, tokenID, bidder, owner, status *string, limit *int) ([]*Offer, error) {
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

func offerFromRow(row *db.OfferRow) *Offer {
	return &Offer{
		OfferID: row.OfferID, Bidder: row.Bidder, Collection: row.Collection,
		TokenID: row.TokenID, AmountWei: row.AmountWei, FeeWei: row.FeeWei,
		Units: int(row.Units), Standard: row.Standard, ExpiresAt: row.ExpiresAt,
		Status: row.Status, MakeTx: row.MakeTx, CreatedAt: row.CreatedAt,
	}
}

func (r *resolver) OfferPositions(ctx context.Context, collection string, tokenID string) (*OfferSummary, error) {
	rows, err := r.q.GetActiveOffersForToken(ctx, collection, tokenID, 200)
	if err != nil {
		return nil, err
	}
	total := new(big.Int)
	best := "0"
	positions := make([]Offer, 0, len(rows))
	for i := range rows {
		o := *offerFromRow(&rows[i])
		positions = append(positions, o)
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

// ── Token metadata ────────────────────────────────────────────────────────────

func (r *resolver) TokenMeta(ctx context.Context, collection string, tokenID string) (*TokenMeta, error) {
	name, imageURI, err := r.q.GetTokenMeta(ctx, strings.ToLower(collection), tokenID)
	if err != nil {
		return nil, err
	}
	return &TokenMeta{Name: name, ImageURI: imageURI}, nil
}

func (r *resolver) TokenFullMetadata(ctx context.Context, collection string, tokenID string) (*TokenFullMetadata, error) {
	name, desc, image, anim, metaURI, fetchedAt, err := r.q.GetTokenFullMetadata(ctx, strings.ToLower(collection), tokenID)
	if err != nil {
		return nil, err
	}
	return &TokenFullMetadata{
		Name: name, Description: desc, ImageURI: image, AnimationURI: anim,
		MetadataURI: metaURI, FetchedAt: fetchedAt,
	}, nil
}

func (r *resolver) TokenAttributes(ctx context.Context, collection string, tokenID string) ([]*Trait, error) {
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

func (r *resolver) TokenActivity(ctx context.Context, collection string, tokenID string, limit *int) ([]*TokenActivity, error) {
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

// ── Activity ──────────────────────────────────────────────────────────────────

func (r *resolver) Activity(ctx context.Context, limit *int, address, collection, tokenID *string) ([]*Activity, error) {
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

// ── Profile / Notifications ───────────────────────────────────────────────────

func (r *resolver) Profile(ctx context.Context, address string) (*Profile, error) {
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

func (r *resolver) Notifications(ctx context.Context, address string, limit *int) ([]*Notification, error) {
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
		r := &rows[i]
		out = append(out, &Notification{
			ID: r.ID, Kind: r.Kind, Title: r.Title,
			Body: r.Body, Link: r.Link, Read: r.Read, CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// ── Wallet ────────────────────────────────────────────────────────────────────

func (r *resolver) WalletNFTs(ctx context.Context, owner string) ([]*OwnedNFT, error) {
	rows, err := r.q.WalletNFTs(ctx, strings.ToLower(owner))
	if err != nil {
		return nil, err
	}
	out := make([]*OwnedNFT, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, &OwnedNFT{
			Collection: r.Collection, TokenID: r.TokenID, Units: r.Units,
			Standard: r.Standard, Name: r.Name, ImageURI: r.ImageURI,
		})
	}
	return out, nil
}

// ── Search ────────────────────────────────────────────────────────────────────

func (r *resolver) Search(ctx context.Context, query string, limit *int) ([]*SearchResult, error) {
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
		r := &rows[i]
		out = append(out, &SearchResult{
			Kind: r.Kind, Collection: r.Collection, TokenID: r.TokenID,
			Name: r.Name, ImageURI: r.ImageURI,
		})
	}
	return out, nil
}

// ── Metrics ───────────────────────────────────────────────────────────────────

func (r *resolver) Metrics(ctx context.Context) (*MarketMetrics, error) {
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

func (r *resolver) Trending(ctx context.Context, window *string, limit *int) ([]*TrendingScore, error) {
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
		r := &rows[i]
		volStr := "0"
		if r.VolumeWei != nil {
			volStr = r.VolumeWei.String()
		}
		out = append(out, &TrendingScore{
			Collection: r.Collection, Window: r.Window, Score: r.Score,
			Views: int(r.Views), Bids: int(r.Bids), VolumeWei: volStr,
		})
	}
	return out, nil
}

// ── Saved searches ────────────────────────────────────────────────────────────

func (r *resolver) SavedSearches(ctx context.Context, address string, page *string, limit *int) ([]*SavedSearch, error) {
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
		r := &rows[i]
		out = append(out, &SavedSearch{
			ID: r.ID, UserAddr: r.UserAddr, Name: r.Name,
			Page: r.Page, Params: r.Params, CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// ── Utility ───────────────────────────────────────────────────────────────────

func (r *resolver) TraitValues(ctx context.Context, collection string) (map[string]any, error) {
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

func (r *resolver) CollectionStats(ctx context.Context, collection string) (*CollectionStats, error) {
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

func (r *resolver) CountActiveListings(ctx context.Context) (int, error) {
	n, err := r.q.CountActiveListings(ctx)
	return int(n), err
}

func (r *resolver) CountActiveAuctions(ctx context.Context) (int, error) {
	n, err := r.q.CountActiveAuctions(ctx)
	return int(n), err
}

func (r *resolver) CountCollections(ctx context.Context) (int, error) {
	n, err := r.q.CountCollections(ctx)
	return int(n), err
}

func (r *resolver) TotalVolume24h(ctx context.Context) (string, error) {
	return r.q.TotalVolume24hWei(ctx)
}

// clampLimit returns the bounded limit for a list resolver. When limit is nil
// or non-positive, def is returned. When limit exceeds max, max is returned.
func clampLimit(limit *int, def, max int) int {
	if limit == nil || *limit <= 0 {
		return def
	}
	if *limit > max {
		return max
	}
	return *limit
}

// Ensure fmt is used (referenced by generated code, not by us directly)
var _ = fmt.Sprintf
