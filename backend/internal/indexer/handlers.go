package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

type handlers struct {
	q   *db.Q
	rdb *cache.Client
}

// chunk returns the i-th 32-byte ABI word from event data.
func chunk(data []byte, i int) []byte {
	if len(data) < (i+1)*32 {
		return make([]byte, 32)
	}
	return data[i*32 : (i+1)*32]
}

func addrStr(b []byte) string       { return common.BytesToAddress(b).Hex() }
func bigInt(b []byte) *big.Int      { return new(big.Int).SetBytes(b) }
func bigStr(b []byte) string        { return bigInt(b).String() }
func tsUnix(b []byte) time.Time     { return time.Unix(bigInt(b).Int64(), 0) }

func (h *handlers) pub(ctx context.Context, ch string, payload any) {
	b, _ := json.Marshal(payload)
	if err := h.rdb.Publish(ctx, ch, string(b)); err != nil {
		log.Warn().Err(err).Str("ch", ch).Msg("redis publish failed")
	}
}

// dispatch routes a log to the correct handler.
func (h *handlers) dispatch(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) == 0 {
		return nil
	}
	sel := l.Topics[0]
	switch sel {
	case TopicListed:
		return h.onListed(ctx, l, blockTime)
	case TopicCancelled:
		return h.onCancelled(ctx, l)
	case TopicBought:
		return h.onBought(ctx, l, blockTime)
	case TopicAuctionCreated:
		return h.onAuctionCreated(ctx, l)
	case TopicBidPlaced:
		return h.onBidPlaced(ctx, l, blockTime)
	case TopicAuctionSettled:
		return h.onAuctionSettled(ctx, l)
	case TopicAuctionCancelled:
		return h.onAuctionCancelled(ctx, l)
	case TopicOfferAccepted:
		return h.onOfferAccepted(ctx, l, blockTime)
	case TopicOffer1155Accepted:
		return h.onOffer1155Accepted(ctx, l, blockTime)
	}
	return nil
}

// ── Marketplace ───────────────────────────────────────────────────────────────

// Listed(address indexed coll, uint256 indexed id, address indexed seller,
//         uint8 standard, uint128 amount, uint128 price, uint64 expiresAt)
func (h *handlers) onListed(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) < 4 || len(l.Data) < 4*32 {
		return fmt.Errorf("onListed: short log tx=%s", l.TxHash.Hex())
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID    := bigStr(l.Topics[2].Bytes())
	seller     := addrStr(l.Topics[3].Bytes())
	standard   := "ERC721"
	if chunk(l.Data, 0)[31] == 1 {
		standard = "ERC1155"
	}
	amtRaw  := bigInt(chunk(l.Data, 1))
	priceWei := bigStr(chunk(l.Data, 2))
	expiresAt := tsUnix(chunk(l.Data, 3))
	listedAt  := time.Unix(int64(blockTime), 0)

	amount := int64(1)
	if standard == "ERC1155" {
		amount = amtRaw.Int64()
	}

	r := db.ListingRow{
		Collection: collection,
		TokenID:    tokenID,
		Seller:     seller,
		PriceWei:   priceWei,
		Amount:     amount,
		Standard:   standard,
		ExpiresAt:  expiresAt,
		ListedAt:   listedAt,
		TxHash:     l.TxHash.Hex(),
	}
	if err := h.q.UpsertListing(ctx, r); err != nil {
		return fmt.Errorf("onListed upsert: %w", err)
	}
	h.pub(ctx, "mktplace:events", map[string]any{"event": "Listed", "data": r})
	return nil
}

