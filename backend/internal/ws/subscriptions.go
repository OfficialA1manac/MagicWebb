package ws

import (
	"strings"
)

// ── Channel patterns ──────────────────────────────────────────────────────────
//
// Channels follow the convention:
//   "token:<collection_addr>:<token_id>"   — events for a specific token
//   "collection:<collection_addr>"         — events for a collection
//   "user:<wallet_addr>"                   — events for a specific user

const (
	channelToken      = "token:"
	channelCollection = "collection:"
	channelUser       = "user:"
)

// isValidChannel reports whether a channel name follows our naming convention.
func isValidChannel(ch string) bool {
	// Must match a known prefix AND have at least one character after the colon.
	// Token channels additionally require a second colon ("token:addr:id" format).
	if len(ch) > len(channelToken) && strings.HasPrefix(ch, channelToken) {
		// Enforce "token:<addr>:<id>" — must contain a second colon.
		rest := ch[len(channelToken):]
		return strings.Contains(rest, ":")
	}
	return (len(ch) > len(channelCollection) && strings.HasPrefix(ch, channelCollection)) ||
		(len(ch) > len(channelUser) && strings.HasPrefix(ch, channelUser))
}

// channelMatchesEventType returns true if the channel pattern could ever
// produce relevant events. This is a coarse pre-filter used by the SSE
// bridge goroutine to skip events the client doesn't care about.
//
// v1 filter granularity: only the channel prefix (token:/collection:/user:)
// is checked, not the full encoded address/ID. A subscription to
// "token:0xAAA:1" matches ALL token events across every collection/token,
// not just 0xAAA:1. Per-entity scoping would require peeking into SSE
// payload bodies; this coarse category-level filter is the intentional
// v1 trade-off. Clients should not assume per-entity event isolation.
func channelMatchesEventType(channel, eventType string) bool {
	// Token and collection channels match all event types.
	if strings.HasPrefix(channel, channelToken) || strings.HasPrefix(channel, channelCollection) {
		return true
	}
	// User channels primarily match notification events.
	if strings.HasPrefix(channel, channelUser) {
		return eventType == "notification"
	}
	return false
}
