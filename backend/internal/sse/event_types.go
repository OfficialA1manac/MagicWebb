// Package sse — SSE-4: Typed event payloads for protobuf-native delivery.
//
// These Go types mirror the proto oneof messages defined in events.proto.
// When the gRPC bridge populates a typed event, receivers can access the
// structured data directly without JSON marshalling/unmarshalling.
//
// SSE-4 status: ACTIVE. The bridge Send() populates the oneof event field
// and StreamEvents() reads it back, eliminating JSON round-trips for all
// six known event types. The bytes data field remains for backward compat.

package sse

import (
	"math"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse/proto"
)

// ── SSE-4: Typed event structs ────────────────────────────────────────────

// ListingUpdatedEvent carries the structured payload for listing-updated events.
type ListingUpdatedEvent struct {
	Event      string `json:"event"`      // sub-event: "Listed", "Cancelled", "Bought", "Transfer", "TransferSingle", "TransferBatch"
	Collection string `json:"collection"`
	TokenID    string `json:"token_id"`
	Seller     string `json:"seller"`
	PriceWei   string `json:"price_wei"`
	Buyer      string `json:"buyer,omitempty"`
	ToAddr     string `json:"to_addr,omitempty"`
	FromAddr   string `json:"from_addr,omitempty"`
	// Data carries the full DB row (ListingRow, etc.) for consumers that
	// need fields beyond the proto oneof schema. JSON-marshalled into
	// msg.Data for backward compat across the bridge.
	Data any `json:"data,omitempty"`
}

// AuctionUpdatedEvent carries the structured payload for auction-updated events.
type AuctionUpdatedEvent struct {
	Event         string `json:"event"` // sub-event: "AuctionCreated", "BidPlaced", "OutbidNotification", "AuctionExtended", "AuctionSettled", "AuctionSettlementFailed", "AuctionCancelled", "LoserRefunded"
	AuctionID     int64  `json:"auction_id"`
	Collection    string `json:"collection,omitempty"`
	TokenID       string `json:"token_id,omitempty"`
	Status        string `json:"status,omitempty"`
	HighestBid    string `json:"highest_bid,omitempty"`
	HighestBidder string `json:"highest_bidder,omitempty"`
	EndTimeUnix   int64  `json:"end_time_unix,omitempty"`
	Seller        string `json:"seller,omitempty"`
	Winner        string `json:"winner,omitempty"`
	AmtWei        string `json:"amt_wei,omitempty"`
	Bidder        string `json:"bidder,omitempty"`
	EffectiveWei  string `json:"effective_wei,omitempty"`
	OutbidAddr    string `json:"outbid_addr,omitempty"`
	LeaderTotal   string `json:"leader_total,omitempty"`
	// Data carries the full DB row (AuctionRow, etc.).
	Data any `json:"data,omitempty"`
}

// OfferUpdatedEvent carries the structured payload for offer-updated events.
type OfferUpdatedEvent struct {
	Event      string `json:"event"` // sub-event: "OfferMade", "OfferAccepted", "OfferRefunded"
	OfferID    string `json:"offer_id,omitempty"`
	Collection string `json:"collection"`
	TokenID    string `json:"token_id"`
	Bidder     string `json:"bidder"`
	AmountWei  string `json:"amount_wei,omitempty"`
	Status     string `json:"status,omitempty"`
	Seller     string `json:"seller,omitempty"`
	Principal  string `json:"principal,omitempty"`
}

