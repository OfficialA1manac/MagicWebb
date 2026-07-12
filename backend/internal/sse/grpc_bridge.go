package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse/proto"
)

// peerConn bundles a gRPC connection and its send-only stream to one peer.
// Events are sent via the outbox channel (non-blocking at the publish site);
// a dedicated goroutine drains the channel and calls stream.Send().
type peerConn struct {
	conn   *grpc.ClientConn
	stream proto.EventBridge_StreamEventsClient
	outbox chan *proto.EventMessage // buffered, non-blocking send from Publish
	wg     sync.WaitGroup            // tracks drainOutbox goroutine for clean shutdown
}

// GrpcEventBridge manages the gRPC server (receiving events from peers) and
// the client mesh (sending events to all known peers). Replaces the Postgres
// LISTEN/NOTIFY bridge with direct peer-to-peer streaming.
type GrpcEventBridge struct {
	origin string // this instance's UUID for self-origin filtering

	// Server-side: listens on :port for incoming peer connections.
	srv     *grpc.Server
	port    int
	handler *bridgeHandler

	// Client-side: gRPC connections + outbox channels to all peers.
	mu           sync.Mutex
	peers        map[string]*peerConn // peer addr → connection + outbox
	shuttingDown atomic.Bool          // set before outbox closure to prevent Send() panics
}

// GRPCServer returns the underlying gRPC server, allowing external code to
// register additional services (e.g., the standard health check protocol)
// on the same port. Returns nil before NewGrpcEventBridge is called.
func (b *GrpcEventBridge) GRPCServer() *grpc.Server {
	if b == nil {
		return nil
	}
	return b.srv
}

// bridgeHandler implements proto.EventBridgeServer. It receives events from
// connected peers and feeds them into the local Broadcaster's events channel.
type bridgeHandler struct {
	proto.UnimplementedEventBridgeServer
	bridge   *GrpcEventBridge
	eventsCh chan<- Event // Broadcaster's events channel for local fan-out
}

// NewGrpcEventBridge creates and starts a gRPC event bridge.
//
// port: the port this instance listens on for incoming peer connections.
// peers: list of peer addresses (host:port) to connect to. Empty = standalone mode.
// eventsCh: the Broadcaster's events channel — events received from peers are
// published here for local fan-out.
// origin: this instance's UUID for filtering self-originated events.
func NewGrpcEventBridge(ctx context.Context, port int, peerAddrs []string, eventsCh chan<- Event, origin string) (*GrpcEventBridge, error) {
	b := &GrpcEventBridge{
		origin: origin,
		port:   port,
		peers:  make(map[string]*peerConn),
	}

	// Start gRPC server.
	b.handler = &bridgeHandler{bridge: b, eventsCh: eventsCh}
	b.srv = grpc.NewServer()
	proto.RegisterEventBridgeServer(b.srv, b.handler)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("gRPC listen on :%d: %w", port, err)
	}
	go func() {
		log.Info().Int("port", port).Msg("grpc: event bridge server started")
		if err := b.srv.Serve(lis); err != nil {
			log.Error().Err(err).Msg("grpc: server stopped")
		}
	}()

	// Connect to peers with staggered start to allow peers to come online.
	if len(peerAddrs) > 0 {
		for _, peer := range peerAddrs {
			go b.connectPeerLoop(ctx, peer)
		}
	}

	return b, nil
}