// Cancelled(address indexed coll, uint256 indexed id, address indexed seller)
func (h *handlers) onCancelled(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 3 {
		return fmt.Errorf("onCancelled: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID    := bigStr(l.Topics[2].Bytes())
	if err := h.q.DeactivateListing(ctx, collection, tokenID); err != nil {
		return fmt.Errorf("onCancelled: %w", err)
	}
	h.pub(ctx, "mktplace:events", map[string]any{
		"event": "Cancelled", "collection": collection, "tokenId": tokenID,
	})
	return nil
}

// Bought(address indexed coll, uint256 indexed id, address indexed buyer,
//        address seller, uint8 standard, uint128 amount, uint128 price, uint256 fee, uint256 royalty)
func (h *handlers) onBought(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) < 4 || len(l.Data) < 6*32 {
		return fmt.Errorf("onBought: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID    := bigStr(l.Topics[2].Bytes())
	buyer      := addrStr(l.Topics[3].Bytes())
	seller     := addrStr(chunk(l.Data, 0))
	priceWei   := bigStr(chunk(l.Data, 3))
	feeWei     := bigStr(chunk(l.Data, 4))
	royaltyWei := bigStr(chunk(l.Data, 5))
	occurredAt := time.Unix(int64(blockTime), 0)

	if err := h.q.DeactivateListing(ctx, collection, tokenID); err != nil {
		return fmt.Errorf("onBought deactivate: %w", err)
	}
	if err := h.q.InsertSale(ctx, collection, tokenID, seller, buyer,
		priceWei, feeWei, royaltyWei, l.TxHash.Hex(), l.BlockNumber, occurredAt); err != nil {
		return fmt.Errorf("onBought sale: %w", err)
	}
	h.pub(ctx, "mktplace:events", map[string]any{
		"event": "Bought", "collection": collection, "tokenId": tokenID,
		"buyer": buyer, "seller": seller, "priceWei": priceWei,
	})
	return nil
}

// ── AuctionHouse ──────────────────────────────────────────────────────────────

// AuctionCreated(uint256 indexed id, address indexed coll, uint256 indexed tokenId,
//                address seller, uint8 standard, uint128 amount, uint128 reserve, uint64 startsAt, uint64 endsAt)
func (h *handlers) onAuctionCreated(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 || len(l.Data) < 6*32 {
		return fmt.Errorf("onAuctionCreated: short log")
	}
	auctionID  := bigInt(l.Topics[1].Bytes()).Int64()
	collection := addrStr(l.Topics[2].Bytes())
	tokenID    := bigStr(l.Topics[3].Bytes())
	seller     := addrStr(chunk(l.Data, 0))
	standard   := "ERC721"
	if chunk(l.Data, 1)[31] == 1 {
		standard = "ERC1155"
	}
	reserve  := bigStr(chunk(l.Data, 3))
	startsAt := tsUnix(chunk(l.Data, 4))
	endsAt   := tsUnix(chunk(l.Data, 5))

	r := db.AuctionRow{
		AuctionID:       auctionID,
		Collection:      collection,
		TokenID:         tokenID,
		Seller:          seller,
		Standard:        standard,
		ReservePriceWei: reserve,
		HighestBidWei:   "0",
		HighestBidder:   "",
		MinIncrementBps: 500,
		StartsAt:        startsAt,
		EndsAt:          endsAt,
		Status:          "active",
		CreateTx:        l.TxHash.Hex(),
	}
	if err := h.q.UpsertAuction(ctx, r); err != nil {
		return fmt.Errorf("onAuctionCreated: %w", err)
	}
	h.pub(ctx, "auction:events", map[string]any{"event": "AuctionCreated", "data": r})
	return nil
}

// BidPlaced(uint256 indexed id, address indexed bidder, uint128 amount)
func (h *handlers) onBidPlaced(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) < 3 || len(l.Data) < 32 {
		return fmt.Errorf("onBidPlaced: short log")
	}
	auctionID := bigInt(l.Topics[1].Bytes()).Int64()
	bidder    := addrStr(l.Topics[2].Bytes())
	amtWei    := bigStr(chunk(l.Data, 0))
	placedAt  := time.Unix(int64(blockTime), 0)

	if err := h.q.InsertBid(ctx, auctionID, bidder, amtWei, l.TxHash.Hex(), placedAt); err != nil {
		return fmt.Errorf("onBidPlaced: %w", err)
	}
	// Also update the auction's highest bid in auctions table.
	row := db.AuctionRow{AuctionID: auctionID, HighestBidWei: amtWei, HighestBidder: bidder}
	if err := h.q.UpdateAuctionBid(ctx, row); err != nil {
		return fmt.Errorf("onBidPlaced updateBid: %w", err)
	}
	h.pub(ctx, "auction:events", map[string]any{
		"event": "BidPlaced", "auctionId": auctionID, "bidder": bidder, "amtWei": amtWei,
	})
	return nil
}

// AuctionSettled(uint256 indexed id, address indexed winner, address indexed seller,
//                uint128 amount, uint256 fee, uint256 royalty)
func (h *handlers) onAuctionSettled(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 || len(l.Data) < 3*32 {
		return fmt.Errorf("onAuctionSettled: short log")
	}
	auctionID := bigInt(l.Topics[1].Bytes()).Int64()
	if err := h.q.SetAuctionStatus(ctx, auctionID, "settled"); err != nil {
		return fmt.Errorf("onAuctionSettled: %w", err)
	}
	h.pub(ctx, "auction:events", map[string]any{
		"event": "AuctionSettled", "auctionId": auctionID,
		"winner": addrStr(l.Topics[2].Bytes()), "seller": addrStr(l.Topics[3].Bytes()),
		"amtWei": bigStr(chunk(l.Data, 0)),
	})
	return nil
}

// AuctionCancelled(uint256 indexed id)
func (h *handlers) onAuctionCancelled(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 2 {
		return fmt.Errorf("onAuctionCancelled: short log")
	}
	auctionID := bigInt(l.Topics[1].Bytes()).Int64()
	if err := h.q.SetAuctionStatus(ctx, auctionID, "cancelled"); err != nil {
		return fmt.Errorf("onAuctionCancelled: %w", err)
	}
	h.pub(ctx, "auction:events", map[string]any{"event": "AuctionCancelled", "auctionId": auctionID})
	return nil
}

// ── OfferBook ─────────────────────────────────────────────────────────────────

// OfferAccepted(address indexed coll, uint256 indexed tokenId, address indexed seller,
//               address bidder, uint128 amount, uint256 fee, uint256 royalty, uint64 nonce)
func (h *handlers) onOfferAccepted(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) < 4 || len(l.Data) < 5*32 {
		return fmt.Errorf("onOfferAccepted: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID    := bigStr(l.Topics[2].Bytes())
	seller     := addrStr(l.Topics[3].Bytes())
	bidder     := addrStr(chunk(l.Data, 0))
	amtWei     := bigStr(chunk(l.Data, 1))
	feeWei     := bigStr(chunk(l.Data, 2))
	royaltyWei := bigStr(chunk(l.Data, 3))
	occurredAt := time.Unix(int64(blockTime), 0)

	if err := h.q.InsertSale(ctx, collection, tokenID, seller, bidder,
		amtWei, feeWei, royaltyWei, l.TxHash.Hex(), l.BlockNumber, occurredAt); err != nil {
		return fmt.Errorf("onOfferAccepted: %w", err)
	}
	h.pub(ctx, "mktplace:events", map[string]any{
		"event": "OfferAccepted", "collection": collection, "tokenId": tokenID,
		"seller": seller, "bidder": bidder, "amtWei": amtWei,
	})
	return nil
}

// Offer1155Accepted(address indexed coll, uint256 indexed tokenId, address indexed seller,
//                   address bidder, uint128 units, uint128 amount, uint256 fee, uint256 royalty, uint64 nonce)
func (h *handlers) onOffer1155Accepted(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) < 4 || len(l.Data) < 6*32 {
		return fmt.Errorf("onOffer1155Accepted: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID    := bigStr(l.Topics[2].Bytes())
	seller     := addrStr(l.Topics[3].Bytes())
	bidder     := addrStr(chunk(l.Data, 0))
	amtWei     := bigStr(chunk(l.Data, 2))
	feeWei     := bigStr(chunk(l.Data, 3))
	royaltyWei := bigStr(chunk(l.Data, 4))
	occurredAt := time.Unix(int64(blockTime), 0)

	if err := h.q.InsertSale(ctx, collection, tokenID, seller, bidder,
		amtWei, feeWei, royaltyWei, l.TxHash.Hex(), l.BlockNumber, occurredAt); err != nil {
		return fmt.Errorf("onOffer1155Accepted: %w", err)
	}
	h.pub(ctx, "mktplace:events", map[string]any{
		"event": "Offer1155Accepted", "collection": collection, "tokenId": tokenID,
		"seller": seller, "bidder": bidder, "amtWei": amtWei,
	})
	return nil
}