// NotificationEvent carries the structured payload for notification events.
type NotificationEvent struct {
	User     string `json:"user"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Link     string `json:"link"`
	Kind     string `json:"kind"`
	UserAddr string `json:"user_addr"`
}

// ActivityEvent carries the structured payload for activity events.
type ActivityEvent struct {
	EventType  string `json:"event_type"`
	Collection string `json:"collection"`
	TokenID    string `json:"token_id"`
	From       string `json:"from"`
	To         string `json:"to"`
	PriceWei   string `json:"price_wei"`
	TxHash     string `json:"tx_hash"`
}

// RPCHealthEvent carries the structured payload for rpc-health events (RPC-1).
type RPCHealthEvent struct {
	EndpointIndex int32 `json:"endpoint_index"`
	Healthy       bool  `json:"healthy"`
	EndpointCount int32 `json:"endpoint_count"`
	HealthyCount  int32 `json:"healthy_count"`
}

// ── Typed event interface ─────────────────────────────────────────────────

// TypedEvent is implemented by all SSE-4 typed event payloads. It allows
// the bridge to serialize events into protobuf oneof fields without JSON.
type TypedEvent interface {
	ProtoOneofField() string
}

func (ListingUpdatedEvent) ProtoOneofField() string  { return "listing_updated" }
func (AuctionUpdatedEvent) ProtoOneofField() string   { return "auction_updated" }
func (OfferUpdatedEvent) ProtoOneofField() string     { return "offer_updated" }
func (NotificationEvent) ProtoOneofField() string     { return "notification" }
func (ActivityEvent) ProtoOneofField() string         { return "activity" }
func (RPCHealthEvent) ProtoOneofField() string        { return "rpc_health" }

// ── Event type name → protobuf oneof field name mapping ───────────────────

var typedEventProtoField = map[string]string{
	"listing-updated": "listing_updated",
	"auction-updated": "auction_updated",
	"offer-updated":   "offer_updated",
	"notification":    "notification",
	"activity":        "activity",
	"rpc-health":      "rpc_health",
}

// HasTypedPayload returns true when the event type has a corresponding
// protobuf oneof message.
func HasTypedPayload(eventType string) bool {
	_, ok := typedEventProtoField[eventType]
	return ok
}

// ProtoFieldName returns the protobuf oneof field name for a given string
// event type (e.g., "listing-updated" → "listing_updated"). Returns empty
// string for event types without a typed payload.
func ProtoFieldName(eventType string) string {
	return typedEventProtoField[eventType]
}

// ── SSE-4: Proto oneof conversion (bridge Send / StreamEvents) ────────────

// PopulateProtoOneof sets the typed oneof event field on a proto.EventMessage
// when the event type is known. Handles both typed Go structs (preferred) and
// map[string]any (backward compat). Sets msg.Event directly because the proto
// oneof interface type (isEventMessage_Event) is unexported.
func PopulateProtoOneof(msg *proto.EventMessage, eventType string, data any) {
	switch eventType {
	case "listing-updated":
		populateListingUpdated(msg, data)
	case "auction-updated":
		populateAuctionUpdated(msg, data)
	case "offer-updated":
		populateOfferUpdated(msg, data)
	case "notification":
		populateNotification(msg, data)
	case "activity":
		populateActivity(msg, data)
	case "rpc-health":
		populateRPCHealth(msg, data)
	}
}

// FromProtoOneof extracts a typed Go struct from a protobuf EventMessage's
// oneof field. Returns nil when no typed payload is present (fall back to
// json.RawMessage). The returned struct implements TypedEvent.
func FromProtoOneof(msg *proto.EventMessage) TypedEvent {
	if msg == nil {
		return nil
	}
	switch e := msg.GetEvent().(type) {
	case *proto.EventMessage_ListingUpdated:
		return fromListingUpdatedProto(e.ListingUpdated)
	case *proto.EventMessage_AuctionUpdated:
		return fromAuctionUpdatedProto(e.AuctionUpdated)
	case *proto.EventMessage_OfferUpdated:
		return fromOfferUpdatedProto(e.OfferUpdated)
	case *proto.EventMessage_Notification:
		return fromNotificationProto(e.Notification)
	case *proto.EventMessage_Activity:
		return fromActivityProto(e.Activity)
	case *proto.EventMessage_RpcHealth:
		return fromRPCHealthProto(e.RpcHealth)
	}
	return nil
}

// ── SSE-4: Populate helpers (set msg.Event directly) ──────────────────────

func populateListingUpdated(msg *proto.EventMessage, data any) {
	var ev *ListingUpdatedEvent
	switch d := data.(type) {
	case *ListingUpdatedEvent:
		ev = d
	case ListingUpdatedEvent:
		ev = &d
	case map[string]any:
		ev = &ListingUpdatedEvent{
			Event:      mapStr(d, "event"),
			Collection: mapStr(d, "collection"),
			TokenID:    mapStr(d, "tokenId"),
			Seller:     mapStr(d, "seller"),
			PriceWei:   mapStr(d, "priceWei"),
			Buyer:      mapStr(d, "buyer"),
			ToAddr:     mapStr(d, "to"),
			FromAddr:   mapStr(d, "from"),
			Data:       d["data"],
		}
	default:
		return
	}
	msg.Event = &proto.EventMessage_ListingUpdated{
		ListingUpdated: &proto.ListingUpdated{
			Event:      ev.Event,
			Collection: ev.Collection,
			TokenId:    ev.TokenID,
			Seller:     ev.Seller,
			PriceWei:   ev.PriceWei,
			Buyer:      ev.Buyer,
			ToAddr:     ev.ToAddr,
			FromAddr:   ev.FromAddr,
		},
	}
}

func fromListingUpdatedProto(p *proto.ListingUpdated) *ListingUpdatedEvent {
	if p == nil {
		return nil
	}
	return &ListingUpdatedEvent{
		Event:      p.Event,
		Collection: p.Collection,
		TokenID:    p.TokenId,
		Seller:     p.Seller,
		PriceWei:   p.PriceWei,
		Buyer:      p.Buyer,
		ToAddr:     p.ToAddr,
		FromAddr:   p.FromAddr,
	}
}

func populateAuctionUpdated(msg *proto.EventMessage, data any) {
	var ev *AuctionUpdatedEvent
	switch d := data.(type) {
	case *AuctionUpdatedEvent:
		ev = d
	case AuctionUpdatedEvent:
		ev = &d
	case map[string]any:
		ev = &AuctionUpdatedEvent{
			Event:        mapStr(d, "event"),
			AuctionID:    mapInt64(d, "auctionId"),
			Collection:   mapStr(d, "collection"),
			TokenID:      mapStr(d, "tokenId"),
			Seller:       mapStr(d, "seller"),
			Winner:       mapStr(d, "winner"),
			AmtWei:       mapStr(d, "amtWei"),
			Bidder:       mapStr(d, "bidder"),
			EffectiveWei: mapStr(d, "effectiveWei"),
			OutbidAddr:   mapStr(d, "outbid"),
			LeaderTotal:  mapStr(d, "leaderTotalWei"),
			EndTimeUnix:  mapInt64(d, "endsAt"),
			Data:         d["data"],
		}
	default:
		return
	}
	msg.Event = &proto.EventMessage_AuctionUpdated{
		AuctionUpdated: &proto.AuctionUpdated{
			Event:         ev.Event,
			AuctionId:     ev.AuctionID,
			Collection:    ev.Collection,
			TokenId:       ev.TokenID,
			Seller:        ev.Seller,
			Winner:        ev.Winner,
			AmtWei:        ev.AmtWei,
			Bidder:        ev.Bidder,
			EffectiveWei:  ev.EffectiveWei,
			OutbidAddr:    ev.OutbidAddr,
			LeaderTotal:   ev.LeaderTotal,
			EndTimeUnix:   ev.EndTimeUnix,
			Status:        ev.Status,
			HighestBid:    ev.HighestBid,
			HighestBidder: ev.HighestBidder,
		},
	}
}

func fromAuctionUpdatedProto(p *proto.AuctionUpdated) *AuctionUpdatedEvent {
	if p == nil {
		return nil
	}
	return &AuctionUpdatedEvent{
		Event:         p.Event,
		AuctionID:     p.AuctionId,
		Collection:    p.Collection,
		TokenID:       p.TokenId,
		Status:        p.Status,
		HighestBid:    p.HighestBid,
		HighestBidder: p.HighestBidder,
		EndTimeUnix:   p.EndTimeUnix,
		Seller:        p.Seller,
		Winner:        p.Winner,
		AmtWei:        p.AmtWei,
		Bidder:        p.Bidder,
		EffectiveWei:  p.EffectiveWei,
		OutbidAddr:    p.OutbidAddr,
		LeaderTotal:   p.LeaderTotal,
	}
}

func populateOfferUpdated(msg *proto.EventMessage, data any) {
	var ev *OfferUpdatedEvent
	switch d := data.(type) {
	case *OfferUpdatedEvent:
		ev = d
	case OfferUpdatedEvent:
		ev = &d
	case map[string]any:
		ev = &OfferUpdatedEvent{
			Event:      mapStr(d, "event"),
			Collection: mapStr(d, "collection"),
			TokenID:    mapStr(d, "tokenId"),
			Bidder:     mapStr(d, "bidder"),
			Principal:  mapStr(d, "principal"),
			Seller:     mapStr(d, "seller"),
		}
	default:
		return
	}
	msg.Event = &proto.EventMessage_OfferUpdated{
		OfferUpdated: &proto.OfferUpdated{
			Event:      ev.Event,
			Collection: ev.Collection,
			TokenId:    ev.TokenID,
			Bidder:     ev.Bidder,
			Principal:  ev.Principal,
			Seller:     ev.Seller,
			AmountWei:  firstNonEmpty(ev.AmountWei, ev.Principal),
		},
	}
}

func fromOfferUpdatedProto(p *proto.OfferUpdated) *OfferUpdatedEvent {
	if p == nil {
		return nil
	}
	return &OfferUpdatedEvent{
		Event:      p.Event,
		Collection: p.Collection,
		TokenID:    p.TokenId,
		Bidder:     p.Bidder,
		Principal:  p.Principal,
		Seller:     p.Seller,
		AmountWei:  firstNonEmpty(p.AmountWei, p.Principal),
	}
}

func populateNotification(msg *proto.EventMessage, data any) {
	var ev *NotificationEvent
	switch d := data.(type) {
	case *NotificationEvent:
		ev = d
	case NotificationEvent:
		ev = &d
	case map[string]any:
		ev = &NotificationEvent{
			User:     mapStr(d, "user_addr"),
			UserAddr: mapStr(d, "user_addr"),
			Title:    mapStr(d, "title"),
			Body:     mapStr(d, "body"),
			Link:     mapStr(d, "link"),
			Kind:     mapStr(d, "kind"),
		}
	default:
		return
	}
	msg.Event = &proto.EventMessage_Notification{
		Notification: &proto.Notification{
			User:     ev.User,
			UserAddr: ev.UserAddr,
			Title:    ev.Title,
			Body:     ev.Body,
			Link:     ev.Link,
			Kind:     ev.Kind,
		},
	}
}

func fromNotificationProto(p *proto.Notification) *NotificationEvent {
	if p == nil {
		return nil
	}
	return &NotificationEvent{
		User:     p.User,
		UserAddr: p.UserAddr,
		Title:    p.Title,
		Body:     p.Body,
		Link:     p.Link,
		Kind:     p.Kind,
	}
}

func populateActivity(msg *proto.EventMessage, data any) {
	var ev *ActivityEvent
	switch d := data.(type) {
	case *ActivityEvent:
		ev = d
	case ActivityEvent:
		ev = &d
	case map[string]any:
		ev = &ActivityEvent{
			EventType:  mapStr(d, "eventType"),
			Collection: mapStr(d, "collection"),
			TokenID:    mapStr(d, "tokenId"),
			From:       mapStr(d, "from"),
			To:         mapStr(d, "to"),
			PriceWei:   mapStr(d, "priceWei"),
			TxHash:     mapStr(d, "txHash"),
		}
	default:
		return
	}
	msg.Event = &proto.EventMessage_Activity{
		Activity: &proto.Activity{
			EventType:  ev.EventType,
			Collection: ev.Collection,
			TokenId:    ev.TokenID,
			From:       ev.From,
			To:         ev.To,
			PriceWei:   ev.PriceWei,
			TxHash:     ev.TxHash,
		},
	}
}

func fromActivityProto(p *proto.Activity) *ActivityEvent {
	if p == nil {
		return nil
	}
	return &ActivityEvent{
		EventType:  p.EventType,
		Collection: p.Collection,
		TokenID:    p.TokenId,
		From:       p.From,
		To:         p.To,
		PriceWei:   p.PriceWei,
		TxHash:     p.TxHash,
	}
}

func populateRPCHealth(msg *proto.EventMessage, data any) {
	var ev *RPCHealthEvent
	switch d := data.(type) {
	case *RPCHealthEvent:
		ev = d
	case RPCHealthEvent:
		ev = &d
	case map[string]any:
		healthy, _ := d["healthy"].(bool)
		ev = &RPCHealthEvent{
			EndpointIndex: int32(mapInt64(d, "endpointIndex")),
			Healthy:       healthy,
			EndpointCount: int32(mapInt64(d, "endpointCount")),
			HealthyCount:  int32(mapInt64(d, "healthyCount")),
		}
	default:
		return
	}
	msg.Event = &proto.EventMessage_RpcHealth{
		RpcHealth: &proto.RPCHealth{
			EndpointIndex: ev.EndpointIndex,
			Healthy:       ev.Healthy,
			EndpointCount: ev.EndpointCount,
			HealthyCount:  ev.HealthyCount,
		},
	}
}

func fromRPCHealthProto(p *proto.RPCHealth) *RPCHealthEvent {
	if p == nil {
		return nil
	}
	return &RPCHealthEvent{
		EndpointIndex: p.EndpointIndex,
		Healthy:       p.Healthy,
		EndpointCount: p.EndpointCount,
		HealthyCount:  p.HealthyCount,
	}
}

// ── SSE-4: Map extraction helpers ─────────────────────────────────────────

// mapStr extracts a string value from a map[string]any by key.
// Accepts string, int, int64, and float64 (JSON numbers deserialize as float64).
func mapStr(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case string:
			return val
		case int:
			return itoa(int64(val))
		case int64:
			return itoa(val)
		case float64:
			return itoa(int64(val))
		}
	}
	return ""
}

// mapInt64 extracts an int64 value from a map[string]any by key.
// Accepts int, int64, float64. Returns 0 if absent or unconvertible.
func mapInt64(m map[string]any, key string) int64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case int64:
			return n
		case int:
			return int64(n)
		case float64:
			return int64(n)
		}
	}
	return 0
}

// itoa is a minimal int64→string converter (no fmt import for perf).
// Handles math.MinInt64 safely by special-casing before negation.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		if n == math.MinInt64 {
			// -n would overflow int64
			return "-9223372036854775808"
		}
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte(n%10) + '0'
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// firstNonEmpty returns the first non-empty string. Used for coalescing
// fields that may be populated under different names (e.g., amount_wei vs principal).
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
