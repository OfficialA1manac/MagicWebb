package indexer

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

type handlers struct {
	q     *db.Q
	bcast *sse.Broadcaster
}

// errMalformedLog marks a dispatch failure as PERMANENT: the on-chain log's
// topics/data fail structural validation, so no retry can ever succeed
// (chain logs are immutable). Callers use errors.Is(err, errMalformedLog) to
// log-and-skip such logs instead of aborting the range — otherwise one
// hostile or non-standard log would halt cursor advancement for all events
// and all collections until an operator intervenes. DB/RPC failures are NOT
// wrapped with this sentinel; those stay retriable and abort the range.
var errMalformedLog = errors.New("malformed log")

// malformedLogf builds a validation error carrying the errMalformedLog
// sentinel.
func malformedLogf(format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, errMalformedLog)...)
}

// chunk returns the i-th 32-byte ABI word from event data.
func chunk(data []byte, i int) []byte {
	if len(data) < (i+1)*32 {
		return make([]byte, 32)
	}
	return data[i*32 : (i+1)*32]
}

// maxBatchLength caps TransferBatch iteration as a safety ceiling against
// hostile on-chain logs. Legitimate ERC-1155 batches rarely exceed ~100
// elements; type(uint256).max encoded in the ids-length word previously
// drove the inner loop to run billions of times (each iteration issuing
// a Postgres upsert), OOM-ing the chain indexer in seconds. The bound
// applies BEFORE the chunk() reads and BEFORE the loop. See audit
// priority-stack `onTransferBatch`.
const maxBatchLength = 1024

// addrStr renders a 20-byte address as LOWERCASE hex.
//
// common.Address.Hex() returns EIP-55 CHECKSUMMED output ("0x687DE6fA…"), and
// this helper feeds all 30 address-extraction sites in this file. Every reader
// in the codebase lowercases before querying, and the address columns are
// case-sensitive CHAR(42) — so writing checksummed here made rows invisible to
// every per-address and per-token query: profile listings read 0, token pages
// reported "Not listed for sale", and collections.listed_count stayed 0 while
// the unfiltered /listings page (which compares no address) rendered fine.
//
// Lowercase is the storage convention the rest of the schema already uses —
// profiles, saved_searches and the RLS policies all assume it explicitly
// (011_rls_rework.sql, 024_rls_audit_fixes.sql). Migration 039 backfills the
// rows written before this fix.
func addrStr(b []byte) string {
	return strings.ToLower(common.BytesToAddress(b).Hex())
}
func bigInt(b []byte) *big.Int  { return new(big.Int).SetBytes(b) }
func bigStr(b []byte) string    { return bigInt(b).String() }
func tsUnix(b []byte) time.Time { return time.Unix(bigInt(b).Int64(), 0) }

// standardOf maps the on-chain TokenStandard enum to the Postgres
// token_standard enum — which is lowercase ('erc721'|'erc1155'); uppercase
// values are rejected by the DB with SQLSTATE 22P02.
func standardOf(b []byte) string {
	if b[31] == 1 {
		return "erc1155"
	}
	return "erc721"
}

// pubTyped publishes a typed SSE-4 event payload. The payload must implement
// sse.TypedEvent for the bridge to populate the proto oneof.
func (h *handlers) pubTyped(evType string, payload sse.TypedEvent) {
	h.bcast.Publish(sse.Event{Type: evType, Data: payload})
}

