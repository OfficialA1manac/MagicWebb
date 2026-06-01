// Package indexer provides the chain watcher and background workers.
package indexer

import (
	"context"
	cryptoecdsa "crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
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
	// metadataQueue receives (collection, tokenID) pairs to fetch metadata for.
	metadataQueue chan metaJob
}

type metaJob struct {
	collection string
	tokenID    string
	standard   string
}

// New creates a Runner with all dependencies injected.
func New(cfg *config.Config, q *db.Q, bcast *sse.Broadcaster, eth *ethclient.Client, serverTimeMs *int64) *Runner {
	return &Runner{
		cfg:           cfg,
		q:             q,
		bcast:         bcast,
		eth:           eth,
		h:             &handlers{q: q, bcast: bcast},
		serverTimeMs:  serverTimeMs,
		metadataQueue: make(chan metaJob, 1024),
	}
}

// Run starts all workers and blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	var wg sync.WaitGroup

	wg.Add(1)
	go func() { defer wg.Done(); r.runMarketWatcher(ctx) }()

	wg.Add(1)
	go func() { defer wg.Done(); r.runTransferWatcher(ctx) }()

	wg.Add(1)
	go func() { defer wg.Done(); r.runScoreWorker(ctx) }()

	wg.Add(1)
	go func() { defer wg.Done(); r.runOfferExpirySweeper(ctx) }()

	wg.Add(1)
	go func() { defer wg.Done(); r.runMetadataWorker(ctx) }()

	if r.cfg.KeeperKey != "" {
		wg.Add(1)
		go func() { defer wg.Done(); r.runAuctionKeeper(ctx) }()
	}

	wg.Wait()
}

// ── Marketplace event watcher ─────────────────────────────────────────────

func (r *Runner) runMarketWatcher(ctx context.Context) {
	chainID := int(r.cfg.ChainID)
	contracts := []common.Address{
		common.HexToAddress(r.cfg.MarketplaceAddr),
		common.HexToAddress(r.cfg.AuctionAddr),
		common.HexToAddress(r.cfg.OfferBookAddr),
	}
	topics := allMarketTopics()

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
		log.Info().Uint64("from", fromBlock).Uint64("to", head).Msg("market watcher: backfill start")
		r.backfill(ctx, fromBlock, head, contracts, topics, chainID, false)
		log.Info().Msg("market watcher: backfill complete")
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
				log.Warn().Err(err).Msg("market watcher: block poll failed")
				continue
			}
			if newHead <= lastBlock {
				continue
			}
			r.processRange(ctx, lastBlock+1, newHead, contracts, topics, chainID, false)
			lastBlock = newHead
			if header, err := r.eth.HeaderByNumber(ctx, big.NewInt(int64(newHead))); err == nil {
				atomic.StoreInt64(r.serverTimeMs, int64(header.Time*1000))
			}
		}
	}
}

// ── Transfer watcher (auto-track collections, mark orphans) ──────────────

func (r *Runner) runTransferWatcher(ctx context.Context) {
	// Transfers come from ANY contract (no address filter) — only the topic.
	// This is broad but tractable on a testnet. For mainnet performance, we'd
	// add an address filter once the tracked_collections list stabilizes.
	topics := transferTopics()

	fromBlock, _ := r.q.GetIndexedBlock(ctx, int(r.cfg.ChainID)+1_000) // separate cursor
	if r.cfg.IndexFromBlock > fromBlock {
		fromBlock = r.cfg.IndexFromBlock
	}

	head, err := r.eth.BlockNumber(ctx)
	if err != nil {
		log.Error().Err(err).Msg("transfer watcher: initial block number")
		return
	}
	if fromBlock < head {
		log.Info().Uint64("from", fromBlock).Uint64("to", head).Msg("transfer watcher: backfill start")
		r.backfill(ctx, fromBlock, head, nil, topics, int(r.cfg.ChainID)+1_000, true)
		log.Info().Msg("transfer watcher: backfill complete")
	}

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	lastBlock := head
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			newHead, err := r.eth.BlockNumber(ctx)
			if err != nil {
				log.Warn().Err(err).Msg("transfer watcher: block poll failed")
				continue
			}
			if newHead <= lastBlock {
				continue
			}
			r.processRange(ctx, lastBlock+1, newHead, nil, topics, int(r.cfg.ChainID)+1_000, true)
			lastBlock = newHead
		}
	}
}

func (r *Runner) backfill(ctx context.Context, from, to uint64, contracts []common.Address, topics [][]common.Hash, chainID int, isTransfer bool) {
	chunk := r.cfg.GetLogsChunk
	if chunk == 0 {
		chunk = 30
	}
	for start := from; start <= to; start += chunk {
		end := start + chunk - 1
		if end > to {
			end = to
		}
		r.processRange(ctx, start, end, contracts, topics, chainID, isTransfer)
		if ctx.Err() != nil {
			return
		}
	}
}

