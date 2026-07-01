package graphql

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	gqlparser "github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// Schema is the parsed GraphQL schema, loaded once at startup.
var Schema *ast.Schema

//go:embed schema.graphql
var schemaSource string

func init() {
	var err error
	Schema, err = gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: schemaSource})
	if err != nil {
		panic(fmt.Sprintf("graphql: schema parse error: %v", err))
	}
}

// QueryResolver must implement all query fields defined in the GraphQL schema.
type QueryResolver interface {
	Collection(ctx context.Context, address string) (*Collection, error)
	Collections(ctx context.Context, limit *int) ([]*Collection, error)
	Listing(ctx context.Context, collection string, tokenID string) (*Listing, error)
	Listings(ctx context.Context, collection, seller, sort *string, limit *int, minPrice, maxPrice, traits *string) ([]*Listing, error)
	Auction(ctx context.Context, id int) (*Auction, error)
	Auctions(ctx context.Context, collection, seller, status *string, limit *int, minPrice, maxPrice *string) ([]*Auction, error)
	Offers(ctx context.Context, collection, tokenID, bidder, owner, status *string, limit *int) ([]*Offer, error)
	OfferPositions(ctx context.Context, collection string, tokenID string) (*OfferSummary, error)
	TokenMeta(ctx context.Context, collection string, tokenID string) (*TokenMeta, error)
	TokenFullMetadata(ctx context.Context, collection string, tokenID string) (*TokenFullMetadata, error)
	TokenAttributes(ctx context.Context, collection string, tokenID string) ([]*Trait, error)
	TokenActivity(ctx context.Context, collection string, tokenID string, limit *int) ([]*TokenActivity, error)
	Activity(ctx context.Context, limit *int, address, collection, tokenID *string) ([]*Activity, error)
	Profile(ctx context.Context, address string) (*Profile, error)
	Notifications(ctx context.Context, address string, limit *int) ([]*Notification, error)
	WalletNFTs(ctx context.Context, owner string) ([]*OwnedNFT, error)
	Search(ctx context.Context, query string, limit *int) ([]*SearchResult, error)
	Metrics(ctx context.Context) (*MarketMetrics, error)
	Trending(ctx context.Context, window *string, limit *int) ([]*TrendingScore, error)
	SavedSearches(ctx context.Context, address string, page *string, limit *int) ([]*SavedSearch, error)
	TraitValues(ctx context.Context, collection string) (map[string]any, error)
	CollectionStats(ctx context.Context, collection string) (*CollectionStats, error)
	CountActiveListings(ctx context.Context) (int, error)
	CountActiveAuctions(ctx context.Context) (int, error)
	CountCollections(ctx context.Context) (int, error)
	TotalVolume24h(ctx context.Context) (string, error)
}

// Execute parses and executes a GraphQL query against the provided resolver.
func Execute(ctx context.Context, r QueryResolver, queryStr string, variables map[string]any) ([]byte, error) {
	doc, qErr := gqlparser.LoadQuery(Schema, queryStr)
	if qErr != nil {
		return errorJSON(qErr.Error()), fmt.Errorf("parse error: %w", qErr)
	}
	if len(doc.Operations) == 0 {
		return errorJSON("no operations"), fmt.Errorf("no operations")
	}
	op := doc.Operations[0]
	if op.Operation != ast.Query {
		return errorJSON("only queries are supported"), fmt.Errorf("unsupported operation: %s", op.Operation)
	}

	vars := buildVars(op, variables)
	data, errs := walkSelectionSet(ctx, r, op.SelectionSet, vars, nil)
	if len(errs) > 0 {
		gqlErrs := make([]map[string]any, len(errs))
		for i, e := range errs {
			gqlErrs[i] = map[string]any{"message": e.Error()}
		}
		resp := map[string]any{"data": data, "errors": gqlErrs}
		b, _ := json.Marshal(resp)
		return b, fmt.Errorf("execution errors: %s", errorsJoin(errs))
	}
	resp := map[string]any{"data": data}
	b, _ := json.Marshal(resp)
	return b, nil
}

