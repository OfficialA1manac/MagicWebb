package keeper

import (
	"sync"
	"testing"
	"time"
)

// newTestElection builds an Election with no gRPC server and no peers. Nothing
// here starts the election loop or touches the network — these tests exercise
// the state machine directly, which is where every bug found in this file so
// far has lived.
func newTestElection(t *testing.T) *Election {
	t.Helper()
	e := New(nil, nil, "127.0.0.1:0")
	if e == nil {
		t.Fatal("New returned nil")
	}
	return e
}

// A leader that resigns must remain ELECTABLE. resign() used to clear both
// leaderID and lastHeartbeat, and the failure detector required a non-empty
// leaderID, so nothing could ever fire again: the instance stayed a follower
// for the rest of the process lifetime. On a single-instance deployment (no
// peers to elect anyone) that stopped keeper work permanently.
func TestResignLeavesInstanceElectable(t *testing.T) {
	e := newTestElection(t)

	e.mu.Lock()
	e.state = StateLeader
	e.leaderID = e.instanceID
	e.lastHeartbeat = time.Now().Add(-time.Hour) // stale, as a long-lived leader's would be
	e.mu.Unlock()

	e.resign()

	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.state != StateFollower {
		t.Fatalf("state = %v, want StateFollower", e.state)
	}
	if e.leaderID != "" {
		t.Fatalf("leaderID = %q, want empty after resign", e.leaderID)
	}
	// The failover window must be OPEN, not zeroed. A zero timestamp means
	// "never heard anything", which the detector treats as startup and skips.
	if e.lastHeartbeat.IsZero() {
		t.Fatal("lastHeartbeat is zero after resign — the failure detector can never re-elect this instance")
	}
	// And it must be recent, so the detector fires after the normal failover
	// window rather than immediately.
	if time.Since(e.lastHeartbeat) > time.Minute {
		t.Fatalf("lastHeartbeat = %v, want ~now", e.lastHeartbeat)
	}
}

// resign() is triggered by a streak of degraded ticks. If the counter survives
// the resign, the instance yields again the moment it is re-elected, on the
// strength of a streak that already cost it the leadership once.
func TestResignResetsDegradedCounter(t *testing.T) {
	e := newTestElection(t)

	e.mu.Lock()
	e.state = StateLeader
	e.mu.Unlock()
	e.degradedCnt.Store(99)

	e.resign()

	if got := e.degradedCnt.Load(); got != 0 {
		t.Fatalf("degradedCnt = %d, want 0 after resign", got)
	}
}

// resign() on a follower is a no-op: it must not disturb a leader identity
// this instance legitimately learned from a peer.
func TestResignIsNoOpForFollower(t *testing.T) {
	e := newTestElection(t)

	e.mu.Lock()
	e.state = StateFollower
	e.leaderID = "someone-else"
	e.mu.Unlock()

	e.resign()

	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.leaderID != "someone-else" {
		t.Fatalf("leaderID = %q, want it untouched for a follower", e.leaderID)
	}
}

// Release(), resign() and the heartbeat paths all REASSIGN e.gateReady under
// mu. Readers must copy it under the lock; reading the field bare is a data
// race. Run with -race, where the old code fails.
func TestGateReadyAccessIsRaceFree(t *testing.T) {
	e := newTestElection(t)

	// Close the initial gate so LockCtx() does not block.
	e.mu.Lock()
	close(e.gateReady)
	e.mu.Unlock()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Reader: LockCtx copies the channel under mu before receiving.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = e.LockCtx()
			}
		}
	}()

	// Writer: Release swaps in a fresh gate, then we re-close it so the
	// reader never blocks forever.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			e.Release()
			e.mu.Lock()
			close(e.gateReady)
			e.mu.Unlock()
		}
		close(stop)
	}()

	wg.Wait()
}

// connectPeers must be idempotent. Run() is called again after every
// Release(), so an unguarded connectPeers started a second dialer per peer on
// every cycle, each dialing its own grpc.ClientConn and overwriting the entry
// in e.clients.
func TestConnectPeersSkipsPeersAlreadyDialing(t *testing.T) {
	e := New(nil, []string{"192.0.2.1:1", "192.0.2.2:1"}, "127.0.0.1:0")

	// Pre-mark both peers as having a live dialer, which is the state a second
	// Run() finds. connectPeers must then start nothing at all.
	e.clientsMu.Lock()
	for _, p := range e.peers {
		e.dialing[p] = struct{}{}
	}
	e.clientsMu.Unlock()

	done := make(chan struct{})
	go func() {
		e.connectPeers(t.Context())
		// If connectPeers spawned dialers against these unroutable addresses,
		// they would sit in a dial/backoff loop and this Wait would block.
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("connectPeers started dialers for peers already marked as dialing")
	}

	e.clientsMu.Lock()
	n := len(e.dialing)
	e.clientsMu.Unlock()
	if n != len(e.peers) {
		t.Fatalf("dialing has %d entries, want %d — the guard must not add or drop markers", n, len(e.peers))
	}
}
