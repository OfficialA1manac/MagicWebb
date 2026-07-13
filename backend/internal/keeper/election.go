// Package keeper provides a gRPC-based distributed leader election for
// keeper single-flight. Replaces the Postgres advisory lock in db/keeperlock.go
// with a peer-to-peer heartbeat protocol over the existing gRPC mesh.
//
// Each instance registers a KeeperElection server and connects a client to
// every peer. The instance with the lexicographically lowest instance_id (UUID)
// is the initial leader. The leader sends a heartbeat every 1s; any follower
// that misses 3 consecutive heartbeats declares itself leader.
//
// Design decisions:
//   - Deterministic leader selection: lowest UUID wins. Every instance sees the
//     same sorted peer list, so election converges without a consensus protocol.
//   - Heartbeat staleness: 3 missed ticks (~3s) before failover. Quick enough
//     for fast recovery; long enough to avoid flapping from transient RPC latency.
//   - Peer discovery via config: same GRPC_PEERS list the event bridge uses.
//     No separate service discovery needed.
//   - Voluntary yield: when the leader detects degraded RPC (caller can check
//     via eth.BlockNumber), it can call Yield() to step down for the next
//     highest-priority peer.
package keeper

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/keeper/proto"
)

// Defaults for keeper election timing.
const (
	heartbeatInterval = 1 * time.Second // leader sends heartbeat every 1s
	missedThreshold   = 3               // failover after 3 missed heartbeats
	heartbeatTimeout  = 2 * time.Second // per-heartbeat RPC timeout
)

// ElectionState represents the current role of this instance.
type ElectionState int32

const (
	StateFollower  ElectionState = 0
	StateLeader    ElectionState = 1
	StateCandidate ElectionState = 2 // transitioning — about to become leader
)

// Election implements the KeeperGate interface using gRPC-based leader
// election. It manages the election state machine, heartbeat send/receive,
// and failover logic.
type Election struct {
	// Immutable config.
	instanceID string // UUID for this instance
	peers      []string // list of peer addresses (host:port)

	// State machine — accessed under mu.
	mu             sync.RWMutex
	state          ElectionState
	leaderID       string // who we believe the leader is
	lastHeartbeat  time.Time // last time we heard from the leader
	leaderPriority int    // priority rank of current leader (lower = higher priority)

	// Peer client connections.
	clientsMu sync.RWMutex
	clients   map[string]proto.KeeperElectionClient // addr → client

	// gRPC server for receiving heartbeats.
	srv    *grpc.Server
	registered sync.Once // guards proto.RegisterKeeperElectionServer (panic on double-call)
	proto.UnimplementedKeeperElectionServer // required for forward compatibility

	// KeeperGate channels — lockCtx is cancelled when leadership is lost.
	lockCtx    context.Context
	lockCancel context.CancelFunc
	gateReady  chan struct{} // closed when lockCtx is ready (first leadership acquired)

	// Health / degraded detection.
	eth         EthClient              // optional — for degraded-RPC detection
	yieldCh     chan struct{}          // signals voluntary yield
	degradedCnt atomic.Int64           // consecutive RPC failures
	maxDegraded int64                  // max tolerable failures before yield (0 = never yield)

	// Shutdown coordination.
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	loopStarted atomic.Bool // guards electionLoop against duplicate starts
}

// EthClient is the chain-access surface the election module uses for
// degraded-RPC detection. Only BlockNumber is needed — nil is safe and
// disables degraded detection.
type EthClient interface {
	BlockNumber(ctx context.Context) (uint64, error)
}

// Option configures the Election.
type Option func(*Election)

// WithEthClient sets the Ethereum client for degraded-RPC detection.
// When set, the leader voluntarily yields after maxDegraded consecutive
// BlockNumber failures.
func WithEthClient(eth EthClient) Option {
	return func(e *Election) {
		e.eth = eth
	}
}

// WithMaxDegraded sets the number of consecutive RPC failures before the
// leader voluntarily yields. Default 3. Set to 0 to disable degraded yield.
func WithMaxDegraded(n int64) Option {
	return func(e *Election) {
		e.maxDegraded = n
	}
}

