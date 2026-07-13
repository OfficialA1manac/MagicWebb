// Package marketplacev1 — streaming subscription types for gRPC real-time push.
//
// These types back the server-side streaming RPCs (SubscribeListings,
// SubscribeAuctions, SubscribeActivity, SubscribeNotifications). They are
// defined as plain Go structs (not protobuf-generated) so we can add
// streaming without regenerating the entire .pb.go file. The struct tags
// match the protobuf JSON mapping so Connect-RPC JSON encoding works.
package marketplacev1

// ── SubscribeListings ──────────────────────────────────────────────────────────

// SubscribeListingsRequest filters listing push events.
type SubscribeListingsRequest struct {
	Collection string `json:"collection,omitempty"` // optional collection address filter
	TokenID    string `json:"token_id,omitempty"`   // optional token ID filter
}

// SubscribeListingsResponse wraps a single listing push event.
type SubscribeListingsResponse struct {
	Listing *Listing `json:"listing"`
}

// ── SubscribeAuctions ───────────────────────────────────────────────────────────

// SubscribeAuctionsRequest filters auction push events.
type SubscribeAuctionsRequest struct {
	AuctionID int64 `json:"auction_id,omitempty"` // optional auction ID filter
}

// SubscribeAuctionsResponse wraps a single auction push event.
type SubscribeAuctionsResponse struct {
	Auction *Auction `json:"auction"`
}

// ── SubscribeActivity ───────────────────────────────────────────────────────────

// SubscribeActivityRequest filters activity push events.
type SubscribeActivityRequest struct {
	Address    string `json:"address,omitempty"`    // optional address filter
	Collection string `json:"collection,omitempty"` // optional collection filter
	TokenID    string `json:"token_id,omitempty"`   // optional token ID filter
}

// SubscribeActivityResponse wraps a single activity push event.
type SubscribeActivityResponse struct {
	Event *ActivityEvent `json:"event"`
}

// ── SubscribeNotifications ──────────────────────────────────────────────────────

// SubscribeNotificationsRequest filters notification push events.
type SubscribeNotificationsRequest struct {
	Address string `json:"address,omitempty"` // optional user address filter
}

// SubscribeNotificationsResponse wraps a single notification push event.
type SubscribeNotificationsResponse struct {
	// Notification fields are passed as raw JSON bytes because notifications
	// have no protobuf equivalent yet. The GraphQL resolver maps them.
	Payload []byte `json:"payload"`
}
