package sse

import (
	"testing"
	"time"
)

func recv(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SSE message")
		return ""
	}
}

func TestPublishDelivers(t *testing.T) {
	b := New()
	ch, cancel, ok := b.Subscribe()
	if !ok {
		t.Fatal("subscribe should succeed")
	}
	defer cancel()

	b.Publish(Event{Type: "test", Data: map[string]int{"x": 1}})
	got := recv(t, ch)
	want := "event: test\ndata: {\"x\":1}\n\n"
	if got != want {
		t.Fatalf("msg = %q, want %q", got, want)
	}
}

func TestFanOutToAllSubscribers(t *testing.T) {
	b := New()
	chans := make([]<-chan string, 3)
	for i := range chans {
		ch, cancel, ok := b.Subscribe()
		if !ok {
			t.Fatal("subscribe should succeed")
		}
		defer cancel()
		chans[i] = ch
	}
	b.Publish(Event{Type: "x", Data: 1})
	for i, ch := range chans {
		if recv(t, ch) == "" {
			t.Fatalf("subscriber %d received nothing", i)
		}
	}
}

func TestCancelUnsubscribes(t *testing.T) {
	b := New()
	_, cancel, _ := b.Subscribe()

	b.mu.RLock()
	before := len(b.clients)
	b.mu.RUnlock()
	if before != 1 {
		t.Fatalf("clients = %d, want 1", before)
	}

	cancel()
	b.mu.RLock()
	after := len(b.clients)
	b.mu.RUnlock()
	if after != 0 {
		t.Fatalf("clients after cancel = %d, want 0", after)
	}
}

func TestSubscriberCap(t *testing.T) {
	b := New()
	for i := range MaxClients {
		if _, _, ok := b.Subscribe(); !ok {
			t.Fatalf("subscribe %d should be within cap", i)
		}
	}
	if _, _, ok := b.Subscribe(); ok {
		t.Fatal("subscribe beyond MaxClients should be rejected")
	}
}
