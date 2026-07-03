package api

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// NotificationsService handles notification-related API operations.
type NotificationsService struct {
	q *db.Q
}

// NewNotificationsService creates a NotificationsService.
func NewNotificationsService(q *db.Q) *NotificationsService {
	return &NotificationsService{q: q}
}

// RegisterRoutes registers all notification-related routes under the given router group.
func (s *NotificationsService) RegisterRoutes(api fiber.Router, cfg *config.Config) {
	api.Get("/notifications", jwtMiddleware(cfg), s.handleList)
	api.Post("/notifications/read", jwtMiddleware(cfg), s.handleMarkRead)
}

// Caller returns the SIWE-authenticated address from the JWT middleware, if any.
func Caller(c *fiber.Ctx) string {
	return caller(c)
}

// caller returns the SIWE-authenticated address from the JWT middleware, if any.
func caller(c *fiber.Ctx) string {
	if a, ok := c.Locals(string(auth.CallerKey)).(string); ok {
		return strings.ToLower(a)
	}
	return ""
}

func (s *NotificationsService) handleList(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" {
		return writeErr(c, fiber.StatusUnauthorized, "unauthorized")
	}
	limit := 50
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 {
		if n > 200 {
			n = 200
		}
		limit = n
	}
	rows, err := s.q.ListNotifications(c.Context(), addr, limit)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if rows == nil {
		rows = []db.NotificationRow{}
	}
	unread, err := s.q.UnreadCount(c.Context(), addr)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	return c.JSON(fiber.Map{"notifications": rows, "unread": unread})
}

func (s *NotificationsService) handleMarkRead(c *fiber.Ctx) error {
	addr := caller(c)
	if addr == "" {
		return writeErr(c, fiber.StatusUnauthorized, "unauthorized")
	}
	if err := s.q.MarkNotificationsRead(c.Context(), addr); err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	return c.SendStatus(fiber.StatusNoContent)
}
