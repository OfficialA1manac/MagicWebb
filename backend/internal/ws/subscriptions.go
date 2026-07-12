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
	if len(ch) > len(channelToken) && strings.HasPrefix(ch, channelToken) {
		rest := ch[len(channelToken):]
		return strings.Contains(rest, ":")
	}
	return (len(ch) > len(channelCollection) && strings.HasPrefix(ch, channelCollection)) ||
		(len(ch) > len(channelUser) && strings.HasPrefix(ch, channelUser))
}

// eventPayload is the JSON shape extracted from sse.Event.Data for per-entity
// subscription scoping (W5). Only fields relevant to channel matching are
// included — collection address, token ID, and address-like fields.
type eventPayload struct {
	Collection string `json:"collection"`
	TokenID    string `json:"token_id"`
	Address    string `json:"address"`
	Seller     string `json:"seller"`
	Buyer      string `json:"buyer"`
	Bidder     string `json:"bidder"`
	Owner      string `json:"owner"`
	FromAddr   string `json:"from_addr"`
	ToAddr     string `json:"to_addr"`
}

// channelMatchesEvent returns true if the channel matches the event, using
// per-entity scoping when an event payload is available (W5).
//
// When ev is nil, falls back to coarse prefix-only matching (v1 behaviour):
// token/collection channels receive all events, user channels receive only
// notification events. This preserves backward compatibility for consumers
// that don't yet pass payload data.
//
// When ev is non-nil, performs exact entity matching:
//   - "token:0xABC:1" matches events where collection=="0xABC" AND token_id=="1"
//   - "collection:0xABC" matches events where collection=="0xABC"
//   - "user:0xDEF" matches events where any address field equals "0xDEF"
func channelMatchesEvent(channel, eventType string, ev *eventPayload) bool {
	if !channelMatchesPrefix(channel, eventType) {
		return false
	}
	if ev == nil {
		return true // no payload → coarse match
	}
	return channelMatchesPayload(channel, ev)
}

// channelMatchesPrefix is the v1 coarse filter — checks only the channel
// prefix against the event type. Token/collection channels match all events;
// user channels match notification events only.
func channelMatchesPrefix(channel, eventType string) bool {
	if strings.HasPrefix(channel, channelToken) || strings.HasPrefix(channel, channelCollection) {
		return true
	}
	if strings.HasPrefix(channel, channelUser) {
		return eventType == "notification"
	}
	return false
}

// channelMatchesPayload performs exact entity matching between the channel
// and the event payload.
func channelMatchesPayload(channel string, ev *eventPayload) bool {
	switch {
	case strings.HasPrefix(channel, channelToken):
		return channelMatchesToken(channel, ev)
	case strings.HasPrefix(channel, channelCollection):
		return channelMatchesCollection(channel, ev)
	case strings.HasPrefix(channel, channelUser):
		return channelMatchesUser(channel, ev)
	}
	return false
}

func channelMatchesToken(channel string, ev *eventPayload) bool {
	rest := strings.TrimPrefix(channel, channelToken)
	if rest == "" {
		return false
	}
	idx := strings.LastIndex(rest, ":")
	if idx <= 0 || idx >= len(rest)-1 {
		return false
	}
	return strings.EqualFold(rest[:idx], ev.Collection) && rest[idx+1:] == ev.TokenID
}

func channelMatchesCollection(channel string, ev *eventPayload) bool {
	return strings.EqualFold(strings.TrimPrefix(channel, channelCollection), ev.Collection)
}

func channelMatchesUser(channel string, ev *eventPayload) bool {
	chanAddr := strings.TrimPrefix(channel, channelUser)
	for _, a := range []string{ev.Address, ev.Seller, ev.Buyer, ev.Bidder, ev.Owner, ev.FromAddr, ev.ToAddr} {
		if a != "" && strings.EqualFold(chanAddr, a) {
			return true
		}
	}
	return false
}
