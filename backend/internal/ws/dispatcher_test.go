package ws

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// newDispatchHandler builds a Handler wired to a real broadcaster with the
// single-subscription dispatcher running, and registers cleanup.
func newDispatchHandler(t *testing.T) (*Handler, *sse.Broadcaster) {
	t.Helper()
	bc := sse.New()
	h := &Handler{
		bcast: bc,
		conns: make(map[string]*Connection),
	}
	if !h.StartDispatcher() {
		t.Fatal("StartDispatcher returned false")
	}
	t.Cleanup(func() {
		h.StopDispatcher()
		bc.Shutdown()
	})
	return h, bc
}

func registerConn(h *Handler, id, addr string, subs ...string) *Connection {
	conn := &Connection{
		id:   id,
		addr: addr,
		send: make(chan []byte, 8),
		done: make(chan struct{}),
	}
	if len(subs) > 0 {
		conn.subscriptions = make(map[string]struct{}, len(subs))
		for _, s := range subs {
			conn.subscriptions[s] = struct{}{}
		}
	}
	h.mu.Lock()
	h.conns[id] = conn
	h.mu.Unlock()
	return conn
}

// recvMsg waits for one message on the connection's send channel.
func recvMsg(t *testing.T, conn *Connection, within time.Duration) ([]byte, bool) {
	t.Helper()
	select {
	case msg := <-conn.send:
		return msg, true
	case <-time.After(within):
		return nil, false
	}
}

// TestDispatcherSharesBytesAcrossConns verifies one published event is
// marshalled once and the identical bytes reach every connection.
func TestDispatcherSharesBytesAcrossConns(t *testing.T) {
	h, bc := newDispatchHandler(t)
	a := registerConn(h, "a", "")
	b := registerConn(h, "b", "")

	bc.Publish(sse.Event{Type: "listing-updated", Data: map[string]any{"collection": "0xCAFE", "token_id": "7"}})

	ma, oka := recvMsg(t, a, time.Second)
	mb, okb := recvMsg(t, b, time.Second)
	if !oka || !okb {
		t.Fatalf("both conns should receive the event (a=%v b=%v)", oka, okb)
	}
	if string(ma) != string(mb) {
		t.Fatalf("conns received different bytes:\n a=%s\n b=%s", ma, mb)
	}
	var env Message
	if err := json.Unmarshal(ma, &env); err != nil {
		t.Fatalf("envelope did not decode: %v", err)
	}
	if env.Type != "listing-updated" {
		t.Fatalf("env.Type = %q, want listing-updated", env.Type)
	}
	if env.Seq == 0 {
		t.Fatal("env.Seq should be set by the broadcaster")
	}
}

// TestDispatcherNotificationRBAC verifies notification events reach only the
// connection whose authenticated wallet matches user_addr.
func TestDispatcherNotificationRBAC(t *testing.T) {
	h, bc := newDispatchHandler(t)
	target := "0x1111111111111111111111111111111111111111"
	other := "0x2222222222222222222222222222222222222222"
	mine := registerConn(h, "mine", target)
	theirs := registerConn(h, "theirs", other)
	anon := registerConn(h, "anon", "") // unauthenticated → never gets notifications

	bc.Publish(sse.Event{Type: "notification", Data: map[string]any{"user_addr": target, "message": "hi"}})

	if _, ok := recvMsg(t, mine, time.Second); !ok {
		t.Fatal("matching wallet should receive the notification")
	}
	if _, ok := recvMsg(t, theirs, 200*time.Millisecond); ok {
		t.Fatal("non-matching wallet must NOT receive the notification")
	}
	if _, ok := recvMsg(t, anon, 200*time.Millisecond); ok {
		t.Fatal("unauthenticated conn must NOT receive the notification")
	}
}

