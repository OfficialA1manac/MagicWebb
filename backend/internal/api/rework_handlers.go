package api

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// caller returns the SIWE-authenticated address from the JWT middleware, if any.
func caller(c *fiber.Ctx) string {
	if a, ok := c.Locals(string(auth.CallerKey)).(string); ok {
		return strings.ToLower(a)
	}
	return ""
}

// ── Wallet NFTs (picker source) ────────────────────────────────────────────

func walletNFTs(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := strings.ToLower(c.Params("addr"))
		if addr == "" {
			return writeErr(c, fiber.StatusBadRequest, "address required")
		}
		nfts, err := q.WalletNFTs(c.Context(), addr)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		if nfts == nil {
			nfts = []db.OwnedNFT{}
		}
		return c.JSON(nfts)
	}
}

// ── Collection trait values (listing filters) ─────────────────────────────

func collectionTraits(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		coll := strings.ToLower(c.Params("address"))
		m, err := q.ListTraitValues(c.Context(), coll)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		if m == nil {
			m = map[string][]string{}
		}
		return c.JSON(m)
	}
}

// ── Buy preflight (stale-listing guard) ────────────────────────────────────

func listingPreflight(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		coll := strings.ToLower(c.Params("collection"))
		tokenID := c.Params("id")
		seller := strings.ToLower(c.Query("seller"))
		if seller == "" {
			return writeErr(c, fiber.StatusBadRequest, "seller query param required")
		}
		pf, err := q.ListingPreflight(c.Context(), coll, tokenID, seller)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		pf.SellerOwns = pf.SellerOwns && pf.Listed
		return c.JSON(fiber.Map{
			"ok":          pf.Listed && pf.SellerOwns && !pf.Orphaned,
			"listed":      pf.Listed,
			"orphaned":    pf.Orphaned,
			"seller_owns": pf.SellerOwns,
			"price_wei":   pf.PriceWei,
		})
	}
}

// ── Notifications ──────────────────────────────────────────────────────────

func listNotifications(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := caller(c)
		if addr == "" {
			return writeErr(c, fiber.StatusUnauthorized, "unauthorized")
		}
		limit := 50
		if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 {
			limit = n
		}
		rows, err := q.ListNotifications(c.Context(), addr, limit)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		unread, _ := q.UnreadCount(c.Context(), addr)
		if rows == nil {
			rows = []db.NotificationRow{}
		}
		return c.JSON(fiber.Map{"notifications": rows, "unread": unread})
	}
}

func markNotificationsRead(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := caller(c)
		if addr == "" {
			return writeErr(c, fiber.StatusUnauthorized, "unauthorized")
		}
		if err := q.MarkNotificationsRead(c.Context(), addr); err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

// ── Profiles ───────────────────────────────────────────────────────────────

func getProfile(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := strings.ToLower(c.Params("addr"))
		if addr == "" {
			return writeErr(c, fiber.StatusBadRequest, "address required")
		}
		p, err := q.GetProfile(c.Context(), addr)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		return c.JSON(p)
	}
}

type profileUpdate struct {
	DisplayName string `json:"display_name"`
	Bio         string `json:"bio"`
	AvatarURI   string `json:"avatar_uri"`
	BannerURI   string `json:"banner_uri"`
	Twitter     string `json:"twitter"`
	Website     string `json:"website"`
}

func putProfile(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := caller(c)
		if addr == "" {
			return writeErr(c, fiber.StatusUnauthorized, "unauthorized")
		}
		// A caller may only edit their own profile.
		if target := strings.ToLower(c.Params("addr")); target != "" && target != addr {
			return writeErr(c, fiber.StatusForbidden, "cannot edit another profile")
		}
		var u profileUpdate
		if err := bodyDecode(c, &u); err != nil {
			return writeErr(c, fiber.StatusBadRequest, "invalid request body")
		}
		if len(u.DisplayName) > 64 || len(u.Bio) > 500 {
			return writeErr(c, fiber.StatusBadRequest, "field too long")
		}
		p := db.ProfileRow{
			Address: addr, DisplayName: u.DisplayName, Bio: u.Bio,
			AvatarURI: u.AvatarURI, BannerURI: u.BannerURI, Twitter: u.Twitter, Website: u.Website,
		}
		if err := q.UpsertProfile(c.Context(), p); err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		return c.JSON(p)
	}
}

// ── Reports ────────────────────────────────────────────────────────────────

type reportRequest struct {
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Reason     string `json:"reason"`
	Detail     string `json:"detail"`
}

func createReport(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
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
		if err := q.InsertReport(c.Context(), addr, req.TargetType, req.TargetID, req.Reason, req.Detail); err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		return c.SendStatus(fiber.StatusCreated)
	}
}

// ── Admin verify (env allowlist + SIWE JWT) ────────────────────────────────

type verifyRequest struct {
	Address  string `json:"address"`
	Verified bool   `json:"verified"`
}

func adminVerify(q *db.Q, cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := caller(c)
		if addr == "" || !cfg.IsAdmin(addr) {
			return writeErr(c, fiber.StatusForbidden, "admin only")
		}
		var req verifyRequest
		if err := bodyDecode(c, &req); err != nil || req.Address == "" {
			return writeErr(c, fiber.StatusBadRequest, "address required")
		}
		if err := q.SetVerified(c.Context(), strings.ToLower(req.Address), req.Verified); err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		return c.JSON(fiber.Map{"address": strings.ToLower(req.Address), "verified": req.Verified})
	}
}