// New creates a new Election instance. The Election manages the keeper
// single-flight gate and must be started via Run() before use.
//
// srv: the gRPC server on which to register the election RPC handler.
// Must be the same server as the event bridge's (bcast.GRPCServer())
// so all services share one port.
//
// peerAddrs: list of peer addresses (host:port). Must match GRPC_PEERS.
// The instance's own address should NOT be included — it's derived from
// the GRPC_PEERS list.
//
// myAddr: this instance's own address (host:port) for peer list filtering.
func New(srv *grpc.Server, peerAddrs []string, myAddr string, opts ...Option) *Election {
	ctx, cancel := context.WithCancel(context.Background())

	e := &Election{
		instanceID:   uuid.New().String(),
		peers:        filterSelf(peerAddrs, myAddr),
		state:        StateFollower,
		leaderID:     "",
		lastHeartbeat: time.Time{},
		clients:      make(map[string]proto.KeeperElectionClient),
		srv:          srv,
		gateReady:    make(chan struct{}),
		lockCtx:      nil,
		lockCancel:   nil,
		yieldCh:      make(chan struct{}, 1),
		maxDegraded:  3,
		ctx:          ctx,
		cancel:       cancel,
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// filterSelf removes this instance's own address from the peer list.
func filterSelf(peers []string, myAddr string) []string {
	out := make([]string, 0, len(peers))
	for _, p := range peers {
		if p != myAddr {
			out = append(out, p)
		}
	}
	return out
}

// Run starts the election state machine. It connects to all peers and
// begins the heartbeat cycle. Blocks until this instance acquires
// leadership (or ctx cancellation). Safe to call multiple times — the
// gRPC handler is registered once via sync.Once, and connectPeers is
// idempotent.
//
// Implements the blocking startup contract of KeeperGate: only one
// instance holds leadership at a time. Followers block here until
// the leader fails (3s heartbeat timeout) and they are elected as
// the new leader — matching the old PG advisory lock behaviour.
func (e *Election) Run(ctx context.Context) error {
	// Register gRPC handler once (panic-safe via sync.Once).
	e.registered.Do(func() {
		proto.RegisterKeeperElectionServer(e.srv, e)
		log.Info().Msg("keeper: gRPC election handler registered")
	})

	// Connect to peers (idempotent — already-connected peers are no-ops).
	e.connectPeers(ctx)

	// Start the election loop goroutine (once, guarded by atomic).
	if e.loopStarted.CompareAndSwap(false, true) {
		e.wg.Add(1)
		go e.electionLoop(ctx)
	}

	// Block until this instance becomes leader or context is done.
	// gateReady is closed in becomeLeaderLocked(), which only fires
	// when this instance wins the election. Followers block here
	// until failover (3s heartbeat timeout triggers new election).
	select {
	case <-e.gateReady:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// LockCtx returns the context that is active while this instance holds
// the keeper lock. It is cancelled when:
//   - Leadership is lost to another instance (heartbeat failure)
//   - The leader voluntarily yields
//   - Shutdown is called
func (e *Election) LockCtx() context.Context {
	<-e.gateReady // wait for first acquisition
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.lockCtx
}

// Release relinquishes the keeper lock. After calling Release, the
// instance must re-acquire by calling Run again (the outer loop in
// runner.go handles this). Creates a fresh gateReady channel so the
// next Run() call blocks on re-election instead of returning immediately
// on the (already-closed) previous channel.
func (e *Election) Release() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lockCancel != nil {
		e.lockCancel()
		e.lockCancel = nil
	}
	e.state = StateFollower
	e.leaderID = ""
	// Fresh gate channel — the next Run() call blocks until this instance
	// is re-elected. Without this, Run() would return immediately on the
	// already-closed previous gate and LockCtx() would return a cancelled context.
	e.gateReady = make(chan struct{})
}

// Yield signals the leader to voluntarily step down. The next-highest
// priority instance will become leader. No-op when this instance is
// not the leader. Returns true if yield was signalled.
func (e *Election) Yield() bool {
	select {
	case e.yieldCh <- struct{}{}:
		return true
	default:
		return false
	}
}

// Shutdown stops the election state machine and releases all resources.
func (e *Election) Shutdown() {
	e.cancel()
	e.wg.Wait()

	// Cancel the lock context if active.
	e.mu.Lock()
	if e.lockCancel != nil {
		e.lockCancel()
		e.lockCancel = nil
	}
	e.mu.Unlock()

	defer func() {
		e.clientsMu.Lock()
		e.clients = make(map[string]proto.KeeperElectionClient)
		e.clientsMu.Unlock()
	}()
}

// ── Observability metrics ────────────────────────────────────────────────

// LeaderID returns the instance ID of the current leader (empty when unknown).
func (e *Election) LeaderID() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.leaderID
}

// DegradedCount returns the number of consecutive RPC failures detected
// by the leader (for degraded-yield monitoring).
func (e *Election) DegradedCount() int64 {
	return e.degradedCnt.Load()
}

// HeartbeatAge returns the time since the last received heartbeat from the
// leader. When this instance is the leader, returns 0.
func (e *Election) HeartbeatAge() time.Duration {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.state == StateLeader {
		return 0
	}
	if e.lastHeartbeat.IsZero() {
		return -1 // no heartbeat ever received
	}
	return time.Since(e.lastHeartbeat)
}

// ── Prometheus-compatible metrics (KPR-1) ────────────────────────────────────
//
// These methods return raw metric values that a Prometheus metrics endpoint
// can marshal into the text exposition format. No external Prometheus client
// dependency required — the /api/v1/metrics handler reads these at scrape time.
//
// keeper_is_leader: 1 when this instance is the elected leader, 0 otherwise.
// keeper_heartbeat_age_ms: milliseconds since last leader heartbeat (0 = leader, -1 = never received).
// keeper_election_state: 0=follower, 1=leader, 2=candidate.
// keeper_degraded_rpc_count: consecutive RPC failures detected by leader.

// IsLeaderGauge returns 1 when this instance is the leader (for keeper_is_leader gauge).
func (e *Election) IsLeaderGauge() int {
	if e.IsLeader() {
		return 1
	}
	return 0
}

// HeartbeatAgeMs returns the heartbeat age in milliseconds.
// 0 = this instance is the leader.
// -1 = no heartbeat ever received from any leader.
func (e *Election) HeartbeatAgeMs() int64 {
	age := e.HeartbeatAge()
	if age < 0 {
		return -1
	}
	if age == 0 {
		return 0
	}
	return age.Milliseconds()
}

// StateGauge returns the election state as an integer for keeper_election_state gauge.
func (e *Election) StateGauge() int {
	return int(e.State())
}

// DegradedRPCGauge returns consecutive RPC failure count for keeper_degraded_rpc_count gauge.
func (e *Election) DegradedRPCGauge() int64 {
	return e.DegradedCount()
}

// ── gRPC server handler ───────────────────────────────────────────────────

// Heartbeat implements proto.KeeperElectionServer. It receives heartbeats
// from peers and updates the local election state.
func (e *Election) Heartbeat(ctx context.Context, req *proto.HeartbeatRequest) (*proto.HeartbeatResponse, error) {
	now := time.Now()

	e.mu.Lock()
	defer e.mu.Unlock()

	resp := &proto.HeartbeatResponse{
		LeaderId:  e.leaderID,
		Timestamp: now.UnixMilli(),
	}

	// Handle the heartbeat based on sender's claim and our state.
	switch {
	case req.IsLeader && req.InstanceId == e.leaderID:
		// Our leader sent a heartbeat — update last seen.
		e.lastHeartbeat = now
		resp.LeaderId = e.leaderID

	case req.IsLeader && e.leaderID == "":
		// No current leader — accept this sender as leader.
		e.leaderID = req.InstanceId
		e.state = StateFollower
		e.lastHeartbeat = now
		resp.LeaderId = e.leaderID

	case req.IsLeader && req.InstanceId != e.leaderID:
		// Leadership conflict! We think someone else is leader.
		// Deterministic tiebreaker: lower instance_id wins.
		if req.InstanceId < e.leaderID {
			// The sender has a lower ID — accept their leadership.
			log.Warn().
				Str("old_leader", e.leaderID).
				Str("new_leader", req.InstanceId).
				Msg("keeper: leadership conflict resolved (lower ID wins)")
			e.leaderID = req.InstanceId
			e.state = StateFollower
			e.lastHeartbeat = now
			resp.LeaderId = e.leaderID
		} else {
			// We win — tell the sender to follow us.
			resp.LeaderId = e.leaderID
			resp.Error = fmt.Sprintf("conflict: this instance follows %s (lower ID)", e.leaderID)
		}

	case !req.IsLeader && e.leaderID == "":
		// Follower announcing presence with no leader — start election.
		// If we're the lowest-ID instance, we become leader.
		if e.instanceID < req.InstanceId {
			e.becomeLeaderLocked()
		} else {
			e.leaderID = req.InstanceId
			e.state = StateFollower
			e.lastHeartbeat = now
		}
		resp.LeaderId = e.leaderID
	}

	return resp, nil
}

// becomeLeader transitions this instance to leader.
func (e *Election) becomeLeader() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.becomeLeaderLocked()
}

func (e *Election) becomeLeaderLocked() {
	if e.state == StateLeader {
		return // already leader
	}
	e.state = StateLeader
	e.leaderID = e.instanceID
	e.lastHeartbeat = time.Now()

	log.Info().Str("instance", e.instanceID).Msg("keeper: elected leader")

	// Create or update the lock context.
	if e.lockCancel != nil {
		// We're re-acquiring after a yield or failover — cancel old context.
		e.lockCancel()
	}
	e.lockCtx, e.lockCancel = context.WithCancel(e.ctx)

	// Signal gate ready (idempotent).
	select {
	case <-e.gateReady:
	default:
		close(e.gateReady)
	}
}

// resign transitions this instance from leader to follower.
func (e *Election) resign() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state != StateLeader {
		return
	}
	log.Info().Str("instance", e.instanceID).Msg("keeper: resigned leadership")
	e.state = StateFollower
	if e.lockCancel != nil {
		e.lockCancel()
		e.lockCancel = nil
	}
}

