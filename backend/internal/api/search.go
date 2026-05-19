package api

import (
	"net/http"
	"strconv"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// handleSearch serves GET /api/v1/search?q=<query>&limit=<n>
func handleSearch(q *db.Q) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		if len(query) < 2 {
			writeErr(w, http.StatusBadRequest, "q must be at least 2 characters")
			return
		}
		limit := 20
		if lim := r.URL.Query().Get("limit"); lim != "" {
			if n, err := strconv.Atoi(lim); err == nil {
				limit = n
			}
		}
		results, err := q.Search(r.Context(), query, limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		if results == nil {
			results = []db.SearchResult{}
		}
		writeJSON(w, http.StatusOK, results)
	}
}
