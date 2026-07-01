package api

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// AdminService handles trust & safety and admin verification operations.
type AdminService struct {
	q   *db.Q
	cfg *config.Config
}

// NewAdminService creates an AdminService.
func NewAdminService(q *db.Q, cfg *config.Config) *AdminService {
	return &AdminService{q: q, cfg: cfg}
}

// RegisterRoutes registers all admin/report-related routes under the given router group.
func (s *AdminService) RegisterRoutes(api fiber.Router, cfg *config.Config) {
	api.Post("/reports", jwtMiddleware(cfg), s.handleCreateReport)
	api.Post("/admin/verify", jwtMiddleware(cfg), s.handleAdminVerify)
	api.Post("/admin/collections/verify", jwtMiddleware(cfg), s.handleAdminVerifyCollection)
	api.Get("/admin/auctions/stalled", jwtMiddleware(cfg), s.handleStalledAuctions)
}

type reportRequest struct {
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Reason     string `json:"reason"`
	Detail     string `json:"detail"`
}

func (s *AdminService) handleCreateReport(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" {
		return writeErr(c, fiber.StatusUnauthorized, "unauthorized")
	}
	var req reportRequest
	if err := bodyDecode(c, &req); err != nil {
		return writeErr(c, fiber.StatusBadRequest, "invalid request body")
	}
	if req.TargetType == "" || req.TargetID == "" || req.Reason == "" {
		return writeErr(c, fiber.StatusBadRequest, "missing required fields")
	}
	if len(req.Detail) > 1000 {
		return writeErr(c, fiber.StatusBadRequest, "detail too long")
	}
	if err := s.q.InsertReport(c.Context(), addr, req.TargetType, req.TargetID, req.Reason, req.Detail); err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	return c.SendStatus(fiber.StatusCreated)
}

type verifyRequest struct {
	Address  string `json:"address"`
	Verified bool   `json:"verified"`
}

func (s *AdminService) handleAdminVerify(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" || !s.cfg.IsAdmin(addr) {
		return writeErr(c, fiber.StatusForbidden, "admin only")
	}
	var req verifyRequest
	if err := bodyDecode(c, &req); err != nil || req.Address == "" {
		return writeErr(c, fiber.StatusBadRequest, "address required")
	}
	if err := s.q.SetVerified(c.Context(), strings.ToLower(req.Address), req.Verified); err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	return c.JSON(fiber.Map{"address": strings.ToLower(req.Address), "verified": req.Verified})
}

func (s *AdminService) handleAdminVerifyCollection(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" || !s.cfg.IsAdmin(addr) {
		return writeErr(c, fiber.StatusForbidden, "admin only")
	}
	var req verifyRequest
	if err := bodyDecode(c, &req); err != nil || req.Address == "" {
		return writeErr(c, fiber.StatusBadRequest, "address required")
	}
	if err := s.q.SetCollectionVerified(c.Context(), strings.ToLower(req.Address), req.Verified); err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	return c.JSON(fiber.Map{"collection": strings.ToLower(req.Address), "verified": req.Verified})
}

// handleStalledAuctions returns detailed stalled auction rows for admin inspection.
// Response includes both summary counts and the full row list.
func (s *AdminService) handleStalledAuctions(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" || !s.cfg.IsAdmin(addr) {
		return writeErr(c, fiber.StatusForbidden, "admin only")
	}
	limit := 100
	if ls := c.Query("limit"); ls != "" {
		if n, err := strconv.Atoi(ls); err == nil && n > 0 {
			if n > 200 {
				n = 200
			}
			limit = n
		}
	}

	counts, err := s.q.GetStalledAuctionCounts(c.Context())
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	rows, err := s.q.ListStalledAuctions(c.Context(), limit)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if rows == nil {
		rows = []db.StalledAuctionRow{}
	}
	return c.JSON(fiber.Map{
		"counts": counts,
		"rows":   rows,
	})
}
