// Package indexer provides the chain watcher and background workers.
package indexer

import (
	"context"
	cryptoecdsa "crypto/ecdsa"
	"fmt"
	"math"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// EthClient is the chain-access surface the indexer and keepers need. Both
// *ethclient.Client and *rpcpool.Pool satisfy it; production injects the pool
// so every read, write and log filter gets sticky failover.
type EthClient interface {
	BlockNumber(ctx context.Context) (uint64, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
	FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error)
	CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
	PendingNonceAt(ctx context.Context, account common.Address) (uint64, error)
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
	SuggestGasTipCap(ctx context.Context) (*big.Int, error)
	SendTransaction(ctx context.Context, tx *types.Transaction) error
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error)
	// Phase 4 V4.1: BalanceAt returns the native balance of an account at the
	// given block (nil = latest). Used by the keeper startup balance check.
	BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error)
}

// KeeperGate blocks until this instance may run keeper broadcasts (cluster
// single-flight). It returns a context that is cancelled if lock ownership is
// later lost (keepers must stop immediately and re-acquire) plus a release
// func. nil gate = keepers start at once under the parent ctx.
type KeeperGate func(ctx context.Context) (lockCtx context.Context, release func(), err error)

// Runner orchestrates all indexer workers.
type Runner struct {
	cfg        *config.Config
	q          *db.Q
	bcast      *sse.Broadcaster
	eth        EthClient
	h          *handlers
	keeperGate KeeperGate
	// serverTimeMs is the latest block timestamp in milliseconds (atomic).
	serverTimeMs *int64
	// headLagBlocks is the difference between the chain head and the last
	// indexed block, updated atomically every watcher tick. Exported via
	// HeadLagBlocks() for SLO monitoring and health check integration.
	// A value > 15 indicates the indexer is falling behind (≈ 30 seconds
	// at 2s block time on Flare/Coston2).
	headLagBlocks int64
}

// HeadLagBlocks returns the current head lag in blocks (chain head minus last
// indexed block), updated atomically every watcher tick. Used by the /healthz
// endpoint to detect when the indexer is falling behind the chain head —
// values > 15 indicate the indexer is >30 seconds behind at Flare/Coston2's
// ~2s block time.
func (r *Runner) HeadLagBlocks() uint64 {
	return uint64(atomic.LoadInt64(&r.headLagBlocks))
}

// New creates a Runner with all dependencies injected.
func New(cfg *config.Config, q *db.Q, bcast *sse.Broadcaster, eth EthClient, serverTimeMs *int64) *Runner {
	return &Runner{
		cfg:          cfg,
		q:            q,
		bcast:        bcast,
		eth:          eth,
		h:            &handlers{q: q, bcast: bcast},
		serverTimeMs: serverTimeMs,
	}
}

// WithKeeperGate sets the single-flight gate the keeper workers must win
// before broadcasting transactions.
func (r *Runner) WithKeeperGate(g KeeperGate) *Runner {
	r.keeperGate = g
	return r
}

// Run starts all workers and blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	var wg sync.WaitGroup

	wg.Add(1)
	go func() { defer wg.Done(); r.runWatcher(ctx) }()

	wg.Add(1)
	go func() { defer wg.Done(); r.runScoreWorker(ctx) }()

	wg.Add(1)
	go func() { defer wg.Done(); r.runOfferExpirySweeper(ctx) }()

	// Offer Keeper goroutine REMOVED: the contract no longer has a
	// permissionless refundExpiredOffer function. The user explicitly
	// required "Users cannot refund offers. Only sellers can reject offers" —
	// there is no keeper path for auto-refunding expired offers because only
	// the seller (contract owner via rejectOffer) can refund. The DB-side
	// offer-expiry sweeper (runOfferExpirySweeper) still marks expired
	// offers in the database so the UI can accurately reflect offer status;
	// the on-chain refund is now seller-only via rejectOffer.

	wg.Add(1)
	go func() { defer wg.Done(); r.runMetadataWorker(ctx) }()

	wg.Add(1)
	go func() { defer wg.Done(); r.runImageRetryWorker(ctx) }()

	wg.Add(1)
	go func() { defer wg.Done(); r.runOwnershipRepairWorker(ctx) }()

	wg.Add(1)
	go func() { defer wg.Done(); r.runWithdrawalSweeper(ctx) }()

	if r.cfg.KeeperKey != "" {
		// Phase 4 V4.1: one-shot keeper wallet balance check before any
		// keeper goroutine starts. Runs once per process lifetime.
		// Non-fatal on RPC failure.
		// Normalise optional 0x prefix before parsing (config.Load()
		// validates the hex but does not strip the prefix from C.KeeperKey).
		keeperKeyHex := strings.TrimPrefix(r.cfg.KeeperKey, "0x")
		key, keyErr := crypto.HexToECDSA(keeperKeyHex)
		if keyErr != nil {
			log.Error().Err(keyErr).Msg("keeper: invalid KEEPER_KEY at startup, keepers disabled")
		} else {
			keeperAddr := crypto.PubkeyToAddress(key.PublicKey)
			// Initial balance check (best-effort; non-fatal on RPC failure).
			r.checkKeeperBalance(ctx, keeperAddr)

			wg.Add(1)
			go func() {
				defer wg.Done()
				// Acquire → run → (lock lost) → re-acquire, until shutdown. The
				// keepers run under lockCtx so they stop the moment single-flight
				// ownership can no longer be proven (no split-brain broadcasts).
				for ctx.Err() == nil {
					kctx, release := ctx, func() {}
					if r.keeperGate != nil {
						var err error
						kctx, release, err = r.keeperGate(ctx)
						if err != nil {
							if ctx.Err() == nil {
								log.Error().Err(err).Msg("keeper gate: acquisition failed")
							}
							return
						}
					}
					var kwg sync.WaitGroup
					kwg.Add(2)
					go func() { defer kwg.Done(); r.runAuctionKeeper(kctx) }()
					go func() { defer kwg.Done(); r.runLoserRefundSweeper(kctx) }()
					kwg.Wait()
					release()
					if r.keeperGate == nil {
						return // no gate: keepers only stop on shutdown
					}
				}
			}()
		}
	}

	wg.Wait()
}

