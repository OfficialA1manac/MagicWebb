package api

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

func listNotifications(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		caller, ok := c.Locals(string(auth.CallerKey)).(string)
		if !ok || caller == "" {
			return writeErr(c, fiber.StatusUnauthorized, "unauthorized")
		}
		unread := c.Query("unread") == "true"
		limit := 50
		if l := c.Query("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				limit = n
			}
		}
		rows, err := q.ListNotifications(c.Context(), strings.ToLower(caller), unread, limit)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		count, _ := q.CountUnreadNotifications(c.Context(), strings.ToLower(caller))
		return c.JSON(fiber.Map{"items": rows, "unread": count})
	}
}

func markRead(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		caller, ok := c.Locals(string(auth.CallerKey)).(string)
		if !ok || caller == "" {
			return writeErr(c, fiber.StatusUnauthorized, "unauthorized")
		}
		if err := q.MarkNotificationsRead(c.Context(), strings.ToLower(caller)); err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}