func errorJSON(msg string) []byte {
	b, _ := json.Marshal(map[string]any{"errors": []map[string]any{{"message": msg}}})
	return b
}

func buildVars(op *ast.OperationDefinition, variables map[string]any) map[string]any {
	out := make(map[string]any)
	for k, v := range variables {
		out[k] = v
	}
	if op.VariableDefinitions != nil {
		for _, vdef := range op.VariableDefinitions {
			if _, ok := out[vdef.Variable]; !ok && vdef.DefaultValue != nil {
				out[vdef.Variable] = resolveValueAST(vdef.DefaultValue, nil)
			}
		}
	}
	return out
}

func walkSelectionSet(ctx context.Context, r QueryResolver, selSet ast.SelectionSet, vars map[string]any, parent any) (json.RawMessage, []error) {
	result := make(map[string]any)
	var errs []error

	for _, sel := range selSet {
		switch s := sel.(type) {
		case *ast.Field:
			fieldName := s.Name
			if s.Alias != "" {
				fieldName = s.Alias
			}
			args := resolveArgs(s.Arguments, vars)

			val, err := resolveField(ctx, r, s.Name, args, parent)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", s.Name, err))
				result[fieldName] = nil
				continue
			}

			if len(s.SelectionSet) > 0 {
				// When val is a slice, iterate each element and merge sub-data into an array.
				if slice, ok := reflectSlice(val); ok {
					arr := make([]json.RawMessage, 0, len(slice))
					for _, item := range slice {
						subData, subErrs := walkSelectionSet(ctx, r, s.SelectionSet, vars, item)
						if len(subErrs) > 0 {
							errs = append(errs, subErrs...)
						}
						arr = append(arr, json.RawMessage(subData))
					}
					result[fieldName] = arr
				} else {
					subData, subErrs := walkSelectionSet(ctx, r, s.SelectionSet, vars, val)
					if len(subErrs) > 0 {
						errs = append(errs, subErrs...)
					}
					result[fieldName] = json.RawMessage(subData)
				}
			} else {
				result[fieldName] = val
			}

		case *ast.InlineFragment:
			// Inline fragments — merge fields from the parent context
			subData, subErrs := walkSelectionSet(ctx, r, s.SelectionSet, vars, parent)
			if len(subErrs) > 0 {
				errs = append(errs, subErrs...)
			}
			// Merge subData into result
			if len(subData) > 2 { // at least "{}\n"
				for mk, mv := range rawMessageToMap(subData) {
					result[mk] = mv
				}
			}

		case *ast.FragmentSpread:
			// Skip named fragments
		}
	}

	b, _ := json.Marshal(result)
	return b, errs
}

func resolveArgs(args ast.ArgumentList, vars map[string]any) map[string]any {
	out := make(map[string]any)
	for _, arg := range args {
		out[arg.Name] = resolveValueAST(arg.Value, vars)
	}
	return out
}

func resolveValueAST(val *ast.Value, vars map[string]any) any {
	if val == nil {
		return nil
	}
	switch val.Kind {
	case ast.Variable:
		if v, ok := vars[val.Raw]; ok {
			return v
		}
		return nil
	case ast.IntValue:
		n, err := strconv.ParseInt(val.Raw, 10, 64)
		if err != nil {
			return val.Raw
		}
		return n
	case ast.FloatValue:
		f, err := strconv.ParseFloat(val.Raw, 64)
		if err != nil {
			return val.Raw
		}
		return f
	case ast.StringValue:
		return val.Raw
	case ast.BooleanValue:
		return val.Raw == "true"
	case ast.NullValue:
		return nil
	case ast.ListValue:
		out := make([]any, len(val.Children))
		for i, child := range val.Children {
			out[i] = resolveValueAST(child.Value, vars)
		}
		return out
	case ast.ObjectValue:
		out := make(map[string]any)
		for _, child := range val.Children {
			out[child.Name] = resolveValueAST(child.Value, vars)
		}
		return out
	case ast.EnumValue:
		return val.Raw
	default:
		return val.Raw
	}
}