// ── Chain Watcher ─────────────────────────────────────────────────────────

// reorgSafetyBlocks is the confirmation depth before the indexer considers a
// block final. The indexer stays this many blocks behind the reported head so
// that a chain reorganisation (reorg) of up to this depth does not produce
// orphaned events in the DB. Flare mainnet and Songbird have ~2s block times
// and near-zero reorg risk past 2 blocks; Coston2 (testnet) can reorg deeper.
// 12 blocks ≈ 24 seconds of finalisation on Coston2 — conservative for all
// three target chains.
const reorgSafetyBlocks = 12

// headLag keeps the indexer this many blocks behind the reported head: cheap
// reorg tolerance, and tolerance for a mid-iteration failover to an endpoint
// whose own head slightly lags the one that answered BlockNumber.
const headLag = 2

// ReindexCollection forces the indexer to re-scan Transfer events for a single
// collection from fromBlock to the current chain head. Useful after adding a
// new collection to TRACKED_COLLECTIONS — past holdings become visible without
// waiting for new Transfer events. Returns the number of blocks scanned.
func (r *Runner) ReindexCollection(ctx context.Context, collectionAddr string, fromBlock uint64) (int, error) {
	addr := common.HexToAddress(collectionAddr)
	head, err := r.eth.BlockNumber(ctx)
	if err != nil {
		return 0, fmt.Errorf("reindex: block number: %w", err)
	}
	target := head - headLag
	if target <= fromBlock {
		return 0, fmt.Errorf("reindex: target block %d <= fromBlock %d", target, fromBlock)
	}

	log.Info().
		Str("collection", collectionAddr).
		Uint64("from", fromBlock).
		Uint64("to", target).
		Msg("reindex: starting collection backfill")

	chunk := r.cfg.GetLogsChunk
	if chunk == 0 {
		chunk = 30
	}

	var scanned, processed int
	for start := fromBlock; start <= target; start += chunk {
		end := start + chunk - 1
		if end > target {
			end = target
		}

		logs, err := r.eth.FilterLogs(ctx, ethereum.FilterQuery{
			FromBlock: big.NewInt(int64(start)),
			ToBlock:   big.NewInt(int64(end)),
			Addresses: []common.Address{addr},
			Topics:    transferTopics(),
		})
		if err != nil {
			return scanned, fmt.Errorf("reindex: filter logs [%d..%d]: %w", start, end, err)
		}

		for _, l := range logs {
			// Fetch block timestamp on demand.
			hctx, hcancel := context.WithTimeout(ctx, 2*time.Second)
			h, herr := r.eth.HeaderByNumber(hctx, big.NewInt(int64(l.BlockNumber)))
			hcancel()
			if herr != nil {
				log.Warn().Err(herr).Uint64("block", l.BlockNumber).
					Msg("reindex: header lookup failed, skipping log")
				continue
			}
			if err := r.h.dispatch(ctx, l, h.Time); err != nil {
				log.Warn().Err(err).Str("tx", l.TxHash.Hex()).
					Msg("reindex: dispatch failed")
			} else {
				processed++
			}
		}

		scanned += int(end - start + 1)

		if err := ctx.Err(); err != nil {
			return scanned, err
		}
	}

	log.Info().
		Str("collection", collectionAddr).
		Int("blocks_scanned", scanned).
		Int("logs_processed", processed).
		Msg("reindex: collection backfill complete")

	return scanned, nil
}

