package api

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// SavedSearchesService handles saved-search API operations.
type SavedSearchesService struct {
	q *db.Q
}

// NewSavedSearchesService creates a SavedSearchesService.
func NewSavedSearchesService(q *db.Q) *SavedSearchesService {
	return &SavedSearchesService{q: q}
}

// RegisterRoutes registers all saved-search routes under the given router group.
func (s *SavedSearchesService) RegisterRoutes(api fiber.Router, cfg *config.Config) {
	api.Get("/saved-searches", jwtMiddleware(cfg), s.handleList)
	api.Post("/saved-searches", jwtMiddleware(cfg), s.handleCreate)
	api.Delete("/saved-searches/:id", jwtMiddleware(cfg), s.handleDelete)
}

// handleList returns the authenticated user's saved searches.
func (s *SavedSearchesService) handleList(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" {
		return writeErr(c, fiber.StatusUnauthorized, "unauthorized")
	}
	limit := 50
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
	// Filter by page if requested, so callers can request e.g. only "auctions"
	// or "listings" saved searches without client-side post-filtering.
	page := strings.TrimSpace(c.Query("page"))
	rows, err := s.q.ListSavedSearches(c.Context(), addr, limit, page)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if rows == nil {
		rows = []db.SavedSearchRow{}
	}
	return c.JSON(rows)
}

type createSavedSearchRequest struct {
	Name   string `json:"name"`
	Page   string `json:"page"`
	Params string `json:"params"`
}

// handleCreate persists a new saved search for the authenticated user.
func (s *SavedSearchesService) handleCreate(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" {
		return writeErr(c, fiber.StatusUnauthorized, "unauthorized")
	}
	var req createSavedSearchRequest
	if err := bodyDecode(c, &req); err != nil {
		return writeErr(c, fiber.StatusBadRequest, "invalid request body")
	}
	if req.Name == "" {
		return writeErr(c, fiber.StatusBadRequest, "name is required")
	}
	if len(req.Name) > 100 {
		return writeErr(c, fiber.StatusBadRequest, "name too long (max 100)")
	}
	if req.Page != "listings" && req.Page != "auctions" {
		return writeErr(c, fiber.StatusBadRequest, "page must be 'listings' or 'auctions'")
	}
	if len(req.Params) > 500 {
		return writeErr(c, fiber.StatusBadRequest, "params too long (max 500)")
	}
	id, err := s.q.InsertSavedSearch(c.Context(), addr, req.Name, req.Page, req.Params)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id})
}

// handleDelete removes a saved search owned by the authenticated user.
func (s *SavedSearchesService) handleDelete(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" {
		return writeErr(c, fiber.StatusUnauthorized, "unauthorized")
	}
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return writeErr(c, fiber.StatusBadRequest, "invalid id")
	}
	deleted, err := s.q.DeleteSavedSearch(c.Context(), id, addr)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if !deleted {
		return writeErr(c, fiber.StatusNotFound, "saved search not found")
	}
	return c.SendStatus(fiber.StatusNoContent)
}