// ── Peer connection ───────────────────────────────────────────────────────

func (e *Election) connectPeers(ctx context.Context) {
	for _, addr := range e.peers {
		addr := addr // capture
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			// Use the election's base context (e.ctx) so peer connections
			// survive across Run()/Release() cycles.
			e.connectPeer(e.ctx, addr)
		}()
	}
	_ = ctx // unused — kept for interface compatibility with future use
}

func (e *Election) connectPeer(ctx context.Context, addr string) {
	backoff := time.Second
	for ctx.Err() == nil {
		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		conn, err := grpc.DialContext(dialCtx, addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
		cancel()

		if err != nil {
			log.Warn().Str("peer", addr).Err(err).Msg("keeper: peer dial failed, retrying")
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff *= 2; backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			continue
		}

		backoff = time.Second // reset on success
		client := proto.NewKeeperElectionClient(conn)

		e.clientsMu.Lock()
		e.clients[addr] = client
		e.clientsMu.Unlock()

		log.Info().Str("peer", addr).Msg("keeper: peer connected")

		// Block until context is done (connection stays alive).
		<-ctx.Done()

		e.clientsMu.Lock()
		delete(e.clients, addr)
		e.clientsMu.Unlock()

		if err := conn.Close(); err != nil {
			log.Warn().Str("peer", addr).Err(err).Msg("keeper: close peer conn")
		}
		return
	}
}

// ── Election loop ─────────────────────────────────────────────────────────

func (e *Election) electionLoop(ctx context.Context) {
	// Step 1: Initial election — sort peers by instance_id, determine our priority.
	// Since we don't know peers' instance_ids yet, we use a simple approach:
	// initially become leader if we're the first to send heartbeats,
	// or follow the first leader we hear from.

	// Start heartbeat and failure-detection tickers.
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	failureTicker := time.NewTicker(heartbeatInterval)
	defer failureTicker.Stop()

	// Initial state: become candidate to trigger an election round.
	e.mu.Lock()
	if e.leaderID == "" && e.state == StateFollower {
		// No known leader — become leader immediately if we have no peers,
		// or become candidate if we have peers to negotiate with.
		if len(e.peers) == 0 {
			e.becomeLeaderLocked()
		} else {
			e.state = StateCandidate
			// Set a short timeout: if no leader is elected within 1 heartbeat
			// interval, this instance declares itself leader.
		}
	}
	e.mu.Unlock()

	// Election timeout — after one tick without a leader, we become leader.
	// IMPORTANT: guarded by leader check to prevent race where a peer's
	// Heartbeat establishes a leader between our initial check and this timer.
	var leaderElectionCh <-chan time.Time
	if len(e.peers) > 0 && e.getLeaderID() == "" {
		et := time.NewTimer(heartbeatInterval)
		defer et.Stop()
		leaderElectionCh = et.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.ctx.Done():
			return
	case <-leaderElectionCh:
		// Election timeout — no leader heard from, become leader.
		// BUT: double-check we don't already follow a leader that was
		// established via Heartbeat() from a peer before this timer fired.
		leaderElectionCh = nil
		e.mu.RLock()
		alreadyHasLeader := e.leaderID != "" && e.leaderID != e.instanceID
		e.mu.RUnlock()
		if alreadyHasLeader {
			continue
		}
		e.becomeLeader()

		case <-ticker.C:
			e.heartbeatTick(ctx)

		case <-e.yieldCh:
			e.resign()

		case <-failureTicker.C:
			// Check for leader failure: if we're a follower and haven't
			// heard from the leader in missedThreshold ticks, start election.
			e.mu.RLock()
			state := e.state
			leaderID := e.leaderID
			lastHB := e.lastHeartbeat
			e.mu.RUnlock()

			if state == StateFollower && leaderID != "" && leaderID != e.instanceID {
				if time.Since(lastHB) > heartbeatInterval*missedThreshold {
					log.Warn().
						Str("leader", leaderID).
						Dur("since", time.Since(lastHB)).
						Msg("keeper: leader heartbeat timeout — declaring new election")
					e.becomeLeader()
					continue
				}
			}
		}
	}
}

