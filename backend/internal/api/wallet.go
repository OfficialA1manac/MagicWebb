package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
			src := "db"
			if sv, ok3 := s.merged.Get("wallet-nfts-src:" + addr); ok3 {
				if str, ok4 := sv.(string); ok4 {
					src = str
				}
			}
			c.Set("X-MW-Wallet-Source", src)
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
	s.merged.Set("wallet-nfts-src:"+addr, source)
	c.Set("Content-Type", "application/json")
	return c.Send(body)
}

// blockscoutNFTItem is one owned-NFT entry from Blockscout's
// /api/v2/addresses/{addr}/nft response.
type blockscoutNFTItem struct {
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
}

// blockscoutNFTPage is the subset of Blockscout's paginated response the
// merge needs.
type blockscoutNFTPage struct {
	Items []blockscoutNFTItem `json:"items"`
	// NextPageParams is Blockscout's keyset cursor: append its key/values as
	// query parameters to fetch the next page; null when done.
	NextPageParams map[string]any `json:"next_page_params"`
}

// mergeExplorerNFTs unions the DB inventory with the explorer's view of the
// wallet. DB rows win on conflict (they carry our metadata/media joins).
// Any explorer failure degrades silently to the DB-only view — the wallet
// page must never break because a third-party API is down.
func (s *WalletService) mergeExplorerNFTs(ctx context.Context, addr string, dbRows []db.OwnedNFT) ([]db.OwnedNFT, string) {
	if s.explorerURL == "" {
		return dbRows, "db"
	}

	baseURL := s.explorerURL + "/api/v2/addresses/" + url.PathEscape(addr) + "/nft?type=" + url.QueryEscape("ERC-721,ERC-1155")

	// Follow Blockscout's keyset pagination. Pages are ~50 items; cap the
	// walk so one enormous wallet cannot stall the profile request — 4 pages
	// (~200 NFTs) is far beyond what the grid renders anyway.
	const maxPages = 4
	var items []blockscoutNFTItem
	pageURL := baseURL
	for p := 0; p < maxPages && pageURL != ""; p++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
		if err != nil {
			return dbRows, "db"
		}
		resp, err := s.httpc.Do(req)
		if err != nil {
			log.Debug().Err(err).Msg("wallet-nfts: explorer fetch failed, serving db-only")
			return dbRows, "db"
		}
		var page blockscoutNFTPage
		decErr := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&page)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || decErr != nil {
			log.Debug().Int("status", resp.StatusCode).Err(decErr).Msg("wallet-nfts: explorer page failed, serving db-only")
			return dbRows, "db"
		}
		items = append(items, page.Items...)
		pageURL = ""
		if len(page.NextPageParams) > 0 {
			q := url.Values{}
			q.Set("type", "ERC-721,ERC-1155")
			for k, v := range page.NextPageParams {
				switch t := v.(type) {
				case string:
					q.Set(k, t)
				case float64:
					q.Set(k, strconv.FormatFloat(t, 'f', -1, 64))
				case bool:
					if t {
						q.Set(k, "true")
					} else {
						q.Set(k, "false")
					}
				}
			}
			pageURL = s.explorerURL + "/api/v2/addresses/" + url.PathEscape(addr) + "/nft?" + q.Encode()
		}
	}

	seen := make(map[string]bool, len(dbRows))
	for _, r := range dbRows {
		seen[strings.ToLower(r.Collection)+"/"+r.TokenID] = true
	}
	out := dbRows
	for _, it := range items {
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