// TestDispatcherSubscriptionFilter verifies channel subscriptions gate
// delivery: a conn subscribed only to user:* (notification-only) does not get
// listing-updated, while an unsubscribed conn gets everything.
func TestDispatcherSubscriptionFilter(t *testing.T) {
	h, bc := newDispatchHandler(t)
	userAddr := "0x3333333333333333333333333333333333333333"
	scoped := registerConn(h, "scoped", userAddr, "user:"+userAddr)
	open := registerConn(h, "open", "")

	bc.Publish(sse.Event{Type: "listing-updated", Data: map[string]any{"collection": "0xC", "token_id": "1"}})

	if _, ok := recvMsg(t, open, time.Second); !ok {
		t.Fatal("unsubscribed conn should receive listing-updated")
	}
	if _, ok := recvMsg(t, scoped, 200*time.Millisecond); ok {
		t.Fatal("user-only subscription must NOT receive listing-updated")
	}
}

// TestDispatcherMalformedStillDelivered verifies a payload that is not a JSON
// object (undecodable into eventPayload) is still delivered to a subscribed
// conn — parity with the pre-refactor "err on the side of delivery" rule.
func TestDispatcherMalformedStillDelivered(t *testing.T) {
	h, bc := newDispatchHandler(t)
	// A subscription forces the decode path (no-subscription short-circuits).
	scoped := registerConn(h, "scoped", "", "token:0xC")

	// A bare JSON string is valid JSON but fails to decode into eventPayload.
	bc.Publish(sse.Event{Type: "listing-updated", Data: "not-an-object"})

	if _, ok := recvMsg(t, scoped, time.Second); !ok {
		t.Fatal("malformed payload should still be delivered to a subscriber")
	}
}

// TestDispatcherSlowClientDrop verifies a full send buffer drops the new event
// without blocking the dispatcher or affecting other connections.
func TestDispatcherSlowClientDrop(t *testing.T) {
	h, bc := newDispatchHandler(t)
	// Slow conn: cap-1 buffer pre-filled so the next send must drop.
	slow := &Connection{id: "slow", send: make(chan []byte, 1), done: make(chan struct{})}
	slow.send <- []byte("prefill")
	h.mu.Lock()
	h.conns[slow.id] = slow
	h.mu.Unlock()
	fast := registerConn(h, "fast", "")

	bc.Publish(sse.Event{Type: "activity", Data: map[string]any{"x": 1}})

	// Fast conn still gets the event — the slow one did not block the loop.
	if _, ok := recvMsg(t, fast, time.Second); !ok {
		t.Fatal("fast conn should receive the event despite a slow peer")
	}
	// Slow conn's buffer still holds only the prefill; the new event dropped.
	got := <-slow.send
	if string(got) != "prefill" {
		t.Fatalf("slow conn should still hold the prefill, got %q", got)
	}
}

// TestWelcomeIsFirstMessage verifies the welcome-before-register ordering used
// by HandleWebSocket: the ack is enqueued before the connection is registered,
// so no dispatched event can precede it.
func TestWelcomeIsFirstMessage(t *testing.T) {
	h, bc := newDispatchHandler(t)

	conn := &Connection{id: "w", send: make(chan []byte, 8), done: make(chan struct{})}
	// Mirror HandleWebSocket: enqueue welcome, THEN register.
	welcome := Message{Type: MsgAck, Data: mustJSON(AckData{Status: "ok", Message: "connected"})}
	wb, _ := json.Marshal(welcome)
	conn.send <- wb
	h.mu.Lock()
	h.conns[conn.id] = conn
	h.mu.Unlock()

	bc.Publish(sse.Event{Type: "activity", Data: map[string]any{"x": 1}})

	first, ok := recvMsg(t, conn, time.Second)
	if !ok {
		t.Fatal("expected the welcome message")
	}
	var env Message
	if err := json.Unmarshal(first, &env); err != nil {
		t.Fatalf("first message did not decode: %v", err)
	}
	if env.Type != MsgAck {
		t.Fatalf("first message type = %q, want ack (welcome must precede events)", env.Type)
	}
}
