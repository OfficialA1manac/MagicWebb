package indexer

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

type handlers struct {
	q     *db.Q
	bcast *sse.Broadcaster
}

// chunk returns the i-th 32-byte ABI word from event data.
func chunk(data []byte, i int) []byte {
	if len(data) < (i+1)*32 {
		return make([]byte, 32)
	}
	return data[i*32 : (i+1)*32]
}

func addrStr(b []byte) string   { return strings.ToLower(common.BytesToAddress(b).Hex()) }
func bigInt(b []byte) *big.Int  { return new(big.Int).SetBytes(b) }
func bigStr(b []byte) string    { return bigInt(b).String() }
func tsUnix(b []byte) time.Time { return time.Unix(bigInt(b).Int64(), 0) }

func (h *handlers) pub(evType string, payload any) {
	h.bcast.Publish(sse.Event{Type: evType, Data: payload})
}

// dispatch routes a marketplace event log to the correct handler.
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
	case TopicAuctionExtended:
		return h.onAuctionExtended(ctx, l)
	case TopicAuctionSettled:
		return h.onAuctionSettled(ctx, l)
	case TopicAuctionCancelled:
		return h.onAuctionCancelled(ctx, l)
	case TopicOfferPositionUpdated:
		return h.onOfferPositionUpdated(ctx, l, blockTime, "erc721", "1")
	case TopicOffer1155PositionUpdated:
		return h.onOffer1155PositionUpdated(ctx, l, blockTime)
	case TopicOfferRefunded:
		return h.onOfferRefunded(ctx, l, "erc721")
	case TopicOffer1155Refunded:
		return h.onOfferRefunded(ctx, l, "erc1155")
	case TopicOfferAccepted:
		return h.onOfferAccepted(ctx, l, blockTime, "erc721")
	case TopicOffer1155Accepted:
		return h.onOfferAccepted(ctx, l, blockTime, "erc1155")
	}
	return nil
}

// ── Marketplace ───────────────────────────────────────────────────────────────

