package api

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// walletNFTs returns every NFT held by an address with market-context flags.
func walletNFTs(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := strings.ToLower(strings.TrimSpace(c.Params("addr")))
		if !isHexAddr(addr) {
			return writeErr(c, fiber.StatusBadRequest, "invalid address")
		}
		rows, err := q.GetWalletNFTs(c.Context(), addr)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		if rows == nil {
			rows = []db.WalletNFT{}
		}
		return c.JSON(rows)
	}
}

// PreflightResult describes whether a buyer is likely to succeed in calling buy().
type PreflightResult struct {
	CanBuy        bool             `json:"can_buy"`
	Reason        string           `json:"reason,omitempty"`
	Stale         bool             `json:"stale"`
	OnchainOwner  string           `json:"onchain_owner,omitempty"`
	ApprovalOK    bool             `json:"approval_ok"`
	Listings      []db.ListingRow  `json:"listings"`
}

// preflight inspects the on-chain owner + approval + DB listings for a token
// and returns whether buying is likely to succeed. Used by the UI to short-circuit
// doomed transactions and to disambiguate ERC-1155 multi-seller listings.
func preflight(q *db.Q, eth *ethclient.Client, marketplaceAddr string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		coll := strings.ToLower(strings.TrimSpace(c.Params("coll")))
		id := strings.TrimSpace(c.Params("id"))
		if !isHexAddr(coll) || id == "" {
			return writeErr(c, fiber.StatusBadRequest, "invalid params")
		}
		listings, err := q.GetListingsForToken(c.Context(), coll, id)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		res := PreflightResult{Listings: listings}
		if len(listings) == 0 {
			res.Reason = "no active listings"
			return c.JSON(res)
		}

		// Pick the cheapest active listing for the on-chain check.
		l := listings[0]
		owner, ok := onchainOwner(c.Context(), eth, coll, id)
		if ok {
			res.OnchainOwner = strings.ToLower(owner)
			if !strings.EqualFold(owner, l.Seller) {
				res.Stale = true
				res.Reason = "listing is stale — NFT has moved"
				return c.JSON(res)
			}
		}
		// Check approval (ERC-721 path only — 1155 uses isApprovedForAll which is the same)
		if marketplaceAddr != "" {
			approved := isApprovedForAll(c.Context(), eth, coll, l.Seller, marketplaceAddr)
			res.ApprovalOK = approved
			if !approved {
				res.Reason = "seller has not approved marketplace"
				return c.JSON(res)
			}
		}
		res.CanBuy = true
		return c.JSON(res)
	}
}

// tokenOfferPositions returns all pending offer positions on a token.
func tokenOfferPositions(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		coll := strings.ToLower(strings.TrimSpace(c.Params("coll")))
		id := strings.TrimSpace(c.Params("id"))
		rows, err := q.GetOfferPositionsForToken(c.Context(), coll, id)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		if rows == nil {
			rows = []db.OfferPositionRow{}
		}
		return c.JSON(rows)
	}
}

// userOfferPositions returns all pending offer positions for a bidder.
func userOfferPositions(q *db.Q) fiber.Handler {
	return func(c *fiber.Ctx) error {
		addr := strings.ToLower(strings.TrimSpace(c.Params("addr")))
		rows, err := q.GetOfferPositionsByBidder(c.Context(), addr)
		if err != nil {
			return writeErr(c, fiber.StatusInternalServerError, "internal error")
		}
		if rows == nil {
			rows = []db.OfferPositionRow{}
		}
		return c.JSON(rows)
	}
}

// ── on-chain helpers ──────────────────────────────────────────────────────

var ownerOfABI, _ = abi.JSON(strings.NewReader(`[
	{"type":"function","name":"ownerOf","inputs":[{"name":"id","type":"uint256"}],"outputs":[{"type":"address"}],"stateMutability":"view"}
]`))

var isApprovedForAllABI, _ = abi.JSON(strings.NewReader(`[
	{"type":"function","name":"isApprovedForAll","inputs":[{"name":"owner","type":"address"},{"name":"op","type":"address"}],"outputs":[{"type":"bool"}],"stateMutability":"view"}
]`))

func onchainOwner(ctx context.Context, eth *ethclient.Client, coll, tokenID string) (string, bool) {
	if eth == nil {
		return "", false
	}
	id := new(big.Int)
	if _, ok := id.SetString(tokenID, 10); !ok {
		return "", false
	}
	calldata, err := ownerOfABI.Pack("ownerOf", id)
	if err != nil {
		return "", false
	}
	addr := common.HexToAddress(coll)
	out, err := eth.CallContract(ctx, ethereum.CallMsg{To: &addr, Data: calldata}, nil)
	if err != nil || len(out) < 32 {
		return "", false
	}
	owner := common.BytesToAddress(out[12:32])
	return owner.Hex(), true
}

func isApprovedForAll(ctx context.Context, eth *ethclient.Client, coll, owner, op string) bool {
	if eth == nil {
		return false
	}
	calldata, err := isApprovedForAllABI.Pack("isApprovedForAll", common.HexToAddress(owner), common.HexToAddress(op))
	if err != nil {
		return false
	}
	addr := common.HexToAddress(coll)
	out, err := eth.CallContract(ctx, ethereum.CallMsg{To: &addr, Data: calldata}, nil)
	if err != nil || len(out) < 32 {
		return false
	}
	return out[31] == 1
}

func isHexAddr(s string) bool {
	if len(s) != 42 || s[:2] != "0x" {
		return false
	}
	for i := 2; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// _ silences the "fmt unused" if no path uses it. Keep import below the helpers.
var _ = fmt.Sprintf
