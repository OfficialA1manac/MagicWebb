package sse

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse/proto"
)

// peerConn bundles a gRPC connection and its send-only stream to one peer.
// Events are sent via the outbox channel (non-blocking at the publish site);
// a dedicated goroutine drains the channel and calls stream.Send().
type peerConn struct {
	conn   *grpc.ClientConn
	stream proto.EventBridge_StreamEventsClient
	outbox chan *proto.EventMessage // buffered, non-blocking send from Publish
	wg     sync.WaitGroup           // tracks drainOutbox goroutine for clean shutdown
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
	cancel       context.CancelFunc   // stops connectPeerLoop goroutines on Shutdown
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
// eventsCh: the Broadcaster's events channel.
// origin: this instance's UUID for filtering self-originated events.
//
// SSE-3: When GRPC_TLS_CERT and GRPC_TLS_KEY env vars are both set, the bridge
// uses mutual TLS (mTLS) for peer connections. GRPC_TLS_CA_CERT optionally sets
// a custom CA for client certificate verification. On Fly.io, private networking
// already encrypts inter-instance traffic; mTLS provides defense-in-depth.
func NewGrpcEventBridge(ctx context.Context, port int, peerAddrs []string, eventsCh chan<- Event, origin string) (*GrpcEventBridge, error) {
	b := &GrpcEventBridge{
		origin: origin,
		port:   port,
		peers:  make(map[string]*peerConn),
	}

	// SSE-3: Optional mTLS for inter-instance bridge connections.
	serverOpts := []grpc.ServerOption{}
	creds, err := loadTLSCredentials()
	if err != nil {
		return nil, err
	}
	if creds != nil {
		serverOpts = append(serverOpts, grpc.Creds(creds))
		log.Info().Msg("grpc: bridge server using mTLS")
	}

	// Start gRPC server.
	b.handler = &bridgeHandler{bridge: b, eventsCh: eventsCh}
	b.srv = grpc.NewServer(serverOpts...)
	proto.RegisterEventBridgeServer(b.srv, b.handler)

	// SSE-1: Register the standard gRPC health check protocol on the event
	// bridge port. External monitoring (grpc_health_probe, grpcurl, health
	// check sidecars) can now verify bridge connectivity without needing
	// a custom probe. The Keeper election also uses this same gRPC port
	// (via bcast.GRPCServer()), so health covers BOTH the event bridge
	// AND keeper election in one check.
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(b.srv, healthServer)

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
	// The peer-loop context is cancellable so Shutdown can stop reconnect
	// loops that would otherwise re-dial and re-register peers forever.
	peerCtx, cancel := context.WithCancel(ctx)
	b.cancel = cancel
	if len(peerAddrs) > 0 {
		for _, peer := range peerAddrs {
			go b.connectPeerLoop(peerCtx, peer)
		}
	}

	return b, nil
}

// Send forwards an event to all connected peers. Non-blocking — if a peer's
// outbox is full, the event is dropped for that peer (logged at warn level).
// Other peers and local subscribers still receive the event. This mirrors the
// old bridge channel's non-blocking select pattern.
//
// SSE-4: Populates the protobuf oneof event field for known event types,
// eliminating JSON round-trips on the receiving side. The bytes data field
// is always populated for backward compat with older instances.
func (b *GrpcEventBridge) Send(ev Event) {
	data, err := json.Marshal(ev.Data)
	if err != nil {
		return
	}
	msg := &proto.EventMessage{
		Origin: b.origin,
		Type:   ev.Type,
		Data:   data,
		Seq:    ev.Seq, // SSE-2: pass sequence number through bridge
	}

	// SSE-4: populate typed oneof when the event type is known.
	// PopulateProtoOneof sets msg.Event directly (proto oneof interface is unexported).
	// msg.Data remains populated for backward compat (old instances).
	if HasTypedPayload(ev.Type) {
		PopulateProtoOneof(msg, ev.Type, ev.Data)
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
	// Signal drainOutbox goroutines to stop sending (avoids Send() on
	// a soon-to-be-closed connection) and stop connectPeerLoop goroutines
	// from re-dialing and re-registering peers after the map is drained.
	b.shuttingDown.Store(true)
	if b.cancel != nil {
		b.cancel()
	}

	// Stop the server — no new peer connections accepted. GracefulStop
	// waits for in-flight RPCs, but StreamEvents blocks in Recv() for as
	// long as a peer stays connected, so bound the graceful phase and
	// fall back to Stop to force-close remaining peer streams.
	stopped := make(chan struct{})
	go func() {
		b.srv.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		b.srv.Stop()
		<-stopped
	}

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
		if b.shuttingDown.Load() {
			// Shutdown already drained the peers map — do not re-register
			// (nobody would close this outbox or connection).
			b.mu.Unlock()
			pc.conn.Close()
			return
		}
		b.peers[peer] = pc
		// wg.Add must happen under the same lock Shutdown uses to snapshot
		// b.peers, so wg.Wait() is guaranteed to see the drainOutbox
		// goroutine for every registered peer.
		pc.wg.Add(1)
		b.mu.Unlock()

		// Drain the outbox: read events and send them over the stream.
		// This call blocks until the outbox is closed (disconnect/shutdown).
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

	// SSE-3: Use TLS when certificates are configured; fall back to insecure.
	dialOpts := []grpc.DialOption{}
	creds, err := loadTLSCredentials()
	if err != nil {
		return nil, err
	}
	if creds != nil {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(creds))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// DEPRECATION NOTE (SA1019): grpc.NewClient replaces DialContext but does
	// not dial eagerly, so a peer that is down would no longer fail here — the
	// error would surface at StreamEvents below instead, changing where the
	// caller's retry/backoff sees failures. Migrating the mesh dial is tracked
	// with the keeper-election dial (same deprecation, same reasoning) rather
	// than done piecemeal.
	//
	//lint:ignore SA1019 eager dial keeps failures at the dial site; see note above
	conn, err := grpc.DialContext(dialCtx, peer, dialOpts...)
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
	// Note: pc.wg.Add(1) is done by the caller (connectPeerLoop) under
	// b.mu, BEFORE this goroutine's work is observable via b.peers —
	// otherwise Shutdown's wg.Wait() could pass before Add runs.
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

// loadTLSCredentials reads TLS certificate and key from GRPC_TLS_CERT and
// GRPC_TLS_KEY env vars. When GRPC_TLS_CA_CERT is set, it configures mTLS
// with client certificate verification (SSE-3). Returns (nil, nil) when no cert
// is configured — callers fall back to insecure credentials.
//
// A misconfigured CA is a hard error: the operator asked for mTLS, so silently
// returning credentials with no ClientAuth/RootCAs would accept any client
// certificate and lose peer authentication without anyone noticing.
func loadTLSCredentials() (credentials.TransportCredentials, error) {
	certFile := os.Getenv("GRPC_TLS_CERT")
	keyFile := os.Getenv("GRPC_TLS_KEY")
	if certFile == "" || keyFile == "" {
		return nil, nil
	}

	// Same fail-CLOSED rule as the CA branch below: the operator set
	// GRPC_TLS_CERT/KEY, so they asked for TLS. Warning and then serving the
	// event bridge in plaintext silently downgrades the transport for the whole
	// mesh; a bad path or unreadable key is a config error worth failing on.
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("grpc: GRPC_TLS_CERT/GRPC_TLS_KEY load failed: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	// mTLS: verify client certificates when CA is configured.
	if caFile := os.Getenv("GRPC_TLS_CA_CERT"); caFile != "" {
		caPEM, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("grpc: GRPC_TLS_CA_CERT %q read failed: %w", caFile, err)
		}
		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("grpc: GRPC_TLS_CA_CERT %q contains no usable certificates", caFile)
		}
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
		tlsCfg.ClientCAs = caPool
		// Client side of the mesh: trust peer server certs signed by
		// the same CA. Without RootCAs, dialPeer verifies against the
		// system trust store and every private-CA peer connect fails.
		tlsCfg.RootCAs = caPool
		log.Info().Msg("grpc: mTLS enabled with client certificate verification")
	}

	return credentials.NewTLS(tlsCfg), nil
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

// attachBridgedData restores the Data field (full DB row) on typed events
// received over the bridge. PopulateProtoOneof never puts Data into the proto
// oneof, but the JSON payload in msg.Data still carries it under the "data"
// key — without this, a subscriber on a peer instance would see Data as nil
// while a local subscriber sees the full row.
func attachBridgedData(typed TypedEvent, raw []byte) {
	var wrapper struct {
		Data json.RawMessage `json:"data"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &wrapper) != nil ||
		len(wrapper.Data) == 0 || string(wrapper.Data) == "null" {
		return
	}
	switch ev := typed.(type) {
	case *ListingUpdatedEvent:
		ev.Data = wrapper.Data
	case *AuctionUpdatedEvent:
		ev.Data = wrapper.Data
	}
}

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
		// SSE-2: preserve the origin instance's sequence number so clients
		// see consistent ordering across instances.
		// SSE-4: prefer typed proto oneof payload when available;
		// fall back to JSON raw message for unknown event types or
		// backward compat with older bridge instances.
		var evData any
		if typed := FromProtoOneof(msg); typed != nil {
			// The proto oneof schema carries no Data field (full DB row).
			// Restore it from the JSON payload so bridged consumers see
			// the same event shape as local subscribers.
			attachBridgedData(typed, msg.Data)
			evData = typed
		} else {
			evData = json.RawMessage(msg.Data)
		}
		select {
		case h.eventsCh <- Event{
			Type: msg.Type,
			Data: evData,
			Seq:  msg.Seq,
		}:
		default:
			log.Warn().Str("type", msg.Type).Msg("grpc: local fan-out saturated, dropping bridged event")
		}
	}
}