func (r *Runner) runWatcher(ctx context.Context) {
	chainID := int(r.cfg.ChainID)
	contracts := []common.Address{
		common.HexToAddress(r.cfg.MarketplaceAddr),
		common.HexToAddress(r.cfg.AuctionAddr),
		common.HexToAddress(r.cfg.OfferBookAddr),
	}
	topics := coreTopics()

	fromBlock, err := r.q.GetIndexedBlock(ctx, chainID)
	if err != nil {
		log.Error().Err(err).Msg("watcher: get indexed block")
	}
	if r.cfg.IndexFromBlock > fromBlock {
		fromBlock = r.cfg.IndexFromBlock
	}

	// lastBlock is the highest block KNOWN indexed. It only ever advances after
	// a fully successful range — a failed/partial range is retried next tick,
	// so RPC failures can delay events but never permanently drop them.
	lastBlock := fromBlock
	// lastBlockHash tracks the block hash of lastBlock for reorg detection.
	// On each new range, we fetch the header of lastBlock+1 and check that its
	// parentHash matches the stored hash. If it doesn't match, a reorg has
	// occurred and we rewind lastBlock by reorgSafetyBlocks to re-index the
	// affected range.
	var lastBlockHash common.Hash
	if lastBlock > 0 {
		if h, err := r.eth.HeaderByNumber(ctx, big.NewInt(int64(lastBlock))); err == nil {
			lastBlockHash = h.Hash()
		}
	}
	backfilled := false

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			head, err := r.eth.BlockNumber(ctx)
			if err != nil {
				log.Warn().Err(err).Msg("watcher: block poll failed")
				continue
			}
			if head <= reorgSafetyBlocks {
				continue
			}
			target := head - headLag
			if target <= lastBlock {
				// Even when the head hasn't advanced, check for reorgs at the
				// current tip. Flare/Coston2 rarely reorg past 1 block, but a
				// deep reorg on testnet can orphan blocks the indexer already
				// processed. We verify chain continuity by checking the parent
				// hash of target against lastBlockHash every tick (cheap: one
				// HeaderByNumber call per tick when idle).
				if lastBlock > 0 && lastBlockHash != (common.Hash{}) {
					targetHeader, err := r.eth.HeaderByNumber(ctx, big.NewInt(int64(target)))
					if err == nil && targetHeader.ParentHash != lastBlockHash {
						// Reorg detected: the block the indexer thinks is the
						// last indexed block no longer sits on the canonical chain.
						// Rewind by reorgSafetyBlocks to re-index the affected range.
						log.Warn().
							Str("expected_parent", lastBlockHash.Hex()).
							Str("actual_parent", targetHeader.ParentHash.Hex()).
							Uint64("head", head).
							Uint64("rewind_to", lastBlock-reorgSafetyBlocks).
							Msg("watcher: reorg detected — rewinding indexer")
						if lastBlock > reorgSafetyBlocks {
							lastBlock -= reorgSafetyBlocks
						} else {
							lastBlock = 0
						}
						// Reset hash so continuity is re-established on the next tick.
						lastBlockHash = common.Hash{}
						// Persist the rewind so the indexer resumes from the
						// rewound position on restart.
						if err := r.q.SetIndexedBlock(ctx, chainID, lastBlock); err != nil {
							log.Error().Err(err).Uint64("block", lastBlock).
								Msg("watcher: persist rewind cursor failed")
						}
					}
				}
				// Update serverTimeMs from the latest header even when idle.
				if header, err := r.eth.HeaderByNumber(ctx, big.NewInt(int64(target))); err == nil {
					atomic.StoreInt64(r.serverTimeMs, int64(header.Time*1000))
				}
				// Store head lag metric on idle ticks too.
				if head > lastBlock {
					atomic.StoreInt64(&r.headLagBlocks, int64(head-lastBlock))
				}
				continue
			}
			if !backfilled {
				log.Info().Uint64("from", lastBlock+1).Uint64("to", target).Msg("watcher: backfill start")
			}
			// Verify chain continuity before processing the new range.
			// Fetch the header of the first new block (lastBlock+1) and check
			// its parentHash against the lastBlockHash we cached.
			if lastBlock > 0 && lastBlockHash != (common.Hash{}) {
				firstNew, err := r.eth.HeaderByNumber(ctx, big.NewInt(int64(lastBlock+1)))
				if err == nil && firstNew.ParentHash != lastBlockHash {
					log.Warn().
						Str("expected_parent", lastBlockHash.Hex()).
						Str("actual_parent", firstNew.ParentHash.Hex()).
						Uint64("head", head).
						Uint64("last_block", lastBlock).
						Msg("watcher: chain continuity break — reorg detected before new range; rewinding")
					if lastBlock > reorgSafetyBlocks {
						lastBlock -= reorgSafetyBlocks
					} else {
						lastBlock = 0
					}
					lastBlockHash = common.Hash{}
					if err := r.q.SetIndexedBlock(ctx, chainID, lastBlock); err != nil {
						log.Error().Err(err).Uint64("block", lastBlock).
							Msg("watcher: persist rewind cursor failed")
					}
					continue // skip this range; retry on next tick after rewind
				}
			}
			// backfill chunks every range, so cold start, outage catch-up and
			// the steady 1-2 block tick all share one code path.
			if err := r.backfill(ctx, lastBlock+1, target, contracts, topics, chainID); err != nil {
				log.Error().Err(err).Uint64("from", lastBlock+1).Uint64("to", target).
					Msg("watcher: range failed, will retry")
				continue // lastBlock unchanged: the same range is retried next tick
			}
			// Cache the last block's hash for continuity checking on the next tick.
			if header, err := r.eth.HeaderByNumber(ctx, big.NewInt(int64(target))); err == nil {
				lastBlockHash = header.Hash()
				atomic.StoreInt64(r.serverTimeMs, int64(header.Time*1000))
			} else {
				// If we can't get the header, reset the hash so the next tick
				// re-establishes continuity conservatively (still processes the
				// range that was already backfilled).
				lastBlockHash = common.Hash{}
			}
			lastBlock = target
			if !backfilled {
				backfilled = true
				log.Info().Msg("watcher: backfill complete")
			}
			// Store head lag metric (head minus last indexed block).
			// On Flare/Coston2 (~2s block time), 15 blocks ≈ 30 seconds.
			atomic.StoreInt64(&r.headLagBlocks, int64(head-lastBlock))
		}
	}
}

