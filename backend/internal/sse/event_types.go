// Package sse — SSE-4: Typed event payloads for protobuf-native delivery.
//
// These Go types mirror the proto oneof messages defined in events.proto.
// When the gRPC bridge populates a typed event, receivers can access the
// structured data directly without JSON marshalling/unmarshalling.
//
// UNUSED: The TypedEvent interface + helpers below are forward-looking.
// The bridge currently uses json.Marshal(ev.Data) for serialization.
// TODO: After protoc regeneration produces native oneof accessors in
// events.pb.go, wire these typed payloads into the bridge for zero-JSON
// event delivery across the gRPC mesh.

package sse

// ── SSE-4: Typed event structs ────────────────────────────────────────────

// ListingUpdatedEvent carries the structured payload for listing-updated events.
type ListingUpdatedEvent struct {
	Collection string `json:"collection"`
	TokenID    string `json:"token_id"`
	Seller     string `json:"seller"`
	PriceWei   string `json:"price_wei"`
}

// AuctionUpdatedEvent carries the structured payload for auction-updated events.
type AuctionUpdatedEvent struct {
	AuctionID      int64  `json:"auction_id"`
	Collection     string `json:"collection"`
	TokenID        string `json:"token_id"`
	Status         string `json:"status"`
	HighestBid     string `json:"highest_bid"`
	HighestBidder  string `json:"highest_bidder"`
	EndTimeUnix    int64  `json:"end_time_unix"`
}

// OfferUpdatedEvent carries the structured payload for offer-updated events.
type OfferUpdatedEvent struct {
	OfferID    string `json:"offer_id"`
	Collection string `json:"collection"`
	TokenID    string `json:"token_id"`
	Bidder     string `json:"bidder"`
	AmountWei  string `json:"amount_wei"`
	Status     string `json:"status"`
}

// NotificationEvent carries the structured payload for notification events.
type NotificationEvent struct {
	User  string `json:"user"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Link  string `json:"link"`
}

// ActivityEvent carries the structured payload for activity events.
// Field names match the proto Activity message:
//   string event_type = 1  →  EventType
//   string collection = 2  →  Collection
//   ...
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

// UNUSED: Forward-looking — will be used when bridge switches to typed payloads.
// TypedEvent is implemented by all SSE-4 typed event payloads. It allows
// the bridge to serialize events into protobuf oneof fields without JSON.
// Each type returns its discriminator string matching the proto field name.
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

// string event type → proto oneof field
// UNUSED: Forward-looking — mapping from event type strings to proto oneof fields.
var typedEventProtoField = map[string]string{
	"listing-updated":  "listing_updated",
	"auction-updated":  "auction_updated",
	"offer-updated":    "offer_updated",
	"notification":     "notification",
	"activity":         "activity",
	"rpc-health":       "rpc_health",
}

// HasTypedPayload returns true when the event type has a corresponding
// protobuf oneof message. Producers that populate a TypedEvent can skip
// JSON marshalling entirely — the bridge serializes via proto binary.
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
