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
	// chainID namespaces the cache keys (cache.Key). Set by Mount.
	chainID uint64
}

// NewWalletService creates a WalletService. explorerURL is the network's
// Blockscout base (config.C.ExplorerURL); empty disables the explorer merge.
// redisURL selects the shared cache backend (empty = per-instance memory).
func NewWalletService(q *db.Q, explorerURL, redisURL string) *WalletService {
	return &WalletService{
		q:           q,
		explorerURL: strings.TrimRight(explorerURL, "/"),
		httpc:       &http.Client{Timeout: 5 * time.Second},
		// 2s: live-read requirement (owner directive 2026-09-01 — nothing
		// user-visible may lag >1s beyond transport). The cache only
		// deduplicates bursts (grid + header ask together); every navigation
		// re-reads the wallet live.
		merged: cache.NewRedisOrMemory(redisURL, 2*time.Second),
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

	if v, ok := s.merged.Get(cache.Key(s.chainID, "wallet-nfts", addr)); ok {
		// cachedBytes, not a []byte assertion: the Redis backend
		// JSON-round-trips cache values into strings, so a bare assertion
		// discards every hit whenever REDIS_URL is set.
		if body := cachedBytes(v); body != nil {
			src := "db"
			if sv, ok := s.merged.Get(cache.Key(s.chainID, "wallet-nfts-src", addr)); ok {
				if b := cachedBytes(sv); b != nil {
					src = string(b)
				}
			}
			c.Set("X-MW-Wallet-Source", src)
			// A cached view is always non-degraded (degraded views are never
			// cached), so it gets the healthy short private cache.
			c.Set("Cache-Control", "private, max-age=2, stale-while-revalidate=10")
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
	// Signal a degraded inventory: an explorer is configured but its fan-out
	// failed, so this list may be missing the wallet's explorer-held NFTs.
	// The client treats this like a failed refresh and keeps last-known-good
	// rather than repainting a shrunken/empty grid. (Cache hits never reach
	// here — degraded views are never cached; see below.)
	if s.explorerURL != "" && source == "db" {
		c.Set("X-MW-Degraded", "explorer-unavailable")
		// Degraded: force a live retry, never let the browser cache a
		// possibly-incomplete inventory (mirrors the server-cache rule below).
		c.Set("Cache-Control", "no-store")
	} else {
		// Healthy: short private cache + SWR, matching the 2s live-read TTL.
		c.Set("Cache-Control", "private, max-age=2, stale-while-revalidate=10")
	}

	body, err := json.Marshal(merged)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	// Never cache a DEGRADED view. When an explorer is configured but the
	// fan-out failed (source stays "db"), this response may be missing the
	// wallet's whole explorer inventory — caching it made the profile show
	// "0 NFTs" for a full TTL after one explorer hiccup (reported 2026-09-01
	// as the profile being "thrown off after switching tabs"). Serve it, but
	// let the next request retry live.
	if !(s.explorerURL != "" && source == "db") {
		// Stored as a string — see cachedBytes: a []byte would return from
		// Redis base64-encoded and be served as the response body.
		s.merged.Set(cache.Key(s.chainID, "wallet-nfts", addr), string(body))
		s.merged.Set(cache.Key(s.chainID, "wallet-nfts-src", addr), source)
	}
	c.Set("Content-Type", "application/json")
	return c.Send(body)
}

// blockscoutNFTItem is one owned-NFT entry from Blockscout's
// /api/v2/addresses/{addr}/nft response.
type blockscoutNFTItem struct {
	ID       string `json:"id"`
	Value    string `json:"value"`
	ImageURL string `json:"image_url"`
	// Newer Blockscout puts the standard on the item as token_type.
	TokenType string `json:"token_type"`
	Metadata  struct {
		Name  string `json:"name"`
		Image string `json:"image"`
	} `json:"metadata"`
	Token struct {
		// Older Blockscout: "address". Newer (Flare/Songbird explorers):
		// "address_hash". Accept both — the rename silently emptied every
		// merged item's collection and dropped the whole explorer inventory.
		Address     string `json:"address"`
		AddressHash string `json:"address_hash"`
		Name        string `json:"name"`
		Type        string `json:"type"` // "ERC-721" | "ERC-1155"
	} `json:"token"`
}

// collectionAddr returns the token's contract address across Blockscout
// schema versions.
func (it blockscoutNFTItem) collectionAddr() string {
	if it.Token.Address != "" {
		return it.Token.Address
	}
	return it.Token.AddressHash
}

// standard returns the token standard across Blockscout schema versions.
func (it blockscoutNFTItem) standard() string {
	if it.Token.Type != "" {
		return it.Token.Type
	}
	return it.TokenType
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
		dec := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
		// Cursor values can be token IDs above 2^53-1; default float64 decoding
		// would corrupt them before they are echoed back as query params.
		dec.UseNumber()
		decErr := dec.Decode(&page)
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
				case json.Number:
					q.Set(k, t.String())
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

	// Index the explorer items so DB rows can be ENRICHED, not just deduped.
	// A DB ownership row can exist while its metadata/media joins are still
	// empty (e.g. right after an indexer checkpoint reset) — it would win the
	// merge and shadow an explorer item that carries a perfectly good live
	// image URL, rendering the wallet grid as placeholders. Live-read rule:
	// whatever field the DB doesn't have yet, take from the explorer.
	byKey := make(map[string]blockscoutNFTItem, len(items))
	for _, it := range items {
		k := strings.ToLower(it.collectionAddr()) + "/" + it.ID
		byKey[k] = it
	}
	seen := make(map[string]bool, len(dbRows))
	for i := range dbRows {
		k := strings.ToLower(dbRows[i].Collection) + "/" + dbRows[i].TokenID
		seen[k] = true
		if it, ok := byKey[k]; ok {
			if dbRows[i].ImageURI == "" {
				if it.ImageURL != "" {
					dbRows[i].ImageURI = it.ImageURL
				} else if it.Metadata.Image != "" {
					dbRows[i].ImageURI = it.Metadata.Image
				}
			}
			if dbRows[i].Name == "" {
				if it.Metadata.Name != "" {
					dbRows[i].Name = it.Metadata.Name
				} else if n := strings.TrimSpace(it.Token.Name); n != "" {
					dbRows[i].Name = n
				}
			}
		}
	}
	out := dbRows
	for _, it := range items {
		coll := strings.ToLower(it.collectionAddr())
		if coll == "" || it.ID == "" || seen[coll+"/"+it.ID] {
			continue
		}
		seen[coll+"/"+it.ID] = true
		std := "erc721"
		if strings.EqualFold(it.standard(), "ERC-1155") {
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