func resolveField(ctx context.Context, r QueryResolver, name string, args map[string]any, parent any) (any, error) {
	// If we have a parent and it's one of our domain types, resolve sub-field
	if parent != nil {
		if val, ok := resolveSubField(parent, name, args, ctx, r); ok {
			return val, nil
		}
		data, ok := mapField(parent, name)
		if ok {
			return data, nil
		}
		return nil, fmt.Errorf("unknown field %s on parent type %T", name, parent)
	}

	// Top-level query fields
	return resolveTopLevel(ctx, r, name, args)
}

func resolveTopLevel(ctx context.Context, r QueryResolver, name string, args map[string]any) (any, error) {
	switch name {
	case "collection":
		return r.Collection(ctx, argStr(args, "address"))
	case "collections":
		return r.Collections(ctx, argIntPtr(args, "limit"))
	case "listing":
		return r.Listing(ctx, argStr(args, "collection"), argStr(args, "tokenID"))
	case "listings":
		return r.Listings(ctx, argStrPtr(args, "collection"), argStrPtr(args, "seller"), argStrPtr(args, "sort"), argIntPtr(args, "limit"), argStrPtr(args, "minPrice"), argStrPtr(args, "maxPrice"), argStrPtr(args, "traits"))
	case "auction":
		return r.Auction(ctx, int(argInt(args, "id")))
	case "auctions":
		return r.Auctions(ctx, argStrPtr(args, "collection"), argStrPtr(args, "seller"), argStrPtr(args, "status"), argIntPtr(args, "limit"), argStrPtr(args, "minPrice"), argStrPtr(args, "maxPrice"))
	case "offers":
		return r.Offers(ctx, argStrPtr(args, "collection"), argStrPtr(args, "tokenID"), argStrPtr(args, "bidder"), argStrPtr(args, "owner"), argStrPtr(args, "status"), argIntPtr(args, "limit"))
	case "offerPositions":
		return r.OfferPositions(ctx, argStr(args, "collection"), argStr(args, "tokenID"))
	case "tokenMeta":
		return r.TokenMeta(ctx, argStr(args, "collection"), argStr(args, "tokenID"))
	case "tokenFullMetadata":
		return r.TokenFullMetadata(ctx, argStr(args, "collection"), argStr(args, "tokenID"))
	case "tokenAttributes":
		return r.TokenAttributes(ctx, argStr(args, "collection"), argStr(args, "tokenID"))
	case "tokenActivity":
		return r.TokenActivity(ctx, argStr(args, "collection"), argStr(args, "tokenID"), argIntPtr(args, "limit"))
	case "activity":
		return r.Activity(ctx, argIntPtr(args, "limit"), argStrPtr(args, "address"), argStrPtr(args, "collection"), argStrPtr(args, "tokenID"))
	case "profile":
		return r.Profile(ctx, argStr(args, "address"))
	case "notifications":
		return r.Notifications(ctx, argStr(args, "address"), argIntPtr(args, "limit"))
	case "walletNFTs":
		return r.WalletNFTs(ctx, argStr(args, "owner"))
	case "search":
		return r.Search(ctx, argStr(args, "query"), argIntPtr(args, "limit"))
	case "metrics":
		return r.Metrics(ctx)
	case "trending":
		return r.Trending(ctx, argStrPtr(args, "window"), argIntPtr(args, "limit"))
	case "savedSearches":
		return r.SavedSearches(ctx, argStr(args, "address"), argStrPtr(args, "page"), argIntPtr(args, "limit"))
	case "traitValues":
		return r.TraitValues(ctx, argStr(args, "collection"))
	case "collectionStats":
		return r.CollectionStats(ctx, argStr(args, "collection"))
	case "countActiveListings":
		return r.CountActiveListings(ctx)
	case "countActiveAuctions":
		return r.CountActiveAuctions(ctx)
	case "countCollections":
		return r.CountCollections(ctx)
	case "totalVolume24h":
		return r.TotalVolume24h(ctx)
	default:
		return nil, fmt.Errorf("unknown query: %s", name)
	}
}

