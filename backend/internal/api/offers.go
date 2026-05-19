package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

func handleListOffers(q *db.Q) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f := db.OffersFilter{
			Collection: r.URL.Query().Get("collection"),
			TokenID:    r.URL.Query().Get("token_id"),
			Bidder:     r.URL.Query().Get("bidder"),
			Owner:      r.URL.Query().Get("owner"),
			Status:     r.URL.Query().Get("status"),
		}
		if lim := r.URL.Query().Get("limit"); lim != "" {
			if n, err := strconv.Atoi(lim); err == nil {
				f.Limit = n
			}
		}
		rows, err := q.ListOffers(r.Context(), f)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		if rows == nil {
			rows = []db.OfferRow{}
		}
		writeJSON(w, http.StatusOK, rows)
	}
}

type offerRequest struct {
	Bidder     string `json:"bidder"`
	Collection string `json:"collection"`
	TokenID    string `json:"token_id"`
	AmountWei  string `json:"amount_wei"`
	Nonce      string `json:"nonce"`
	ExpiresAt  int64  `json:"expires_at"` // unix seconds
	Signature  string `json:"signature"`
}

func handleNotifyOffer(q *db.Q) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

		var req offerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid request body")
			return
		}

		// Basic field validation
		if req.Bidder == "" || req.Collection == "" || req.AmountWei == "" ||
			req.Nonce == "" || req.Signature == "" || req.ExpiresAt == 0 {
			writeErr(w, http.StatusBadRequest, "missing required fields")
			return
		}

		row := db.OfferRow{
			Bidder:     req.Bidder,
			Collection: req.Collection,
			TokenID:    req.TokenID,
			AmountWei:  req.AmountWei,
			Nonce:      req.Nonce,
			ExpiresAt:  time.Unix(req.ExpiresAt, 0).UTC(),
			Signature:  req.Signature,
			Status:     "pending",
		}

		id, err := q.InsertOffer(r.Context(), row)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"offer_id": id})
	}
}
