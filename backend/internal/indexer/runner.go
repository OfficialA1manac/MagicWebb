// Package indexer provides the chain watcher and background workers.
package indexer

import (
	"context"
	cryptoecdsa "crypto/ecdsa"
	"math"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// Runner orchestrates all indexer workers.
type Runner struct {
	cfg   *config.Config
	q     *db.Q
	bcast *sse.Broadcaster
	eth   *ethclient.Client
	h     *handlers
	// serverTimeMs is the latest block timestamp in milliseconds (atomic).
	serverTimeMs *int64
}

// New creates a Runner with all dependencies injected.
func New(cfg *config.Config, q *db.Q, bcast *sse.Broadcaster, eth *ethclient.Client, serverTimeMs *int64) *Runner {
	return &Runner{
		cfg:          cfg,
		q:            q,
		bcast:        bcast,
		eth:          eth,
		h:            &handlers{q: q, bcast: bcast},
		serverTimeMs: serverTimeMs,
	}
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

	if r.cfg.KeeperKey != "" {
		wg.Add(1)
		go func() { defer wg.Done(); r.runAuctionKeeper(ctx) }()
	}

	wg.Wait()
}

// ── Chain Watcher ─────────────────────────────────────────────────────────

func (r *Runner) runWatcher(ctx context.Context) {
	chainID := int(r.cfg.ChainID)
	contracts := []common.Address{
		common.HexToAddress(r.cfg.MarketplaceAddr),
		common.HexToAddress(r.cfg.AuctionAddr),
		common.HexToAddress(r.cfg.OfferBookAddr),
	}
	topics := allTopics()

	fromBlock, err := r.q.GetIndexedBlock(ctx, chainID)
	if err != nil {
		log.Error().Err(err).Msg("watcher: get indexed block")
	}
	if r.cfg.IndexFromBlock > fromBlock {
		fromBlock = r.cfg.IndexFromBlock
	}

	head, err := r.eth.BlockNumber(ctx)
	if err != nil {
		log.Error().Err(err).Msg("watcher: initial block number")
	} else if fromBlock < head {
		log.Info().Uint64("from", fromBlock).Uint64("to", head).Msg("watcher: backfill start")
		r.backfill(ctx, fromBlock, head, contracts, topics, chainID)
		log.Info().Msg("watcher: backfill complete")
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	lastBlock := head
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			newHead, err := r.eth.BlockNumber(ctx)
			if err != nil {
				log.Warn().Err(err).Msg("watcher: block poll failed")
				continue
			}
			if newHead <= lastBlock {
				continue
			}
			r.processRange(ctx, lastBlock+1, newHead, contracts, topics, chainID)
			lastBlock = newHead
			if header, err := r.eth.HeaderByNumber(ctx, big.NewInt(int64(newHead))); err == nil {
				atomic.StoreInt64(r.serverTimeMs, int64(header.Time*1000))
			}
		}
	}
}

func (r *Runner) backfill(ctx context.Context, from, to uint64, contracts []common.Address, topics [][]common.Hash, chainID int) {
	chunk := r.cfg.GetLogsChunk
	if chunk == 0 {
		chunk = 30
	}
	for start := from; start <= to; start += chunk {
		end := start + chunk - 1
		if end > to {
			end = to
		}
		r.processRange(ctx, start, end, contracts, topics, chainID)
		if ctx.Err() != nil {
			return
		}
	}
}