// backfill processes [from..to] in getLogs-cap-sized chunks, stopping at the
// first failure so the caller never advances its cursor past an unindexed gap.
func (r *Runner) backfill(ctx context.Context, from, to uint64, contracts []common.Address, topics [][]common.Hash, chainID int) error {
	chunk := r.cfg.GetLogsChunk
	if chunk == 0 {
		chunk = 30
	}
	for start := from; start <= to; start += chunk {
		end := start + chunk - 1
		if end > to {
			end = to
		}
		if err := r.processRange(ctx, start, end, contracts, topics, chainID); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

// processRange indexes one block range. The persisted cursor advances only
// when the whole range (core events + transfers) succeeded.
func (r *Runner) processRange(ctx context.Context, from, to uint64, contracts []common.Address, topics [][]common.Hash, chainID int) error {
	logs, err := r.eth.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: big.NewInt(int64(from)),
		ToBlock:   big.NewInt(int64(to)),
		Addresses: contracts,
		Topics:    topics,
	})
	if err != nil {
		return fmt.Errorf("filter logs [%d..%d]: %w", from, to, err)
	}

	blockTimes := make(map[uint64]uint64)
	for _, l := range logs {
		if _, ok := blockTimes[l.BlockNumber]; !ok {
			// Per-RPC 2s timeout so a stuck HeaderByNumber can't stall the
			// whole 30-block chunk. Log + skip on failure rather than faking
			// a wall-clock timestamp — chains drift from wall-clock and
			// downstream sort-by-block-time queries would otherwise see
			// drift between chain-truth and DB-truth. The next indexer
			// cycle re-indexes this log when the RPC recovers; processTransfers
			// sees the same blockTimes map and skips blocks it has no header for.
			hctx, hcancel := context.WithTimeout(ctx, 2*time.Second)
			h, herr := r.eth.HeaderByNumber(hctx, big.NewInt(int64(l.BlockNumber)))
			hcancel()
			if herr != nil {
				// ABORT the whole range so lastBlock does NOT advance past this
				// unindexed block. The previous log.Warn + continue pattern
				// silently dropped the log: the cursor advanced with the rest of
				// the chunk and the unwitnessed block's events became permanently
				// unindexed. Returning an error makes backfill() exit; the next
				// watcher tick re-attempts the same lastBlock+1..target range, so
				// when the RPC recovers the events byte-for-byte replay (handlers
				// are idempotent upserts). The log.Error preserves the structured
				// per-block context that the old log.Warn surfaced.
				log.Error().Err(herr).Uint64("block", l.BlockNumber).Msg("watcher: header lookup failed; aborting range for retry on next tick")
				return fmt.Errorf("watcher: header lookup failed for block %d: %w", l.BlockNumber, herr)
			}
			blockTimes[l.BlockNumber] = h.Time
		}
		if err := r.h.dispatch(ctx, l, blockTimes[l.BlockNumber]); err != nil {
			log.Error().Err(err).Str("tx", l.TxHash.Hex()).Msg("watcher: dispatch")
		}
	}

	if err := r.processTransfers(ctx, from, to, blockTimes); err != nil {
		return err
	}

	if err := r.q.SetIndexedBlock(ctx, chainID, to); err != nil {
		// Persistence failure is non-fatal: the in-memory cursor stays correct
		// and a restart simply re-indexes (handlers are idempotent upserts).
		log.Error().Err(err).Uint64("block", to).Msg("watcher: set indexed block")
	}
	return nil
}

