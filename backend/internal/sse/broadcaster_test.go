package sse

import (
	"encoding/json"
	"testing"
	"time"
)

// recvRaw reads a raw Event from a SubscribeRaw channel.
func recvRaw(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
	return Event{}
}

func TestPublishDelivers(t *testing.T) {
	b := New()
	ch, cancel, ok := b.SubscribeRaw()
	if !ok {
		t.Fatal("subscribe should succeed")
	}
	defer cancel()

	b.Publish(Event{Type: "test", Data: map[string]int{"x": 1}})
	got := recvRaw(t, ch)
	if got.Type != "test" {
		t.Fatalf("event type = %q, want %q", got.Type, "test")
	}
	payload, _ := json.Marshal(got.Data)
	if string(payload) != `{"x":1}` {
		t.Fatalf("data = %s, want {\"x\":1}", payload)
	}
}

func TestFanOutToAllSubscribers(t *testing.T) {
	b := New()
	chans := make([]<-chan Event, 3)
	for i := range chans {
		ch, cancel, ok := b.SubscribeRaw()
		if !ok {
			t.Fatal("subscribe should succeed")
		}
		defer cancel()
		chans[i] = ch
	}
	b.Publish(Event{Type: "x", Data: 1})
	for i, ch := range chans {
		ev := recvRaw(t, ch)
		if ev.Type != "x" {
			t.Fatalf("subscriber %d received type %q, want %q", i, ev.Type, "x")
		}
	}
}

func TestCancelUnsubscribes(t *testing.T) {
	b := New()
	_, cancel, _ := b.SubscribeRaw()

	rawClientsMu.RLock()
	before := len(rawClients)
	rawClientsMu.RUnlock()
	if before != 1 {
		t.Fatalf("rawClients = %d, want 1", before)
	}

	cancel()
	rawClientsMu.RLock()
	after := len(rawClients)
	rawClientsMu.RUnlock()
	if after != 0 {
		t.Fatalf("rawClients after cancel = %d, want 0", after)
	}
}

func TestSubscriberCap(t *testing.T) {
	b := New()
	for i := range MaxClients {
		if _, _, ok := b.SubscribeRaw(); !ok {
			t.Fatalf("subscribe %d should be within cap", i)
		}
	}
	if _, _, ok := b.SubscribeRaw(); ok {
		t.Fatal("subscribe beyond MaxClients should be rejected")
	}
}

func TestPublishSaturationMetricsIncrement(t *testing.T) {
	b := newNoLoop()
	preDrop := DroppedTotal.Load()
	preStreak := SaturationStreak.Load()
	for i := 0; i < 256; i++ {
		b.events <- Event{Type: "filler"}
	}
	b.Publish(Event{Type: "dropped"})
	if DroppedTotal.Load()-preDrop < 1 {
		t.Fatal("expected drop")
	}
	if SaturationStreak.Load()-preStreak < 1 {
		t.Fatal("expected streak increase")
	}
	select {
	case <-b.events:
	default:
	}
	b.Publish(Event{Type: "ok"})
	if SaturationStreak.Load() != 0 {
		t.Fatal("expected streak reset")
	}
}
