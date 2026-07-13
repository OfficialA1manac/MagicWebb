package api

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// AdminService handles trust & safety and admin verification operations.
type AdminService struct {
	q        *db.Q
	cfg      *config.Config
	apiKeys  auth.APIKeyStore  // AUTH-3: API key store for machine-to-machine auth
	auditLog auth.AuditLogger  // AUTH-3: audit log for API key events
}

// NewAdminService creates an AdminService.
func NewAdminService(q *db.Q, cfg *config.Config) *AdminService {
	return &AdminService{q: q, cfg: cfg}
}

// WithAPIKeyStore sets the API key store for machine-to-machine auth endpoints (AUTH-3).
func (s *AdminService) WithAPIKeyStore(store auth.APIKeyStore, log auth.AuditLogger) *AdminService {
	s.apiKeys = store
	s.auditLog = log
	return s
}

// RegisterRoutes registers all admin/report-related routes under the given router group.
func (s *AdminService) RegisterRoutes(api fiber.Router, cfg *config.Config) {
	api.Post("/reports", jwtMiddleware(cfg), s.handleCreateReport)
	api.Post("/admin/verify", jwtMiddleware(cfg), s.handleAdminVerify)
	api.Post("/admin/collections/verify", jwtMiddleware(cfg), s.handleAdminVerifyCollection)
	api.Get("/admin/auctions/stalled", jwtMiddleware(cfg), s.handleStalledAuctions)
	api.Get("/admin/debug/tracked-collections", jwtMiddleware(cfg), s.handleTrackedCollections)

	// AUTH-3: API key management (admin-only).
	// POST   /admin/apikeys       — create a new API key (returns plaintext once).
	// GET    /admin/apikeys       — list all API keys for this admin.
	// DELETE /admin/apikeys/:id   — revoke an API key.
	if s.apiKeys != nil {
		api.Post("/admin/apikeys", jwtMiddleware(cfg), s.handleCreateAPIKey)
		api.Get("/admin/apikeys", jwtMiddleware(cfg), s.handleListAPIKeys)
		api.Delete("/admin/apikeys/:id", jwtMiddleware(cfg), s.handleRevokeAPIKey)
	}
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

// handleTrackedCollections returns the current tracked_collections table contents
// for operators diagnosing "why is WalletNFTs returning zero results". Admin-only.
func (s *AdminService) handleTrackedCollections(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" || !s.cfg.IsAdmin(addr) {
		return writeErr(c, fiber.StatusForbidden, "admin only")
	}
	addrs, err := s.q.ListTrackedCollections(c.Context())
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if addrs == nil {
		addrs = []string{}
	}
	return c.JSON(fiber.Map{
		"tracked_collections": addrs,
		"count":               len(addrs),
	})
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

// ── AUTH-3: API Key Management ────────────────────────────────────────────

type createAPIKeyRequest struct {
	Label       string   `json:"label"`
	Permissions []string `json:"permissions"`
}

func (s *AdminService) handleCreateAPIKey(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" || !s.cfg.IsAdmin(addr) {
		return writeErr(c, fiber.StatusForbidden, "admin only")
	}
	var req createAPIKeyRequest
	if err := bodyDecode(c, &req); err != nil {
		return writeErr(c, fiber.StatusBadRequest, "invalid request body")
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		return writeErr(c, fiber.StatusBadRequest, "label is required")
	}
	if len(req.Label) > 100 {
		return writeErr(c, fiber.StatusBadRequest, "label too long (max 100 chars)")
	}

	plaintext, hash, err := auth.GenerateAPIKey()
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "key generation failed")
	}

	id, err := s.apiKeys.Create(c.Context(), req.Label, addr, req.Permissions, hash, nil)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "key storage failed")
	}

	// Audit the creation.
	if s.auditLog != nil {
		auth.AuditAPIKeyCreated(s.auditLog, addr, ClientIP(c), c.Get("User-Agent"), req.Label)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":         id,
		"label":      req.Label,
		"api_key":    plaintext, // returned ONLY once — caller must store it
		"created_at": "now",
		"warning":    "Store this key securely — it will not be shown again.",
	})
}

func (s *AdminService) handleListAPIKeys(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" || !s.cfg.IsAdmin(addr) {
		return writeErr(c, fiber.StatusForbidden, "admin only")
	}
	keys, err := s.apiKeys.List(c.Context(), addr)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "list failed")
	}
	return c.JSON(fiber.Map{"api_keys": keys})
}

func (s *AdminService) handleRevokeAPIKey(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" || !s.cfg.IsAdmin(addr) {
		return writeErr(c, fiber.StatusForbidden, "admin only")
	}
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id <= 0 {
		return writeErr(c, fiber.StatusBadRequest, "invalid key id")
	}
	if err := s.apiKeys.Revoke(c.Context(), id, addr); err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "revoke failed")
	}
	if s.auditLog != nil {
		auth.AuditAPIKeyRevoked(s.auditLog, addr, ClientIP(c), c.Get("User-Agent"), id)
	}
	return c.JSON(fiber.Map{"id": id, "revoked": true})
}