// resolveSubField handles computed sub-fields that require DB calls.
func resolveSubField(parent any, name string, args map[string]any, ctx context.Context, r QueryResolver) (any, bool) {
	switch p := parent.(type) {
	case *Collection:
		switch name {
		case "stats":
			stats, err := r.CollectionStats(ctx, p.Address)
			if err != nil {
				return nil, false
			}
			return stats, true
		case "floorPrice":
			stats, err := r.CollectionStats(ctx, p.Address)
			if err != nil {
				return nil, false
			}
			return stats.FloorPriceWei, true
		case "volume24h":
			stats, err := r.CollectionStats(ctx, p.Address)
			if err != nil {
				return nil, false
			}
			return stats.Volume24hWei, true
		case "listedCount":
			stats, err := r.CollectionStats(ctx, p.Address)
			if err != nil {
				return nil, false
			}
			return stats.ListedCount, true
		case "listings":
			l, err := r.Listings(ctx, &p.Address, nil, nil, argIntPtr(args, "limit"), nil, nil, argStrPtr(args, "traits"))
			return l, err == nil
		case "auctions":
			a, err := r.Auctions(ctx, &p.Address, nil, nil, argIntPtr(args, "limit"), nil, nil)
			return a, err == nil
		}
	case *Auction:
		switch name {
		case "bids":
			if r2, ok := r.(*resolver); ok && r2.q != nil {
				rows, err := r2.q.GetBidsForAuction(ctx, p.AuctionID)
				if err != nil {
					return nil, false
				}
				out := make([]*Bid, len(rows))
				for i := range rows {
					out[i] = &Bid{Bidder: rows[i].Bidder, AmountWei: rows[i].AmountWei, TxHash: rows[i].TxHash, PlacedAt: rows[i].PlacedAt}
				}
				return out, true
			}
			return nil, false
		case "effectiveBids":
			if r2, ok := r.(*resolver); ok && r2.q != nil {
				rows, err := r2.q.GetEffectiveBids(ctx, p.AuctionID)
				if err != nil {
					return nil, false
				}
				out := make([]*EffectiveBid, len(rows))
				for i := range rows {
					out[i] = &EffectiveBid{Bidder: rows[i].Bidder, EffectiveWei: rows[i].EffectiveWei, BidCount: int(rows[i].BidCount), LastBidAt: rows[i].LastBidAt}
				}
				return out, true
			}
			return nil, false
		}
	}
	return nil, false
}

