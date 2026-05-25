package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

func handleListAuctions(q *db.Q) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f := db.AuctionsFilter{
			Collection: r.URL.Query().Get("collection"),
			Status:     r.URL.Query().Get("status"),
		}
		if lim := r.URL.Query().Get("limit"); lim != "" {
			if n, err := strconv.Atoi(lim); err == nil {
				f.Limit = n
			}
		}
		rows, err := q.ListAuctions(r.Context(), f)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		if rows == nil {
			rows = []db.AuctionRow{}
		}
		writeJSON(w, http.StatusOK, rows)
	}
}

func handleGetAuction(q *db.Q) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid auction id")
			return
		}
		row, err := q.GetAuction(r.Context(), id)
		if err != nil {
			if isNotFound(err) {
				writeErr(w, http.StatusNotFound, "auction not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, row)
	}
}

func handleServerTime() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]int64{"unix_ms": time.Now().UnixMilli()})
	}
}

func handleGetAuctionBids(q *db.Q) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid auction id")
			return
		}
		rows, err := q.GetBidsForAuction(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		if rows == nil {
			rows = []db.BidRow{}
		}
		writeJSON(w, http.StatusOK, rows)
	}
}
