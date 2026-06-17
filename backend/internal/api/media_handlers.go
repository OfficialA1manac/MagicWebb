package api

import (
	"context"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/chain"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/media"
)

// listingPreflightWithChain reports fillability and repairs stale nft_ownership
// rows by verifying the seller on-chain when the DB projection is missing.
func listingPreflightWithChain(q *db.Q, eth chain.Caller) fiber.Handler {
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

		// On-chain verify before buy: repair missing ownership or orphan stale listings.
		if pf.Listed && !pf.Orphaned && eth != nil {
			owns, std, amt, verified, verr := verifySellerOnChain(c.Context(), eth, coll, tokenID, seller)
			if verr == nil && verified {
				if owns {
					if !pf.SellerOwns {
						_ = q.EnsureListingSellerOwnership(c.Context(), coll, tokenID, seller, std, amt)
						pf.SellerOwns = true
					}
				} else {
					_ = q.OrphanListing(c.Context(), coll, tokenID, seller)
					pf.Orphaned = true
					pf.Listed = false
					pf.SellerOwns = false
				}
			}
		}

		ok := pf.Listed && pf.SellerOwns && !pf.Orphaned
		return c.JSON(fiber.Map{
			"ok":          ok,
			"listed":      pf.Listed,
			"orphaned":    pf.Orphaned,
			"seller_owns": pf.SellerOwns,
			"price_wei":   pf.PriceWei,
		})
	}
}

// verifySellerOnChain returns verified=true when RPC returned a definitive answer.
func verifySellerOnChain(ctx context.Context, eth chain.Caller, collection, tokenID, seller string) (owns bool, standard string, amount int64, verified bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	owner, err721 := chain.OwnerOf721(ctx, eth, collection, tokenID)
	if err721 == nil {
		if chain.SameAddr(owner, seller) {
			return true, "erc721", 1, true, nil
		}
		return false, "erc721", 1, true, nil
	}

	bal, err1155 := chain.Balance1155(ctx, eth, collection, tokenID, seller)
	if err1155 == nil {
		if bal.Sign() > 0 {
			return true, "erc1155", bal.Int64(), true, nil
		}
		return false, "erc1155", 0, true, nil
	}
	return false, "", 0, false, err721
}

// mediaProxy serves external NFT images through same-origin with SSRF guards.
func mediaProxy() fiber.Handler {
	return func(c *fiber.Ctx) error {
		raw := c.Query("url")
		if raw == "" || !media.ProxyAllowed(raw) {
			return c.Status(fiber.StatusBadRequest).SendString("invalid url")
		}
		body, err := media.FetchBytes(c.Context(), raw, "")
		if err != nil {
			return c.Status(fiber.StatusBadGateway).SendString("upstream unavailable")
		}
		ct := detectContentType(raw, body)
		c.Set("Content-Type", ct)
		c.Set("Cache-Control", "public, max-age=86400")
		return c.Send(body)
	}
}

func detectContentType(url string, body []byte) string {
	lower := strings.ToLower(url)
	switch {
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	case strings.HasSuffix(lower, ".svg"):
		return "image/svg+xml"
	}
	if len(body) > 0 && body[0] == '{' {
		return "application/json"
	}
	return "image/jpeg"
}
