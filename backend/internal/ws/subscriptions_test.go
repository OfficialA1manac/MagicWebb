package ws

import (
	"encoding/json"
	"testing"
)

// ── isValidChannel ──────────────────────────────────────────────────────────

func TestIsValidChannel_ValidPatterns(t *testing.T) {
	tests := []struct {
		channel string
	}{
		{"token:0xabc:1"},
		{"token:0xdef:12345"},
		{"collection:0xabc"},
		{"collection:0xdef"},
		{"user:0xabc"},
		{"user:0xdef"},
	}
	for _, tc := range tests {
		if !isValidChannel(tc.channel) {
			t.Errorf("isValidChannel(%q) = false, want true", tc.channel)
		}
	}
}

func TestIsValidChannel_InvalidPatterns(t *testing.T) {
	tests := []struct {
		channel string
	}{
		{""},
		{"token:"},           // missing addr:id
		{"token:0xabc"},      // missing :id
		{"collection:"},      // missing addr
		{":0xabc"},           // no prefix
		{"user:"},            // missing addr
		{"notify:0xabc"},     // unknown prefix
		{"Token:0xabc:1"},    // wrong case
		{"Collection:0xabc"}, // wrong case
	}
	for _, tc := range tests {
		if isValidChannel(tc.channel) {
			t.Errorf("isValidChannel(%q) = true, want false", tc.channel)
		}
	}
}

// ── channelMatchesEventType ─────────────────────────────────────────────────

func TestChannelMatchesEventType_TokenOrCollectionMatchesAll(t *testing.T) {
	eventTypes := []string{"listing-updated", "auction-updated", "offer-updated", "notification", "activity", "some-unknown-event"}
	for _, ch := range []string{"token:0xabc:1", "collection:0xabc"} {
		for _, ev := range eventTypes {
			if !channelMatchesEventType(ch, ev) {
				t.Errorf("channelMatchesEventType(%q, %q) = false, want true", ch, ev)
			}
		}
	}
}

func TestChannelMatchesEventType_UserMatchesNotificationOnly(t *testing.T) {
	tests := []struct {
		eventType string
		want      bool
	}{
		{"notification", true},
		{"listing-updated", false},
		{"auction-updated", false},
		{"offer-updated", false},
		{"activity", false},
		{"", false},
	}
	for _, tc := range tests {
		got := channelMatchesEventType("user:0xabc", tc.eventType)
		if got != tc.want {
			t.Errorf("channelMatchesEventType(\"user:0xabc\", %q) = %v, want %v", tc.eventType, got, tc.want)
		}
	}
}

// ── subscribe / unsubscribe / isSubscribedToEvent ───────────────────────────

// newTestConn creates a Connection with a buffered send channel for test inspection.
func newTestConn() *Connection {
	return &Connection{
		id:   "test-conn",
		send: make(chan []byte, 64),
		done: make(chan struct{}),
	}
}

// recvWS decodes the next message from the connection's send channel, or fails.
func recvWS(t *testing.T, conn *Connection) Message {
	t.Helper()
	select {
	case raw := <-conn.send:
		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatalf("recvWS: invalid JSON: %v", err)
		}
		return msg
	default:
		t.Fatal("recvWS: no message available")
		return Message{}
	}
}

func TestSubscribe_AddsValidChannels(t *testing.T) {
	conn := newTestConn()
	conn.subscribe([]string{"token:0xabc:1", "collection:0xdef", "invalid:channel"})

	// Only valid channels should be in the subscription set
	conn.subMu.RLock()
	if _, ok := conn.subscriptions["token:0xabc:1"]; !ok {
		t.Error("expected token:0xabc:1 to be subscribed")
	}
	if _, ok := conn.subscriptions["collection:0xdef"]; !ok {
		t.Error("expected collection:0xdef to be subscribed")
	}
	if _, ok := conn.subscriptions["invalid:channel"]; ok {
		t.Error("invalid:channel should not be subscribed")
	}
	conn.subMu.RUnlock()

	// Check confirmation message
	msg := recvWS(t, conn)
	if msg.Type != MsgSubscribed {
		t.Fatalf("expected MsgSubscribed, got %s", msg.Type)
	}
	var data SubscribedData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		t.Fatalf("unmarshal SubscribedData: %v", err)
	}
	if len(data.Channels) != 2 {
		t.Fatalf("expected 2 subscribed channels, got %d: %v", len(data.Channels), data.Channels)
	}
}

func TestSubscribe_EmptyChannels(t *testing.T) {
	conn := newTestConn()
	conn.subscribe(nil)

	conn.subMu.RLock()
	if len(conn.subscriptions) != 0 {
		t.Errorf("expected empty subscriptions, got %d", len(conn.subscriptions))
	}
	conn.subMu.RUnlock()

	msg := recvWS(t, conn)
	if msg.Type != MsgSubscribed {
		t.Fatalf("expected MsgSubscribed, got %s", msg.Type)
	}
	var data SubscribedData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		t.Fatalf("unmarshal SubscribedData: %v", err)
	}
	if len(data.Channels) != 0 {
		t.Fatalf("expected 0 subscribed channels, got %d", len(data.Channels))
	}
}

