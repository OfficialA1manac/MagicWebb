// Package indexer provides the chain watcher and background workers.
package indexer

import (
	"context"
	cryptoecdsa "crypto/ecdsa"
	"errors"
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
	"github.com/OfficialA1manac/MagicWebb/backend/internal/imagestore"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/webhook"
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
	// observed dedupes instant-lane ObserveTx calls (see observe.go).
	observed observedSet
	// serverTimeMs is the latest block timestamp in milliseconds (atomic).
	serverTimeMs *int64
	// gasAlertLastFired tracks the last time a gas cost alert webhook was sent.
	// Used to enforce a cooldown so threshold breaches don't spam the webhook.
	gasAlertLastFired time.Time
	// gasAlertWasFiring tracks whether the gas cost was above the threshold on
	// the last tick. When the cost drops below threshold after having been above,
	// a "resolved" notification is sent to inform operators the situation is
	// back to normal.
	gasAlertWasFiring bool

	// headLagBlocks is the difference between the chain head and the last
	// indexed block, updated atomically every watcher tick. Exported via
	// HeadLagBlocks() for SLO monitoring and health check integration.
	// A value > 15 indicates the indexer is falling behind (≈ 30 seconds
	// at 2s block time on Flare/Coston2).
	headLagBlocks int64

	// IMG-3: blob store backend (Postgres BYTEA by default, S3/MinIO when configured).
	// Used by metadata workers for image ingest and thumbnail generation.
	imgStore imagestore.Store

	// KPR-2: cached gas price to avoid repeated SuggestGasPrice RPC calls.
	// Only the leader refreshes this; followers reuse the cached value.
	gasPriceMu     sync.Mutex
	cachedGasPrice *big.Int
	lastGasPriceAt time.Time

	// KPR-3: tracks the last nonce sent per keeper address to avoid
	// re-submitting identical transactions that are already pending.
	// lastNonceAt records WHEN each nonce was broadcast: if the tx is
	// dropped or replaced, PendingNonceAt keeps returning the same nonce
	// forever, so the guard expires after lastNonceTTL to let the keeper
	// re-submit instead of blocking all sends for the process lifetime.
	lastNonceMu sync.Mutex
	lastNonce   map[common.Address]uint64
	lastNonceAt map[common.Address]time.Time
}

// lastNonceTTL bounds how long the KPR-3 duplicate-nonce guard suppresses
// re-submission. Long enough to cover normal mining latency on ~2s-block
// Flare chains (the guard's dedupe purpose), short enough that a dropped
// broadcast only pauses keeper sends briefly instead of permanently.
const lastNonceTTL = 2 * time.Minute

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
		lastNonce:    make(map[common.Address]uint64),
		lastNonceAt:  make(map[common.Address]time.Time),
		imgStore:     q, // default to Postgres BYTEA; override via WithImgStore
	}
}

