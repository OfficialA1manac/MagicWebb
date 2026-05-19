package api

import (
	"net/http"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

type indexerStatusResp struct {
	IndexedBlock uint64 `json:"indexed_block"`
	TotalEvents  uint64 `json:"total_events"`
	Last1hEvents uint64 `json:"last_1h_events"`
}

func handleIndexerStatus(q *db.Q) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// chainID 0 is the default key used by the indexer runner
		block, err := q.GetIndexedBlock(r.Context(), 0)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		total, last1h, err := q.GetEventCounts(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, indexerStatusResp{
			IndexedBlock: block,
			TotalEvents:  total,
			Last1hEvents: last1h,
		})
	}
}