// Send forwards an event to all connected peers. Non-blocking — if a peer's
// outbox is full, the event is dropped for that peer (logged at warn level).
// Other peers and local subscribers still receive the event. This mirrors the
// old bridge channel's non-blocking select pattern.
func (b *GrpcEventBridge) Send(ev Event) {
	data, err := json.Marshal(ev.Data)
	if err != nil {
		return
	}
	msg := &proto.EventMessage{
		Origin: b.origin,
		Type:   ev.Type,
		Data:   data,
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	for addr, pc := range b.peers {
		select {
		case pc.outbox <- msg:
		default:
			log.Warn().Str("peer", addr).Msg("grpc: peer outbox full, dropping event")
		}
	}
}

// Shutdown gracefully stops the gRPC server and closes all peer connections.
// Acquires peers under lock, then releases before closing outboxes to avoid
// blocking connectPeerLoop goroutines that try to acquire the lock.
//
// Sets shuttingDown before closing outboxes so drainOutbox can bail early
// instead of calling stream.Send() on a stream whose connection may already
// be closed — avoiding a panic. The drainOutbox goroutines are still waited
// on via wg.Wait() to confirm they've exited before conns are closed.
func (b *GrpcEventBridge) Shutdown() {
	// Stop the server first — no new peer connections accepted.
	b.srv.GracefulStop()

	// Signal drainOutbox goroutines to stop sending (avoids Send() on
	// a soon-to-be-closed connection).
	b.shuttingDown.Store(true)

	// Collect peers under lock, then release before closing outboxes.
	b.mu.Lock()
	peers := make([]*peerConn, 0, len(b.peers))
	for _, pc := range b.peers {
		peers = append(peers, pc)
	}
	b.peers = make(map[string]*peerConn)
	b.mu.Unlock()

	// Close outboxes to signal drainOutbox goroutines to exit.
	// Since b.peers was already cleared, connectPeerLoop's delete is a no-op.
	// drainOutbox checks shuttingDown and returns immediately on any remaining
	// messages without calling stream.Send().
	for _, pc := range peers {
		close(pc.outbox)
	}
	// Wait for drainOutbox goroutines to finish.
	for _, pc := range peers {
		pc.wg.Wait()
	}

	// Close connections — drainOutbox goroutines have already returned.
	for _, pc := range peers {
		if err := pc.conn.Close(); err != nil {
			log.Warn().Err(err).Msg("grpc: close conn failed")
		}
	}

	log.Info().Msg("grpc: event bridge shut down")
}

// ── Peer connection management ───────────────────────────────────────────────

func (b *GrpcEventBridge) connectPeerLoop(ctx context.Context, peer string) {
	backoff := time.Second
	for ctx.Err() == nil {
		pc, err := b.dialPeer(ctx, peer)
		if err != nil {
			log.Warn().Str("peer", peer).Err(err).Msg("grpc: peer connect failed, retrying")
			b.sleep(ctx, &backoff)
			continue
		}
		log.Info().Str("peer", peer).Msg("grpc: peer stream established")
		backoff = time.Second // reset on successful connect

		b.mu.Lock()
		b.peers[peer] = pc
		b.mu.Unlock()

		// Drain the outbox: read events and send them over the stream.
		// This goroutine blocks until the outbox is closed (disconnect/shutdown).
		b.drainOutbox(pc, peer)

		// Outbox drained — peer disconnected or errored.
		b.mu.Lock()
		_, exists := b.peers[peer]
		delete(b.peers, peer)
		b.mu.Unlock()

		if exists {
			// Normal disconnect — we own the cleanup.
			if err := pc.conn.Close(); err != nil {
				log.Warn().Str("peer", peer).Err(err).Msg("grpc: close conn failed")
			}
		}
		// If !exists, Shutdown already handled it — skip double-close.
	}
}

func (b *GrpcEventBridge) dialPeer(ctx context.Context, peer string) (*peerConn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, peer,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, err
	}

	client := proto.NewEventBridgeClient(conn)
	stream, err := client.StreamEvents(ctx)
	if err != nil {
		conn.Close()
		return nil, err
	}

	return &peerConn{
		conn:   conn,
		stream: stream,
		outbox: make(chan *proto.EventMessage, 64),
	}, nil
}

// drainOutbox reads events from the outbox channel and sends them over the
// gRPC stream. Returns when the outbox is closed (peer disconnected or
// bridge shutting down). Send errors trigger disconnect.
//
// When shuttingDown is set, drainOutbox drops any remaining messages in the
// outbox without calling stream.Send() — this avoids a panic from Send() on
// a connection that Shutdown() is about to close.
func (b *GrpcEventBridge) drainOutbox(pc *peerConn, peer string) {
	pc.wg.Add(1)
	defer pc.wg.Done()
	for msg := range pc.outbox {
		if b.shuttingDown.Load() {
			return
		}
		if err := pc.stream.Send(msg); err != nil {
			log.Warn().Str("peer", peer).Err(err).Msg("grpc: send failed, disconnecting")
			return
		}
	}
}

func (b *GrpcEventBridge) sleep(ctx context.Context, backoff *time.Duration) {
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

// ── gRPC server handler ──────────────────────────────────────────────────────

// StreamEvents implements proto.EventBridgeServer.StreamEvents. It receives
// events from a connected peer and feeds them into the local Broadcaster.
// The stream is receive-only from the server's perspective — peers send
// events to us, and we feed them into local fan-out. Outbound events to
// peers are sent through the client mesh (connectPeerLoop + drainOutbox).
func (h *bridgeHandler) StreamEvents(stream proto.EventBridge_StreamEventsServer) error {
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil // clean disconnect
		}
		if err != nil {
			return err
		}
		// Skip self-originated events (already delivered locally).
		if msg.Origin == h.bridge.origin {
			continue
		}
		// Feed into the Broadcaster's events channel for local fan-out.
		select {
		case h.eventsCh <- Event{
			Type: msg.Type,
			Data: json.RawMessage(msg.Data),
		}:
		default:
			log.Warn().Str("type", msg.Type).Msg("grpc: local fan-out saturated, dropping bridged event")
		}
	}
}