// WithImgStore sets the blob store backend for image ingest and thumbnail
// generation (IMG-3). Defaults to q (Postgres BYTEA) when not called.
// Pass an *imagestore.S3Store to use S3/MinIO for blob body storage.
func (r *Runner) WithImgStore(s imagestore.Store) *Runner {
	r.imgStore = s
	return r
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

	// Every worker runs under supervise(): a panic is logged and the worker
	// restarts, instead of killing the process that also serves HTTP, SSE, WS
	// and GraphQL. See supervise.go.
	workers := []struct {
		name string
		fn   func(context.Context)
	}{
		{"watcher", r.runWatcher},
		{"score", r.runScoreWorker},
		{"offer-expiry", r.runOfferExpirySweeper},
		{"listing-expiry", r.runListingExpirySweeper},
		{"metadata", r.runMetadataWorker},
		{"image-retry", r.runImageRetryWorker},
		{"ownership-repair", r.runOwnershipRepairWorker},
		{"withdrawal-sweeper", r.runWithdrawalSweeper},
		// Gas alert worker — runs independently of keeper key; just needs DB access.
		{"gas-alert", r.runGasAlertWorker},
	}
	for _, w := range workers {
		wg.Add(1)
		go func() { defer wg.Done(); supervise(ctx, w.name, w.fn) }()
	}

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
					// Keepers must stop on EITHER loss of single-flight
					// ownership or process shutdown. The election's lock
					// context descends from the election's own background
					// context, not from ctx, so a shutdown signal would
					// otherwise never reach them.
					wctx, wcancel := context.WithCancel(kctx)
					stop := make(chan struct{})
					go func() {
						select {
						case <-ctx.Done():
							wcancel()
						case <-stop:
						}
					}()

					keepers := []struct {
						name string
						fn   func(context.Context)
					}{
						{"auction-keeper", r.runAuctionKeeper},
						{"loser-refund-sweeper", r.runLoserRefundSweeper},
						{"offer-refund-sweeper", r.runOfferRefundSweeper},
					}
					// Fee sweep (Zodiac Allowance Module) — only when SAFE_ADDR is configured.
					if r.cfg.SafeAddr != "" && r.cfg.PersonalWalletAddr != "" {
						keepers = append(keepers, struct {
							name string
							fn   func(context.Context)
						}{"fee-sweeper", r.runFeeSweeper})
					}

					var kwg sync.WaitGroup
					for _, k := range keepers {
						kwg.Add(1)
						go func() { defer kwg.Done(); supervise(wctx, k.name, k.fn) }()
					}

					// Wait for every keeper worker to exit before handing the
					// lock back. Releasing first returned immediately while the
					// workers were still running, so this loop spawned a fresh
					// worker set on every iteration: an unbounded pile of 1s
					// tickers all polling Postgres (single-instance), or a
					// re-election storm that killed each keeper microseconds
					// after it started (multi-instance).
					kwg.Wait()
					close(stop)
					wcancel()
					release()
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
//
// The value now comes from the chain profile (internal/chain/profile):
// 3 on Coston2, 2 on Songbird/Flare. This constant is the fallback when a
// Runner is built without config (tests).
const reorgSafetyBlocks = 12

// reorgSafety returns the profile's finality depth for this chain.
func (r *Runner) reorgSafety() uint64 {
	if r.cfg != nil && r.cfg.Profile.ReorgSafety > 0 {
		return r.cfg.Profile.ReorgSafety
	}
	return reorgSafetyBlocks
}

// pollInterval returns the head-poll cadence for this chain.
func (r *Runner) pollInterval() time.Duration {
	if r.cfg != nil && r.cfg.Profile.PollInterval > 0 {
		return r.cfg.Profile.PollInterval
	}
	return 2 * time.Second
}

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
	// head is unsigned: on a chain whose head is still below headLag this
	// subtraction wraps to ~2^64, the check below passes, and the scan loop
	// walks a practically endless block range issuing FilterLogs calls.
	if head < headLag {
		return 0, fmt.Errorf("reindex: chain head %d is below head lag %d", head, headLag)
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

	// ── IDX-2: Per-collection checkpoint recovery ────────────────────────
	// The global indexed_block tracks marketplace events (listings, auctions,
	// bids, sales). Transfer events are tracked per-collection via
	// tracked_collections.last_scanned_block. On restart, we must use the
	// MINIMUM of both so no Transfer events are missed for any collection.
	// A collection that was added after initial deploy will have
	// last_scanned_block=0, so the indexer will backfill from the global
	// fromBlock (which ensures marketplace events for that collection are
	// also indexed).
	if minCheckpoint, err := r.q.GetMinCollectionCheckpoint(ctx); err == nil && minCheckpoint > 0 && minCheckpoint < fromBlock {
		log.Info().
			Uint64("global_cursor", fromBlock).
			Uint64("min_collection_checkpoint", minCheckpoint).
			Msg("watcher: per-collection checkpoint behind global cursor; rewinding to min checkpoint")
		fromBlock = minCheckpoint
	} else if err != nil {
		log.Warn().Err(err).Msg("watcher: failed to read collection checkpoints; using global cursor")
	}

	if r.cfg.IndexFromBlock > fromBlock {
		fromBlock = r.cfg.IndexFromBlock
	}

	// lastBlock is the highest block KNOWN indexed. It only ever advances after
	// a fully successful range — a failed/partial range is retried next tick,
	// so RPC failures can delay events but never permanently drop them.
	lastBlock := fromBlock
	// lastBlockParent tracks the PARENT hash of lastBlock for reorg detection:
	// re-fetching header(lastBlock) later and seeing a different parentHash
	// means the block at that height was replaced — a reorg past our cursor.
	//
	// The parent hash is compared, not the block's own hash, because the
	// node-reported field survives the round-trip while a recomputed hash does
	// not: Flare's C-chain headers carry coreth-specific fields that
	// go-ethereum's types.Header drops on JSON decode, so header.Hash()
	// re-RLP-encodes a subset and NEVER equals the chain's real block hash.
	// Comparing recomputed hashes declared a phantom reorg on every single
	// tick (rewind, re-index, repeat — constant duplicate getLogs load).
	var lastBlockParent common.Hash
	if lastBlock > 0 {
		if h, err := r.eth.HeaderByNumber(ctx, big.NewInt(int64(lastBlock))); err == nil {
			lastBlockParent = h.ParentHash
		}
	}
	backfilled := false

	ticker := time.NewTicker(r.pollInterval())
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
			if head <= r.reorgSafety() {
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
				if lastBlock > 0 && lastBlockParent != (common.Hash{}) {
					// Same-height lineage check: if header(lastBlock) now
					// reports a different parentHash than when we indexed it,
					// the block at our cursor height was replaced.
					lastHeader, err := r.eth.HeaderByNumber(ctx, big.NewInt(int64(lastBlock)))
					if err == nil && lastHeader.ParentHash != lastBlockParent {
						// Reorg detected: the block the indexer thinks is the
						// last indexed block no longer sits on the canonical chain.
						// Rewind by reorgSafetyBlocks to re-index the affected range.
						log.Warn().
							Str("expected_parent", lastBlockParent.Hex()).
							Str("actual_parent", lastHeader.ParentHash.Hex()).
							Uint64("head", head).
							Uint64("rewind_to", lastBlock-r.reorgSafety()).
							Msg("watcher: reorg detected — rewinding indexer")
						if lastBlock > r.reorgSafety() {
							lastBlock -= r.reorgSafety()
						} else {
							lastBlock = 0
						}
						// Reset so continuity is re-established on the next tick.
						lastBlockParent = common.Hash{}
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
			// Verify chain continuity before processing the new range:
			// re-fetch header(lastBlock) and confirm its parentHash still
			// matches the lineage we indexed. (Its own hash cannot be used —
			// see the lastBlockParent comment above.)
			if lastBlock > 0 && lastBlockParent != (common.Hash{}) {
				cur, err := r.eth.HeaderByNumber(ctx, big.NewInt(int64(lastBlock)))
				if err == nil && cur.ParentHash != lastBlockParent {
					log.Warn().
						Str("expected_parent", lastBlockParent.Hex()).
						Str("actual_parent", cur.ParentHash.Hex()).
						Uint64("head", head).
						Uint64("last_block", lastBlock).
						Msg("watcher: chain continuity break — reorg detected before new range; rewinding")
					if lastBlock > r.reorgSafety() {
						lastBlock -= r.reorgSafety()
					} else {
						lastBlock = 0
					}
					lastBlockParent = common.Hash{}
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
			// Cache the last block's parent hash for continuity checking on
			// the next tick.
			if header, err := r.eth.HeaderByNumber(ctx, big.NewInt(int64(target))); err == nil {
				lastBlockParent = header.ParentHash
				atomic.StoreInt64(r.serverTimeMs, int64(header.Time*1000))
			} else {
				// If we can't get the header, reset so the next tick
				// re-establishes continuity conservatively (still processes the
				// range that was already backfilled).
				lastBlockParent = common.Hash{}
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
	var firstDispatchErr error
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
			if errors.Is(err, errMalformedLog) {
				// Permanent: the log's structure can never change, so a
				// retry can never succeed. Log-and-skip instead of
				// aborting, or one malformed log would stall the cursor
				// (and every other event) forever.
				log.Error().Err(err).Str("tx", l.TxHash.Hex()).Msg("watcher: dispatch skipped malformed log")
				continue
			}
			// Retriable (DB/RPC): remember the first failure and abort the
			// range below so SetIndexedBlock does NOT advance the cursor
			// past an unapplied event. The next tick replays the same
			// range; handlers are idempotent upserts, so replay is safe.
			log.Error().Err(err).Str("tx", l.TxHash.Hex()).Msg("watcher: dispatch")
			if firstDispatchErr == nil {
				firstDispatchErr = fmt.Errorf("dispatch tx %s: %w", l.TxHash.Hex(), err)
			}
		}
	}
	if firstDispatchErr != nil {
		return firstDispatchErr
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

// maxTransferWorkers is the concurrency cap for parallel Transfer event
// dispatch (IDX-1). Each collection's Transfer logs are dispatched to a
// separate goroutine, bounded by this semaphore so a deployment with
// hundreds of tracked collections doesn't exhaust DB connections.
const maxTransferWorkers = 4

// processTransfers watches NFT Transfer events on every tracked collection in the
// block range, maintaining ownership and orphaning listings whose seller moved out.
//
// IDX-1: Transfer logs are bucketed by collection and dispatched in parallel
// goroutines via a bounded semaphore. A collection with zero Transfer events
// in this range completes instantly; collections with many events are processed
// concurrently so slow DB writes (ApplyTransfer721/1155 with tx commits) don't
// block fast collections.
//
// IDX-2: After all collection buckets are dispatched successfully, every tracked
// collection's last_scanned_block is advanced to `to` in a single bulk update.
// This makes crash recovery O(1) — on restart, the indexer resumes from the
// minimum checkpoint across all collections rather than re-scanning from the
// deploy block.
//
// Header policy mirrors processRange: per-RPC 2s timeout, on failure abort
// the chunk so the cursor doesn't advance past unindexed events.
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

	// Single FilterLogs for all collections — efficient RPC usage.
	logs, err := r.eth.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: big.NewInt(int64(from)),
		ToBlock:   big.NewInt(int64(to)),
		Addresses: addrs,
		Topics:    transferTopics(),
	})
	if err != nil {
		return fmt.Errorf("transfer logs [%d..%d]: %w", from, to, err)
	}

	// Resolve block timestamps for all transfer logs (same pass as before).
	for _, l := range logs {
		// Existence check only — the cached value is not needed here, just the
		// fact that this block has already been resolved.
		if _, ok := blockTimes[l.BlockNumber]; ok {
			continue
		}
		hctx, hcancel := context.WithTimeout(ctx, 2*time.Second)
		h, herr := r.eth.HeaderByNumber(hctx, big.NewInt(int64(l.BlockNumber)))
		hcancel()
		if herr != nil {
			log.Error().Err(herr).Uint64("block", l.BlockNumber).Str("tx", l.TxHash.Hex()).
				Msg("transfer: header lookup failed; aborting chunk for retry on next tick")
			return fmt.Errorf("transfer: header lookup failed for block %d: %w", l.BlockNumber, herr)
		}
		blockTimes[l.BlockNumber] = h.Time
	}

	// ── IDX-1: Bucket logs by collection for parallel dispatch ────────────
	// Group Transfer logs by their emitting contract address so each
	// collection's ownership updates can be processed concurrently.
	// The semaphore (buffered channel) bounds goroutine count to
	// maxTransferWorkers, preventing DB connection exhaustion.
	type bucket struct {
		addr common.Address
		logs []types.Log
	}
	buckets := make(map[common.Address]*bucket, len(tracked))
	for i := range logs {
		addr := logs[i].Address
		b, ok := buckets[addr]
		if !ok {
			b = &bucket{addr: addr}
			buckets[addr] = b
		}
		b.logs = append(b.logs, logs[i])
	}

	if len(buckets) == 0 {
		// No Transfer events in this range — all collections are still
		// caught up. Advance their checkpoints anyway so a restart
		// doesn't re-scan this empty range.
		if err := r.q.SetCollectionCheckpointsBatch(ctx, to); err != nil {
			log.Warn().Err(err).Uint64("block", to).Msg("transfer: bulk checkpoint update failed (non-fatal)")
		}
		return nil
	}

	// Parallel dispatch with bounded concurrency.
	sem := make(chan struct{}, maxTransferWorkers)
	var wg sync.WaitGroup
	var dispatchErr atomic.Value // first non-nil error

	for _, b := range buckets {
		wg.Add(1)
		go func(b *bucket) {
			defer wg.Done()

			// Fast-fail BEFORE acquiring the semaphore: if another
			// goroutine already errored, don't waste a worker slot.
			if dispatchErr.Load() != nil {
				return
			}

			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release

			for _, l := range b.logs {
				if dispatchErr.Load() != nil {
					return
				}
				if err := r.h.dispatch(ctx, l, blockTimes[l.BlockNumber]); err != nil {
					if errors.Is(err, errMalformedLog) {
						// Permanent decode failure: retrying the chunk can
						// never fix an immutable on-chain log. Skip it so
						// one malformed log does not pin lastBlock and
						// stall every collection forever.
						log.Error().Err(err).Str("tx", l.TxHash.Hex()).Str("collection", b.addr.Hex()).
							Msg("watcher: transfer dispatch skipped malformed log")
						continue
					}
					dispatchErr.Store(err)
					log.Error().Err(err).Str("tx", l.TxHash.Hex()).Str("collection", b.addr.Hex()).
						Msg("watcher: transfer dispatch failed")
					return
				}
			}
		}(b)
	}
	wg.Wait()

	if firstErr := dispatchErr.Load(); firstErr != nil {
		return fmt.Errorf("transfer dispatch: %w", firstErr.(error))
	}

	// ── IDX-2: Persist per-collection checkpoints ────────────────────────
	// All collections are now caught up to block `to`. Bulk-update so
	// crash recovery (restart) uses the minimum checkpoint across all
	// tracked collections instead of re-scanning from deploy block.
	if err := r.q.SetCollectionCheckpointsBatch(ctx, to); err != nil {
		// Non-fatal: the in-memory state is correct; the DB checkpoint
		// will be retried on the next successful range.
		log.Warn().Err(err).Uint64("block", to).
			Msg("transfer: bulk checkpoint update failed (non-fatal)")
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

// ── Listing Expiry Sweeper ─────────────────────────────────────────────────

// runListingExpirySweeper checks for expired listings every 5 minutes and
// sends notifications to sellers whose listings have expired without being
// sold. The seller must re-list the NFT if they still want to sell — the
// listing is deactivated automatically per the requirement: "when nfts expire
// the seller is notified and if nobody has purchased the nft then it is
// removed from listing and the seller must relist the nft on the marketplace."
func (r *Runner) runListingExpirySweeper(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			expired, err := r.q.ListExpiredListings(ctx, 50)
			if err != nil {
				log.Error().Err(err).Msg("listing sweeper: list expired failed")
				continue
			}
			for _, l := range expired {
				// Deactivate the listing first so it's no longer visible to buyers.
				if err := r.q.DeactivateExpiredListing(ctx, l.Collection, l.TokenID, l.Seller); err != nil {
					log.Warn().Err(err).
						Str("collection", l.Collection).Str("token", l.TokenID).
						Msg("listing sweeper: deactivate failed")
					continue
				}
				// Notify the seller that their listing expired.
				title := l.Name
				if title == "" {
					// An empty or truncated collection value would panic this
					// worker; supervise restarts it, but the tick is lost and
					// the same row panics again on every retry.
					short := l.Collection
					if len(short) > 10 {
						short = short[:10]
					}
					title = fmt.Sprintf("%s #%s", short, l.TokenID)
				}
				r.h.notify(ctx, l.Seller, "listing_expired",
					"Your listing expired — "+title+" was not purchased",
					"Re-list it on the marketplace",
					"/token/"+l.Collection+"/"+l.TokenID+"?action=list")
				log.Info().
					Str("collection", l.Collection).Str("token", l.TokenID).
					Str("seller", l.Seller).
					Msg("listing sweeper: expired listing deactivated, seller notified")
			}
			if len(expired) > 0 {
				log.Info().Int("expired", len(expired)).Msg("listing sweeper: tick complete")
			}
		}
	}
}

// ── Offer Expiry Sweeper ──────────────────────────────────────────────────

func (r *Runner) runOfferExpirySweeper(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
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
	// Each tick makes ONE eth_call per pending row (up to 200), so a 1-second
	// interval meant up to 200 RPC round trips per second against a single
	// endpoint — enough for a public RPC to rate-limit us, and slow enough
	// that a tick could not finish inside its own period, so ticks queued up
	// behind each other. This sweeper is the notification half of the refund
	// story, so it rides the refund cadence rather than introducing another
	// knob. The batch stays at 200 so a backlog still drains quickly.
	ticker := time.NewTicker(r.tick(r.cfg.Profile.RefundTick, 30*time.Second))
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

// ── Auction Keeper (on-chain settlement + expired-listing cleanup) ────────
// Settlement authority (v3.2 — matches AuctionHouse.settle exactly):
//   1. the single KEEPER — settles immediately after endsAt (this 1s ticker);
//   2. the auction's seller or winner — any time after endsAt.
// Nobody else can ever settle; there is no permissionless or time-widened
// tier. (An earlier version of this comment described a 3-tier gate with a
// 25-hour permissionless fallback — that never matched the contract.)
// If no MarketplaceManager is deployed (manager==address(0)), settlement
// is fully permissionless immediately. Funds are never trapped: losers
// self-serve via withdrawLoserFunds/withdrawRefund, and forceCancel
// (keeper/seller/winner, endsAt + 3d) rescues a stuck auction.
//
// The same loop also drives Marketplace.cleanExpired on a slower cadence:
// expired listings are keeper-cleaned on-chain (the owner's requirement that
// the keeper "handles everything that expires"). cleanExpired had NO backend
// caller before v3.2 — expired listings simply accumulated on-chain.

var settleSelector = crypto.Keccak256([]byte("settle(uint256)"))[:4]

// cleanExpired(address coll, uint256 id, address seller) — keeper-gated
// removal of an expired listing (Marketplace.sol).
var cleanExpiredSelector = crypto.Keccak256([]byte("cleanExpired(address,uint256,address)"))[:4]

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
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	// Expired-listing cleanup runs on a slower cadence than settlement (each
	// clean is a paid tx and listings carry no escrow urgency).
	cleanTick := 0
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
				// Confirm the settle tx was mined. The keeper settles first,
				// but the seller or auction winner can also settle after
				// endsAt + 5 minutes, and anyone after endsAt + 25 hours —
				// another address could settle first, and the keeper's call
				// would revert harmlessly with NotActive on an
				// already-settled auction.
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

			// Expired-listing sweep, every 2 ticks (~2s — owner decision
			// 2026-08-31: everything that expires is handled instantly).
			// cleanExpired is keeper-gated on-chain; a listing already cleaned
			// (or cancelled by its seller) reverts harmlessly — the DB row is
			// marked chain_cleaned by the Cancelled event either way, and the
			// query only returns rows with real on-chain work.
			cleanTick++
			if cleanTick >= 2 {
				cleanTick = 0
				r.cleanExpiredListings(ctx, key, keeperAddr, signer, chainIDBig)
			}
		}
	}
}

// cleanExpiredListings removes expired-but-still-active listings on-chain via
// Marketplace.cleanExpired. Bounded per pass; failures are logged and retried
// on the next pass.
func (r *Runner) cleanExpiredListings(ctx context.Context, key *cryptoecdsa.PrivateKey, keeperAddr common.Address, signer types.Signer, chainIDBig *big.Int) {
	marketplaceAddr := common.HexToAddress(r.cfg.MarketplaceAddr)
	if marketplaceAddr == (common.Address{}) {
		return // read-only network: no contracts
	}
	expired, err := r.q.GetExpiredActiveListings(ctx)
	if err != nil {
		log.Error().Err(err).Msg("keeper: get expired listings")
		return
	}
	for _, l := range expired {
		tokenID, ok := new(big.Int).SetString(l.TokenID, 10)
		if !ok {
			log.Error().Str("tokenId", l.TokenID).Msg("keeper: bad token id in expired listing")
			continue
		}
		txHash, err := r.sendCleanExpired(ctx, key, keeperAddr, marketplaceAddr, signer, chainIDBig,
			common.HexToAddress(l.Collection), tokenID, common.HexToAddress(l.Seller))
		if err != nil {
			log.Error().Err(err).Str("collection", l.Collection).Str("tokenId", l.TokenID).
				Msg("keeper: cleanExpired tx failed")
			continue
		}
		if err := r.waitMined(ctx, txHash, 2*time.Minute); err != nil {
			log.Error().Err(err).Str("collection", l.Collection).Str("tokenId", l.TokenID).Str("tx", txHash.Hex()).
				Msg("keeper: cleanExpired receipt not confirmed; will retry next pass")
			continue
		}
		// Confirmed on-chain: mark immediately rather than waiting for the
		// Cancelled event to index, so the next pass never re-sends.
		if err := r.q.MarkListingChainCleaned(ctx, l.Collection, l.TokenID, l.Seller); err != nil {
			log.Warn().Err(err).Str("collection", l.Collection).Str("tokenId", l.TokenID).
				Msg("keeper: mark chain_cleaned failed (Cancelled event will cover it)")
		}
		log.Info().Str("collection", l.Collection).Str("tokenId", l.TokenID).Str("tx", txHash.Hex()).
			Msg("keeper: cleanExpired confirmed")
	}
}

func (r *Runner) sendCleanExpired(ctx context.Context, key *cryptoecdsa.PrivateKey, from, to common.Address, signer types.Signer, chainID *big.Int, coll common.Address, tokenID *big.Int, seller common.Address) (common.Hash, error) {
	idBytes := make([]byte, 32)
	tokenID.FillBytes(idBytes)
	data := append([]byte(nil), cleanExpiredSelector...)
	data = append(data, common.LeftPadBytes(coll.Bytes(), 32)...)
	data = append(data, idBytes...)
	data = append(data, common.LeftPadBytes(seller.Bytes(), 32)...)
	return r.sendRaw(ctx, key, from, to, signer, chainID, data, 120_000)
}

func (r *Runner) sendSettle(ctx context.Context, key *cryptoecdsa.PrivateKey, from, to common.Address, signer types.Signer, chainID *big.Int, auctionID int64) (common.Hash, error) {
	idBytes := make([]byte, 32)
	big.NewInt(auctionID).FillBytes(idBytes)
	data := append([]byte(nil), settleSelector...)
	data = append(data, idBytes...)
	return r.sendRaw(ctx, key, from, to, signer, chainID, data, 150_000)
}

// getGasPrice returns the current gas price, caching the result for 30 seconds
// (KPR-2). Only the leader refreshes via RPC; followers and re-submissions
// within the cache window reuse the cached value, reducing RPC load.
func (r *Runner) getGasPrice(ctx context.Context) (*big.Int, error) {
	r.gasPriceMu.Lock()
	defer r.gasPriceMu.Unlock()

	if r.cachedGasPrice != nil && time.Since(r.lastGasPriceAt) < 30*time.Second {
		return new(big.Int).Set(r.cachedGasPrice), nil
	}

	price, err := r.eth.SuggestGasPrice(ctx)
	if err != nil {
		return nil, err
	}
	r.cachedGasPrice = new(big.Int).Set(price)
	r.lastGasPriceAt = time.Now()
	return price, nil
}

// sendRaw signs and broadcasts an arbitrary calldata tx from the keeper,
// returning the tx hash for receipt confirmation.
func (r *Runner) sendRaw(ctx context.Context, key *cryptoecdsa.PrivateKey, from, to common.Address, signer types.Signer, chainID *big.Int, data []byte, gas uint64) (common.Hash, error) {
	nonce, err := r.eth.PendingNonceAt(ctx, from)
	if err != nil {
		return common.Hash{}, err
	}

	// KPR-3: skip re-submission if we already broadcast a tx with this nonce.
	// When a previous sendRaw successfully sent (returned tx hash) but the
	// caller hasn't seen it mined yet, re-submitting wastes RPC calls and
	// produces "already known" noise.
	r.lastNonceMu.Lock()
	last, seen := r.lastNonce[from]
	sentAt := r.lastNonceAt[from]
	r.lastNonceMu.Unlock()
	if seen && nonce == last && time.Since(sentAt) < lastNonceTTL {
		return common.Hash{}, fmt.Errorf("keeper: tx with nonce %d already sent; skipping re-submission", nonce)
	}

	tipCap, err := r.eth.SuggestGasTipCap(ctx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("suggest gas tip cap: %w", err)
	}
	gasPrice, err := r.getGasPrice(ctx)
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

	// KPR-3: track nonce AFTER successful broadcast so future calls skip
	// re-submitting the same tx. Recorded post-SendTransaction to avoid
	// blocking retries when SendTransaction fails (network blip, mempool
	// full, gas too low — all retriable).
	r.lastNonceMu.Lock()
	r.lastNonce[from] = nonce
	r.lastNonceAt[from] = time.Now()
	r.lastNonceMu.Unlock()

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

// ── Expired Offer Refund Sweeper (keeper on-chain refund) ──────────────

var refundExpiredOfferSelector = crypto.Keccak256([]byte("refundExpiredOffer(address,uint256,address)"))[:4]

// encodeRefundExpiredOffer ABI-encodes refundExpiredOffer(address,uint256,address):
// selector ‖ coll(32) ‖ tokenId(32) ‖ bidder(32)
func encodeRefundExpiredOffer(coll common.Address, tokenID *big.Int, bidder common.Address) []byte {
	out := append([]byte(nil), refundExpiredOfferSelector...)
	out = append(out, common.LeftPadBytes(coll.Bytes(), 32)...)
	tidWord := make([]byte, 32)
	tokenID.FillBytes(tidWord)
	out = append(out, tidWord...)
	out = append(out, common.LeftPadBytes(bidder.Bytes(), 32)...)
	return out
}

// runOfferRefundSweeper checks for expired offers in the DB and calls
// refundExpiredOffer() on-chain to return escrow to the bidder. This makes
// expired offers auto-refundable without user interaction — the keeper
// handles everything. Idempotent: calling refundExpiredOffer on an already
// refunded offer (position already deleted) simply reverts on-chain and is
// skipped on retry.
func (r *Runner) runOfferRefundSweeper(ctx context.Context) {
	// Normalize like runAuctionKeeper: config.Load() keeps any 0x prefix,
	// and crypto.HexToECDSA rejects it — without the trim a 0x-prefixed
	// KEEPER_KEY silently disables only this sweeper.
	key, err := crypto.HexToECDSA(strings.TrimPrefix(r.cfg.KeeperKey, "0x"))
	if err != nil {
		log.Error().Err(err).Msg("offer refund sweeper: invalid KEEPER_KEY, disabled")
		return
	}
	keeperAddr := crypto.PubkeyToAddress(key.PublicKey)
	offerBookAddr := common.HexToAddress(r.cfg.OfferBookAddr)
	chainIDBig := big.NewInt(int64(r.cfg.ChainID))
	signer := types.NewLondonSigner(chainIDBig)

	log.Info().Str("keeper", keeperAddr.Hex()).Msg("offer refund sweeper: started")
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			offers, err := r.q.ListExpiredPendingOffers(ctx, 50)
			if err != nil {
				log.Error().Err(err).Msg("offer refund sweeper: list expired offers failed")
				continue
			}
			for _, o := range offers {
				coll := common.HexToAddress(o.Collection)
				bidder := common.HexToAddress(o.Bidder)
				tid, ok := new(big.Int).SetString(o.TokenID, 10)
				if !ok {
					log.Warn().Str("collection", o.Collection).Str("tokenId", o.TokenID).
						Msg("offer refund sweeper: invalid token ID, skipping")
					continue
				}
				data := encodeRefundExpiredOffer(coll, tid, bidder)
				h, err := r.sendRaw(ctx, key, keeperAddr, offerBookAddr, signer, chainIDBig, data, 100_000)
				if err != nil {
					// Expect revert on already-refunded offers — that's fine.
					log.Warn().Err(err).
						Str("collection", o.Collection).Str("tokenId", o.TokenID).
						Str("bidder", o.Bidder).
						Msg("offer refund sweeper: refundExpiredOffer tx failed; may already be refunded")
					continue
				}
				// Don't block on receipt for offers — the tx may revert on-chain
				// if already refunded (idempotent), which waitMined reports as an
				// error. Just log and move on; the OfferRefunded event handler
				// updates the DB status.
				log.Info().
					Str("collection", o.Collection).Str("tokenId", o.TokenID).
					Str("bidder", o.Bidder).Str("tx", h.Hex()).
					Msg("offer refund sweeper: refundExpiredOffer tx sent")
			}
			if len(offers) > 0 {
				log.Info().Int("refunded", len(offers)).Msg("offer refund sweeper: tick complete")
			}
		}
	}
}

// ── Keeper Address Helper ──────────────────────────────────────────────────

// keeperAddress derives the keeper wallet address from the configured KEEPER_KEY.
// Returns the address as a hex string (0x-prefixed) on success, or an empty string
// and an error if the key is empty, too short, or not valid hex.
func (r *Runner) keeperAddress() (string, error) {
	if r.cfg.KeeperKey == "" {
		return "", fmt.Errorf("keeper key is empty")
	}
	keeperKeyHex := strings.TrimPrefix(r.cfg.KeeperKey, "0x")
	key, err := crypto.HexToECDSA(keeperKeyHex)
	if err != nil {
		return "", fmt.Errorf("keeper key parse failed: %w", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)
	return addr.Hex(), nil
}

// ── Keeper Gas Logger ──────────────────────────────────────────────────────

// logKeeperGas records a keeper transaction's gas consumption in the
// keeper_gas_logs table for cost tracking and monitoring. If EffectiveGasPrice
// is nil (should never happen on a mined tx, but defensive), logs a warning
// and skips the insert. If the keeper key is invalid, logs a warning and
// skips the insert. Insert errors are logged but not returned (non-fatal —
// the keeper's primary duty is settlement, not accounting).
func (r *Runner) logKeeperGas(ctx context.Context, txHash common.Hash, action string, rec *types.Receipt) {
	// Derive keeper address.
	addr, err := r.keeperAddress()
	if err != nil {
		log.Warn().Err(err).Str("action", action).Str("tx", txHash.Hex()).
			Msg("gas log: key parse failed")
		return
	}

	// Defensive check: EffectiveGasPrice should always be present on a mined
	// EIP-1559 tx, but a nil value would panic on BigInt math below.
	if rec.EffectiveGasPrice == nil {
		log.Warn().Str("keeper", addr).Str("action", action).Str("tx", txHash.Hex()).
			Msg("keeper gas log: EffectiveGasPrice is nil, skipping")
		return
	}

	gasUsed := int64(rec.GasUsed)
	gasPriceWei := rec.EffectiveGasPrice.String()
	gasCost := new(big.Int).Mul(rec.EffectiveGasPrice, new(big.Int).SetUint64(rec.GasUsed))

	if err := r.q.InsertGasLog(ctx, addr, action, txHash.Hex(), gasUsed, gasPriceWei, gasCost.String(), int64(r.cfg.ChainID)); err != nil {
		log.Warn().Err(err).Str("keeper", addr).Str("action", action).Str("tx", txHash.Hex()).
			Msg("gas log insert failed")
	}

	// Publish SSE event so the gas metrics dashboard refreshes in real time.
	// Safe when bcast is nil (test runners don't wire a Broadcaster).
	if r.bcast != nil {
		r.bcast.Publish(sse.Event{
			Type: "keeper-gas-log",
			Data: map[string]any{
				"addr":    addr,
				"action":  action,
				"tx_hash": txHash.Hex(),
				"cost":    gasCost.String(),
			},
		})
	}
}

// ── Gas Cost Alert Worker ────────────────────────────────────────────────────

// gasAlertCooldown is the minimum interval between consecutive webhook alerts
// for the same threshold breach. Prevents spam when the 24h cost stays above
// the threshold across multiple worker ticks.
const gasAlertCooldown = 1 * time.Hour

// runGasAlertWorker periodically checks aggregate keeper gas costs against the
// configured threshold and fires webhooks (Discord, Prometheus Alertmanager,
// or both) when the threshold is breached. At least one webhook URL must be
// configured; if none are set, the worker returns early. Only fires once per
// gasAlertCooldown period to prevent spam.
func (r *Runner) runGasAlertWorker(ctx context.Context) {
	discordURL := r.cfg.DiscordWebhookURL
	promURL := r.cfg.PrometheusWebhookURL
	thresholdStr := r.cfg.GasAlertThresholdWei
	hasAnyURL := discordURL != "" || promURL != ""
	// SMTP email is an additional channel, not a replacement for webhooks.
	// When no webhook URL is set AND no SMTP config is set, skip the worker entirely.
	smtpHost := r.cfg.SMTPHost
	smtpPort := r.cfg.SMTPPort
	smtpUser := r.cfg.SMTPUser
	smtpPass := r.cfg.SMTPPass
	emailFrom := r.cfg.EmailFrom
	emailTo := r.cfg.EmailTo
	hasSMTP := smtpHost != "" && smtpUser != "" && smtpPass != "" && emailFrom != "" && emailTo != ""
	if !hasAnyURL && !hasSMTP || thresholdStr == "" || thresholdStr == "0" {
		return
	}
	threshold, ok := new(big.Int).SetString(thresholdStr, 10)
	if !ok || threshold.Sign() <= 0 {
		log.Warn().Str("threshold", thresholdStr).
			Msg("gas alert: invalid GAS_ALERT_THRESHOLD_WEI, alerts disabled")
		return
	}
	currency := r.cfg.NativeCurrency

	log.Info().Str("threshold_wei", thresholdStr).Str("currency", currency).
		Msg("gas alert worker: started")

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			costStr, err := r.q.GetGasCostSince(ctx, 24*time.Hour)
			if err != nil {
				log.Warn().Err(err).Msg("gas alert: GetGasCostSince failed")
				continue
			}
			cost, ok := new(big.Int).SetString(costStr, 10)
			if !ok || cost.Sign() < 0 {
				continue
			}

			// Compute FLR values early so they're available in both the
			// below-threshold (resolved) and above-threshold (alert) branches.
			costFLR, _ := new(big.Float).Quo(
				new(big.Float).SetInt(cost),
				new(big.Float).SetFloat64(1e18),
			).Float64()
			thresholdFLR, _ := new(big.Float).Quo(
				new(big.Float).SetInt(threshold),
				new(big.Float).SetFloat64(1e18),
			).Float64()

			if cost.Cmp(threshold) < 0 {
				// Below threshold. Reset cooldown so the next breach fires immediately.
				r.gasAlertLastFired = time.Time{}

				// If we were previously firing, send a resolved notification.
				if r.gasAlertWasFiring {
					r.gasAlertWasFiring = false
					r.fireGasAlertResolved(ctx, costStr, thresholdStr, costFLR, thresholdFLR, currency,
						discordURL, promURL, hasSMTP, smtpHost, smtpPort, smtpUser, smtpPass, emailFrom, emailTo)
				}
				continue
			}

			// Threshold breached. Check cooldown.
			if !r.gasAlertLastFired.IsZero() && time.Since(r.gasAlertLastFired) < gasAlertCooldown {
				continue
			}

			// Fire webhook(s).
			title := fmt.Sprintf("Keeper gas cost alert: %.4f %s in last 24h", costFLR, currency)
			desc := fmt.Sprintf(
				"Total keeper gas cost over the last 24 hours (%.4f %s) has exceeded the configured threshold (%.4f %s).\n\nThreshold: %s wei (%.4f %s)\nCurrent:   %s wei (%.4f %s)\n\nReview the gas metrics dashboard for a detailed breakdown of keeper costs by transaction type.",
				costFLR, currency, thresholdFLR, currency,
				thresholdStr, thresholdFLR, currency,
				costStr, costFLR, currency,
			)

			whCtx, whCancel := context.WithTimeout(ctx, 20*time.Second)

			// Fire webhooks and capture results for persistence.
			var discordErr, promErr, emailErr error
			if discordURL != "" {
				if err := webhook.SendDiscordAlert(whCtx, discordURL, title, desc); err != nil {
					log.Error().Err(err).Msg("gas alert: Discord webhook send failed")
					discordErr = err
				}
			}
			if promURL != "" {
				if err := webhook.SendPrometheusAlert(whCtx, promURL, "KeeperGasCostHigh", desc, "warning"); err != nil {
					log.Error().Err(err).Msg("gas alert: Prometheus webhook send failed")
					promErr = err
				}
			}
			// Send email alert via SMTP when configured.
			if hasSMTP {
				subject := fmt.Sprintf("🚨 %s", title)
				htmlBody := webhook.BuildAlertEmailBody(
					title, desc,
					thresholdStr, costStr,
					fmt.Sprintf("%.4f", thresholdFLR), fmt.Sprintf("%.4f", costFLR), currency,
				)
				if err := webhook.SendEmail(whCtx, smtpHost, smtpPort, smtpUser, smtpPass, emailFrom, emailTo, subject, htmlBody); err != nil {
					log.Error().Err(err).Msg("gas alert: SMTP email send failed")
					emailErr = err
				}
			}
			whCancel()

			// Persist alert record to DB.
			discordErrStr, promErrStr, emailErrStr := "", "", ""
			if discordErr != nil {
				discordErrStr = discordErr.Error()
			}
			if promErr != nil {
				promErrStr = promErr.Error()
			}
			if emailErr != nil {
				emailErrStr = emailErr.Error()
			}
			if _, err := r.q.InsertGasAlert(ctx, db.GasAlertRow{
				TotalCostWei:    costStr,
				ThresholdWei:    thresholdStr,
				CostFLR:         costFLR,
				ThresholdFLR:    thresholdFLR,
				Currency:        currency,
				DiscordSent:     discordURL != "" && discordErr == nil,
				PrometheusSent:  promURL != "" && promErr == nil,
				EmailSent:       hasSMTP && emailErr == nil,
				DiscordError:    discordErrStr,
				PrometheusError: promErrStr,
				EmailError:      emailErrStr,
			}); err != nil {
				log.Warn().Err(err).Msg("gas alert: InsertGasAlert failed")
			}

			r.gasAlertLastFired = time.Now()
			r.gasAlertWasFiring = true
			log.Warn().Str("cost_wei", costStr).Str("threshold_wei", thresholdStr).
				Str("discord", mapBool(discordURL != "" && discordErr == nil)).
				Str("prometheus", mapBool(promURL != "" && promErr == nil)).
				Str("email", mapBool(hasSMTP && emailErr == nil)).
				Msg("gas alert: threshold breached, alert(s) sent")
		}
	}
}

