package service

import (
	"context"
	"encoding/json"
	"math/big"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/OfficialA1manac/MagicWebb/backend/gen/common/v1"
	auctionv1 "github.com/OfficialA1manac/MagicWebb/backend/gen/auction/v1"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// AuctionService implements auctionv1.AuctionServiceServer.
type AuctionService struct {
	auctionv1.UnimplementedAuctionServiceServer
	q   *db.Q
	rdb *cache.Client
}

// NewAuctionService constructs the auction gRPC service.
func NewAuctionService(q *db.Q, rdb *cache.Client) *AuctionService {
	return &AuctionService{q: q, rdb: rdb}
}

func (s *AuctionService) GetAuction(ctx context.Context, req *auctionv1.GetAuctionRequest) (*auctionv1.Auction, error) {
	r, err := s.q.GetAuction(ctx, int64(req.GetAuctionId()))
	if err != nil {
		return nil, status.Error(codes.NotFound, "auction not found")
	}
	return auctionRowToProto(*r), nil
}

func (s *AuctionService) ListAuctions(ctx context.Context, req *auctionv1.ListAuctionsRequest) (*auctionv1.ListAuctionsResponse, error) {
	f := db.AuctionsFilter{Limit: int(req.GetPagination().GetLimit())}
	if req.GetCollection() != nil {
		f.Collection = req.GetCollection().GetValue()
	}
	switch req.GetStatus() {
	case auctionv1.AuctionStatus_ACTIVE:
		f.Status = "active"
	case auctionv1.AuctionStatus_SETTLED:
		f.Status = "settled"
	case auctionv1.AuctionStatus_CANCELLED:
		f.Status = "cancelled"
	}
	rows, err := s.q.ListAuctions(ctx, f)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list auctions: %v", err)
	}
	resp := &auctionv1.ListAuctionsResponse{}
	for _, r := range rows {
		resp.Auctions = append(resp.Auctions, auctionRowToProto(r))
	}
	return resp, nil
}

func (s *AuctionService) StreamAuctionEvents(_ *emptypb.Empty, stream grpc.ServerStreamingServer[auctionv1.AuctionEvent]) error {
	ctx := stream.Context()
	sub := s.rdb.Subscribe(ctx, "auction:events")
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
				log.Warn().Err(err).Msg("auction stream: bad payload")
				continue
			}
			evtType := eventTypeFromString(raw["event"])
			evt := &auctionv1.AuctionEvent{
				Type:       evtType,
				OccurredAt: timestamppb.New(time.Now()),
			}
			if err := stream.Send(evt); err != nil {
				return err
			}
		}
	}
}

func (s *AuctionService) GetServerTime(_ context.Context, _ *emptypb.Empty) (*auctionv1.ServerTimeResponse, error) {
	return &auctionv1.ServerTimeResponse{
		UnixMs: time.Now().UnixMilli(),
	}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func auctionRowToProto(r db.AuctionRow) *auctionv1.Auction {
	reserve, _ := new(big.Int).SetString(r.ReservePriceWei, 10)
	highBid, _ := new(big.Int).SetString(r.HighestBidWei, 10)
	standard := commonv1.TokenStandard_ERC721
	if r.Standard == "ERC1155" || r.Standard == "erc1155" {
		standard = commonv1.TokenStandard_ERC1155
	}
	var resBytes, bidBytes []byte
	if reserve != nil {
		resBytes = reserve.Bytes()
	}
	if highBid != nil {
		bidBytes = highBid.Bytes()
	}
	a := &auctionv1.Auction{
		AuctionId:       uint64(r.AuctionID),
		Collection:      &commonv1.Address{Value: r.Collection},
		TokenId:         &commonv1.TokenId{Value: r.TokenID},
		Seller:          &commonv1.Address{Value: r.Seller},
		Standard:        standard,
		ReservePrice:    &commonv1.Wei{Value: resBytes},
		HighestBid:      &commonv1.Wei{Value: bidBytes},
		MinIncrementBps: uint32(r.MinIncrementBps),
		StartsAt:        timestamppb.New(r.StartsAt),
		EndsAt:          timestamppb.New(r.EndsAt),
		Settled:         r.Status == "settled",
		Cancelled:       r.Status == "cancelled",
	}
	if r.HighestBidder != "" {
		a.HighestBidder = &commonv1.Address{Value: r.HighestBidder}
	}
	return a
}
