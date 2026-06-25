package api

import (
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

func listListings(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		f := db.ListingsFilter{
			Collection: c.Query("collection"),
			Seller:     c.Query("seller"),
		}
		if lim := c.Query("limit"); lim != "" {
			if n, err := strconv.Atoi(lim); err == nil {
				if n < 1 {
					n = 1
				} else if n > 200 {
					n = 200
				}
				f.Limit = n
			}
		}
		rows, err := q.ListActiveListings(c.Context(), f)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		if rows == nil {
			rows = []db.ListingRow{}
		}
		return c.JSON(rows)
	}
}

func getListing(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		collection := c.Params("collection")
		id := c.Params("id")
		row, err := q.GetListing(c.Context(), collection, id)
		if err != nil {
			if isNotFound(err) {
				return writeErr(c, fiber.StatusNotFound, "listing not found")
			}
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		return c.JSON(row)
	}
}

func listCollections(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		limit := 50
		if lim := c.Query("limit"); lim != "" {
			if n, err := strconv.Atoi(lim); err == nil {
				if n < 1 {
					n = 1
				} else if n > 200 {
					n = 200
				}
				limit = n
			}
		}
		rows, err := q.ListCollections(c.Context(), limit)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		if rows == nil {
			rows = []db.CollectionRow{}
		}
		return c.JSON(rows)
	}
}

type collectionDetail struct {
	db.CollectionRow
	FloorPriceWei string `json:"floor_price_wei"`
	Volume24hWei  string `json:"volume_24h_wei"`
	ListedCount   int    `json:"listed_count"`
}

func getCollection(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		address := c.Params("address")
		col, err := q.GetCollection(c.Context(), address)
		if err != nil {
			if isNotFound(err) {
				return writeErr(c, fiber.StatusNotFound, "collection not found")
			}
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		floor, _ := q.GetFloorPrice(c.Context(), address)
		vol, _ := q.Get24hVolume(c.Context(), address)
		listed, _ := q.GetListedCount(c.Context(), address)

		detail := collectionDetail{CollectionRow: *col, ListedCount: listed}
		if floor != nil {
			detail.FloorPriceWei = floor.String()
		}
		if vol != nil {
			detail.Volume24hWei = vol.String()
		}
		return c.JSON(detail)
	}
}

func getTrending(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		window := c.Query("window")
		if window == "" {
			window = "24h"
		}
		limit := 20
		if lim := c.Query("limit"); lim != "" {
			if n, err := strconv.Atoi(lim); err == nil {
				if n < 1 {
					n = 1
				} else if n > 100 {
					n = 100
				}
				limit = n
			}
		}
		rows, err := q.GetTrendingCollections(c.Context(), window, limit)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		if rows == nil {
			rows = []db.TrendingScore{}
		}
		return c.JSON(rows)
	}
}
