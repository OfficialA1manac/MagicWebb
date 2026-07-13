// Package sse provides a real-time event broadcaster. Single-instance fan-out
// is in-memory; NewGrpcBridged adds a gRPC streaming mesh so events fan out
// across instances without Redis or Postgres LISTEN/NOTIFY.
package sse

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
)

// Event is published by the indexer and fan-out to all connected clients.
type Event struct {
	Type string // "listing-updated", "auction-updated", "offer-updated", "activity"
	Data any    // will be JSON-marshalled
}

// Broadcaster fans a single publish channel out to N subscriber channels.
// When grpcBridge != nil it also bridges events across instances via gRPC.
type Broadcaster struct {
	events     chan Event
	grpcBridge *GrpcEventBridge // nil → local-only (tests/single instance)
	origin     string           // this instance's id, for self-origin filtering
}

// New creates and starts a local-only Broadcaster.
func New() *Broadcaster {
	b := newNoLoop()
	go b.loop()
	return b
}

// newNoLoop returns a Broadcaster without starting the loop goroutine.
// Unexported; tests in this package use it to fill the events channel
// deterministically before starting the loop (e.g., saturation-metric tests).
func newNoLoop() *Broadcaster {
	return &Broadcaster{
		events: make(chan Event, 256),
		origin: uuid.New().String(),
	}
}

// NewGrpcBridged creates a Broadcaster that fans events across instances via
// a gRPC streaming mesh. Replaces the old Postgres LISTEN/NOTIFY bridge.
//
// port: the port this instance listens on for incoming peer connections.
// peers: list of peer addresses (host:port). Empty = standalone mode.
//
// Cross-instance events are delivered directly peer-to-peer — no Postgres
// intermediary, no 8KB payload limit, no DB connection drain.
func NewGrpcBridged(ctx context.Context, port int, peers []string) *Broadcaster {
	if port <= 0 && len(peers) == 0 {
		return New()
	}
	b := New()
	var err error
	b.grpcBridge, err = NewGrpcEventBridge(ctx, port, peers, b.events, b.origin)
	if err != nil {
		log.Warn().Err(err).Msg("sse: gRPC bridge init failed, running local-only")
		b.grpcBridge = nil
	}
	return b
}

// Publish delivers locally (non-blocking; slow clients skipped) and, when
// bridged, notifies other instances.
//
// Local enqueue and the cross-instance bridge are intentionally COUPLED:
// if the local fan-out queue is saturated we suppress the bridge too. That
// prevents a drift bug where this instance's subscribers miss an event while
// remote instances' subscribers receive it (the Origin UUID in the LISTEN
// path only suppresses SAME-origin messages; bridge-dispatched events from
// this origin already passed the local check, so dropping here keeps every
// instance's view consistent). The trade-off is that under saturation we
// drop the event everywhere rather than partially — we never tell one
// instance "yes" and another "no" for the same publish call.
// DroppedTotal accumulates every event we couldn't fan out due to a saturated
// local queue. Saturation is DELIBERATELY fatal: dropping locally + still
// bridging would create per-instance drift, but a single log.Warn was too
// quiet to surface the failure mode in metrics. The /api/v1/metrics handler
// reads these atomics so the metrics page can show a "saturation alert"
// panel when the count is non-zero over any window.
var (
	DroppedTotal     atomic.Uint64
	SaturationStreak atomic.Uint64
)

func (b *Broadcaster) Publish(ev Event) {
	select {
	case b.events <- ev:
		SaturationStreak.Store(0)
		// Cross-instance fan-out via gRPC streaming mesh.
		if b.grpcBridge != nil {
			b.grpcBridge.Send(ev)
		}
	default:
		DroppedTotal.Add(1)
		SaturationStreak.Add(1)
		log.Warn().Str("type", ev.Type).Uint64("streak", SaturationStreak.Load()).
			Msg("sse: local fan-out saturated; dropping event (bridge suppressed for consistency)")
	}
}

// GRPCServer returns the underlying gRPC server from the event bridge,
// or nil when no gRPC bridge is configured. Allows external code to register
// additional services (e.g., the standard health check protocol) on the same
// gRPC port.
func (b *Broadcaster) GRPCServer() *grpc.Server {
	if b == nil || b.grpcBridge == nil {
		return nil
	}
	return b.grpcBridge.GRPCServer()
}

// Shutdown gracefully stops the gRPC bridge if one is active.
func (b *Broadcaster) Shutdown() {
	if b.grpcBridge != nil {
		b.grpcBridge.Shutdown()
	}
}

// MaxClients caps concurrent subscribers, bounding memory against connection-bombing.
const MaxClients = 10_000

// rawClients tracks subscribers that receive raw Event objects (WebSocket),
// not SSE-formatted strings.
type rawClient struct {
	ch     chan Event
	cancel func()
}

// rawClientsMu protects rawClients.
var rawClientsMu sync.RWMutex
var rawClients = make(map[string]rawClient)

// SubscribeRaw registers a subscriber that receives raw Event objects (no SSE
// formatting). This is the WebSocket-native subscriber path — events are
// delivered as typed Go values so the WS handler can marshal them directly
// into its JSON envelope without an intermediary SSE→JSON conversion.
// ok is false when MaxClients is reached.
func (b *Broadcaster) SubscribeRaw() (<-chan Event, func(), bool) {
	id := uuid.New().String()
	c := make(chan Event, 64)

	rawClientsMu.Lock()
	if len(rawClients) >= MaxClients {
		rawClientsMu.Unlock()
		return nil, nil, false
	}
	cancel := func() {
		rawClientsMu.Lock()
		delete(rawClients, id)
		rawClientsMu.Unlock()
		for len(c) > 0 {
			<-c
		}
	}
	rawClients[id] = rawClient{ch: c, cancel: cancel}
	rawClientsMu.Unlock()
	return c, cancel, true
}

func (b *Broadcaster) loop() {
	for ev := range b.events {
		// Raw Event delivery for all subscribers (WebSocket and any future
		// consumers). The legacy /events SSE endpoint was removed in favor of
		// WebSocket push.
		rawClientsMu.RLock()
		for _, rc := range rawClients {
			select {
			case rc.ch <- ev:
			default:
			}
		}
		rawClientsMu.RUnlock()
	}
}