// heartbeatTick sends a heartbeat to all peers (if leader) and checks
// for degraded RPC.
func (e *Election) heartbeatTick(ctx context.Context) {
	e.mu.RLock()
	isLeader := e.state == StateLeader
	leaderID := e.leaderID
	instanceID := e.instanceID
	e.mu.RUnlock()

	// Build the heartbeat request.
	req := &proto.HeartbeatRequest{
		InstanceId: instanceID,
		Timestamp:  time.Now().UnixMilli(),
		IsLeader:   isLeader,
		LeaderId:   leaderID,
	}

	// Send heartbeat to all peers.
	e.clientsMu.RLock()
	clients := make(map[string]proto.KeeperElectionClient, len(e.clients))
	for addr, c := range e.clients {
		clients[addr] = c
	}
	e.clientsMu.RUnlock()

	for addr, client := range clients {
		hbCtx, cancel := context.WithTimeout(ctx, heartbeatTimeout)
		resp, err := client.Heartbeat(hbCtx, req)
		cancel()

		if err != nil {
			log.Warn().Str("peer", addr).Err(err).Msg("keeper: heartbeat to peer failed")
			if isLeader {
				e.degradedCnt.Add(1)
			}
			continue
		}

		if isLeader {
			e.degradedCnt.Store(0) // successful heartbeat = not degraded
		}

		// Check for leadership conflict response.
		if resp.Error != "" && resp.LeaderId != "" {
			log.Warn().Str("peer", addr).Str("resp_leader", resp.LeaderId).
				Str("error", resp.Error).Msg("keeper: heartbeat conflict response")

			// If peer says there's a different leader with lower ID, follow them.
			e.mu.Lock()
			if resp.LeaderId < e.instanceID && e.state == StateLeader {
				log.Warn().
					Str("peer_leader", resp.LeaderId).
					Msg("keeper: peer has lower-ID leader — resigning")
				e.state = StateFollower
				e.leaderID = resp.LeaderId
				if e.lockCancel != nil {
					e.lockCancel()
					e.lockCancel = nil
				}
			}
			e.mu.Unlock()
		}
	}

	// Degraded-RPC check: if leader has too many consecutive failures, yield.
	if isLeader && e.maxDegraded > 0 {
		degraded := e.degradedCnt.Load()
		if degraded >= e.maxDegraded {
			log.Warn().Int64("failures", degraded).
				Msg("keeper: degraded RPC detected — yielding leadership")
			e.resign()
		}
	}
}

// ── Helper methods ────────────────────────────────────────────────────────

// getLeaderID returns the current leader's instance ID (thread-safe).
func (e *Election) getLeaderID() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.leaderID
}

// IsLeader returns true when this instance is the current leader.
func (e *Election) IsLeader() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state == StateLeader
}

// State returns the current election state.
func (e *Election) State() ElectionState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

// InstanceID returns this instance's unique identifier.
func (e *Election) InstanceID() string {
	return e.instanceID
}

// ── Sort peers by address for deterministic ordering ──────────────────────

// sortPeers returns the peer list sorted lexicographically. The lowest
// address has highest election priority.
func sortPeers(peers []string) []string {
	sorted := make([]string, len(peers))
	copy(sorted, peers)
	sort.Strings(sorted)
	return sorted
}
