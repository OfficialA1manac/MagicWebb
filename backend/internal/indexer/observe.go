package indexer

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// ErrTxPending is returned by ObserveTx when the transaction has no receipt yet.
var ErrTxPending = errors.New("transaction not mined yet")

// ErrTxIrrelevant is returned when the receipt exists but touches none of the
// marketplace contracts or tracked collections.
var ErrTxIrrelevant = errors.New("transaction emits no marketplace events")

// ObserveResult summarises what the instant lane did with one transaction.
type ObserveResult struct {
	Hash        string `json:"hash"`
	Block       uint64 `json:"block"`
	Events      int    `json:"events"`
	Status      string `json:"status"` // "indexed" | "reverted"
	AlreadySeen bool   `json:"already_seen"`
}

// ObserveTx is the instant lane: the frontend posts a transaction hash the
// moment its wallet reports it, and we index that transaction's logs right
// away instead of waiting for the reorg-safe watcher (reorgSafetyBlocks
// behind the head). Every handler write is an idempotent upsert keyed on
// (tx_hash, log_index, …), so the safe pass re-dispatching the same logs a
// few seconds later is a no-op. On success a "tx-indexed" event is published
// so the UI waiting on channel tx:<hash> can flip from "confirmed" to
// "live" without polling.
//
// Flare, Songbird and Coston2 run Snowman consensus with single-slot
// finality, so a mined receipt is not going to disappear; the watcher's
// safety margin exists for RPC inconsistency, not chain reorgs.
func (r *Runner) ObserveTx(ctx context.Context, hash common.Hash) (ObserveResult, error) {
	res := ObserveResult{Hash: hash.Hex()}

	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	rcpt, err := r.eth.TransactionReceipt(rctx, hash)
	cancel()
	if err != nil || rcpt == nil {
		if err == nil || errors.Is(err, ethereum.NotFound) || strings.Contains(err.Error(), "not found") {
			return res, ErrTxPending
		}
		return res, fmt.Errorf("receipt: %w", err)
	}
	res.Block = rcpt.BlockNumber.Uint64()
	if rcpt.Status != 1 {
		res.Status = "reverted"
		return res, nil
	}

	// Dedupe: if another observer already did this hash recently, skip work
	// but still answer 200 so the client stops retrying.
	if r.observed.seen(hash) {
		res.Status = "indexed"
		res.AlreadySeen = true
		return res, nil
	}

	relevant := r.relevantAddresses(ctx)
	var logs = rcpt.Logs[:0:0]
	for _, l := range rcpt.Logs {
		if _, ok := relevant[l.Address]; ok && len(l.Topics) > 0 {
			logs = append(logs, l)
		}
	}
	if len(logs) == 0 {
		return res, ErrTxIrrelevant
	}

	hctx, hcancel := context.WithTimeout(ctx, 3*time.Second)
	hdr, err := r.eth.HeaderByNumber(hctx, new(big.Int).SetUint64(res.Block))
	hcancel()
	if err != nil {
		return res, fmt.Errorf("header %d: %w", res.Block, err)
	}

	for _, l := range logs {
		if err := r.h.dispatch(ctx, *l, hdr.Time); err != nil {
			return res, fmt.Errorf("dispatch log %d: %w", l.Index, err)
		}
		res.Events++
	}
	r.observed.mark(hash)
	res.Status = "indexed"

	if r.bcast != nil {
		r.bcast.Publish(sse.Event{Type: "tx-indexed", Data: map[string]any{
			"tx_hash": strings.ToLower(hash.Hex()),
			"block":   res.Block,
			"events":  res.Events,
		}})
	}
	log.Info().Str("tx", hash.Hex()).Uint64("block", res.Block).Int("events", res.Events).Msg("instant lane: indexed")
	return res, nil
}

// relevantAddresses is the set of contracts whose logs we index: the three
// cores plus every tracked collection (Transfer events drive ownership).
func (r *Runner) relevantAddresses(ctx context.Context) map[common.Address]struct{} {
	set := map[common.Address]struct{}{}
	for _, a := range []string{r.cfg.MarketplaceAddr, r.cfg.AuctionAddr, r.cfg.OfferBookAddr} {
		if a != "" {
			set[common.HexToAddress(a)] = struct{}{}
		}
	}
	if tracked, err := r.q.ListTrackedCollections(ctx); err == nil {
		for _, a := range tracked {
			set[common.HexToAddress(a)] = struct{}{}
		}
	}
	return set
}

// observedSet remembers recently observed hashes so a client retrying the
// POST (or two tabs) does not redo the work. Bounded by both time (entries
// older than observedTTL are swept) and size (never more than
// observedMaxEntries after mark returns).
type observedSet struct {
	mu sync.Mutex
	m  map[common.Hash]time.Time
}

const (
	observedTTL        = 10 * time.Minute
	observedMaxEntries = 4096
)

func (s *observedSet) seen(h common.Hash) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		return false
	}
	return s.m[h].After(time.Now().Add(-observedTTL))
}

func (s *observedSet) mark(h common.Hash) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = make(map[common.Hash]time.Time)
	}
	now := time.Now()
	s.m[h] = now
	if len(s.m) <= observedMaxEntries {
		return
	}
	// Age sweep first: cheap, and normally enough.
	for k, t := range s.m {
		if now.Sub(t) > observedTTL {
			delete(s.m, k)
		}
	}
	// Hard cap: more than observedMaxEntries live hashes inside one TTL
	// window means the age sweep freed nothing, so evict the oldest until
	// we are under the cap. A dropped hash only costs a retry redoing its
	// observation — cheaper than an unbounded map.
	for len(s.m) > observedMaxEntries {
		var (
			oldestK common.Hash
			oldestT time.Time
			first   = true
		)
		for k, t := range s.m {
			if first || t.Before(oldestT) {
				oldestK, oldestT, first = k, t, false
			}
		}
		delete(s.m, oldestK)
	}
}
