package indexer

import (
	"context"
	cryptoecdsa "crypto/ecdsa"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// refundLosers(uint256 id, address[] batch) selector.
var refundLosersSelector = crypto.Keccak256([]byte("refundLosers(uint256,address[])"))[:4]

// refundBatchSize bounds losers per refundLosers call so gas stays well under the
// block limit (the contract loops the batch, one ETH push each).
const refundBatchSize = 50

// encodeRefundLosers ABI-encodes refundLosers(uint256,address[]):
// selector ‖ id(32) ‖ offset=0x40(32) ‖ len(32) ‖ addr0(32) ‖ addr1(32) …
func encodeRefundLosers(auctionID int64, addrs []common.Address) []byte {
	out := append([]byte(nil), refundLosersSelector...)

	idWord := make([]byte, 32)
	big.NewInt(auctionID).FillBytes(idWord)
	out = append(out, idWord...)

	// address[] is dynamic: head holds the offset to the tail (one word past the
	// two head words → 0x40).
	out = append(out, common.LeftPadBytes(big.NewInt(0x40).Bytes(), 32)...)

	lenWord := make([]byte, 32)
	big.NewInt(int64(len(addrs))).FillBytes(lenWord)
	out = append(out, lenWord...)

	for _, a := range addrs {
		out = append(out, common.LeftPadBytes(a.Bytes(), 32)...)
	}
	return out
}

// ── Loser Refund Sweeper ──────────────────────────────────────────────────
//
// AuctionHouse v2 escrows every bid until settlement and never auto-refunds on
// outbid. After an auction settles (winner takes the NFT) or cancels (no sale),
// every non-winning bidder's escrow is reclaimable via refundLosers(id, batch) —
// permissionless, idempotent (zeroed escrow is skipped). This sweeper makes that
// reclaim autonomous: no losing bidder ever has to call a function to get paid.
func (r *Runner) runLoserRefundSweeper(ctx context.Context) {
	key, err := crypto.HexToECDSA(r.cfg.KeeperKey)
	if err != nil {
		log.Error().Err(err).Msg("refund sweeper: invalid KEEPER_KEY, disabled")
		return
	}
	keeperAddr := crypto.PubkeyToAddress(key.PublicKey)
	auctionAddr := common.HexToAddress(r.cfg.AuctionAddr)
	chainIDBig := big.NewInt(int64(r.cfg.ChainID))
	signer := types.NewLondonSigner(chainIDBig)

	log.Info().Str("keeper", keeperAddr.Hex()).Msg("refund sweeper: started")
	ticker := time.NewTicker(45 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sweepLoserRefunds(ctx, key, keeperAddr, auctionAddr, signer, chainIDBig)
		}
	}
}

func (r *Runner) sweepLoserRefunds(
	ctx context.Context,
	key *cryptoecdsa.PrivateKey,
	keeperAddr, auctionAddr common.Address,
	signer types.Signer,
	chainID *big.Int,
) {
	auctions, err := r.q.GetSettledUnrefundedAuctions(ctx)
	if err != nil {
		log.Error().Err(err).Msg("refund sweeper: list unrefunded")
		return
	}
	for _, a := range auctions {
		losers, err := r.collectLosers(ctx, a)
		if err != nil {
			log.Error().Err(err).Int64("auctionId", a.AuctionID).Msg("refund sweeper: collect losers")
			continue
		}
		if len(losers) == 0 {
			// Winner-only (or no bids): nothing to refund — close it out.
			if err := r.q.MarkLosersRefunded(ctx, a.AuctionID); err != nil {
				log.Error().Err(err).Int64("auctionId", a.AuctionID).Msg("refund sweeper: mark (empty)")
			}
			continue
		}

		// Broadcast every batch, then require a MINED, successful receipt for
		// each before closing the auction out in the DB. A broadcast that never
		// lands (dropped, replaced, "nonce too low" false-success upstream)
		// must not flip the flag — the sweep retries next tick and
		// refundLosers is idempotent on-chain (zeroed escrow is skipped).
		allMined := true
		var hashes []common.Hash
		for start := 0; start < len(losers); start += refundBatchSize {
			end := start + refundBatchSize
			if end > len(losers) {
				end = len(losers)
			}
			batch := losers[start:end]
			data := encodeRefundLosers(a.AuctionID, batch)
			gas := uint64(120_000 + 50_000*len(batch))
			h, err := r.sendRaw(ctx, key, keeperAddr, auctionAddr, signer, chainID, data, gas)
			if err != nil {
				log.Error().Err(err).Int64("auctionId", a.AuctionID).Int("batch", len(batch)).
					Msg("refund sweeper: refundLosers tx failed")
				allMined = false
				break
			}
			hashes = append(hashes, h)
			log.Info().Int64("auctionId", a.AuctionID).Int("losers", len(batch)).Str("tx", h.Hex()).
				Msg("refund sweeper: refundLosers tx sent")
		}
		if allMined {
			for _, h := range hashes {
				if err := r.waitMined(ctx, h, 60*time.Second); err != nil {
					log.Error().Err(err).Int64("auctionId", a.AuctionID).
						Msg("refund sweeper: refundLosers tx not confirmed")
					allMined = false
					break
				}
			}
		}

		// Mark only after every batch is confirmed on-chain. LoserRefunded
		// events then sync the per-bidder refund rows for the UI.
		if allMined {
			if err := r.q.MarkLosersRefunded(ctx, a.AuctionID); err != nil {
				log.Error().Err(err).Int64("auctionId", a.AuctionID).Msg("refund sweeper: mark refunded")
			}
		} else {
			if err := r.q.SetRefundAttempt(ctx, a.AuctionID); err != nil {
				log.Error().Err(err).Int64("auctionId", a.AuctionID).Msg("refund sweeper: set attempt")
			}
		}
	}
}

// collectLosers returns the addresses owed an escrow refund: every bidder for a
// cancelled auction; every bidder except the winner for a settled one. Backed by
// the effective_bids view (one row per distinct bidder).
func (r *Runner) collectLosers(ctx context.Context, a db.RefundableAuction) ([]common.Address, error) {
	bids, err := r.q.GetEffectiveBids(ctx, a.AuctionID)
	if err != nil {
		return nil, err
	}
	winner := strings.ToLower(a.Winner)
	out := make([]common.Address, 0, len(bids))
	for _, b := range bids {
		if a.Status == "settled" && strings.ToLower(b.Bidder) == winner {
			continue // winner's escrow was consumed at settle
		}
		out = append(out, common.HexToAddress(b.Bidder))
	}
	return out, nil
}
