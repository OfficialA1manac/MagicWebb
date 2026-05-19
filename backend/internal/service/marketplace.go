// Package service implements the gRPC service handlers for WebbPlace.
package service

import (
	"context"
	"encoding/json"
	"math"
	"math/big"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/OfficialA1manac/MagicWebb/backend/gen/common/v1"
	mktv1 "github.com/OfficialA1manac/MagicWebb/backend/gen/marketplace/v1"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// MarketplaceService implements mktv1.MarketplaceServiceServer.
type MarketplaceService struct {
	mktv1.UnimplementedMarketplaceServiceServer
	q            *db.Q
	rdb          *cache.Client
	scoreWeights scoreWeights
}

type scoreWeights struct {
	views, bids, volume, decayLambda float64
}

// NewMarketplaceService constructs the marketplace gRPC service.
func NewMarketplaceService(q *db.Q, rdb *cache.Client, wViews, wBids, wVolume, decay float64) *MarketplaceService {
	return &MarketplaceService{
		q:   q,
		rdb: rdb,
		scoreWeights: scoreWeights{
			views:       wViews,
			bids:        wBids,
			volume:      wVolume,
			decayLambda: decay,
		},
	}
}

func (s *MarketplaceService) GetListing(ctx context.Context, req *mktv1.GetListingRequest) (*mktv1.Listing, error) {
	if req.GetCollection() == nil || req.GetTokenId() == nil {
		return nil, status.Error(codes.InvalidArgument, "collection and token_id required")
	}
	r, err := s.q.GetListing(ctx, req.GetCollection().GetValue(), req.GetTokenId().GetValue())
	if err != nil {
		return nil, status.Error(codes.NotFound, "listing not found")
	}
	// Increment view count asynchronously.
	go func() {
		_ = s.q.IncrementTokenViews(context.Background(), r.Collection, r.TokenID)
	}()
	return listingToProto(*r), nil
}

func (s *MarketplaceService) GetCollection(ctx context.Context, req *mktv1.GetCollectionRequest) (*mktv1.Collection, error) {
	if req.GetAddress() == nil {
		return nil, status.Error(codes.InvalidArgument, "address required")
	}
	addr := req.GetAddress().GetValue()
	c, err := s.q.GetCollection(ctx, addr)
	if err != nil {
		return nil, status.Error(codes.NotFound, "collection not found")
	}
	floor, _ := s.q.GetFloorPrice(ctx, addr)
	vol24, _ := s.q.Get24hVolume(ctx, addr)
	listed, _ := s.q.GetListedCount(ctx, addr)
	scores, _ := s.q.GetTrendingCollections(ctx, "24h", 500)
	trendScore := float32(0)
	for _, sc := range scores {
		if sc.Collection == addr {
			trendScore = float32(sc.Score)
			break
		}
	}
	return collectionToProto(c, floor, vol24, uint64(listed), trendScore), nil
}

func (s *MarketplaceService) ListListings(ctx context.Context, req *mktv1.ListListingsRequest) (*mktv1.ListListingsResponse, error) {
	f := db.ListingsFilter{Limit: int(req.GetPagination().GetLimit())}
	if req.GetCollection() != nil {
		f.Collection = req.GetCollection().GetValue()
	}
	if req.GetSeller() != nil {
		f.Seller = req.GetSeller().GetValue()
	}
	rows, err := s.q.ListActiveListings(ctx, f)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list listings: %v", err)
	}
	resp := &mktv1.ListListingsResponse{}
	for _, r := range rows {
		resp.Listings = append(resp.Listings, listingToProto(r))
	}
	return resp, nil
}

func (s *MarketplaceService) ListCollections(ctx context.Context, req *mktv1.ListCollectionsRequest) (*mktv1.ListCollectionsResponse, error) {
	lim := int(req.GetPagination().GetLimit())
	if lim == 0 {
		lim = 50
	}
	cols, err := s.q.ListCollections(ctx, lim)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list collections: %v", err)
	}
	resp := &mktv1.ListCollectionsResponse{}
	for _, c := range cols {
		floor, _ := s.q.GetFloorPrice(ctx, c.Address)
		vol24, _ := s.q.Get24hVolume(ctx, c.Address)
		listed, _ := s.q.GetListedCount(ctx, c.Address)
		resp.Collections = append(resp.Collections, collectionToProto(&c, floor, vol24, uint64(listed), 0))
	}
	return resp, nil
}

