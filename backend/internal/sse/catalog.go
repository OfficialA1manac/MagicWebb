package sse

// Catalog is the one list of event types the Broadcaster carries. Every
// consumer-facing transport is a face of the same stream:
//
//	/ws                 WS   — product UI, channel-filtered JSON, replay by seq
//	/graphql/ws         GQL  — third-party dashboards, hydrated objects
//	Connect streams     CN   — bots/keepers, protobuf, backpressure
//	webhooks            WH   — outbound POSTs
//
// catalog_test.go asserts each transport handles exactly the types this
// table says it should, so adding an event here without wiring every face
// fails the build rather than drifting silently.
type CatalogEntry struct {
	Type string // wire name, e.g. "listing-updated"
	// Which faces must carry it.
	WS, GraphQL, Connect, Webhook bool
	// WS channels that can scope it (see ws/subscriptions.go).
	Channels []string
	Doc      string
}

// EventCatalog is ordered for documentation output.
var EventCatalog = []CatalogEntry{
	{Type: "listing-updated", WS: true, GraphQL: true, Connect: true, Webhook: true, Channels: []string{"token", "collection", "activity"}, Doc: "Listing created, cancelled, bought, price changed, or the NFT transferred."},
	{Type: "auction-updated", WS: true, GraphQL: true, Connect: true, Webhook: true, Channels: []string{"token", "collection", "activity"}, Doc: "Auction created, bid placed, extended (anti-snipe), settled, cancelled, loser refunded."},
	{Type: "offer-updated", WS: true, GraphQL: false, Connect: false, Webhook: true, Channels: []string{"token", "collection", "user", "activity"}, Doc: "Offer made, raised, accepted, declined, cancelled, refunded."},
	{Type: "notification", WS: true, GraphQL: true, Connect: true, Webhook: true, Channels: []string{"user"}, Doc: "Addressed to one wallet: outbid, sold, offer received, refund available."},
	{Type: "activity", WS: true, GraphQL: true, Connect: true, Webhook: true, Channels: []string{"activity", "collection", "token"}, Doc: "Feed row for any marketplace event."},
	{Type: "tx-indexed", WS: true, GraphQL: false, Connect: false, Webhook: false, Channels: []string{"tx", "activity"}, Doc: "Instant lane: the backend indexed this transaction (POST /api/v1/tx/observe)."},
	{Type: "rpc-health", WS: false, GraphQL: false, Connect: false, Webhook: false, Channels: nil, Doc: "Internal: RPC pool failover; surfaced on /metrics only."},
}

// CatalogTypes returns the wire names in catalog order.
func CatalogTypes() []string {
	out := make([]string, 0, len(EventCatalog))
	for _, e := range EventCatalog {
		out = append(out, e.Type)
	}
	return out
}

// CatalogEntryFor looks an entry up by wire name.
func CatalogEntryFor(t string) (CatalogEntry, bool) {
	for _, e := range EventCatalog {
		if e.Type == t {
			return e, true
		}
	}
	return CatalogEntry{}, false
}