func (r *Runner) processRange(ctx context.Context, from, to uint64, contracts []common.Address, topics [][]common.Hash, chainID int) {
	logs, err := r.eth.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: big.NewInt(int64(from)),
		ToBlock:   big.NewInt(int64(to)),
		Addresses: contracts,
		Topics:    topics,
	})
	if err != nil {
		log.Error().Err(err).Uint64("from", from).Uint64("to", to).Msg("watcher: filter logs")
		return
	}

	blockTimes := make(map[uint64]uint64)
	for _, l := range logs {
		if _, ok := blockTimes[l.BlockNumber]; !ok {
			if h, err := r.eth.HeaderByNumber(ctx, big.NewInt(int64(l.BlockNumber))); err == nil {
				blockTimes[l.BlockNumber] = h.Time
			} else {
				blockTimes[l.BlockNumber] = uint64(time.Now().Unix())
			}
		}
		if err := r.h.dispatch(ctx, l, blockTimes[l.BlockNumber]); err != nil {
			log.Error().Err(err).Str("tx", l.TxHash.Hex()).Msg("watcher: dispatch")
		}
	}
	if err := r.q.SetIndexedBlock(ctx, chainID, to); err != nil {
		log.Error().Err(err).Uint64("block", to).Msg("watcher: set indexed block")
	}
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
	cols, err := r.q.ListCollections(ctx, 500)
	if err != nil {
		log.Error().Err(err).Msg("score worker: list collections")
		return
	}
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
	for _, col := range cols {
		for _, win := range windows {
			since := time.Now().Add(-win.since)
			views, _ := r.q.GetCollectionViews(ctx, col.Address)
			bids, _ := r.q.GetCollectionBidCount(ctx, col.Address, since)
			volWei, _ := r.q.GetCollectionVolume(ctx, col.Address, since)
			score := computeScore(uint64(views), uint64(bids), weiToEth(volWei), win.since.Hours(), w)
			ts := db.TrendingScore{
				Collection: col.Address, Window: win.name,
				Score: score, Views: views, Bids: bids, VolumeWei: volWei,
			}
			if err := r.q.UpsertTrendingScore(ctx, ts); err != nil {
				log.Warn().Err(err).Str("coll", col.Address).Msg("score worker: upsert")
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

// ── Auction Keeper (on-chain settlement) ──────────────────────────────────

var settleSelector = crypto.Keccak256([]byte("settle(uint256)"))[:4]

func (r *Runner) runAuctionKeeper(ctx context.Context) {
	key, err := crypto.HexToECDSA(r.cfg.KeeperKey)
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
				if err := r.sendSettle(ctx, key, keeperAddr, auctionAddr, signer, chainIDBig, int64(a.AuctionID)); err != nil {
					log.Error().Err(err).Int64("auctionId", a.AuctionID).Msg("keeper: settle tx failed")
				} else {
					log.Info().Int64("auctionId", a.AuctionID).Msg("keeper: settle tx sent")
				}
			}

			inactive, err := r.q.GetInactiveAuctions(ctx)
			if err != nil {
				log.Error().Err(err).Msg("keeper: get inactive auctions")
				continue
			}
			for _, a := range inactive {
				if err := r.sendSettle(ctx, key, keeperAddr, auctionAddr, signer, chainIDBig, int64(a.AuctionID)); err != nil {
					log.Error().Err(err).Int64("auctionId", a.AuctionID).Msg("keeper: cancel-inactive tx failed")
				} else {
					log.Info().Int64("auctionId", a.AuctionID).Msg("keeper: cancel-inactive tx sent")
				}
			}
		}
	}
}

func (r *Runner) sendSettle(ctx context.Context, key *cryptoecdsa.PrivateKey, from, to common.Address, signer types.Signer, chainID *big.Int, auctionID int64) error {
	idBytes := make([]byte, 32)
	big.NewInt(auctionID).FillBytes(idBytes)
	data := append([]byte(nil), settleSelector...)
	data = append(data, idBytes...)

	nonce, err := r.eth.PendingNonceAt(ctx, from)
	if err != nil {
		return err
	}
	tipCap, _ := r.eth.SuggestGasTipCap(ctx)
	gasPrice, err := r.eth.SuggestGasPrice(ctx)
	if err != nil {
		return err
	}
	feeCap := new(big.Int).Add(tipCap, new(big.Int).Mul(gasPrice, big.NewInt(2)))

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		To:        &to,
		Gas:       150_000,
		GasFeeCap: feeCap,
		GasTipCap: tipCap,
		Data:      data,
	})
	signed, err := types.SignTx(tx, signer, key)
	if err != nil {
		return err
	}
	return r.eth.SendTransaction(ctx, signed)
}