// processTransfers watches NFT Transfer events on every tracked collection in the
// block range, maintaining ownership and orphaning listings whose seller moved out.
//
// Header policy mirrors processRange: per-RPC 2s timeout, on failure log+skip
// (never fall back to wall-clock — chain drift would put sort-by-blockTime
// queries out of order). The previous implementation zeroed blockTimes[blk]
// to time.Now() when missing; flagged in Priority Stack as
// `processTransfersWallClock` at 🟠 P1 (DoS-with-recoverable-state: the next
// watcher tick re-indexes the log when HeaderByNumber recovers).
func (r *Runner) processTransfers(ctx context.Context, from, to uint64, blockTimes map[uint64]uint64) error {
	tracked, err := r.q.ListTrackedCollections(ctx)
	if err != nil {
		return fmt.Errorf("list tracked collections: %w", err)
	}
	if len(tracked) == 0 {
		return nil
	}
	addrs := make([]common.Address, len(tracked))
	for i, a := range tracked {
		addrs[i] = common.HexToAddress(a)
	}
	logs, err := r.eth.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: big.NewInt(int64(from)),
		ToBlock:   big.NewInt(int64(to)),
		Addresses: addrs,
		Topics:    transferTopics(),
	})
	if err != nil {
		return fmt.Errorf("transfer logs [%d..%d]: %w", from, to, err)
	}
	for _, l := range logs {
		bt, ok := blockTimes[l.BlockNumber]
		if !ok {
			hctx, hcancel := context.WithTimeout(ctx, 2*time.Second)
			h, herr := r.eth.HeaderByNumber(hctx, big.NewInt(int64(l.BlockNumber)))
			hcancel()
			// v29 audit F-02: mirror processRange's abort-on-miss policy.
			// The previous `continue` skipped the log AND let the chunk's
			// SetIndexedBlock advance past the unindexed block — orphaned
			// ownership events leaked into the next chain pull. Aborting
			// the chunk keeps the cursor stalled until the RPC recovers;
			// idempotent upserts replay on the next tick.
			if herr != nil {
				log.Error().Err(herr).Uint64("block", l.BlockNumber).Str("tx", l.TxHash.Hex()).
					Msg("transfer: header lookup failed; aborting chunk for retry on next tick")
				return fmt.Errorf("transfer: header lookup failed for block %d: %w", l.BlockNumber, herr)
			}
			bt = h.Time
			// Memoise so the next Transfer log in the same block
			// within this chunk reuses the cached timestamp without
			// another HeaderByNumber round-trip.
			blockTimes[l.BlockNumber] = bt
		}
		if err := r.h.dispatch(ctx, l, bt); err != nil {
			log.Error().Err(err).Str("tx", l.TxHash.Hex()).Msg("watcher: transfer dispatch")
		}
	}
	return nil
}

// ── Trending Score Worker ─────────────────────────────────────────────────

type scoreWeights struct{ views, bids, volume, decayLambda float64 }

func computeScore(views, bids uint64, volumeEth, ageHours float64, w scoreWeights) float64 {
	raw := float64(views)*w.views + float64(bids)*w.bids + volumeEth*w.volume
	return raw * math.Exp(-w.decayLambda*ageHours)
}

func weiToEth(wei *big.Int) float64 {
	if wei == nil || wei.Sign() == 0 {
		return 0
	}
	f, _ := new(big.Float).Quo(new(big.Float).SetInt(wei), new(big.Float).SetFloat64(1e18)).Float64()
	return f
}

func (r *Runner) runScoreWorker(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.computeAllScores(ctx)
		}
	}
}

