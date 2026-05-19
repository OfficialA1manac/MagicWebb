package api

import (
	"net/http"
	"strconv"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

func handleListListings(q *db.Q) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f := db.ListingsFilter{
			Collection: r.URL.Query().Get("collection"),
			Seller:     r.URL.Query().Get("seller"),
		}
		if lim := r.URL.Query().Get("limit"); lim != "" {
			if n, err := strconv.Atoi(lim); err == nil {
				f.Limit = n
			}
		}
		rows, err := q.ListActiveListings(r.Context(), f)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		if rows == nil {
			rows = []db.ListingRow{}
		}
		writeJSON(w, http.StatusOK, rows)
	}
}

func handleGetListing(q *db.Q) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		collection := r.PathValue("collection")
		id := r.PathValue("id")
		row, err := q.GetListing(r.Context(), collection, id)
		if err != nil {
			if isNotFound(err) {
				writeErr(w, http.StatusNotFound, "listing not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, row)
	}
}

func handleListCollections(q *db.Q) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if lim := r.URL.Query().Get("limit"); lim != "" {
			if n, err := strconv.Atoi(lim); err == nil {
				limit = n
			}
		}
		rows, err := q.ListCollections(r.Context(), limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		if rows == nil {
			rows = []db.CollectionRow{}
		}
		writeJSON(w, http.StatusOK, rows)
	}
}

type collectionDetail struct {
	db.CollectionRow
	FloorPriceWei string `json:"floor_price_wei"`
	Volume24hWei  string `json:"volume_24h_wei"`
	ListedCount   int    `json:"listed_count"`
}

func handleGetCollection(q *db.Q) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		address := r.PathValue("address")
		col, err := q.GetCollection(r.Context(), address)
		if err != nil {
			if isNotFound(err) {
				writeErr(w, http.StatusNotFound, "collection not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		floor, _ := q.GetFloorPrice(r.Context(), address)
		vol, _ := q.Get24hVolume(r.Context(), address)
		listed, _ := q.GetListedCount(r.Context(), address)

		detail := collectionDetail{
			CollectionRow: *col,
			ListedCount:   listed,
		}
		if floor != nil {
			detail.FloorPriceWei = floor.String()
		}
		if vol != nil {
			detail.Volume24hWei = vol.String()
		}
		writeJSON(w, http.StatusOK, detail)
	}
}

func handleGetTrending(q *db.Q) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		window := r.URL.Query().Get("window")
		if window == "" {
			window = "24h"
		}
		limit := 20
		if lim := r.URL.Query().Get("limit"); lim != "" {
			if n, err := strconv.Atoi(lim); err == nil {
				limit = n
			}
		}
		rows, err := q.GetTrendingCollections(r.Context(), window, limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		if rows == nil {
			rows = []db.TrendingScore{}
		}
		writeJSON(w, http.StatusOK, rows)
	}
}