// mapField accesses struct fields by name for simple field projection.
func mapField(parent any, name string) (any, bool) {
	switch p := parent.(type) {
	case *Collection:
		switch name {
		case "address":
			return p.Address, true
		case "name":
			return p.Name, true
		case "symbol":
			return p.Symbol, true
		case "standard":
			return p.Standard, true
		case "deployBlock":
			return p.DeployBlock, true
		case "verified":
			return p.Verified, true
		}
	case *Listing:
		switch name {
		case "collection":
			return p.Collection, true
		case "tokenID":
			return p.TokenID, true
		case "seller":
			return p.Seller, true
		case "priceWei":
			return p.PriceWei, true
		case "amount":
			return p.Amount, true
		case "standard":
			return p.Standard, true
		case "expiresAt":
			return p.ExpiresAt.Format(time.RFC3339Nano), true
		case "listedAt":
			return p.ListedAt.Format(time.RFC3339Nano), true
		case "txHash":
			return p.TxHash, true
		case "name":
			return p.Name, true
		case "imageURI":
			return p.ImageURI, true
		case "collectionVerified":
			return p.CollectionVerified, true
		}
	case *Auction:
		switch name {
		case "auctionID":
			return p.AuctionID, true
		case "collection":
			return p.Collection, true
		case "tokenID":
			return p.TokenID, true
		case "seller":
			return p.Seller, true
		case "standard":
			return p.Standard, true
		case "reservePriceWei":
			return p.ReservePriceWei, true
		case "highestBidWei":
			return p.HighestBidWei, true
		case "highestBidder":
			return p.HighestBidder, true
		case "minIncrementBps":
			return p.MinIncrementBps, true
		case "startsAt":
			return p.StartsAt.Format(time.RFC3339Nano), true
		case "endsAt":
			return p.EndsAt.Format(time.RFC3339Nano), true
		case "status":
			return p.Status, true
		case "createTx":
			return p.CreateTx, true
		case "name":
			return p.Name, true
		case "imageURI":
			return p.ImageURI, true
		}
	case *Offer:
		switch name {
		case "offerID":
			return p.OfferID, true
		case "bidder":
			return p.Bidder, true
		case "collection":
			return p.Collection, true
		case "tokenID":
			return p.TokenID, true
		case "amountWei":
			return p.AmountWei, true
		case "feeWei":
			return p.FeeWei, true
		case "units":
			return p.Units, true
		case "standard":
			return p.Standard, true
		case "expiresAt":
			return p.ExpiresAt.Format(time.RFC3339Nano), true
		case "status":
			return p.Status, true
		case "makeTx":
			return p.MakeTx, true
		case "createdAt":
			return p.CreatedAt.Format(time.RFC3339Nano), true
		}
	case *Profile:
		switch name {
		case "address":
			return p.Address, true
		case "displayName":
			return p.DisplayName, true
		case "bio":
			return p.Bio, true
		case "avatarURI":
			return p.AvatarURI, true
		case "bannerURI":
			return p.BannerURI, true
		case "twitter":
			return p.Twitter, true
		case "website":
			return p.Website, true
		case "verified":
			return p.Verified, true
		}
	case *MarketMetrics:
		switch name {
		case "totalActiveListings":
			return p.TotalActiveListings, true
		case "totalSales":
			return p.TotalSales, true
		case "grossVolumeWei":
			return p.GrossVolumeWei, true
		case "totalAuctions":
			return p.TotalAuctions, true
		case "totalBids":
			return p.TotalBids, true
		case "totalOffers":
			return p.TotalOffers, true
		}
	case *TrendingScore:
		switch name {
		case "collection":
			return p.Collection, true
		case "window":
			return p.Window, true
		case "score":
			return p.Score, true
		case "views":
			return p.Views, true
		case "bids":
			return p.Bids, true
		case "volumeWei":
			return p.VolumeWei, true
		}
	case *SearchResult:
		switch name {
		case "kind":
			return p.Kind, true
		case "collection":
			return p.Collection, true
		case "tokenID":
			return p.TokenID, true
		case "name":
			return p.Name, true
		case "imageURI":
			return p.ImageURI, true
		}
	case *OwnedNFT:
		switch name {
		case "collection":
			return p.Collection, true
		case "tokenID":
			return p.TokenID, true
		case "units":
			return p.Units, true
		case "standard":
			return p.Standard, true
		case "name":
			return p.Name, true
		case "imageURI":
			return p.ImageURI, true
		}
	case *Notification:
		switch name {
		case "id":
			return p.ID, true
		case "kind":
			return p.Kind, true
		case "title":
			return p.Title, true
		case "body":
			return p.Body, true
		case "link":
			return p.Link, true
		case "read":
			return p.Read, true
		case "createdAt":
			return p.CreatedAt.Format(time.RFC3339Nano), true
		}
	case *TokenMeta:
		switch name {
		case "name":
			return p.Name, true
		case "imageURI":
			return p.ImageURI, true
		}
	case *TokenFullMetadata:
		switch name {
		case "name":
			return p.Name, true
		case "description":
			return p.Description, true
		case "imageURI":
			return p.ImageURI, true
		case "animationURI":
			return p.AnimationURI, true
		case "metadataURI":
			return p.MetadataURI, true
		case "fetchedAt":
			return p.FetchedAt.Format(time.RFC3339Nano), true
		}
	case *Trait:
		switch name {
		case "type":
			return p.Type, true
		case "value":
			return p.Value, true
		}
	case *TokenActivity:
		switch name {
		case "type":
			return p.Type, true
		case "amountWei":
			return p.AmountWei, true
		case "fromAddr":
			return p.FromAddr, true
		case "toAddr":
			return p.ToAddr, true
		case "timestamp":
			return p.Timestamp.Format(time.RFC3339Nano), true
		case "txHash":
			return p.TxHash, true
		}
	case *Activity:
		switch name {
		case "type":
			return p.Type, true
		case "collection":
			return p.Collection, true
		case "tokenID":
			return p.TokenID, true
		case "amountWei":
			return p.AmountWei, true
		case "timestamp":
			return p.Timestamp.Format(time.RFC3339Nano), true
		case "txHash":
			return p.TxHash, true
		}
	case *Bid:
		switch name {
		case "bidder":
			return p.Bidder, true
		case "amountWei":
			return p.AmountWei, true
		case "txHash":
			return p.TxHash, true
		case "placedAt":
			return p.PlacedAt.Format(time.RFC3339Nano), true
		}
	case *EffectiveBid:
		switch name {
		case "bidder":
			return p.Bidder, true
		case "effectiveWei":
			return p.EffectiveWei, true
		case "bidCount":
			return p.BidCount, true
		case "lastBidAt":
			return p.LastBidAt.Format(time.RFC3339Nano), true
		}
	case *OfferSummary:
		switch name {
		case "collection":
			return p.Collection, true
		case "tokenID":
			return p.TokenID, true
		case "positions":
			return p.Positions, true
		case "count":
			return p.Count, true
		case "highest":
			return p.Highest, true
		case "totalWei":
			return p.TotalWei, true
		case "truncated":
			return p.Truncated, true
		}
	case *CollectionStats:
		switch name {
		case "floorPriceWei":
			return p.FloorPriceWei, true
		case "volume24hWei":
			return p.Volume24hWei, true
		case "listedCount":
			return p.ListedCount, true
		}
	case *SavedSearch:
		switch name {
		case "id":
			return p.ID, true
		case "userAddr":
			return p.UserAddr, true
		case "name":
			return p.Name, true
		case "page":
			return p.Page, true
		case "params":
			return p.Params, true
		case "createdAt":
			return p.CreatedAt.Format(time.RFC3339Nano), true
		}
	}
	return nil, false
}