func (r *Runner) computeAllScores(ctx context.Context) {
	w := scoreWeights{
		views: r.cfg.ScoreWViews, bids: r.cfg.ScoreWBids,
		volume: r.cfg.ScoreWVolume, decayLambda: r.cfg.ScoreDecay,
	}
	windows := []struct {
		name  string
		since time.Duration
	}{
		{"1h", time.Hour},
		{"24h", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
	}
	// One grouped query per window (3/min total) instead of 3 queries per
	// collection per window (4,500/min at 500 collections).
	for _, win := range windows {
		stats, err := r.q.GetCollectionStatsSince(ctx, time.Now().Add(-win.since), 500)
		if err != nil {
			log.Error().Err(err).Str("window", win.name).Msg("score worker: stats query")
			continue
		}
		for _, s := range stats {
			score := computeScore(uint64(s.Views), uint64(s.Bids), weiToEth(s.VolumeWei), win.since.Hours(), w)
			ts := db.TrendingScore{
				Collection: s.Collection, Window: win.name,
				Score: score, Views: s.Views, Bids: s.Bids, VolumeWei: s.VolumeWei,
			}
			if err := r.q.UpsertTrendingScore(ctx, ts); err != nil {
				log.Warn().Err(err).Str("coll", s.Collection).Msg("score worker: upsert")
			}
		}
	}
}

// ── Offer Expiry Sweeper ──────────────────────────────────────────────────

func (r *Runner) runOfferExpirySweeper(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := r.q.ExpireOffers(ctx)
			if err != nil {
				log.Error().Err(err).Msg("offer sweeper: expire failed")
			} else if n > 0 {
				log.Info().Int64("expired", n).Msg("offer sweeper: offers expired")
			}
		}
	}
}

// ── Withdrawal Sweeper ("withdraw required" verification) ─────────────────

var pendingReturnsSelector = crypto.Keccak256([]byte("pendingReturns(address)"))[:4]

// runWithdrawalSweeper verifies seeded pending-withdrawal candidates against
// AuctionHouse.pendingReturns on-chain. Refund events fire whether a push
// landed or fell back to pull, and withdrawRefund emits nothing — so this is
// the only honest source of "you must click withdraw". Zero balance deletes
// the row; a positive balance verifies it (UI banner) and notifies once.
func (r *Runner) runWithdrawalSweeper(ctx context.Context) {
	auctionAddr := common.HexToAddress(r.cfg.AuctionAddr)
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rows, err := r.q.ListPendingWithdrawals(ctx, 200)
			if err != nil {
				log.Error().Err(err).Msg("withdrawal sweeper: list")
				continue
			}
			for _, row := range rows {
				data := append([]byte(nil), pendingReturnsSelector...)
				data = append(data, common.LeftPadBytes(common.HexToAddress(row.Address).Bytes(), 32)...)
				out, err := r.eth.CallContract(ctx, ethereum.CallMsg{To: &auctionAddr, Data: data}, nil)
				if err != nil || len(out) < 32 {
					log.Warn().Err(err).Str("addr", row.Address).Msg("withdrawal sweeper: pendingReturns call")
					continue
				}
				owed := new(big.Int).SetBytes(out[:32])
				if owed.Sign() == 0 {
					if err := r.q.DeletePendingWithdrawal(ctx, row.Address); err != nil {
						log.Warn().Err(err).Str("addr", row.Address).Msg("withdrawal sweeper: delete")
					}
					continue
				}
				first, err := r.q.MarkPendingWithdrawalVerified(ctx, row.Address, owed.String())
				if err != nil {
					log.Warn().Err(err).Str("addr", row.Address).Msg("withdrawal sweeper: verify")
					continue
				}
				if first {
					r.h.notify(ctx, row.Address, "refund", "Action needed: withdraw your refund",
						owed.String()+" wei is waiting in the auction contract — automatic delivery failed.",
						"/profile/"+row.Address)
				}
			}
		}
	}
}

// ── Auction Keeper (on-chain settlement) ──────────────────────────────────

var settleSelector = crypto.Keccak256([]byte("settle(uint256)"))[:4]

