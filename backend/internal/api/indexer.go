package api

import (
	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// IndexerService handles indexer status API operations.
type IndexerService struct {
	q       *db.Q
	chainID uint64
}

type indexerStatusResp struct {
	IndexedBlock uint64 `json:"indexed_block"`
	TotalEvents  uint64 `json:"total_events"`
	Last1hEvents uint64 `json:"last_1h_events"`
}

// NewIndexerService creates an IndexerService.
func NewIndexerService(q *db.Q, chainID uint64) *IndexerService {
	return &IndexerService{q: q, chainID: chainID}
}

// RegisterRoutes registers the indexer status route under the given router group.
func (s *IndexerService) RegisterRoutes(api fiber.Router) {
	api.Get("/indexer/status", s.handleStatus)
}

func (s *IndexerService) handleStatus(c *fiber.Ctx) error {
	block, err := s.q.GetIndexedBlock(c.Context(), int(s.chainID))
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	total, last1h, err := s.q.GetEventCounts(c.Context())
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	return c.JSON(indexerStatusResp{
		IndexedBlock: block,
		TotalEvents:  total,
		Last1hEvents: last1h,
	})
}
