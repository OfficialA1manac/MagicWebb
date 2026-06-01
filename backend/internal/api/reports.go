package api

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

type reportReq struct {
	TargetKind string `json:"target_kind"` // "collection" | "token" | "profile"
	TargetRef  string `json:"target_ref"`
	Reason     string `json:"reason"`
	Notes      string `json:"notes"`
}

func createReport(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		caller, ok := c.Locals(string(auth.CallerKey)).(string)
		if !ok || caller == "" {
			return writeErr(c, fiber.StatusUnauthorized, "unauthorized")
		}
		var req reportReq
		if err := bodyDecode(c, &req); err != nil {
			return writeErr(c, fiber.StatusBadRequest, "invalid body")
		}
		req.TargetKind = strings.TrimSpace(req.TargetKind)
		req.TargetRef = strings.TrimSpace(req.TargetRef)
		req.Reason = strings.TrimSpace(req.Reason)
		if req.TargetKind == "" || req.TargetRef == "" || req.Reason == "" {
			return writeErr(c, fiber.StatusBadRequest, "target_kind, target_ref, reason required")
		}
		if req.TargetKind != "collection" && req.TargetKind != "token" && req.TargetKind != "profile" {
			return writeErr(c, fiber.StatusBadRequest, "invalid target_kind")
		}
		r := db.ReportRow{
			Reporter:   strings.ToLower(caller),
			TargetKind: req.TargetKind,
			TargetRef:  req.TargetRef,
			Reason:     req.Reason,
			Notes:      req.Notes,
		}
		id, err := q.InsertReport(c.Context(), r)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": id})
	}
}

func listReports(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		status := strings.TrimSpace(c.Query("status"))
		rows, err := q.ListReports(c.Context(), status, 100)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		if rows == nil {
			rows = []db.ReportRow{}
		}
		return c.JSON(rows)
	}
}