// ── Argument helpers ──────────────────────────────────────────────────────────

func argStr(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func argStrPtr(args map[string]any, key string) *string {
	v := argStr(args, key)
	if v == "" {
		return nil
	}
	return &v
}

func argInt(args map[string]any, key string) int64 {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case int64:
			return n
		case float64:
			return int64(n)
		case int:
			return int64(n)
		case json.Number:
			if i, err := n.Int64(); err == nil {
				return i
			}
		}
	}
	return 0
}

func argIntPtr(args map[string]any, key string) *int {
	v := argInt(args, key)
	if v == 0 {
		return nil
	}
	i := int(v)
	return &i
}

// rawMessageToMap unmarshals a JSON raw message into a map.
func rawMessageToMap(data json.RawMessage) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return nil
	}
	return m
}

// reflectSlice returns the []any underlying val when val is a non-nil slice.
// Used by walkSelectionSet to iterate list results (e.g., listings, auctions).
func reflectSlice(val any) ([]any, bool) {
	if val == nil {
		return nil, false
	}
	rv := reflect.ValueOf(val)
	if rv.Kind() != reflect.Slice {
		return nil, false
	}
	out := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out[i] = rv.Index(i).Interface()
	}
	return out, true
}

// errorsJoin joins multiple errors with "; " separators.
func errorsJoin(errs []error) string {
	s := make([]string, len(errs))
	for i, e := range errs {
		s[i] = e.Error()
	}
	return strings.Join(s, "; ")
}