func TestUnsubscribe_RemovesChannels(t *testing.T) {
	conn := newTestConn()
	conn.subscriptions = map[string]struct{}{
		"token:0xabc:1":    {},
		"collection:0xdef": {},
		"user:0xaaa":      {},
	}

	conn.unsubscribe([]string{"token:0xabc:1", "user:0xaaa"})

	conn.subMu.RLock()
	if _, ok := conn.subscriptions["token:0xabc:1"]; ok {
		t.Error("token:0xabc:1 should have been removed")
	}
	if _, ok := conn.subscriptions["collection:0xdef"]; !ok {
		t.Error("collection:0xdef should still be subscribed")
	}
	if _, ok := conn.subscriptions["user:0xaaa"]; ok {
		t.Error("user:0xaaa should have been removed")
	}
	conn.subMu.RUnlock()

	msg := recvWS(t, conn)
	if msg.Type != MsgUnsubscribed {
		t.Fatalf("expected MsgUnsubscribed, got %s", msg.Type)
	}
	var data UnsubscribedData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		t.Fatalf("unmarshal UnsubscribedData: %v", err)
	}
	if len(data.Channels) != 2 {
		t.Fatalf("expected 2 unsubscribed channels, got %d", len(data.Channels))
	}
}

func TestUnsubscribe_NonExistentChannel(t *testing.T) {
	conn := newTestConn()
	conn.subscriptions = map[string]struct{}{
		"token:0xabc:1": {},
	}

	conn.unsubscribe([]string{"token:0xnonexistent"})

	conn.subMu.RLock()
	if len(conn.subscriptions) != 1 {
		t.Errorf("expected 1 subscription, got %d", len(conn.subscriptions))
	}
	conn.subMu.RUnlock()
}

func TestIsSubscribedToEvent_NoSubscriptionsReturnsTrue(t *testing.T) {
	conn := newTestConn()
	// No subscriptions — backward-compatible default: receive all
	if !conn.isSubscribedToEvent("listing-updated") {
		t.Error("expected true for listing-updated with no subscriptions")
	}
	if !conn.isSubscribedToEvent("unknown-event") {
		t.Error("expected true for unknown event with no subscriptions")
	}
}

func TestIsSubscribedToEvent_TokenOrCollectionSubscribed(t *testing.T) {
	conn := newTestConn()
	conn.subscriptions = map[string]struct{}{
		"token:0xabc:1": {},
	}

	// Token subscriptions match all event types
	if !conn.isSubscribedToEvent("listing-updated") {
		t.Error("token subscription should match listing-updated")
	}
	if !conn.isSubscribedToEvent("auction-updated") {
		t.Error("token subscription should match auction-updated")
	}
	if !conn.isSubscribedToEvent("offer-updated") {
		t.Error("token subscription should match offer-updated")
	}
	if !conn.isSubscribedToEvent("notification") {
		t.Error("token subscription should match notification")
	}
	if !conn.isSubscribedToEvent("activity") {
		t.Error("token subscription should match activity")
	}
}

func TestIsSubscribedToEvent_UserSubscribed(t *testing.T) {
	conn := newTestConn()
	conn.subscriptions = map[string]struct{}{
		"user:0xabc": {},
	}

	if !conn.isSubscribedToEvent("notification") {
		t.Error("user subscription should match notification")
	}
	if conn.isSubscribedToEvent("listing-updated") {
		t.Error("user subscription should NOT match listing-updated")
	}
	if conn.isSubscribedToEvent("auction-updated") {
		t.Error("user subscription should NOT match auction-updated")
	}
}

func TestIsSubscribedToEvent_MultipleSubscriptions(t *testing.T) {
	conn := newTestConn()
	conn.subscriptions = map[string]struct{}{
		"collection:0xabc": {},
		"user:0xdef":       {},
	}

	// Collection subscribes to all
	if !conn.isSubscribedToEvent("listing-updated") {
		t.Error("collection subscription should match listing-updated")
	}
	// User subscribes to notification only
	if !conn.isSubscribedToEvent("notification") {
		t.Error("at least one subscription should match notification")
	}
}

// ── Concurrent safety smoke test ────────────────────────────────────────────

func TestSubscribeUnsubscribeConcurrentSafe(t *testing.T) {
	conn := newTestConn()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			conn.subscribe([]string{"token:0xabc:1", "collection:0xdef"})
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 100; i++ {
			_ = conn.isSubscribedToEvent("listing-updated")
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 100; i++ {
			conn.unsubscribe([]string{"token:0xabc:1"})
		}
		done <- struct{}{}
	}()
	// Drain the send channel so subscribe/unsubscribe don't block
	go func() {
		for range conn.send {
		}
	}()
	<-done
	<-done
	<-done
	// If we get here without a race or panic, the mutex is working
}

// ── Helper: test-only writeJSON smoke test ──────────────────────────────────

func TestWriteJSON(t *testing.T) {
	conn := newTestConn()
	msg := Message{Type: MsgPong, Data: mustJSON(PongData{ServerTimeMs: 12345})}
	conn.writeJSON(msg)

	got := recvWS(t, conn)
	if got.Type != MsgPong {
		t.Fatalf("expected MsgPong, got %s", got.Type)
	}
	var pong PongData
	if err := json.Unmarshal(got.Data, &pong); err != nil {
		t.Fatalf("unmarshal PongData: %v", err)
	}
	if pong.ServerTimeMs != 12345 {
		t.Fatalf("expected ServerTimeMs=12345, got %d", pong.ServerTimeMs)
	}
}
