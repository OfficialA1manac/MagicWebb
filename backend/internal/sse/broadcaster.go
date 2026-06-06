// Package sse provides an in-memory SSE broadcaster replacing Redis pub/sub.
package sse

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// Event is published by the indexer and fan-out to all connected SSE clients.
type Event struct {
	Type string // "listing-updated", "auction-updated", "offer-updated", "activity"
	Data any    // will be JSON-marshalled
}

// Broadcaster fans a single publish channel out to N subscriber channels.
type Broadcaster struct {
	mu      sync.RWMutex
	clients map[string]chan string // id → formatted SSE line(s)
	events  chan Event
}

// New creates and starts a Broadcaster. Call Stop to release resources.
func New() *Broadcaster {
	b := &Broadcaster{
		clients: make(map[string]chan string),
		events:  make(chan Event, 256),
	}
	go b.loop()
	return b
}

// Publish sends an event to all subscribers. Non-blocking: slow clients are skipped.
func (b *Broadcaster) Publish(ev Event) {
	select {
	case b.events <- ev:
	default:
	}
}

// MaxClients caps concurrent SSE subscribers, bounding memory against connection-bombing.
const MaxClients = 10_000

// Subscribe registers a subscriber and returns its message channel and a cancel func.
// ok is false when the subscriber cap is reached — the caller should reject the request.
func (b *Broadcaster) Subscribe() (ch <-chan string, cancel func(), ok bool) {
	id := uuid.New().String()
	c := make(chan string, 64)

	b.mu.Lock()
	if len(b.clients) >= MaxClients {
		b.mu.Unlock()
		return nil, nil, false
	}
	b.clients[id] = c
	b.mu.Unlock()

	cancel = func() {
		b.mu.Lock()
		delete(b.clients, id)
		b.mu.Unlock()
		// drain to unblock any sender
		for len(c) > 0 {
			<-c
		}
	}
	return c, cancel, true
}

func (b *Broadcaster) loop() {
	for ev := range b.events {
		payload, err := json.Marshal(ev.Data)
		if err != nil {
			continue
		}
		msg := fmt.Sprintf("event: %s\ndata: %s\n\n", ev.Type, payload)

		b.mu.RLock()
		for _, ch := range b.clients {
			select {
			case ch <- msg:
			default:
				// slow client — skip, don't block publisher
			}
		}
		b.mu.RUnlock()
	}
}
