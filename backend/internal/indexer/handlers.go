package indexer

import (
	"context"
	"fmt"
	"math/big"
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

func addrStr(b []byte) string   { return common.BytesToAddress(b).Hex() }
func bigInt(b []byte) *big.Int  { return new(big.Int).SetBytes(b) }
func bigStr(b []byte) string    { return bigInt(b).String() }
func tsUnix(b []byte) time.Time { return time.Unix(bigInt(b).Int64(), 0) }
func standardOf(b []byte) string {
	if b[31] == 1 {
		return "ERC1155"
	}
	return "ERC721"
}

func (h *handlers) pub(evType string, payload any) {
	h.bcast.Publish(sse.Event{Type: evType, Data: payload})
}

// notify best-effort persists an in-app notification; failures are non-fatal.
func (h *handlers) notify(ctx context.Context, addr, kind, title, body, link string) {
	if addr == "" {
		return
	}
	if err := h.q.InsertNotification(ctx, addr, kind, title, body, link); err == nil {
		h.pub("notification", map[string]any{"to": addr, "kind": kind, "title": title})
	}
}

// dispatch routes a log to the correct handler.
func (h *handlers) dispatch(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) == 0 {
		return nil
	}
	switch l.Topics[0] {
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
	case TopicOfferMade:
		return h.onOfferMade(ctx, l)
	case TopicOfferAccepted:
		return h.onOfferAccepted(ctx, l, blockTime)
	case TopicOfferRefunded:
		return h.onOfferRefunded(ctx, l)
	case TopicTransfer721:
		return h.onTransfer721(ctx, l)
	case TopicTransferSingle:
		return h.onTransferSingle(ctx, l)
	case TopicTransferBatch:
		return h.onTransferBatch(ctx, l)
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
	tokenID := bigStr(l.Topics[2].Bytes())
	seller := addrStr(l.Topics[3].Bytes())
	standard := standardOf(chunk(l.Data, 0))
	amtRaw := bigInt(chunk(l.Data, 1))
	priceWei := bigStr(chunk(l.Data, 2))
	expiresAt := tsUnix(chunk(l.Data, 3))

	amount := int64(1)
	if standard == "ERC1155" {
		amount = amtRaw.Int64()
	}
	_ = h.q.EnsureCollection(ctx, collection, standard, l.BlockNumber)

	r := db.ListingRow{
		Collection: collection,
		TokenID:    tokenID,
		Seller:     seller,
		PriceWei:   priceWei,
		Amount:     amount,
		Standard:   standard,
		ExpiresAt:  expiresAt,
		ListedAt:   time.Unix(int64(blockTime), 0),
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
	tokenID := bigStr(l.Topics[2].Bytes())
	seller := addrStr(l.Topics[3].Bytes())
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
	tokenID := bigStr(l.Topics[2].Bytes())
	buyer := addrStr(l.Topics[3].Bytes())
	seller := addrStr(chunk(l.Data, 0))
	priceWei := bigStr(chunk(l.Data, 3))
	feeWei := bigStr(chunk(l.Data, 4))
	occurredAt := time.Unix(int64(blockTime), 0)

	if err := h.q.DeactivateListing(ctx, collection, tokenID, seller); err != nil {
		return fmt.Errorf("onBought deactivate: %w", err)
	}
	if err := h.q.InsertSale(ctx, collection, tokenID, seller, buyer,
		priceWei, feeWei, "0", l.TxHash.Hex(), l.BlockNumber, occurredAt); err != nil {
		return fmt.Errorf("onBought sale: %w", err)
	}
	h.notify(ctx, seller, "sold", "Your NFT sold", priceWei+" wei", "/token/"+collection+"/"+tokenID)
	h.pub("listing-updated", map[string]any{
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
	auctionID := bigInt(l.Topics[1].Bytes()).Int64()
	collection := addrStr(l.Topics[2].Bytes())
	tokenID := bigStr(l.Topics[3].Bytes())
	seller := addrStr(chunk(l.Data, 0))
	standard := standardOf(chunk(l.Data, 1))
	reserve := bigStr(chunk(l.Data, 3))
	startsAt := tsUnix(chunk(l.Data, 4))
	endsAt := tsUnix(chunk(l.Data, 5))
	_ = h.q.EnsureCollection(ctx, collection, standard, l.BlockNumber)

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

// BidPlaced(uint256 indexed id, address indexed bidder, uint128 bidAmount, uint128 totalPaid)
func (h *handlers) onBidPlaced(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) < 3 || len(l.Data) < 32 {
		return fmt.Errorf("onBidPlaced: short log")
	}
	auctionID := bigInt(l.Topics[1].Bytes()).Int64()
	bidder := addrStr(l.Topics[2].Bytes())
	amtWei := bigStr(chunk(l.Data, 0)) // bidAmount (bidding is free; fee taken from seller at settle)
	placedAt := time.Unix(int64(blockTime), 0)

	// Notify the previous highest bidder that they were outbid (bid refunded in full).
	if prev, err := h.q.GetAuction(ctx, auctionID); err == nil && prev != nil &&
		prev.HighestBidder != "" && prev.HighestBidder != bidder {
		h.notify(ctx, prev.HighestBidder, "outbid", "You were outbid",
			"New high bid "+amtWei+" wei", "/auction/"+fmt.Sprint(auctionID))
	}

	if err := h.q.InsertBidAndUpdateAuction(ctx, auctionID, bidder, amtWei, l.TxHash.Hex(), placedAt); err != nil {
		return fmt.Errorf("onBidPlaced: %w", err)
	}
	h.pub("auction-updated", map[string]any{
		"event": "BidPlaced", "auctionId": auctionID, "bidder": bidder, "amtWei": amtWei,
	})
	return nil
}

// AuctionExtended(uint256 indexed id, uint64 newEndsAt) — anti-snipe close-time bump.
func (h *handlers) onAuctionExtended(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 2 || len(l.Data) < 32 {
		return fmt.Errorf("onAuctionExtended: short log")
	}
	auctionID := bigInt(l.Topics[1].Bytes()).Int64()
	newEndsAt := tsUnix(chunk(l.Data, 0))
	if err := h.q.ExtendAuction(ctx, auctionID, newEndsAt); err != nil {
		return fmt.Errorf("onAuctionExtended: %w", err)
	}
	h.pub("auction-updated", map[string]any{
		"event": "AuctionExtended", "auctionId": auctionID, "endsAt": newEndsAt.Unix(),
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
	winner := addrStr(l.Topics[2].Bytes())
	seller := addrStr(l.Topics[3].Bytes())
	bidAmt := bigStr(chunk(l.Data, 0))
	if err := h.q.SetAuctionStatus(ctx, auctionID, "settled"); err != nil {
		return fmt.Errorf("onAuctionSettled: %w", err)
	}
	if winner != "" && winner != "0x0000000000000000000000000000000000000000" {
		h.notify(ctx, winner, "auction_won", "You won an auction", bidAmt+" wei", "/auction/"+fmt.Sprint(auctionID))
		h.notify(ctx, seller, "sold", "Your auction settled", bidAmt+" wei", "/auction/"+fmt.Sprint(auctionID))
	}
	h.pub("auction-updated", map[string]any{
		"event": "AuctionSettled", "auctionId": auctionID,
		"winner": winner, "seller": seller, "amtWei": bidAmt,
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

// ── OfferBook (Model A: stacked positions, fee taken at make) ──────────────────

// OfferMade(address indexed coll, uint256 indexed tokenId, address indexed bidder,
//           uint256 principal, uint128 units, uint64 expiresAt)
func (h *handlers) onOfferMade(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 || len(l.Data) < 3*32 {
		return fmt.Errorf("onOfferMade: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID := bigStr(l.Topics[2].Bytes())
	bidder := addrStr(l.Topics[3].Bytes())
	principal := bigStr(chunk(l.Data, 0)) // cumulative escrowed principal
	feeWei := "0"                          // offers are free; the fee is charged from the seller at acceptance
	units := bigInt(chunk(l.Data, 1)).Int64()
	expiresAt := tsUnix(chunk(l.Data, 2))
	standard := "ERC721"
	if units > 1 {
		standard = "ERC1155"
	}
	if units < 1 {
		units = 1
	}

	pos := db.OfferRow{
		Bidder: bidder, Collection: collection, TokenID: tokenID,
		AmountWei: principal, FeeWei: feeWei, Units: units,
		Standard: standard, ExpiresAt: expiresAt, Status: "pending",
		MakeTx: l.TxHash.Hex(),
	}
	if err := h.q.UpsertOfferPosition(ctx, pos); err != nil {
		return fmt.Errorf("onOfferMade upsert: %w", err)
	}
	// Notify current owner (best effort) that a new offer landed.
	if owner, _ := h.q.GetTokenOwner(ctx, collection, tokenID); owner != "" {
		h.notify(ctx, owner, "offer_received", "New offer received",
			principal+" wei", "/token/"+collection+"/"+tokenID)
	}
	h.pub("offer-updated", map[string]any{
		"event": "OfferMade", "collection": collection, "tokenId": tokenID,
		"bidder": bidder, "principal": principal,
	})
	return nil
}

// OfferAccepted(address indexed coll, uint256 indexed tokenId, address indexed seller,
//               address bidder, uint256 principal, uint256 fee, uint128 units, uint8 standard)
func (h *handlers) onOfferAccepted(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) < 4 || len(l.Data) < 5*32 {
		return fmt.Errorf("onOfferAccepted: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID := bigStr(l.Topics[2].Bytes())
	seller := addrStr(l.Topics[3].Bytes())
	bidder := addrStr(chunk(l.Data, 0))
	principal := bigStr(chunk(l.Data, 1))
	feeWei := bigStr(chunk(l.Data, 2)) // 1.5% deducted from the seller on acceptance
	occurredAt := time.Unix(int64(blockTime), 0)

	if err := h.q.SetOfferStatus(ctx, collection, tokenID, bidder, "accepted"); err != nil {
		return fmt.Errorf("onOfferAccepted status: %w", err)
	}
	// Sale fee is charged at acceptance, deducted from the seller's proceeds.
	if err := h.q.InsertSale(ctx, collection, tokenID, seller, bidder,
		principal, feeWei, "0", l.TxHash.Hex(), l.BlockNumber, occurredAt); err != nil {
		return fmt.Errorf("onOfferAccepted sale: %w", err)
	}
	h.notify(ctx, bidder, "offer_accepted", "Your offer was accepted",
		principal+" wei", "/token/"+collection+"/"+tokenID)
	h.pub("offer-updated", map[string]any{
		"event": "OfferAccepted", "collection": collection, "tokenId": tokenID,
		"seller": seller, "bidder": bidder, "principal": principal,
	})
	return nil
}

// OfferRefunded(address indexed coll, uint256 indexed tokenId, address indexed bidder, uint256 principal)
func (h *handlers) onOfferRefunded(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 {
		return fmt.Errorf("onOfferRefunded: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID := bigStr(l.Topics[2].Bytes())
	bidder := addrStr(l.Topics[3].Bytes())
	if err := h.q.SetOfferStatus(ctx, collection, tokenID, bidder, "cancelled"); err != nil {
		return fmt.Errorf("onOfferRefunded: %w", err)
	}
	h.notify(ctx, bidder, "offer_rejected", "Your offer was refunded",
		"", "/token/"+collection+"/"+tokenID)
	h.pub("offer-updated", map[string]any{
		"event": "OfferRefunded", "collection": collection, "tokenId": tokenID, "bidder": bidder,
	})
	return nil
}

// ── NFT transfers (ownership + orphan stale listings) ──────────────────────────

// ERC-721 Transfer(address indexed from, address indexed to, uint256 indexed tokenId).
// ERC-20 shares this signature but indexes only from/to (3 topics) — skip those.
func (h *handlers) onTransfer721(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 {
		return nil // ERC-20 Transfer or malformed — not an NFT
	}
	collection := addrStr(l.Address.Bytes())
	to := addrStr(l.Topics[2].Bytes())
	tokenID := bigStr(l.Topics[3].Bytes())
	if err := h.q.ApplyTransfer721(ctx, collection, tokenID, to); err != nil {
		return fmt.Errorf("onTransfer721: %w", err)
	}
	h.pub("listing-updated", map[string]any{
		"event": "Transfer", "collection": collection, "tokenId": tokenID, "to": to,
	})
	return nil
}

// TransferSingle(address operator, address indexed from, address indexed to, uint256 id, uint256 value)
func (h *handlers) onTransferSingle(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 || len(l.Data) < 2*32 {
		return fmt.Errorf("onTransferSingle: short log")
	}
	collection := addrStr(l.Address.Bytes())
	from := addrStr(l.Topics[2].Bytes())
	to := addrStr(l.Topics[3].Bytes())
	tokenID := bigStr(chunk(l.Data, 0))
	value := bigInt(chunk(l.Data, 1))
	if err := h.q.ApplyTransfer1155(ctx, collection, tokenID, from, to, value.String()); err != nil {
		return fmt.Errorf("onTransferSingle: %w", err)
	}
	h.pub("listing-updated", map[string]any{
		"event": "TransferSingle", "collection": collection, "tokenId": tokenID, "from": from, "to": to,
	})
	return nil
}

// TransferBatch(address operator, address indexed from, address indexed to, uint256[] ids, uint256[] values).
// Data layout: [offset_ids][offset_values][len_ids][ids...][len_values][values...].
func (h *handlers) onTransferBatch(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 || len(l.Data) < 2*32 {
		return fmt.Errorf("onTransferBatch: short log")
	}
	collection := addrStr(l.Address.Bytes())
	from := addrStr(l.Topics[2].Bytes())
	to := addrStr(l.Topics[3].Bytes())

	idsOff := bigInt(chunk(l.Data, 0)).Int64() / 32
	valsOff := bigInt(chunk(l.Data, 1)).Int64() / 32
	idsLen := bigInt(chunk(l.Data, int(idsOff))).Int64()
	valsLen := bigInt(chunk(l.Data, int(valsOff))).Int64()
	if idsLen != valsLen {
		return fmt.Errorf("onTransferBatch: ids/values length mismatch")
	}
	for i := int64(0); i < idsLen; i++ {
		id := bigStr(chunk(l.Data, int(idsOff+1+i)))
		val := bigInt(chunk(l.Data, int(valsOff+1+i))).String()
		if err := h.q.ApplyTransfer1155(ctx, collection, id, from, to, val); err != nil {
			return fmt.Errorf("onTransferBatch id=%s: %w", id, err)
		}
	}
	h.pub("listing-updated", map[string]any{
		"event": "TransferBatch", "collection": collection, "from": from, "to": to,
	})
	return nil
}
