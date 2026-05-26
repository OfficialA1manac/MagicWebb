package api

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

func listOffers(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		f := db.OffersFilter{
			Collection: c.Query("collection"),
			TokenID:    c.Query("token_id"),
			Bidder:     c.Query("bidder"),
			Owner:      c.Query("owner"),
			Status:     c.Query("status"),
		}
		if lim := c.Query("limit"); lim != "" {
			if n, err := strconv.Atoi(lim); err == nil {
				f.Limit = n
			}
		}
		rows, err := q.ListOffers(c.Context(), f)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		if rows == nil {
			rows = []db.OfferRow{}
		}
		return c.JSON(rows)
	}
}

type offerRequest struct {
	Bidder     string `json:"bidder"`
	Collection string `json:"collection"`
	TokenID    string `json:"token_id"`
	AmountWei  string `json:"amount_wei"`
	Nonce      string `json:"nonce"`
	ExpiresAt  int64  `json:"expires_at"`
	Signature  string `json:"signature"`
}

func notifyOffer(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		var req offerRequest
		if err := bodyDecode(c, &req); err != nil {
			return writeErr(c, fiber.StatusBadRequest, "invalid request body")
		}
		if req.Bidder == "" || req.Collection == "" || req.AmountWei == "" ||
			req.Nonce == "" || req.Signature == "" || req.ExpiresAt == 0 {
			return writeErr(c, fiber.StatusBadRequest, "missing required fields")
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
		id, err := q.InsertOffer(c.Context(), row)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"offer_id": id})
	}
}