func (r *Runner) runAuctionKeeper(ctx context.Context) {
	keeperKeyHex := strings.TrimPrefix(r.cfg.KeeperKey, "0x")
	key, err := crypto.HexToECDSA(keeperKeyHex)
	if err != nil {
		log.Error().Err(err).Msg("keeper: invalid KEEPER_KEY, keeper disabled")
		return
	}
	keeperAddr := crypto.PubkeyToAddress(key.PublicKey)
	auctionAddr := common.HexToAddress(r.cfg.AuctionAddr)
	chainIDBig := big.NewInt(int64(r.cfg.ChainID))
	signer := types.NewLondonSigner(chainIDBig)

	log.Info().Str("keeper", keeperAddr.Hex()).Msg("keeper: started")
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			auctions, err := r.q.GetExpiredActiveAuctions(ctx)
			if err != nil {
				log.Error().Err(err).Msg("keeper: get expired auctions")
				continue
			}
			for _, a := range auctions {
				txHash, err := r.sendSettle(ctx, key, keeperAddr, auctionAddr, signer, chainIDBig, int64(a.AuctionID))
				if err != nil {
					log.Error().Err(err).Int64("auctionId", a.AuctionID).Msg("keeper: settle tx failed")
					continue
				}
				// Wait for receipt confirmation before moving to the next auction.
				// Without this, sequential settle calls in one tick race on
				// PendingNonceAt (all see the same nonce) and only the first
				// tx is accepted — the rest fail with "nonce too low" and the
				// auctions remain unsettled until the next keeper tick. The
				// waitMined call also confirms the tx was mined successfully
				// (not reverted), which reduces the number of keeper retries
				// on already-settled auctions.
				if err := r.waitMined(ctx, txHash, 2*time.Minute); err != nil {
					log.Error().Err(err).Int64("auctionId", a.AuctionID).Str("tx", txHash.Hex()).
						Msg("keeper: settle tx receipt not confirmed; will retry on next tick")
					continue
				}
				log.Info().Int64("auctionId", a.AuctionID).Str("tx", txHash.Hex()).Msg("keeper: settle confirmed")
			}

			inactive, err := r.q.GetInactiveAuctions(ctx)
			if err != nil {
				log.Error().Err(err).Msg("keeper: get inactive auctions")
				continue
			}
			for _, a := range inactive {
				txHash, err := r.sendSettle(ctx, key, keeperAddr, auctionAddr, signer, chainIDBig, int64(a.AuctionID))
				if err != nil {
					log.Error().Err(err).Int64("auctionId", a.AuctionID).Msg("keeper: cancel-inactive tx failed")
					continue
				}
				if err := r.waitMined(ctx, txHash, 2*time.Minute); err != nil {
					log.Error().Err(err).Int64("auctionId", a.AuctionID).Str("tx", txHash.Hex()).
					Msg("keeper: cancel-inactive tx receipt not confirmed; will retry on next tick")
					continue
				}
				log.Info().Int64("auctionId", a.AuctionID).Str("tx", txHash.Hex()).Msg("keeper: cancel-inactive confirmed")
			}
		}
	}
}

func (r *Runner) sendSettle(ctx context.Context, key *cryptoecdsa.PrivateKey, from, to common.Address, signer types.Signer, chainID *big.Int, auctionID int64) (common.Hash, error) {
	idBytes := make([]byte, 32)
	big.NewInt(auctionID).FillBytes(idBytes)
	data := append([]byte(nil), settleSelector...)
	data = append(data, idBytes...)
	return r.sendRaw(ctx, key, from, to, signer, chainID, data, 150_000)
}

// sendRaw signs and broadcasts an arbitrary calldata tx from the keeper,
// returning the tx hash for receipt confirmation.
func (r *Runner) sendRaw(ctx context.Context, key *cryptoecdsa.PrivateKey, from, to common.Address, signer types.Signer, chainID *big.Int, data []byte, gas uint64) (common.Hash, error) {
	nonce, err := r.eth.PendingNonceAt(ctx, from)
	if err != nil {
		return common.Hash{}, err
	}
	tipCap, err := r.eth.SuggestGasTipCap(ctx)
	if err != nil {
		// RPC failure on tip-cap query → tipCap stays nil and the very next
		// line's `new(big.Int).Add(tipCap, ...)` would panic, taking down
		// the keeper loop for the rest of the process lifetime. Bounce a
		// clean error to the caller; the next tx build will retry.
		return common.Hash{}, fmt.Errorf("suggest gas tip cap: %w", err)
	}
	gasPrice, err := r.eth.SuggestGasPrice(ctx)
	if err != nil {
		return common.Hash{}, err
	}
	// v29 audit F-03: clamp gas pricing to deploy-configured ceilings. A
	// public RPC can spike gas suggestions during congestion; without a
	// cap, a single keeper tx could drain the keeper wallet. Defaults
	// (100 gwei fee / 5 gwei tip via env) leave plenty of headroom on
	// Coston2 while preventing grief. log.Warn + clamp
	// rather than abort: a clamped tx still gets included next block;
	// aborting risks stuck auctions/offers.
	feeCap := new(big.Int).Add(tipCap, new(big.Int).Mul(gasPrice, big.NewInt(2)))
	if cap := r.cfg.MaxFeeCapWei(); cap != nil && feeCap.Cmp(cap) > 0 {
		log.Warn().Str("requested_wei", feeCap.String()).Str("cap_wei", cap.String()).
			Msg("keeper: feeCap above max; clamping")
		feeCap = cap
	}
	if cap := r.cfg.MaxTipCapWei(); cap != nil && tipCap.Cmp(cap) > 0 {
		log.Warn().Str("requested_wei", tipCap.String()).Str("cap_wei", cap.String()).
			Msg("keeper: tipCap above max; clamping")
		tipCap = cap
	}
	// v29 audit reviewer note: EIP-1559 invariant requires feeCap >= tipCap.
	// If MaxFeeCapGwei < MaxTipCapGwei the two clamps above can produce
	// feeCap < tipCap, leaving the tx un-mineable. Lift feeCap to tipCap when
	// the invariant would otherwise break. We log a warning so misconfig is
	// visible in keeper telemetry.
	if feeCap.Cmp(tipCap) < 0 {
		log.Warn().Str("feeCap", feeCap.String()).Str("tipCap", tipCap.String()).
			Msg("keeper: feeCap < tipCap after clamp; lifting feeCap to tipCap")
		feeCap = new(big.Int).Set(tipCap)
	}
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		To:        &to,
		Gas:       gas,
		GasFeeCap: feeCap,
		GasTipCap: tipCap,
		Data:      data,
	})
	signed, err := types.SignTx(tx, signer, key)
	if err != nil {
		return common.Hash{}, err
	}
	if err := r.eth.SendTransaction(ctx, signed); err != nil {
		return common.Hash{}, err
	}
	return signed.Hash(), nil
}

