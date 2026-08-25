package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// WalletService handles wallet-related API operations.
//
// The NFT inventory endpoint merges two sources:
//   - nft_ownership rows (indexed Transfer events of tracked collections
//     since indexFromBlock) — fast, carries our metadata/media joins;
//   - the network's Blockscout explorer, which knows the wallet's FULL
//     holdings including collections we don't track and tokens minted
//     before indexFromBlock. Without this the profile shows an empty
//     wallet to anyone whose NFTs predate the current contract set.
type WalletService struct {
	q           *db.Q
	explorerURL string
	httpc       *http.Client
	merged      cache.CacheInterface // 30s per-address cache of the merged inventory
}

// NewWalletService creates a WalletService. explorerURL is the network's
// Blockscout base (config.C.ExplorerURL); empty disables the explorer merge.
// redisURL selects the shared cache backend (empty = per-instance memory).
func NewWalletService(q *db.Q, explorerURL, redisURL string) *WalletService {
	return &WalletService{
		q:           q,
		explorerURL: strings.TrimRight(explorerURL, "/"),
		httpc:       &http.Client{Timeout: 5 * time.Second},
		merged:      cache.NewRedisOrMemory(redisURL, 30*time.Second),
	}
}

// RegisterRoutes registers all wallet-related routes under the given router group.
func (s *WalletService) RegisterRoutes(api fiber.Router) {
	api.Get("/wallet/:addr/nfts", s.handleNFTs)
}

func (s *WalletService) handleNFTs(c *fiber.Ctx) error {
	addr := strings.ToLower(c.Params("addr"))
	if !isValidHexAddress(addr) {
		return writeErr(c, fiber.StatusBadRequest, "address required")
	}

	if v, ok := s.merged.Get("wallet-nfts:" + addr); ok {
		if body, ok2 := v.([]byte); ok2 {
			c.Set("Content-Type", "application/json")
			return c.Send(body)
		}
	}

	nfts, err := s.q.WalletNFTs(c.Context(), addr)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	if nfts == nil {
		nfts = []db.OwnedNFT{}
	}
	if len(nfts) == 0 {
		log.Debug().
			Str("owner", addr).
			Msg("wallet-nfts: query returned zero results for owner — no nft_ownership rows found. Check that the NFT contract is in tracked_collections and the indexer has processed relevant Transfer events.")
	}

	merged, source := s.mergeExplorerNFTs(c.Context(), addr, nfts)
	c.Set("X-MW-Wallet-Source", source)

	body, err := json.Marshal(merged)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	s.merged.Set("wallet-nfts:"+addr, body)
	c.Set("Content-Type", "application/json")
	return c.Send(body)
}

// blockscoutNFTPage is the subset of Blockscout's
// /api/v2/addresses/{addr}/nft response the merge needs.
type blockscoutNFTPage struct {
	Items []struct {
		ID       string `json:"id"`
		Value    string `json:"value"`
		ImageURL string `json:"image_url"`
		Metadata struct {
			Name  string `json:"name"`
			Image string `json:"image"`
		} `json:"metadata"`
		Token struct {
			Address string `json:"address"`
			Name    string `json:"name"`
			Type    string `json:"type"` // "ERC-721" | "ERC-1155"
		} `json:"token"`
	} `json:"items"`
}

// mergeExplorerNFTs unions the DB inventory with the explorer's view of the
// wallet. DB rows win on conflict (they carry our metadata/media joins).
// Any explorer failure degrades silently to the DB-only view — the wallet
// page must never break because a third-party API is down.
func (s *WalletService) mergeExplorerNFTs(ctx context.Context, addr string, dbRows []db.OwnedNFT) ([]db.OwnedNFT, string) {
	if s.explorerURL == "" {
		return dbRows, "db"
	}

	reqURL := s.explorerURL + "/api/v2/addresses/" + url.PathEscape(addr) + "/nft?type=" + url.QueryEscape("ERC-721,ERC-1155")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return dbRows, "db"
	}
	resp, err := s.httpc.Do(req)
	if err != nil {
		log.Debug().Err(err).Msg("wallet-nfts: explorer fetch failed, serving db-only")
		return dbRows, "db"
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Debug().Int("status", resp.StatusCode).Msg("wallet-nfts: explorer non-200, serving db-only")
		return dbRows, "db"
	}

	var page blockscoutNFTPage
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&page); err != nil {
		log.Debug().Err(err).Msg("wallet-nfts: explorer decode failed, serving db-only")
		return dbRows, "db"
	}

	seen := make(map[string]bool, len(dbRows))
	for _, r := range dbRows {
		seen[strings.ToLower(r.Collection)+"/"+r.TokenID] = true
	}
	out := dbRows
	for _, it := range page.Items {
		coll := strings.ToLower(it.Token.Address)
		if coll == "" || it.ID == "" || seen[coll+"/"+it.ID] {
			continue
		}
		seen[coll+"/"+it.ID] = true
		std := "erc721"
		if strings.EqualFold(it.Token.Type, "ERC-1155") {
			std = "erc1155"
		}
		units := it.Value
		if units == "" {
			units = "1"
		}
		name := it.Metadata.Name
		if name == "" {
			name = strings.TrimSpace(it.Token.Name)
		}
		img := it.ImageURL
		if img == "" {
			img = it.Metadata.Image
		}
		out = append(out, db.OwnedNFT{
			Collection: coll,
			TokenID:    it.ID,
			Units:      units,
			Standard:   std,
			Name:       name,
			ImageURI:   img,
		})
	}
	return out, "db+explorer"
}
