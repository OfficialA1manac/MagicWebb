package indexer

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/chain"
)

// runOwnershipRepairWorker fixes stale nft_ownership rows that block buy
// preflight when the on-chain holder still matches the listing seller.
func (r *Runner) runOwnershipRepairWorker(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rows, err := r.q.ListActiveListingsMissingOwnership(ctx, 25)
			if err != nil {
				log.Warn().Err(err).Msg("ownership repair: list")
				continue
			}
			for _, row := range rows {
				owns, std, amt, verified, verr := verifyListingSeller(ctx, r.eth, row.Collection, row.TokenID, row.Seller)
				if verr != nil || !verified {
					continue
				}
				if owns {
					if err := r.q.EnsureListingSellerOwnership(ctx, row.Collection, row.TokenID, row.Seller, std, amt); err != nil {
						log.Warn().Err(err).Str("coll", row.Collection).Str("token", row.TokenID).
							Msg("ownership repair: seed failed")
					}
					continue
				}
				if err := r.q.OrphanListing(ctx, row.Collection, row.TokenID, row.Seller); err != nil {
					log.Warn().Err(err).Str("coll", row.Collection).Str("token", row.TokenID).
						Msg("ownership repair: orphan failed")
				}
			}
		}
	}
}

func verifyListingSeller(ctx context.Context, eth chain.Caller, collection, tokenID, seller string) (owns bool, standard string, amount int64, verified bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	owner, err721 := chain.OwnerOf721(ctx, eth, collection, tokenID)
	if err721 == nil {
		if chain.SameAddr(owner, seller) {
			return true, "erc721", 1, true, nil
		}
		return false, "erc721", 1, true, nil
	}
	bal, err1155 := chain.Balance1155(ctx, eth, collection, tokenID, seller)
	if err1155 == nil {
		if bal.Sign() > 0 {
			return true, "erc1155", bal.Int64(), true, nil
		}
		return false, "erc1155", 0, true, nil
	}
	return false, "", 0, false, err721
}
