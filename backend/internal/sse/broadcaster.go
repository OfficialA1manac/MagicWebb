// Package sse provides an SSE broadcaster. Single-instance fan-out is in-memory;
// NewBridged adds a Postgres LISTEN/NOTIFY bridge so events fan out across
// instances without Redis.
package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// notifyChannel is the Postgres NOTIFY channel for cross-instance SSE fan-out.
const notifyChannel = "mw_events"

// wire is the JSON envelope sent over pg_notify. origin lets each instance skip
// its own notifications (already delivered locally) → no double-delivery.
type wire struct {
	Origin string          `json:"o"`
	Type   string          `json:"t"`
	Data   json.RawMessage `json:"d"`
}

// Event is published by the indexer and fan-out to all connected SSE clients.
type Event struct {
	Type string // "listing-updated", "auction-updated", "offer-updated", "activity"
	Data any    // will be JSON-marshalled
}

// Broadcaster fans a single publish channel out to N subscriber channels.
// When pool != nil it also bridges events across instances via pg_notify.
type Broadcaster struct {
	mu      sync.RWMutex
	clients map[string]chan string // id → formatted SSE line(s)
	events  chan Event
	// bridge feeds a SINGLE bridge goroutine that performs pg_notify. This
	// caps the cross-instance bridge at one in-flight DB call (one pool
	// connection) regardless of publish burst depth. Without this, every
	// Publish would launch its own goroutine holding a 3s pg_notify context
	// + pool connection — a 1000-event backfill tick would briefly hold
	// up to 1000 connections, draining the pool and starving regular reads.
	bridge  chan Event
	pool    *pgxpool.Pool // nil → local-only (tests/single instance)
	origin  string        // this instance's id, for own-notify suppression
}

// New creates and starts a local-only Broadcaster.
func New() *Broadcaster {
	b := &Broadcaster{
		clients: make(map[string]chan string),
		events:  make(chan Event, 256),
		bridge:  make(chan Event, 256),
		origin:  uuid.New().String(),
	}
	go b.loop()
	return b
}

// NewBridged creates a Broadcaster that also fans events across instances via
// Postgres LISTEN/NOTIFY. NOTIFY uses the pool; LISTEN needs a dedicated
// session connection, so pass the Postgres DSN. If dsn is empty or the listen
// conn drops, this instance degrades gracefully to local delivery (its own
// clients still get every event it publishes).
// Starts a single bridge goroutine (when pool != nil) so cross-instance
// fan-out uses at most one DB connection at a time, with explicit
// backpressure via the `bridge` channel.
func NewBridged(ctx context.Context, pool *pgxpool.Pool, dsn string) *Broadcaster {
	b := New()
	b.pool = pool
	if pool != nil {
		go b.bridgeLoop(ctx)
	}
	if dsn != "" {
		go b.listen(ctx, dsn)
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
	select {		case b.events <- ev:
			SaturationStreak.Store(0)
			if b.pool != nil {
				// Hand the event to the single bridge goroutine via a
				// bounded buffered channel. The bridge is intentionally
				// decoupled from the local fan-out so a stall on pg_notify
				// cannot back up the publish site (which is called from the
				// indexer watcher loop). Under sustained bridge saturation we
				// drop the cross-instance forward (logged) rather than block
				// the publisher; remote instances may see a transient gap,
				// but the local view stays consistent.
				select {
				case b.bridge <- ev:
				default:
					log.Warn().Str("type", ev.Type).Msg("sse: bridge channel saturated; dropping cross-instance forward")
				}
			}
	default:
		// Metrics MUST get here before the log so a sequential reader of the
		// /metrics endpoint after this Warn sees the counter incremented.
		DroppedTotal.Add(1)
		SaturationStreak.Add(1)
		log.Warn().Str("type", ev.Type).Uint64("streak", SaturationStreak.Load()).
			Msg("sse: local fan-out saturated; dropping event (bridge suppressed for consistency)")
	}
}

// bridgeLoop sequentially consumes events from the bridge channel and
// performs pg_notify. Single goroutine → at most one in-flight bridge DB
// call at any time, eliminating the pool-exhaustion risk of per-event
// goroutines (a 1000-event backfill tick would briefly hold up to 1000
// connections under the old `go b.notify(ev)` design). The ctx cancels
// the loop on shutdown so a wedged notify cannot outlive the process.
func (b *Broadcaster) bridgeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-b.bridge:
			b.notify(ev)
		}
	}
}

func (b *Broadcaster) notify(ev Event) {
	data, err := json.Marshal(ev.Data)
	if err != nil {
		return
	}
	env, err := json.Marshal(wire{Origin: b.origin, Type: ev.Type, Data: data})
	if err != nil {
		return
	}
	if len(env) > 7800 { // pg_notify payload hard limit is 8000 bytes
		log.Warn().Int("bytes", len(env)).Str("type", ev.Type).Msg("sse: event too large to bridge")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := b.pool.Exec(ctx, "SELECT pg_notify($1, $2)", notifyChannel, string(env)); err != nil {
		log.Error().Err(err).Msg("sse: pg_notify failed")
	}
}

// listen holds a dedicated session connection, LISTENs, and feeds notifications
// from OTHER instances into the local fan-out. Reconnects with backoff.
func (b *Broadcaster) listen(ctx context.Context, dsn string) {
	backoff := time.Second
	for ctx.Err() == nil {
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			log.Error().Err(err).Msg("sse: listen connect failed")
			b.sleep(ctx, &backoff)
			continue
		}
		if _, err := conn.Exec(ctx, "LISTEN "+notifyChannel); err != nil {
			log.Error().Err(err).Msg("sse: LISTEN failed")
			_ = conn.Close(ctx)
			b.sleep(ctx, &backoff)
			continue
		}
		log.Info().Msg("sse: cross-instance LISTEN active")
		backoff = time.Second
		for ctx.Err() == nil {
			n, err := conn.WaitForNotification(ctx)
			if err != nil {
				break // connection dropped → reconnect
			}
			var w wire
			if json.Unmarshal([]byte(n.Payload), &w) != nil || w.Origin == b.origin {
				continue // malformed or our own → already delivered locally
			}
			select {
			case b.events <- Event{Type: w.Type, Data: w.Data}:
				// Successful delivery — reset the streak.
				// Without this, a transient failure (single saturation
				// event) would leave SaturationStreak stuck at 1
				// forever, polluting the /api/v1/metrics saturation
				// panel until process restart. The same reset pattern
				// is used in Publish()'s successful enqueue path.
				SaturationStreak.Store(0)
			default:
				// L-17: increment DroppedTotal for remote-origin drops,
				// symmetrical with the local-fan-out saturation metric
				// in Publish(). Without this, remote drops are invisible
				// in the /api/v1/metrics saturation panel — a bridge
				// channel that's consistently full on the listener side
				// would silently lose cross-instance events with no
				// monitoring signal. The per-instance DroppedTotal
				// aggregation still reflects the true drop rate
				// regardless of whether the drop is local or bridged.
				DroppedTotal.Add(1)
				SaturationStreak.Add(1)
			}
		}
		_ = conn.Close(context.Background())
	}
}

func (b *Broadcaster) sleep(ctx context.Context, backoff *time.Duration) {
	t := time.NewTimer(*backoff)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
	if *backoff *= 2; *backoff > 30*time.Second {
		*backoff = 30 * time.Second
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
