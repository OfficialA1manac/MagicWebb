package api

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/webhook"
)

// WebhookService handles webhook configuration CRUD for user-registered
// marketplace event notification URLs (WH-3).
type WebhookService struct {
	q   *db.Q
	cfg *config.Config
}

// NewWebhookService creates a WebhookService.
func NewWebhookService(q *db.Q, cfg *config.Config) *WebhookService {
	return &WebhookService{q: q, cfg: cfg}
}

// RegisterRoutes registers webhook config CRUD endpoints under the given
// router group. All routes require JWT authentication (SIWE session).
// POST   /webhooks       — create a new webhook config
// GET    /webhooks       — list all webhook configs for the authenticated user
// DELETE /webhooks/:id   — delete a webhook config
func (s *WebhookService) RegisterRoutes(api fiber.Router, cfg *config.Config) {
	api.Post("/webhooks", jwtMiddleware(cfg), s.handleCreateWebhook)
	api.Get("/webhooks", jwtMiddleware(cfg), s.handleListWebhooks)
	api.Delete("/webhooks/:id", jwtMiddleware(cfg), s.handleDeleteWebhook)
}

// ── Request / response types ──────────────────────────────────────────────

type createWebhookRequest struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

type webhookResponse struct {
	ID        int64    `json:"id"`
	URL       string   `json:"url"`
	Events    []string `json:"events"`
	Active    bool     `json:"active"`
	Secret    string   `json:"secret,omitempty"` // returned ONLY at creation time
	CreatedAt string   `json:"created_at,omitempty"`
}

// ── Handlers ──────────────────────────────────────────────────────────────

func (s *WebhookService) handleCreateWebhook(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" {
		return writeErr(c, fiber.StatusUnauthorized, "unauthorized")
	}

	var req createWebhookRequest
	if err := bodyDecode(c, &req); err != nil {
		return writeErr(c, fiber.StatusBadRequest, "invalid request body")
	}

	// Validate URL.
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" {
		return writeErr(c, fiber.StatusBadRequest, "url is required")
	}
	if len(req.URL) > 2048 {
		return writeErr(c, fiber.StatusBadRequest, "url too long (max 2048 chars)")
	}
	if !strings.HasPrefix(req.URL, "https://") && !strings.HasPrefix(req.URL, "http://") {
		return writeErr(c, fiber.StatusBadRequest, "url must start with https:// or http://")
	}

	// Validate events.
	if len(req.Events) == 0 {
		return writeErr(c, fiber.StatusBadRequest, "at least one event type is required")
	}
	if len(req.Events) > 20 {
		return writeErr(c, fiber.StatusBadRequest, "too many event types (max 20)")
	}

	// Deduplicate and validate each event type.
	seen := make(map[string]bool, len(req.Events))
	validated := make([]string, 0, len(req.Events))
	for _, e := range req.Events {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if seen[e] {
			continue
		}
		seen[e] = true
		if !webhook.ValidEvents[webhook.MarketplaceEventType(e)] {
			return writeErr(c, fiber.StatusBadRequest, "invalid event type: "+e)
		}
		validated = append(validated, e)
	}
	if len(validated) == 0 {
		return writeErr(c, fiber.StatusBadRequest, "at least one valid event type is required")
	}

	// Sort for deterministic storage order.
	sort.Strings(validated)

	// Generate a random HMAC secret (32 bytes, hex-encoded).
	var secretBytes [32]byte
	if _, err := rand.Read(secretBytes[:]); err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "secret generation failed")
	}
	secret := hex.EncodeToString(secretBytes[:])

	// Enforce per-user limit of 10 active webhook configs.
	// Count existing active configs BEFORE inserting the new one.
	existing, err := s.q.ListWebhookConfigs(c.Context(), addr)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "list failed")
	}
	activeCount := 0
	for _, cfg := range existing {
		if cfg.Active {
			activeCount++
		}
	}
	if activeCount >= 10 {
		return writeErr(c, fiber.StatusTooManyRequests, "maximum 10 active webhook configs per user")
	}

	// Create the config.
	id, err := s.q.CreateWebhookConfig(c.Context(), addr, req.URL, secret, validated)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "webhook creation failed")
	}

	// Return the secret ONCE — the user must store it to verify HMAC
	// signatures on incoming webhook deliveries.
	return c.Status(fiber.StatusCreated).JSON(webhookResponse{
		ID:     id,
		URL:    req.URL,
		Events: validated,
		Active: true,
		Secret: secret,
	})
}

func (s *WebhookService) handleListWebhooks(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" {
		return writeErr(c, fiber.StatusUnauthorized, "unauthorized")
	}

	rows, err := s.q.ListWebhookConfigs(c.Context(), addr)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "list failed")
	}
	if rows == nil {
		rows = []db.WebhookConfigRow{}
	}

	out := make([]webhookResponse, 0, len(rows))
	for _, r := range rows {
		if r.Events == nil {
			r.Events = []string{}
		}
		out = append(out, webhookResponse{
			ID:     r.ID,
			URL:    r.URL,
			Events: r.Events,
			Active: r.Active,
		})
	}

	return c.JSON(fiber.Map{"webhooks": out})
}

func (s *WebhookService) handleDeleteWebhook(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" {
		return writeErr(c, fiber.StatusUnauthorized, "unauthorized")
	}

	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id <= 0 {
		return writeErr(c, fiber.StatusBadRequest, "invalid webhook id")
	}

	deleted, err := s.q.DeleteWebhookConfig(c.Context(), id, addr)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "delete failed")
	}
	if !deleted {
		return writeErr(c, fiber.StatusNotFound, "webhook not found")
	}

	return c.JSON(fiber.Map{"id": id, "deleted": true})
}