func (r *Runner) processRange(ctx context.Context, from, to uint64, contracts []common.Address, topics [][]common.Hash, chainID int, isTransfer bool) {
	q := ethereum.FilterQuery{
		FromBlock: big.NewInt(int64(from)),
		ToBlock:   big.NewInt(int64(to)),
		Topics:    topics,
	}
	if len(contracts) > 0 {
		q.Addresses = contracts
	}
	logs, err := r.eth.FilterLogs(ctx, q)
	if err != nil {
		log.Error().Err(err).Uint64("from", from).Uint64("to", to).Bool("transfer", isTransfer).Msg("watcher: filter logs")
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
		if isTransfer {
			if err := r.h.dispatchTransfer(ctx, l, blockTimes[l.BlockNumber]); err != nil {
				log.Error().Err(err).Str("tx", l.TxHash.Hex()).Msg("transfer dispatch")
			}
			// Enqueue metadata fetch for the recipient token
			r.enqueueMetadataFromTransfer(l)
		} else {
			if err := r.h.dispatch(ctx, l, blockTimes[l.BlockNumber]); err != nil {
				log.Error().Err(err).Str("tx", l.TxHash.Hex()).Msg("market dispatch")
			}
		}
	}
	if err := r.q.SetIndexedBlock(ctx, chainID, to); err != nil {
		log.Error().Err(err).Uint64("block", to).Msg("watcher: set indexed block")
	}
}

func (r *Runner) enqueueMetadataFromTransfer(l types.Log) {
	if len(l.Topics) == 0 {
		return
	}
	collection := strings.ToLower(l.Address.Hex())
	var tokenID string
	var standard string
	switch l.Topics[0] {
	case TopicTransfer721:
		if len(l.Topics) < 4 {
			return
		}
		tokenID = bigStr(l.Topics[3].Bytes())
		standard = "erc721"
	case TopicTransferSingle1155:
		if len(l.Data) < 64 {
			return
		}
		tokenID = bigStr(chunk(l.Data, 0))
		standard = "erc1155"
	default:
		return
	}
	select {
	case r.metadataQueue <- metaJob{collection: collection, tokenID: tokenID, standard: standard}:
	default:
		// Queue full — metadata will be picked up on next transfer.
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

// Marks expired pending offers in the DB. The keeper handles the on-chain refund.
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
				log.Info().Int64("expired", n).Msg("offer sweeper: legacy offers expired")
			}
		}
	}
}

// ── Metadata Worker ───────────────────────────────────────────────────────

func (r *Runner) runMetadataWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-r.metadataQueue:
			r.fetchMetadata(ctx, job)
		}
	}
}

// fetchMetadata calls tokenURI / uri on the collection contract, follows the URL,
// parses ERC-721 metadata JSON, and persists it.
func (r *Runner) fetchMetadata(ctx context.Context, j metaJob) {
	// Pack tokenURI(uint256) for ERC-721, uri(uint256) for ERC-1155
	tokenIDBig := new(big.Int)
	tokenIDBig.SetString(j.tokenID, 10)

	uriABI, _ := abi.JSON(strings.NewReader(`[
		{"type":"function","name":"tokenURI","inputs":[{"name":"id","type":"uint256"}],"outputs":[{"type":"string"}],"stateMutability":"view"},
		{"type":"function","name":"uri","inputs":[{"name":"id","type":"uint256"}],"outputs":[{"type":"string"}],"stateMutability":"view"}
	]`))
	fnName := "tokenURI"
	if j.standard == "erc1155" {
		fnName = "uri"
	}
	calldata, err := uriABI.Pack(fnName, tokenIDBig)
	if err != nil {
		return
	}

	addr := common.HexToAddress(j.collection)
	out, err := r.eth.CallContract(ctx, ethereum.CallMsg{To: &addr, Data: calldata}, nil)
	if err != nil {
		return
	}
	var uri string
	if err := uriABI.UnpackIntoInterface(&uri, fnName, out); err != nil {
		return
	}
	if uri == "" {
		return
	}
	uri = strings.ReplaceAll(uri, "{id}", j.tokenID)
	uri = ipfsToHTTP(uri)

	// Fetch the JSON
	doc := fetchJSON(ctx, uri)
	if doc == nil {
		_ = r.q.UpsertNFTMetadata(ctx, j.collection, j.tokenID, uri, "", "", "")
		return
	}
	name, _ := doc["name"].(string)
	description, _ := doc["description"].(string)
	image, _ := doc["image"].(string)
	image = ipfsToHTTP(image)

	if err := r.q.UpsertNFTMetadata(ctx, j.collection, j.tokenID, uri, name, description, image); err != nil {
		log.Warn().Err(err).Msg("metadata: upsert")
		return
	}

	if attrs, ok := doc["attributes"].([]any); ok {
		var parsed []db.NFTAttribute
		for _, a := range attrs {
			m, ok := a.(map[string]any)
			if !ok {
				continue
			}
			tt, _ := m["trait_type"].(string)
			v := fmt.Sprintf("%v", m["value"])
			if tt != "" && v != "" && v != "<nil>" {
				parsed = append(parsed, db.NFTAttribute{TraitType: tt, Value: v})
			}
		}
		if len(parsed) > 0 {
			_ = r.q.ReplaceNFTAttributes(ctx, j.collection, j.tokenID, parsed)
		}
	}
}

