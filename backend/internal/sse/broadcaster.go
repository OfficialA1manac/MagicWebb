// Package sse provides a real-time event broadcaster. Single-instance fan-out
// is in-memory; NewGrpcBridged adds a gRPC streaming mesh so events fan out
// across instances without Redis or Postgres LISTEN/NOTIFY.
//
// SSE-2: Event delivery acknowledgment via global ring buffer + sequence numbers.
// Every event gets a monotonically increasing sequence number assigned at
// Publish time. The Broadcaster keeps a fixed-size ring buffer of the most
// recent events (default 1024). When a WebSocket client detects a gap in
// received sequence numbers, it sends a MsgRetry action; the server replays
// missing events from the ring buffer. If the requested events have been
// evicted (too old), the server returns a "stale_state" error and the client
// re-fetches current state via REST/Connect-RPC.
//
// Design properties:
//   - Bounded memory: ring buffer size is fixed regardless of client count.
//   - No per-client retry queues: clients track their own last_seq.
//   - Non-blocking fan-out unchanged: slow clients still drop; they recover
//     by requesting replay on the next event they receive.
//   - Eviction threshold: a "stale_state" response tells the client to full-refresh.
package sse

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
)

// RingBufferSize is the number of recent events retained for client replay.
// At ~200 bytes/event (type + JSON payload), 1024 events ≈ 200 KB memory.
// Larger values extend the replay window but increase memory proportionally.
const RingBufferSize = 1024

// RetryEvent is a lightweight event snapshot stored in the ring buffer for
// client replay. Data is stored as-is; re-marshalled on replay.
type RetryEvent struct {
	Seq  uint64
	Type string
	Data any
}

// Event is published by the indexer and fan-out to all connected clients.
type Event struct {
	Type string // "listing-updated", "auction-updated", "offer-updated", "activity"
	Data any    // will be JSON-marshalled
	// Seq is assigned by Broadcaster.Publish(). Clients track last received seq
	// and detect gaps: if new_seq > last_seq + 1, they request replay via
	// the WebSocket MsgRetry action.
	Seq uint64
}

// Broadcaster fans a single publish channel out to N subscriber channels.
// When grpcBridge != nil it also bridges events across instances via gRPC.
type Broadcaster struct {
	events     chan Event
	grpcBridge *GrpcEventBridge // nil → local-only (tests/single instance)
	origin     string           // this instance's id, for self-origin filtering

	// SSE-2: monotonic sequence counter for event delivery tracking.
	seq atomic.Uint64

	// SSE-2: ring buffer of recent events for client replay on gap detection.
	// ringBuf has fixed capacity; ringPos is the next write slot; ringCount
	// tracks total writes (caps at RingBufferSize for IsFull checks).
	ringBuf   [RingBufferSize]RetryEvent
	ringPos   int
	ringCount int
	ringMu    sync.RWMutex
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

// ── Saturation metrics (pre-existing) ─────────────────────────────────────
var (
	DroppedTotal     atomic.Uint64
	SaturationStreak atomic.Uint64
)

// ── SSE-2: Per-client drop counter ────────────────────────────────────────
var droppedClientsTotal atomic.Uint64

// DroppedClientsGauge returns the sum of per-client event drops across all
// connected rawClients. Used by the Prometheus /metrics endpoint.
func DroppedClientsGauge() uint64 {
	return droppedClientsTotal.Load()
}

func (b *Broadcaster) Publish(ev Event) {
	// SSE-2: assign monotonic sequence number. Starts at 1 so clients can
	// distinguish "no events yet" (last_seq=0) from the first event (seq=1).
	ev.Seq = b.seq.Add(1)

	// SSE-2: store in ring buffer for client replay. Writes under ringMu
	// so Replay() sees a consistent snapshot.
	b.ringMu.Lock()
	b.ringBuf[b.ringPos] = RetryEvent{Seq: ev.Seq, Type: ev.Type, Data: ev.Data}
	b.ringPos = (b.ringPos + 1) % RingBufferSize
	if b.ringCount < RingBufferSize {
		b.ringCount++
	}
	b.ringMu.Unlock()

	select {
	case b.events <- ev:
		SaturationStreak.Store(0)
		if b.grpcBridge != nil {
			b.grpcBridge.Send(ev)
		}
	default:
		DroppedTotal.Add(1)
		SaturationStreak.Add(1)
		log.Warn().Str("type", ev.Type).Uint64("seq", ev.Seq).Uint64("streak", SaturationStreak.Load()).
			Msg("sse: local fan-out saturated; dropping event (bridge suppressed for consistency)")
	}
}

// GRPCServer returns the underlying gRPC server from the event bridge.
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

// ── SSE-2: Event replay from ring buffer ──────────────────────────────────

// Replay returns events from the ring buffer with sequence numbers >= fromSeq
// (inclusive). Used by the WebSocket handler to replay missed events when a
// client requests retry after detecting a gap.
//
// Returns nil when fromSeq references an event older than the oldest retained
// event (already evicted) — the caller should request a full state refresh.
func (b *Broadcaster) Replay(fromSeq uint64) []RetryEvent {
	b.ringMu.RLock()
	defer b.ringMu.RUnlock()

	if b.ringCount == 0 {
		return []RetryEvent{} // no events published yet — client is caught up
	}

	oldestSeq := b.ringBuf[oldestPos(b.ringPos, b.ringCount)].Seq
	if fromSeq < oldestSeq {
		return nil // evicted — caller must full-refresh
	}

	var out []RetryEvent
	for i := 0; i < b.ringCount; i++ {
		pos := (oldestPos(b.ringPos, b.ringCount) + i) % RingBufferSize
		if b.ringBuf[pos].Seq >= fromSeq {
			out = append(out, b.ringBuf[pos])
		}
	}
	return out
}

// oldestPos returns the logical index of the oldest entry in the ring buffer.
func oldestPos(writePos, count int) int {
	if count < RingBufferSize {
		return 0 // buffer hasn't wrapped yet
	}
	return writePos // writePos is next slot after newest; modulo wraps to oldest
}

// LastSeq returns the most recently assigned sequence number. Returns 0 when
// no events have been published yet.
func (b *Broadcaster) LastSeq() uint64 {
	return b.seq.Load()
}

// ── Subscriber management ─────────────────────────────────────────────────

// MaxClients caps concurrent subscribers, bounding memory against connection-bombing.
const MaxClients = 10_000

type rawClient struct {
	ch     chan Event
	cancel func()
	// SSE-2: per-client event drop counter for Prometheus metrics.
	dropped atomic.Uint64
}

var rawClientsMu sync.RWMutex
var rawClients = make(map[string]rawClient)

// SubscribeRaw registers a subscriber that receives raw Event objects (no SSE
// formatting). This is the WebSocket-native subscriber path.
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
		rawClientsMu.RLock()
		for _, rc := range rawClients {
			select {
			case rc.ch <- ev:
			default:
				// SSE-2: per-client drop tracking for Prometheus observability.
				// The client detects the gap by comparing seq and requests
				// replay via MsgRetry. No server-side buffering — O(1) memory.
				rc.dropped.Add(1)
				droppedClientsTotal.Add(1)
			}
		}
		rawClientsMu.RUnlock()
	}
}
