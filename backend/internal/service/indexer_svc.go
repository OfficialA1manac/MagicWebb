package service

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	indexerv1 "github.com/OfficialA1manac/MagicWebb/backend/gen/indexer/v1"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// IndexerService implements indexerv1.IndexerServiceServer.
type IndexerService struct {
	indexerv1.UnimplementedIndexerServiceServer
	q   *db.Q
	eth *ethclient.Client
	cfg *config.Config
}

// NewIndexerService constructs the indexer gRPC service.
func NewIndexerService(q *db.Q, eth *ethclient.Client, cfg *config.Config) *IndexerService {
	return &IndexerService{q: q, eth: eth, cfg: cfg}
}

func (s *IndexerService) GetStatus(ctx context.Context, _ *emptypb.Empty) (*indexerv1.IndexerStatus, error) {
	indexed, err := s.q.GetIndexedBlock(ctx, int(s.cfg.ChainID))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get indexed block: %v", err)
	}
	var chainBlock uint64
	if s.eth != nil {
		n, err := s.eth.BlockNumber(ctx)
		if err == nil {
			chainBlock = n
		}
	}
	var lag uint64
	if chainBlock > indexed {
		lag = chainBlock - indexed
	}
	const lagThreshold = 10
	total, last1h, _ := s.q.GetEventCounts(ctx)
	return &indexerv1.IndexerStatus{
		IndexedBlock:       indexed,
		ChainBlock:         chainBlock,
		Lag:                lag,
		Healthy:            lag < lagThreshold,
		LastIndexedAt:      timestamppb.New(time.Now()),
		EventsIndexedTotal: total,
		EventsIndexed_1H:   last1h,
	}, nil
}

func (s *IndexerService) GetBlockHeight(ctx context.Context, _ *emptypb.Empty) (*indexerv1.BlockHeightResponse, error) {
	if s.eth == nil {
		return nil, status.Error(codes.Unavailable, "eth client not connected")
	}
	n, err := s.eth.BlockNumber(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "block number: %v", err)
	}
	header, err := s.eth.HeaderByNumber(ctx, new(big.Int).SetUint64(n))
	if err != nil {
		return &indexerv1.BlockHeightResponse{BlockNumber: n}, nil
	}
	return &indexerv1.BlockHeightResponse{
		BlockNumber: n,
		BlockTime:   timestamppb.New(time.Unix(int64(header.Time), 0)),
	}, nil
}

func (s *IndexerService) Reindex(ctx context.Context, req *indexerv1.ReindexRequest) (*indexerv1.ReindexResponse, error) {
	// Admin only: validate service token.
	if s.cfg.ServiceToken != "" {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "no metadata")
		}
		vals := md.Get("x-service-token")
		if len(vals) == 0 || vals[0] != s.cfg.ServiceToken {
			return nil, status.Error(codes.PermissionDenied, "invalid service token")
		}
	}

	fromBlock := req.GetFromBlock()
	if fromBlock == 0 {
		fromBlock = 0
	}
	// Reset indexed block cursor so the watcher picks up from fromBlock.
	if err := s.q.SetIndexedBlock(ctx, int(s.cfg.ChainID), fromBlock); err != nil {
		return nil, status.Errorf(codes.Internal, "reset cursor: %v", err)
	}
	jobID := fmt.Sprintf("reindex-%d-%d", fromBlock, time.Now().UnixMilli())
	return &indexerv1.ReindexResponse{JobId: jobID}, nil
}