func ipfsToHTTP(uri string) string {
	if strings.HasPrefix(uri, "ipfs://") {
		return "https://ipfs.io/ipfs/" + strings.TrimPrefix(uri, "ipfs://")
	}
	return uri
}

func fetchJSON(ctx context.Context, url string) map[string]any {
	if !strings.HasPrefix(url, "http") {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil
	}
	return doc
}

// ── Auction & Offer Keeper (on-chain settlement / refund) ─────────────────

var (
	settleSelector        = crypto.Keccak256([]byte("settle(uint256)"))[:4]
	refundExpiredSelector = crypto.Keccak256([]byte("refundExpired(address,uint256,address)"))[:4]
)

func (r *Runner) runAuctionKeeper(ctx context.Context) {
	key, err := crypto.HexToECDSA(strings.TrimPrefix(r.cfg.KeeperKey, "0x"))
	if err != nil {
		log.Error().Err(err).Msg("keeper: invalid KEEPER_KEY, keeper disabled")
		return
	}
	keeperAddr := crypto.PubkeyToAddress(key.PublicKey)
	auctionAddr := common.HexToAddress(r.cfg.AuctionAddr)
	offerBookAddr := common.HexToAddress(r.cfg.OfferBookAddr)
	chainIDBig := big.NewInt(int64(r.cfg.ChainID))
	signer := types.NewLondonSigner(chainIDBig)

	log.Info().Str("keeper", keeperAddr.Hex()).Msg("keeper: started")
	pollSec := r.cfg.KeeperPollSeconds
	if pollSec == 0 {
		pollSec = 30
	}
	ticker := time.NewTicker(time.Duration(pollSec) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			auctions, err := r.q.GetExpiredActiveAuctions(ctx)
			if err != nil {
				log.Error().Err(err).Msg("keeper: get expired auctions")
			} else {
				for _, a := range auctions {
					if err := r.sendSettle(ctx, key, keeperAddr, auctionAddr, signer, chainIDBig, int64(a.AuctionID)); err != nil {
						log.Error().Err(err).Int64("auctionId", a.AuctionID).Msg("keeper: settle tx failed")
					} else {
						log.Info().Int64("auctionId", a.AuctionID).Msg("keeper: settle tx sent")
					}
				}
			}

			positions, err := r.q.GetExpiredOfferPositions(ctx, 50)
			if err != nil {
				log.Error().Err(err).Msg("keeper: get expired offer positions")
				continue
			}
			for _, p := range positions {
				if err := r.sendRefundExpired(ctx, key, keeperAddr, offerBookAddr, signer, chainIDBig, p); err != nil {
					log.Error().Err(err).Str("coll", p.Collection).Str("token", p.TokenID).Str("bidder", p.Bidder).
						Msg("keeper: refundExpired tx failed")
				} else {
					log.Info().Str("coll", p.Collection).Str("token", p.TokenID).Str("bidder", p.Bidder).
						Msg("keeper: refundExpired tx sent")
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
	return r.sendKeeperTx(ctx, key, from, to, signer, chainID, data, 150_000)
}

func (r *Runner) sendRefundExpired(ctx context.Context, key *cryptoecdsa.PrivateKey, from, to common.Address, signer types.Signer, chainID *big.Int, p db.OfferPositionRow) error {
	data := append([]byte(nil), refundExpiredSelector...)
	// arg 0: address coll (left-padded)
	addr := common.HexToAddress(p.Collection)
	pad := make([]byte, 12)
	data = append(data, pad...)
	data = append(data, addr.Bytes()...)
	// arg 1: uint256 tokenId
	tidBytes := make([]byte, 32)
	tid := new(big.Int)
	tid.SetString(p.TokenID, 10)
	tid.FillBytes(tidBytes)
	data = append(data, tidBytes...)
	// arg 2: address bidder
	bidAddr := common.HexToAddress(p.Bidder)
	data = append(data, pad...)
	data = append(data, bidAddr.Bytes()...)
	return r.sendKeeperTx(ctx, key, from, to, signer, chainID, data, 200_000)
}

func (r *Runner) sendKeeperTx(ctx context.Context, key *cryptoecdsa.PrivateKey, from, to common.Address, signer types.Signer, chainID *big.Int, data []byte, gas uint64) error {
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
		Gas:       gas,
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