// checkKeeperBalance performs a one-shot balance check at keeper startup.
// Logs a WARN when the keeper wallet is below the configured minimum
// (default 0.1 FLR). A low balance risks tx failures under gas spikes;
// a compromised KEEPER_KEY that drains the wallet is surfaced here on
// every restart. Failing the balance RPC is non-fatal — the keeper starts
// anyway and will retry on the next process restart.
//
// KeeperMinBalanceWei is validated at startup by config.Load() (non-negative
// decimal integer required), so this function trusts the value and skips
// runtime re-validation.
func (r *Runner) checkKeeperBalance(ctx context.Context, keeperAddr common.Address) {
	minStr := r.cfg.KeeperMinBalanceWei
	if minStr == "" || minStr == "0" {
		return // balance check disabled
	}
	// Trust config.Load() validation: SetString is guaranteed to succeed.
	minWei, _ := new(big.Int).SetString(minStr, 10)
	if minWei == nil || minWei.Sign() <= 0 {
		return // defensive: shouldn't happen post-validation, but be safe
	}

	bctx, bcancel := context.WithTimeout(ctx, 5*time.Second)
	defer bcancel()
	// Phase 4 V4.1: use BalanceAt (eth_getBalance) — NOT CallContract
	// (eth_call), which returns empty bytes for EOAs.
	current, err := r.eth.BalanceAt(bctx, keeperAddr, nil)
	if err != nil {
		log.Warn().Err(err).Str("keeper", keeperAddr.Hex()).
			Msg("keeper: balance RPC call failed — cannot verify keeper wallet funding")
		return
	}

	flrVal, _ := new(big.Float).Quo(new(big.Float).SetInt(current), new(big.Float).SetFloat64(1e18)).Float64()
	minVal, _ := new(big.Float).Quo(new(big.Float).SetInt(minWei), new(big.Float).SetFloat64(1e18)).Float64()

	if current.Cmp(minWei) < 0 {
		log.Warn().
			Str("keeper", keeperAddr.Hex()).
			Float64("balance", flrVal).
			Float64("min_required", minVal).
			Str("currency", r.cfg.NativeCurrency).
			Msg("keeper: wallet balance below minimum — top up to avoid settlement failures under gas spikes")
	} else {
		log.Info().
			Str("keeper", keeperAddr.Hex()).
			Float64("balance", flrVal).
			Str("currency", r.cfg.NativeCurrency).
			Msg("keeper: wallet balance OK")
	}
}

// waitMined polls for a successful receipt. Returns an error if the tx
// reverted or was not mined within the timeout — callers treat that as
// "not done" and retry on their next tick (keeper calls are idempotent).
func (r *Runner) waitMined(ctx context.Context, h common.Hash, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		rec, err := r.eth.TransactionReceipt(ctx, h)
		if err == nil && rec != nil {
			if rec.Status == types.ReceiptStatusSuccessful {
				return nil
			}
			return fmt.Errorf("tx %s reverted", h.Hex())
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("tx %s not mined within %s", h.Hex(), timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}