func (s *MarketplaceService) GetTrending(ctx context.Context, req *mktv1.GetTrendingRequest) (*mktv1.GetTrendingResponse, error) {
	window := req.GetTimeWindow()
	if window == "" {
		window = "24h"
	}
	lim := int(req.GetLimit())
	if lim == 0 || lim > 100 {
		lim = 20
	}
	scores, err := s.q.GetTrendingCollections(ctx, window, lim)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get trending: %v", err)
	}
	resp := &mktv1.GetTrendingResponse{}
	for _, sc := range scores {
		c, err := s.q.GetCollection(ctx, sc.Collection)
		if err != nil {
			continue
		}
		floor, _ := s.q.GetFloorPrice(ctx, sc.Collection)
		vol24, _ := s.q.Get24hVolume(ctx, sc.Collection)
		listed, _ := s.q.GetListedCount(ctx, sc.Collection)
		resp.Collections = append(resp.Collections, collectionToProto(c, floor, vol24, uint64(listed), float32(sc.Score)))
	}
	return resp, nil
}

func (s *MarketplaceService) StreamListingEvents(_ *emptypb.Empty, stream grpc.ServerStreamingServer[mktv1.ListingEvent]) error {
	ctx := stream.Context()
	sub := s.rdb.Subscribe(ctx, "mktplace:events")
	defer sub.Close()
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return status.Error(codes.Unavailable, "event stream closed")
			}
			var raw map[string]any
			if err := json.Unmarshal([]byte(msg.Payload), &raw); err != nil {
				log.Warn().Err(err).Msg("marketplace stream: bad payload")
				continue
			}
			evtType := eventTypeFromString(raw["event"])
			evt := &mktv1.ListingEvent{
				Type:       evtType,
				OccurredAt: timestamppb.New(time.Now()),
			}
			if err := stream.Send(evt); err != nil {
				return err
			}
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func computeScore(views, bids uint64, volumeEth float64, ageHours float64, w scoreWeights) float64 {
	raw := float64(views)*w.views + float64(bids)*w.bids + volumeEth*w.volume
	return raw * math.Exp(-w.decayLambda*ageHours)
}

func listingToProto(r db.ListingRow) *mktv1.Listing {
	price, _ := new(big.Int).SetString(r.PriceWei, 10)
	standard := commonv1.TokenStandard_ERC721
	if r.Standard == "ERC1155" || r.Standard == "erc1155" {
		standard = commonv1.TokenStandard_ERC1155
	}
	var priceBytes []byte
	if price != nil {
		priceBytes = price.Bytes()
	}
	return &mktv1.Listing{
		Collection: &commonv1.Address{Value: r.Collection},
		TokenId:    &commonv1.TokenId{Value: r.TokenID},
		Seller:     &commonv1.Address{Value: r.Seller},
		Price:      &commonv1.Wei{Value: priceBytes},
		Amount:     uint64(r.Amount),
		Standard:   standard,
		ExpiresAt:  timestamppb.New(r.ExpiresAt),
		ListedAt:   timestamppb.New(r.ListedAt),
		TxHash:     r.TxHash,
		Name:       r.Name,
		ImageUri:   r.ImageURI,
	}
}

func collectionToProto(c *db.CollectionRow, floor, vol24 *big.Int, listed uint64, trendScore float32) *mktv1.Collection {
	standard := commonv1.TokenStandard_ERC721
	if c.Standard == "erc1155" {
		standard = commonv1.TokenStandard_ERC1155
	}
	var floorBytes, volBytes []byte
	if floor != nil {
		floorBytes = floor.Bytes()
	}
	if vol24 != nil {
		volBytes = vol24.Bytes()
	}
	return &mktv1.Collection{
		Address:       &commonv1.Address{Value: c.Address},
		Name:          c.Name,
		Symbol:        c.Symbol,
		Standard:      standard,
		FloorPrice:    &commonv1.Wei{Value: floorBytes},
		Volume_24H:    &commonv1.Wei{Value: volBytes},
		TrendingScore: trendScore,
		ListedCount:   listed,
	}
}

func eventTypeFromString(v any) commonv1.EventType {
	s, _ := v.(string)
	switch s {
	case "Listed":
		return commonv1.EventType_LISTED
	case "Cancelled":
		return commonv1.EventType_DELISTED
	case "Bought", "OfferAccepted", "Offer1155Accepted":
		return commonv1.EventType_SALE
	case "AuctionCreated":
		return commonv1.EventType_AUCTION_CREATED
	case "BidPlaced":
		return commonv1.EventType_BID_PLACED
	case "AuctionSettled":
		return commonv1.EventType_AUCTION_SETTLED
	case "AuctionCancelled":
		return commonv1.EventType_AUCTION_CANCELLED
	default:
		return commonv1.EventType_EVENT_TYPE_UNSPECIFIED
	}
}
