package api

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// WalletService handles wallet-related API operations.
type WalletService struct {
	q *db.Q
}

// NewWalletService creates a WalletService.
func NewWalletService(q *db.Q) *WalletService {
	return &WalletService{q: q}
}

// RegisterRoutes registers all wallet-related routes under the given router group.
func (s *WalletService) RegisterRoutes(api fiber.Router) {
	api.Get("/wallet/:addr/nfts", s.handleNFTs)
}

func (s *WalletService) handleNFTs(c *fiber.Ctx) error {
	addr := strings.ToLower(c.Params("addr"))
	if addr == "" {
		return writeErr(c, fiber.StatusBadRequest, "address required")
	}
	nfts, err := s.q.WalletNFTs(c.Context(), addr)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if nfts == nil {
		nfts = []db.OwnedNFT{}
	}
	return c.JSON(nfts)
}