// notify best-effort persists an in-app notification; failures are non-fatal.
func (h *handlers) notify(ctx context.Context, addr, kind, title, body, link string) {
	if addr == "" {
		return
	}
	if err := h.q.InsertNotification(ctx, addr, kind, title, body, link); err == nil {
		// Phase 3 RBAC: include user_addr so the GraphQL subscription resolver
		// can filter notifications to only the intended recipient.
		h.pubTyped("notification", &sse.NotificationEvent{
			User: addr, UserAddr: addr, Kind: kind, Title: title, Body: body, Link: link,
		})
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
	case TopicOutbidNotification:
		return h.onOutbidNotification(ctx, l)
	case TopicAuctionExtended:
		return h.onAuctionExtended(ctx, l)
	case TopicAuctionSettled:
		return h.onAuctionSettled(ctx, l)
	case TopicLoserRefunded:
		return h.onLoserRefunded(ctx, l)
	case TopicAuctionCancelled:
		return h.onAuctionCancelled(ctx, l)
	case TopicAuctionSettlementFailed:
		return h.onAuctionSettlementFailed(ctx, l)
	case TopicRefundPushed:
		return h.onRefundPushed(ctx, l)
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
//
//	uint8 standard, uint128 amount, uint128 price, uint64 expiresAt)
func (h *handlers) onListed(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) < 4 || len(l.Data) < 4*32 {
		return malformedLogf("onListed: short log tx=%s", l.TxHash.Hex())
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID := bigStr(l.Topics[2].Bytes())
	seller := addrStr(l.Topics[3].Bytes())
	standard := standardOf(chunk(l.Data, 0))
	amtRaw := bigInt(chunk(l.Data, 1))
	priceWei := bigStr(chunk(l.Data, 2))
	expiresAt := tsUnix(chunk(l.Data, 3))

	amount := int64(1)
	if standard == "erc1155" {
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
	// Single-transaction write: listings row + seller ownership row must be
	// both live or both rolled back — a crash between them would leave a
	// listing visible to the front-end whose preflight cannot find the
	// seller, producing a 'mystery 500' on the next /token/:addr/:id hit.
	if err := h.q.UpsertListingAndOwnership(ctx, r); err != nil {
		return fmt.Errorf("onListed: %w", err)
	}
	h.pubTyped("listing-updated", &sse.ListingUpdatedEvent{
		Event: "Listed", Collection: collection, TokenID: tokenID,
		Seller: seller, PriceWei: priceWei, Data: r,
	})
	return nil
}

// Cancelled(address indexed coll, uint256 indexed id, address indexed seller)
func (h *handlers) onCancelled(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 {
		return malformedLogf("onCancelled: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID := bigStr(l.Topics[2].Bytes())
	seller := addrStr(l.Topics[3].Bytes())
	if err := h.q.DeactivateListing(ctx, collection, tokenID, seller); err != nil {
		return fmt.Errorf("onCancelled: %w", err)
	}
	h.pubTyped("listing-updated", &sse.ListingUpdatedEvent{
		Event: "Cancelled", Collection: collection, TokenID: tokenID, Seller: seller,
	})
	return nil
}

// Bought(address indexed coll, uint256 indexed id, address indexed buyer,
//
//	address seller, uint8 standard, uint128 amount, uint128 price, uint256 fee)
func (h *handlers) onBought(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) < 4 || len(l.Data) < 5*32 {
		return malformedLogf("onBought: short log tx=%s", l.TxHash.Hex())
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID := bigStr(l.Topics[2].Bytes())
	buyer := addrStr(l.Topics[3].Bytes())
	seller := addrStr(chunk(l.Data, 0))
	priceWei := bigStr(chunk(l.Data, 3))
	feeWei := bigStr(chunk(l.Data, 4))
	occurredAt := time.Unix(int64(blockTime), 0)

	// Atomic: deactivate + sale in one tx so a crash between the two can never
	// leave a sold listing active (or a sale without its deactivation).
	if err := h.q.DeactivateAndSale(ctx, collection, tokenID, seller, buyer,
		priceWei, feeWei, "0", l.TxHash.Hex(), l.BlockNumber, occurredAt); err != nil {
		return fmt.Errorf("onBought: %w", err)
	}
	h.notify(ctx, seller, "sold", "Your NFT sold", priceWei+" wei", "/token/"+collection+"/"+tokenID)
	h.pubTyped("listing-updated", &sse.ListingUpdatedEvent{
		Event: "Bought", Collection: collection, TokenID: tokenID,
		Buyer: buyer, Seller: seller, PriceWei: priceWei,
	})
	return nil
}

// ── AuctionHouse ──────────────────────────────────────────────────────────────

// AuctionCreated(uint256 indexed id, address indexed coll, uint256 indexed tokenId,
//
//	address seller, uint8 standard, uint128 amount, uint128 reserve, uint64 startsAt, uint64 endsAt)
func (h *handlers) onAuctionCreated(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 || len(l.Data) < 6*32 {
		return malformedLogf("onAuctionCreated: short log")
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
	h.pubTyped("auction-updated", &sse.AuctionUpdatedEvent{
		Event: "AuctionCreated", AuctionID: auctionID, Data: r,
	})
	return nil
}

// BidPlaced(uint256 indexed id, address indexed bidder, uint256 amount, uint256 newTotal)
// Cumulative model: amount is THIS bid's wei; newTotal is the bidder's cumulative.
// The current leader is recomputed from the effective_bids view (lead changes are
// signalled separately by OutbidNotification).
func (h *handlers) onBidPlaced(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) < 3 || len(l.Data) < 2*32 {
		return malformedLogf("onBidPlaced: short log")
	}
	auctionID := bigInt(l.Topics[1].Bytes()).Int64()
	bidder := addrStr(l.Topics[2].Bytes())
	amtWei := bigStr(chunk(l.Data, 0))   // this bid's wei (escrowed; bidding is free)
	newTotal := bigStr(chunk(l.Data, 1)) // bidder's cumulative after this bid
	placedAt := time.Unix(int64(blockTime), 0)

	// Insert the bid row and set the auction's highest to the current cumulative
	// leader (max effective_wei). Idempotent on tx_hash.
	if err := h.q.InsertBidAndUpdateAuction(ctx, auctionID, bidder, amtWei, l.TxHash.Hex(), placedAt); err != nil {
		return fmt.Errorf("onBidPlaced: %w", err)
	}
	h.pubTyped("auction-updated", &sse.AuctionUpdatedEvent{
		Event: "BidPlaced", AuctionID: auctionID, Bidder: bidder,
		AmtWei: amtWei, EffectiveWei: newTotal,
	})
	return nil
}

// OutbidNotification(uint256 indexed id, address indexed outbid, uint256 newLeaderTotal)
func (h *handlers) onOutbidNotification(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 3 || len(l.Data) < 32 {
		return malformedLogf("onOutbidNotification: short log")
	}
	auctionID := bigInt(l.Topics[1].Bytes()).Int64()
	outbid := addrStr(l.Topics[2].Bytes())
	newLeaderTotal := bigStr(chunk(l.Data, 0))

	h.notify(ctx, outbid, "outbid", "You were outbid",
		"New leading total "+newLeaderTotal+" wei. Add to your bid to reclaim the lead.",
		"/auction/"+fmt.Sprint(auctionID))
	h.pubTyped("auction-updated", &sse.AuctionUpdatedEvent{
		Event: "OutbidNotification", AuctionID: auctionID,
		OutbidAddr: outbid, LeaderTotal: newLeaderTotal,
	})
	return nil
}

// LoserRefunded(uint256 indexed id, address indexed bidder, uint256 amount)
func (h *handlers) onLoserRefunded(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 3 || len(l.Data) < 32 {
		return malformedLogf("onLoserRefunded: short log")
	}
	auctionID := bigInt(l.Topics[1].Bytes()).Int64()
	bidder := addrStr(l.Topics[2].Bytes())
	amount := bigStr(chunk(l.Data, 0))

	h.notify(ctx, bidder, "refund", "Auction escrow refunded",
		amount+" wei returned", "/auction/"+fmt.Sprint(auctionID))
	// The event fires whether the ETH push landed or fell back to
	// pendingReturns — seed a candidate; the withdrawal sweeper verifies
	// on-chain and clears or confirms it.
	if err := h.q.SeedPendingWithdrawal(ctx, bidder); err != nil {
		log.Warn().Err(err).Str("bidder", bidder).Msg("seed pending withdrawal")
	}
	h.pubTyped("auction-updated", &sse.AuctionUpdatedEvent{
		Event: "LoserRefunded", AuctionID: auctionID, Bidder: bidder, AmtWei: amount,
	})
	return nil
}

// RefundPushed(address indexed bidder, uint256 amount) — emitted when settle
// returns a winner's escrow (undeliverable NFT path), push or pull alike.
func (h *handlers) onRefundPushed(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 2 || len(l.Data) < 32 {
		return malformedLogf("onRefundPushed: short log")
	}
	bidder := addrStr(l.Topics[1].Bytes())
	amount := bigStr(chunk(l.Data, 0))

	h.notify(ctx, bidder, "refund", "Auction refund issued", amount+" wei", "/profile/"+bidder)
	if err := h.q.SeedPendingWithdrawal(ctx, bidder); err != nil {
		log.Warn().Err(err).Str("bidder", bidder).Msg("seed pending withdrawal")
	}
	return nil
}

// AuctionExtended(uint256 indexed id, uint64 newEndsAt) — anti-snipe close-time bump.
func (h *handlers) onAuctionExtended(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 2 || len(l.Data) < 32 {
		return malformedLogf("onAuctionExtended: short log")
	}
	auctionID := bigInt(l.Topics[1].Bytes()).Int64()
	newEndsAt := tsUnix(chunk(l.Data, 0))
	if err := h.q.ExtendAuction(ctx, auctionID, newEndsAt); err != nil {
		return fmt.Errorf("onAuctionExtended: %w", err)
	}
	h.pubTyped("auction-updated", &sse.AuctionUpdatedEvent{
		Event: "AuctionExtended", AuctionID: auctionID, EndTimeUnix: newEndsAt.Unix(),
	})
	return nil
}

// AuctionSettled(uint256 indexed id, address indexed winner, address indexed seller,
//
//	uint128 bidAmount, uint256 fee)
func (h *handlers) onAuctionSettled(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 || len(l.Data) < 2*32 {
		return malformedLogf("onAuctionSettled: short log")
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
	h.pubTyped("auction-updated", &sse.AuctionUpdatedEvent{
		Event: "AuctionSettled", AuctionID: auctionID,
		Winner: winner, Seller: seller, AmtWei: bidAmt,
	})
	return nil
}

// AuctionCancelled(uint256 indexed id)
func (h *handlers) onAuctionCancelled(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 2 {
		return malformedLogf("onAuctionCancelled: short log")
	}
	auctionID := bigInt(l.Topics[1].Bytes()).Int64()
	if err := h.q.SetAuctionStatus(ctx, auctionID, "cancelled"); err != nil {
		return fmt.Errorf("onAuctionCancelled: %w", err)
	}
	h.pubTyped("auction-updated", &sse.AuctionUpdatedEvent{
		Event: "AuctionCancelled", AuctionID: auctionID,
	})
	return nil
}

// AuctionSettlementFailed(uint256 indexed id, address indexed winner, uint128 amount)
//
// The seller moved the NFT away or revoked approval, so settle() finalised the
// auction with no sale and returned the winner's escrow on the spot. Emitted
// INSTEAD OF AuctionSettled — nothing else marks the auction terminal, so
// without this handler the row stays 'active' and the keeper keeps re-settling
// an auction the chain considers done.
//
// Status is 'cancelled', not 'settled': 'settled' reads as SOLD everywhere it is
// consumed (sale history, volume, badges) and no sale happened here. Losers are
// still refunded through refundLosers/LoserRefunded as usual.
func (h *handlers) onAuctionSettlementFailed(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 3 || len(l.Data) < 32 {
		return malformedLogf("onAuctionSettlementFailed: short log")
	}
	auctionID := bigInt(l.Topics[1].Bytes()).Int64()
	winner := addrStr(l.Topics[2].Bytes())
	amt := bigStr(chunk(l.Data, 0))
	if err := h.q.SetAuctionStatus(ctx, auctionID, "cancelled"); err != nil {
		return fmt.Errorf("onAuctionSettlementFailed: %w", err)
	}
	// 'system' kind: notification_kind has no refund member, and this is neither
	// an auction_won (no NFT) nor an auction_lost (they had the top bid).
	h.notify(ctx, winner, "system", "Auction could not be completed",
		"The seller did not deliver the NFT. "+amt+" wei has been returned to you.",
		"/auction/"+fmt.Sprint(auctionID))
	h.pubTyped("auction-updated", &sse.AuctionUpdatedEvent{
		Event: "AuctionSettlementFailed", AuctionID: auctionID,
		Winner: winner, AmtWei: amt,
	})
	return nil
}

// ── OfferBook (Model A: stacked positions, fee taken at make) ──────────────────

// OfferMade(address indexed coll, uint256 indexed tokenId, address indexed bidder,
//
//	uint256 principal, uint128 units, uint64 expiresAt)
func (h *handlers) onOfferMade(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 || len(l.Data) < 3*32 {
		return malformedLogf("onOfferMade: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID := bigStr(l.Topics[2].Bytes())
	bidder := addrStr(l.Topics[3].Bytes())
	principal := bigStr(chunk(l.Data, 0)) // cumulative escrowed principal
	feeWei := "0"                         // offers are free; the fee is charged from the seller at acceptance
	units := bigInt(chunk(l.Data, 1)).Int64()
	expiresAt := tsUnix(chunk(l.Data, 2))
	standard := "erc721"
	if units > 1 {
		standard = "erc1155"
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
	h.pubTyped("offer-updated", &sse.OfferUpdatedEvent{
		Event: "OfferMade", Collection: collection, TokenID: tokenID,
		Bidder: bidder, Principal: principal, AmountWei: principal,
	})
	return nil
}

// OfferAccepted(address indexed coll, uint256 indexed tokenId, address indexed seller,
//
//	address bidder, uint256 principal, uint256 fee, uint128 units, uint8 standard)
func (h *handlers) onOfferAccepted(ctx context.Context, l types.Log, blockTime uint64) error {
	if len(l.Topics) < 4 || len(l.Data) < 5*32 {
		return malformedLogf("onOfferAccepted: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID := bigStr(l.Topics[2].Bytes())
	seller := addrStr(l.Topics[3].Bytes())
	bidder := addrStr(chunk(l.Data, 0))
	principal := bigStr(chunk(l.Data, 1))
	feeWei := bigStr(chunk(l.Data, 2)) // 1.5% deducted from the seller on acceptance
	occurredAt := time.Unix(int64(blockTime), 0)

	// Single-transaction write: the offer's status flip MUST land in the
	// same tx as the sale row — a crash between them would either pin the
	// bidder's escrow on a 'pending' offer they can never satisfy again, or
	// record a sale against an offer that never left the pending state.
	if err := h.q.AcceptOfferAndRecordSale(ctx,
		collection, tokenID, seller, bidder,
		principal, feeWei, "0", l.TxHash.Hex(), l.BlockNumber, occurredAt); err != nil {
		return fmt.Errorf("onOfferAccepted: %w", err)
	}
	h.notify(ctx, bidder, "offer_accepted", "Your offer was accepted",
		principal+" wei", "/token/"+collection+"/"+tokenID)
	h.pubTyped("offer-updated", &sse.OfferUpdatedEvent{
		Event: "OfferAccepted", Collection: collection, TokenID: tokenID,
		Seller: seller, Bidder: bidder, Principal: principal, AmountWei: principal,
	})
	return nil
}

// OfferRefunded(address indexed coll, uint256 indexed tokenId, address indexed bidder, uint256 principal)
func (h *handlers) onOfferRefunded(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 {
		return malformedLogf("onOfferRefunded: short log")
	}
	collection := addrStr(l.Topics[1].Bytes())
	tokenID := bigStr(l.Topics[2].Bytes())
	bidder := addrStr(l.Topics[3].Bytes())
	if err := h.q.SetOfferStatus(ctx, collection, tokenID, bidder, "cancelled"); err != nil {
		return fmt.Errorf("onOfferRefunded: %w", err)
	}
	h.notify(ctx, bidder, "offer_rejected", "Your offer was refunded",
		"", "/token/"+collection+"/"+tokenID)
	h.pubTyped("offer-updated", &sse.OfferUpdatedEvent{
		Event: "OfferRefunded", Collection: collection, TokenID: tokenID, Bidder: bidder,
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
	h.pubTyped("listing-updated", &sse.ListingUpdatedEvent{
		Event: "Transfer", Collection: collection, TokenID: tokenID, ToAddr: to,
	})
	return nil
}

// TransferSingle(address operator, address indexed from, address indexed to, uint256 id, uint256 value)
func (h *handlers) onTransferSingle(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 || len(l.Data) < 2*32 {
		return malformedLogf("onTransferSingle: short log")
	}
	collection := addrStr(l.Address.Bytes())
	from := addrStr(l.Topics[2].Bytes())
	to := addrStr(l.Topics[3].Bytes())
	tokenID := bigStr(chunk(l.Data, 0))
	value := bigInt(chunk(l.Data, 1))
	if err := h.q.ApplyTransfer1155(ctx, collection, tokenID, from, to, value.String()); err != nil {
		return fmt.Errorf("onTransferSingle: %w", err)
	}
	h.pubTyped("listing-updated", &sse.ListingUpdatedEvent{
		Event: "TransferSingle", Collection: collection, TokenID: tokenID, FromAddr: from, ToAddr: to,
	})
	return nil
}

// TransferBatch(address operator, address indexed from, address indexed to, uint256[] ids, uint256[] values).
// Data layout: [offset_ids][offset_values][len_ids][ids...][len_values][values...].
//
// Belt-and-braces bound check (Priority Stack P0 `onTransferBatch`): the
// pre-fix code decoded idsLen/valsLen straight off the log data and the
// inner `chunk()` helper silently zero-padded past the data footprint,
// letting a hostile TransferBatch with idsLen=type(uint256).max run the
// loop billions of times. Each iteration issued a Postgres upsert,
// accumulating queries against the indexer connection until OOM. The fix
// caps EVERY pointer by data footprint AND by the application ceiling
// BEFORE the loop runs, so any malicious structure is dropped here as
// malformed.
func (h *handlers) onTransferBatch(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 4 || len(l.Data) < 2*32 {
		return malformedLogf("onTransferBatch: short log tx=%s", l.TxHash.Hex())
	}
	collection := addrStr(l.Address.Bytes())
	from := addrStr(l.Topics[2].Bytes())
	to := addrStr(l.Topics[3].Bytes())

	dataWords := int64(len(l.Data) / 32)
	idsOff := bigInt(chunk(l.Data, 0)).Int64() / 32
	valsOff := bigInt(chunk(l.Data, 1)).Int64() / 32
	// Offsets must point at a length word INSIDE the data (> dataWords-1
	// would put the length word past the payload). A length word as the
	// LAST data word is legal — that is exactly the canonical encoding of
	// an empty array — so the bound is dataWords-1 inclusive; element
	// footprint for non-empty arrays is enforced separately below.
	if idsOff <= 0 || idsOff > dataWords-1 {
		return malformedLogf("onTransferBatch: ids offset out of bounds (%d/%d)", idsOff, dataWords)
	}
	if valsOff <= 0 || valsOff > dataWords-1 {
		return malformedLogf("onTransferBatch: vals offset out of bounds (%d/%d)", valsOff, dataWords)
	}
	idsLenRaw := bigInt(chunk(l.Data, int(idsOff))).Int64()
	valsLenRaw := bigInt(chunk(l.Data, int(valsOff))).Int64()

	if idsLenRaw == 0 && valsLenRaw == 0 {
		return nil // well-formed empty batch: nothing to apply
	}
	idsLen := idsLenRaw
	if idsLen < 0 || idsLen > maxBatchLength || idsLen > dataWords {
		return malformedLogf("onTransferBatch: ids length out of bounds (%d, max=%d)", idsLen, maxBatchLength)
	}
	if valsLenRaw != idsLen {
		return malformedLogf("onTransferBatch: ids/values length mismatch (%d vs %d)", idsLen, valsLenRaw)
	}
	if idsOff+1+idsLen > dataWords || valsOff+1+idsLen > dataWords {
		return malformedLogf("onTransferBatch: array extends past data boundary")
	}
	for i := int64(0); i < idsLen; i++ {
		id := bigStr(chunk(l.Data, int(idsOff+1+i)))
		val := bigInt(chunk(l.Data, int(valsOff+1+i))).String()
		if err := h.q.ApplyTransfer1155(ctx, collection, id, from, to, val); err != nil {
			return fmt.Errorf("onTransferBatch id=%s: %w", id, err)
		}
	}
	h.pubTyped("listing-updated", &sse.ListingUpdatedEvent{
		Event: "TransferBatch", Collection: collection, FromAddr: from, ToAddr: to,
	})
	return nil
}