// fireGasAlertResolved sends resolved notifications across all configured channels
// when the gas cost drops back below the threshold after having been in alert state.
func (r *Runner) fireGasAlertResolved(ctx context.Context, costStr, thresholdStr string, costFLR, thresholdFLR float64, currency string,
	discordURL, promURL string, hasSMTP bool, smtpHost string, smtpPort int, smtpUser, smtpPass, emailFrom, emailTo string) {

	title := fmt.Sprintf("Keeper gas cost resolved: %.4f %s in last 24h", costFLR, currency)
	desc := fmt.Sprintf(
		"Keeper gas cost has dropped back below the configured threshold.\n\nThreshold: %s wei (%.4f %s)\nCurrent:   %s wei (%.4f %s)\n\nNo action needed — costs are back to normal levels.",
		thresholdStr, thresholdFLR, currency,
		costStr, costFLR, currency,
	)

	whCtx, whCancel := context.WithTimeout(ctx, 20*time.Second)
	defer whCancel()

	var discordErr, promErr, emailErr error

	if discordURL != "" {
		if err := webhook.SendDiscordResolvedAlert(whCtx, discordURL, title, desc); err != nil {
			log.Error().Err(err).Msg("gas alert resolved: Discord webhook send failed")
			discordErr = err
		}
	}
	if promURL != "" {
		if err := webhook.SendPrometheusResolvedAlert(whCtx, promURL, "KeeperGasCostHigh", desc); err != nil {
			log.Error().Err(err).Msg("gas alert resolved: Prometheus webhook send failed")
			promErr = err
		}
	}
	if hasSMTP {
		subject := fmt.Sprintf("✅ Keeper gas cost resolved: %.4f %s", costFLR, currency)
		htmlBody := webhook.BuildResolvedAlertEmailBody(
			title, desc,
			thresholdStr, costStr,
			fmt.Sprintf("%.4f", thresholdFLR), fmt.Sprintf("%.4f", costFLR), currency,
		)
		if err := webhook.SendEmail(whCtx, smtpHost, smtpPort, smtpUser, smtpPass, emailFrom, emailTo, subject, htmlBody); err != nil {
			log.Error().Err(err).Msg("gas alert resolved: SMTP email send failed")
			emailErr = err
		}
	}

	// Persist resolved alert record to DB.
	discordErrStr, promErrStr, emailErrStr := "", "", ""
	if discordErr != nil {
		discordErrStr = discordErr.Error()
	}
	if promErr != nil {
		promErrStr = promErr.Error()
	}
	if emailErr != nil {
		emailErrStr = emailErr.Error()
	}
	if _, err := r.q.InsertGasAlert(ctx, db.GasAlertRow{
		TotalCostWei:    costStr,
		ThresholdWei:    thresholdStr,
		CostFLR:         costFLR,
		ThresholdFLR:    thresholdFLR,
		Currency:        currency,
		DiscordSent:     discordURL != "" && discordErr == nil,
		PrometheusSent:  promURL != "" && promErr == nil,
		EmailSent:       hasSMTP && emailErr == nil,
		DiscordError:    discordErrStr,
		PrometheusError: promErrStr,
		EmailError:      emailErrStr,
	}); err != nil {
		log.Warn().Err(err).Msg("gas alert resolved: InsertGasAlert failed")
	}

	log.Warn().Str("cost_wei", costStr).Str("threshold_wei", thresholdStr).
		Str("discord", mapBool(discordURL != "" && discordErr == nil)).
		Str("prometheus", mapBool(promURL != "" && promErr == nil)).
		Str("email", mapBool(hasSMTP && emailErr == nil)).
		Msg("gas alert: threshold recovered, resolved notification(s) sent")
}

// mapBool returns "yes" or "no" for structured log fields.
func mapBool(v bool) string {
	if v {
		return "yes"
	}
	return "no"
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

// tick returns the profile cadence when set, else the historical default.
// Keeps test Runners (built with a nil/zero config) on their old timings.
func (r *Runner) tick(fromProfile, fallback time.Duration) time.Duration {
	if r.cfg != nil && fromProfile > 0 {
		return fromProfile
	}
	return fallback
}
