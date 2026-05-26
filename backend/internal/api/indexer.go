package api

import (
	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

type indexerStatusResp struct {
	IndexedBlock uint64 `json:"indexed_block"`
	TotalEvents  uint64 `json:"total_events"`
	Last1hEvents uint64 `json:"last_1h_events"`
}

func indexerStatus(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		block, err := q.GetIndexedBlock(c.Context(), 0)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		total, last1h, err := q.GetEventCounts(c.Context())
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		return c.JSON(indexerStatusResp{
			IndexedBlock: block,
			TotalEvents:  total,
			Last1hEvents: last1h,
		})
	}
}
