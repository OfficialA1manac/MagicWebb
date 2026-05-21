package api

import (
	"net/http"
	"strconv"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

func handleGetMarketMetrics(q *db.Q) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, err := q.GetMarketMetrics(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, m)
	}
}

func handleGetRecentActivity(q *db.Q) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if s := r.URL.Query().Get("limit"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				limit = n
			}
		}
		rows, err := q.GetRecentTransactions(r.Context(), limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, rows)
	}
}