// Listed(address indexed coll, uint256 indexed id, address indexed seller,
//        uint8 standard, uint128 amount, uint128 price, uint64 expiresAt)
func (h *handlers) onListed(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) < 4 || len(l.Data) < 4*32 {
		return fmt.Errorf("onListed: short log tx=%s", l.TxHash.Hex())
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID    := bigStr(l.Topics[2].Bytes())
	seller     := addrStr(l.Topics[3].Bytes())
	standard   := "erc721"
	if chunk(l.Data, 0)[31] == 1 {
		standard = "erc1155"
	}
	amtRaw   := bigInt(chunk(l.Data, 1))
	priceWei := bigStr(chunk(l.Data, 2))
	expiresAt := tsUnix(chunk(l.Data, 3))
	listedAt  := time.Unix(int64(blockTime), 0)

	amount := int64(1)
	if standard == "erc1155" {
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
	h.pub("listing-updated", map[string]any{"event": "Listed", "data": r})
	return nil
}

// Cancelled(address indexed coll, uint256 indexed id, address indexed seller)
func (h *handlers) onCancelled(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 {
		return fmt.Errorf("onCancelled: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID    := bigStr(l.Topics[2].Bytes())
	seller     := addrStr(l.Topics[3].Bytes())
	if err := h.q.DeactivateListing(ctx, collection, tokenID, seller); err != nil {
		return fmt.Errorf("onCancelled: %w", err)
	}
	h.pub("listing-updated", map[string]any{
		"event": "Cancelled", "collection": collection, "tokenId": tokenID, "seller": seller,
	})
	return nil
}

// Bought(address indexed coll, uint256 indexed id, address indexed buyer,
//        address seller, uint8 standard, uint128 amount, uint128 price, uint256 fee)
func (h *handlers) onBought(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) < 4 || len(l.Data) < 5*32 {
		return fmt.Errorf("onBought: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID    := bigStr(l.Topics[2].Bytes())
	buyer      := addrStr(l.Topics[3].Bytes())
	seller     := addrStr(chunk(l.Data, 0))
	priceWei   := bigStr(chunk(l.Data, 3))
	feeWei     := bigStr(chunk(l.Data, 4))
	occurredAt := time.Unix(int64(blockTime), 0)

	if err := h.q.DeactivateAndSale(ctx, collection, tokenID, seller, buyer,
		priceWei, feeWei, "0", l.TxHash.Hex(), l.BlockNumber, occurredAt); err != nil {
		return fmt.Errorf("onBought: %w", err)
	}
	h.pub("listing-updated", map[string]any{
		"event": "Bought", "collection": collection, "tokenId": tokenID,
		"buyer": buyer, "seller": seller, "priceWei": priceWei,
	})
	_ = h.notify(ctx, seller, "sale_settled", map[string]any{
		"collection": collection, "tokenId": tokenID, "buyer": buyer, "priceWei": priceWei,
	})
	return nil
}

// ── AuctionHouse ──────────────────────────────────────────────────────────────

// AuctionCreated(uint256 indexed id, address indexed coll, uint256 indexed tokenId,
//                address seller, uint8 standard, uint128 amount, uint128 reserve,
//                uint128 sellerFlatMinFLR, uint64 startsAt, uint64 endsAt)
func (h *handlers) onAuctionCreated(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 || len(l.Data) < 7*32 {
		return fmt.Errorf("onAuctionCreated: short log")
	}
	auctionID  := bigInt(l.Topics[1].Bytes()).Int64()
	collection := addrStr(l.Topics[2].Bytes())
	tokenID    := bigStr(l.Topics[3].Bytes())
	seller     := addrStr(chunk(l.Data, 0))
	standard   := "erc721"
	if chunk(l.Data, 1)[31] == 1 {
		standard = "erc1155"
	}
	reserve  := bigStr(chunk(l.Data, 3))
	startsAt := tsUnix(chunk(l.Data, 5))
	endsAt   := tsUnix(chunk(l.Data, 6))

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
	h.pub("auction-updated", map[string]any{"event": "AuctionCreated", "data": r})
	return nil
}

// BidPlaced(uint256 indexed id, address indexed bidder, uint128 bidAmount, uint128 fee)
func (h *handlers) onBidPlaced(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) < 3 || len(l.Data) < 32 {
		return fmt.Errorf("onBidPlaced: short log")
	}
	auctionID := bigInt(l.Topics[1].Bytes()).Int64()
	bidder    := addrStr(l.Topics[2].Bytes())
	amtWei    := bigStr(chunk(l.Data, 0))
	placedAt  := time.Unix(int64(blockTime), 0)

	if err := h.q.InsertBidAndUpdateAuction(ctx, auctionID, bidder, amtWei, l.TxHash.Hex(), placedAt); err != nil {
		return fmt.Errorf("onBidPlaced: %w", err)
	}
	h.pub("auction-updated", map[string]any{
		"event": "BidPlaced", "auctionId": auctionID, "bidder": bidder, "amtWei": amtWei,
	})
	// Notify any previously-leading bidder that they've been outbid (best effort — we don't
	// know who the previous leader was without an extra query, so just push to bidder for now).
	return nil
}

// AuctionExtended(uint256 indexed id, uint64 newEnd)
func (h *handlers) onAuctionExtended(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 2 || len(l.Data) < 32 {
		return fmt.Errorf("onAuctionExtended: short log")
	}
	auctionID := bigInt(l.Topics[1].Bytes()).Int64()
	newEnd    := tsUnix(chunk(l.Data, 0))
	if err := h.q.SetAuctionEndsAt(ctx, auctionID, newEnd); err != nil {
		return fmt.Errorf("onAuctionExtended: %w", err)
	}
	h.pub("auction-updated", map[string]any{
		"event": "AuctionExtended", "auctionId": auctionID, "newEnd": newEnd.Unix(),
	})
	return nil
}

// AuctionSettled(uint256 indexed id, address indexed winner, address indexed seller,
//                uint128 bidAmount, uint256 fee)
func (h *handlers) onAuctionSettled(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 || len(l.Data) < 2*32 {
		return fmt.Errorf("onAuctionSettled: short log")
	}
	auctionID := bigInt(l.Topics[1].Bytes()).Int64()
	if err := h.q.SetAuctionStatus(ctx, auctionID, "settled"); err != nil {
		return fmt.Errorf("onAuctionSettled: %w", err)
	}
	h.pub("auction-updated", map[string]any{
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
	h.pub("auction-updated", map[string]any{"event": "AuctionCancelled", "auctionId": auctionID})
	return nil
}

// ── OfferBook ─────────────────────────────────────────────────────────────────

// OfferPositionUpdated(address indexed coll, uint256 indexed tokenId, address indexed bidder,
//                      uint128 totalOffer, uint128 totalFeePaid, uint64 expiresAt)
func (h *handlers) onOfferPositionUpdated(ctx context.Context, l types.Log, blockTime uint64, standard, units string) error {
	if len(l.Topics) < 4 || len(l.Data) < 3*32 {
		return fmt.Errorf("onOfferPositionUpdated: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID    := bigStr(l.Topics[2].Bytes())
	bidder     := addrStr(l.Topics[3].Bytes())
	totalOffer := bigStr(chunk(l.Data, 0))
	totalFee   := bigStr(chunk(l.Data, 1))
	expiresAt  := tsUnix(chunk(l.Data, 2))

	if totalOffer == "0" {
		// Position cleared (accept / refund). Delete the row.
		if err := h.q.DeleteOfferPosition(ctx, collection, tokenID, bidder); err != nil {
			return fmt.Errorf("delete offer position: %w", err)
		}
		h.pub("offer-updated", map[string]any{
			"event": "OfferPositionCleared",
			"collection": collection, "tokenId": tokenID, "bidder": bidder,
		})
		return nil
	}

	firstAt := time.Unix(int64(blockTime), 0)
	if existing, _ := h.q.GetOfferPosition(ctx, collection, tokenID, bidder); existing != nil {
		firstAt = existing.FirstAt
	}

	r := db.OfferPositionRow{
		Collection:    collection,
		TokenID:       tokenID,
		Bidder:        bidder,
		Standard:      standard,
		Units:         units,
		TotalOfferWei: totalOffer,
		TotalFeeWei:   totalFee,
		FirstAt:       firstAt,
		ExpiresAt:     expiresAt,
		Status:        "pending",
	}
	if err := h.q.UpsertOfferPosition(ctx, r); err != nil {
		return fmt.Errorf("upsert offer position: %w", err)
	}
	h.pub("offer-updated", map[string]any{"event": "OfferPositionUpdated", "data": r})
	return nil
}

// Offer1155PositionUpdated(address indexed coll, uint256 indexed tokenId, address indexed bidder,
//                          uint128 totalOffer, uint128 totalFeePaid, uint128 units, uint64 expiresAt)
func (h *handlers) onOffer1155PositionUpdated(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) < 4 || len(l.Data) < 4*32 {
		return fmt.Errorf("onOffer1155PositionUpdated: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID    := bigStr(l.Topics[2].Bytes())
	bidder     := addrStr(l.Topics[3].Bytes())
	totalOffer := bigStr(chunk(l.Data, 0))
	totalFee   := bigStr(chunk(l.Data, 1))
	units      := bigStr(chunk(l.Data, 2))
	expiresAt  := tsUnix(chunk(l.Data, 3))

	if totalOffer == "0" {
		if err := h.q.DeleteOfferPosition(ctx, collection, tokenID, bidder); err != nil {
			return fmt.Errorf("delete offer position: %w", err)
		}
		h.pub("offer-updated", map[string]any{
			"event": "OfferPositionCleared",
			"collection": collection, "tokenId": tokenID, "bidder": bidder,
		})
		return nil
	}

	firstAt := time.Unix(int64(blockTime), 0)
	if existing, _ := h.q.GetOfferPosition(ctx, collection, tokenID, bidder); existing != nil {
		firstAt = existing.FirstAt
	}

	r := db.OfferPositionRow{
		Collection:    collection,
		TokenID:       tokenID,
		Bidder:        bidder,
		Standard:      "erc1155",
		Units:         units,
		TotalOfferWei: totalOffer,
		TotalFeeWei:   totalFee,
		FirstAt:       firstAt,
		ExpiresAt:     expiresAt,
		Status:        "pending",
	}
	if err := h.q.UpsertOfferPosition(ctx, r); err != nil {
		return fmt.Errorf("upsert offer position: %w", err)
	}
	h.pub("offer-updated", map[string]any{"event": "Offer1155PositionUpdated", "data": r})
	return nil
}

// OfferRefunded(address indexed coll, uint256 indexed tokenId, address indexed bidder, uint128 amount)
func (h *handlers) onOfferRefunded(ctx context.Context, l types.Log, _standard string) error {
	if len(l.Topics) < 4 {
		return fmt.Errorf("onOfferRefunded: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID    := bigStr(l.Topics[2].Bytes())
	bidder     := addrStr(l.Topics[3].Bytes())
	if err := h.q.SetOfferPositionStatus(ctx, collection, tokenID, bidder, "expired"); err != nil {
		return fmt.Errorf("onOfferRefunded: %w", err)
	}
	h.pub("offer-updated", map[string]any{
		"event": "OfferRefunded", "collection": collection, "tokenId": tokenID, "bidder": bidder,
	})
	return nil
}

// OfferAccepted(address indexed coll, uint256 indexed tokenId, address indexed seller,
//               address bidder, uint128 amount, uint256 fee)
func (h *handlers) onOfferAccepted(ctx context.Context, l types.Log, blockTime uint64, standard string) error {
	if len(l.Topics) < 4 || len(l.Data) < 3*32 {
		return fmt.Errorf("onOfferAccepted: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID    := bigStr(l.Topics[2].Bytes())
	seller     := addrStr(l.Topics[3].Bytes())
	bidder     := addrStr(chunk(l.Data, 0))
	amtWei     := bigStr(chunk(l.Data, 1))
	feeWei     := bigStr(chunk(l.Data, 2))
	occurredAt := time.Unix(int64(blockTime), 0)

	if err := h.q.SetOfferPositionStatus(ctx, collection, tokenID, bidder, "accepted"); err != nil {
		return fmt.Errorf("onOfferAccepted: set status: %w", err)
	}
	if err := h.q.InsertSale(ctx, collection, tokenID, seller, bidder,
		amtWei, feeWei, "0", l.TxHash.Hex(), l.BlockNumber, occurredAt); err != nil {
		return fmt.Errorf("onOfferAccepted: insert sale: %w", err)
	}
	h.pub("offer-updated", map[string]any{
		"event": "OfferAccepted", "collection": collection, "tokenId": tokenID,
		"seller": seller, "bidder": bidder, "amtWei": amtWei, "standard": standard,
	})
	_ = h.notify(ctx, bidder, "offer_accepted", map[string]any{
		"collection": collection, "tokenId": tokenID, "seller": seller, "amtWei": amtWei,
	})
	return nil
}

// ── Transfer (off-contract NFT moves) ────────────────────────────────────────

// dispatchTransfer handles ERC-721 and ERC-1155 Transfer* events emitted by
// arbitrary NFT contracts. Triggers ownership update + auto-tracking + listing
// orphan flagging.
func (h *handlers) dispatchTransfer(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) == 0 {
		return nil
	}
	switch l.Topics[0] {
	case TopicTransfer721:
		return h.onTransfer721(ctx, l, blockTime)
	case TopicTransferSingle1155:
		return h.onTransferSingle1155(ctx, l, blockTime)
	case TopicTransferBatch1155:
		return h.onTransferBatch1155(ctx, l, blockTime)
	}
	return nil
}

// Transfer(address indexed from, address indexed to, uint256 indexed tokenId)
func (h *handlers) onTransfer721(ctx context.Context, l types.Log, _blockTime uint64) error {
	if len(l.Topics) < 4 {
		// ERC-20 Transfer also has this selector but only 3 topics — ignore.
		return nil
	}
	collection := strings.ToLower(l.Address.Hex())
	from       := addrStr(l.Topics[1].Bytes())
	to         := addrStr(l.Topics[2].Bytes())
	tokenID    := bigStr(l.Topics[3].Bytes())

	if err := h.q.EnsureTrackedCollection(ctx, collection, "erc721", l.BlockNumber); err != nil {
		return fmt.Errorf("ensure tracked: %w", err)
	}
	if err := h.q.SetNFTOwnership721(ctx, collection, tokenID, from, to, l.BlockNumber); err != nil {
		return fmt.Errorf("set ownership: %w", err)
	}
	if err := h.q.MarkOrphaned(ctx, collection, tokenID, to); err != nil {
		return fmt.Errorf("mark orphaned: %w", err)
	}
	h.pub("nft-transferred", map[string]any{
		"event": "Transfer721", "collection": collection, "tokenId": tokenID, "from": from, "to": to,
	})
	return nil
}

// TransferSingle(address indexed operator, address indexed from, address indexed to,
//                uint256 id, uint256 value)
func (h *handlers) onTransferSingle1155(ctx context.Context, l types.Log, _blockTime uint64) error {
	if len(l.Topics) < 4 || len(l.Data) < 2*32 {
		return nil
	}
	collection := strings.ToLower(l.Address.Hex())
	from       := addrStr(l.Topics[2].Bytes())
	to         := addrStr(l.Topics[3].Bytes())
	tokenID    := bigStr(chunk(l.Data, 0))
	value      := bigInt(chunk(l.Data, 1))

	if err := h.q.EnsureTrackedCollection(ctx, collection, "erc1155", l.BlockNumber); err != nil {
		return fmt.Errorf("ensure tracked: %w", err)
	}
	neg := new(big.Int).Neg(value)
	if err := h.q.AdjustNFTOwnership1155(ctx, collection, tokenID, from, neg, l.BlockNumber); err != nil {
		return fmt.Errorf("adjust 1155 from: %w", err)
	}
	if err := h.q.AdjustNFTOwnership1155(ctx, collection, tokenID, to, value, l.BlockNumber); err != nil {
		return fmt.Errorf("adjust 1155 to: %w", err)
	}
	if err := h.q.MarkOrphaned(ctx, collection, tokenID, to); err != nil {
		return fmt.Errorf("mark orphaned: %w", err)
	}
	return nil
}

// TransferBatch(address indexed operator, address indexed from, address indexed to,
//               uint256[] ids, uint256[] values)
func (h *handlers) onTransferBatch1155(ctx context.Context, l types.Log, _blockTime uint64) error {
	if len(l.Topics) < 4 || len(l.Data) < 4*32 {
		return nil
	}
	collection := strings.ToLower(l.Address.Hex())
	from       := addrStr(l.Topics[2].Bytes())
	to         := addrStr(l.Topics[3].Bytes())

	// data layout: offset(ids), offset(values), then the two arrays
	idsOffset    := bigInt(chunk(l.Data, 0)).Uint64()
	valuesOffset := bigInt(chunk(l.Data, 1)).Uint64()
	if int(idsOffset)+32 > len(l.Data) || int(valuesOffset)+32 > len(l.Data) {
		return nil
	}
	idsLen := bigInt(l.Data[idsOffset : idsOffset+32]).Uint64()
	valLen := bigInt(l.Data[valuesOffset : valuesOffset+32]).Uint64()
	if idsLen != valLen {
		return nil
	}
	if err := h.q.EnsureTrackedCollection(ctx, collection, "erc1155", l.BlockNumber); err != nil {
		return fmt.Errorf("ensure tracked: %w", err)
	}
	for i := uint64(0); i < idsLen; i++ {
		idStart  := idsOffset + 32 + i*32
		valStart := valuesOffset + 32 + i*32
		if int(idStart+32) > len(l.Data) || int(valStart+32) > len(l.Data) {
			return nil
		}
		tokenID := bigStr(l.Data[idStart : idStart+32])
		value   := bigInt(l.Data[valStart : valStart+32])
		neg := new(big.Int).Neg(value)
		if err := h.q.AdjustNFTOwnership1155(ctx, collection, tokenID, from, neg, l.BlockNumber); err != nil {
			return err
		}
		if err := h.q.AdjustNFTOwnership1155(ctx, collection, tokenID, to, value, l.BlockNumber); err != nil {
			return err
		}
		if err := h.q.MarkOrphaned(ctx, collection, tokenID, to); err != nil {
			return err
		}
	}
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (h *handlers) notify(ctx context.Context, recipient, kind string, payload map[string]any) error {
	if recipient == "" {
		return nil
	}
	jsonPayload := mapToJSON(payload)
	_, err := h.q.InsertNotification(ctx, recipient, kind, jsonPayload)
	if err == nil {
		h.pub("notification", map[string]any{"recipient": recipient, "kind": kind, "payload": payload})
	}
	return err
}

func mapToJSON(m map[string]any) string {
	var b strings.Builder
	b.WriteByte('{')
	first := true
	for k, v := range m {
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteByte('"')
		b.WriteString(strings.ReplaceAll(k, `"`, `\"`))
		b.WriteString(`":`)
		switch t := v.(type) {
		case string:
			b.WriteByte('"')
			b.WriteString(strings.ReplaceAll(t, `"`, `\"`))
			b.WriteByte('"')
		case int, int64, uint64, float64:
			fmt.Fprintf(&b, "%v", t)
		case bool:
			fmt.Fprintf(&b, "%v", t)
		default:
			b.WriteByte('"')
			fmt.Fprintf(&b, "%v", t)
			b.WriteByte('"')
		}
	}
	b.WriteByte('}')
	return b.String()
}
