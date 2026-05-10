package indexer

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Upserter writes decoded log events into Postgres idempotently.
type Upserter struct {
	Pool *pgxpool.Pool
}

func (u *Upserter) Apply(ctx context.Context, log types.Log) error {
	name := EventName(log.Topics[0])
	if name == "" {
		return nil
	}
	switch name {
	case "Listed":
		return u.applyListed(ctx, log)
	case "Cancelled":
		return u.applyCancelled(ctx, log)
	case "Bought":
		return u.applyBought(ctx, log)
	case "OfferAccepted":
		return u.applyOfferAccepted(ctx, log)
	}
	return nil
}

func addrFromTopic(t common.Hash) string { return common.BytesToAddress(t.Bytes()).Hex() }
func uintFromTopic(t common.Hash) string { return new(big.Int).SetBytes(t.Bytes()).String() }

func (u *Upserter) applyListed(ctx context.Context, l types.Log) error {
	coll  := addrFromTopic(l.Topics[1])
	tid   := uintFromTopic(l.Topics[2])
	seller := addrFromTopic(l.Topics[3])
	// Non-indexed: price (uint128), expiresAt (uint64) — first 16+8 bytes (padded) of data.
	if len(l.Data) < 64 {
		return nil
	}
	price   := new(big.Int).SetBytes(l.Data[16:32]).String()
	expires := new(big.Int).SetBytes(l.Data[56:64]).Int64()
	_, err := u.Pool.Exec(ctx, `
		INSERT INTO listings (collection, token_id, seller, price_wei, expires_at, tx_hash, block_number, log_index)
		VALUES ($1,$2,$3,$4, to_timestamp($5), $6,$7,$8)
		ON CONFLICT (tx_hash, log_index) DO NOTHING`,
		coll, tid, seller, price, expires, l.TxHash.Bytes(), int64(l.BlockNumber), int(l.Index))
	return err
}

func (u *Upserter) applyCancelled(ctx context.Context, l types.Log) error {
	coll := addrFromTopic(l.Topics[1])
	tid  := uintFromTopic(l.Topics[2])
	_, err := u.Pool.Exec(ctx,
		`UPDATE listings SET status='cancelled' WHERE collection=$1 AND token_id=$2 AND status='active'`,
		coll, tid)
	return err
}

func (u *Upserter) applyBought(ctx context.Context, l types.Log) error {
	coll  := addrFromTopic(l.Topics[1])
	tid   := uintFromTopic(l.Topics[2])
	buyer := addrFromTopic(l.Topics[3])
	if len(l.Data) < 96 {
		return nil
	}
	seller := common.BytesToAddress(l.Data[12:32]).Hex()
	price  := new(big.Int).SetBytes(l.Data[48:64]).String()
	fee    := new(big.Int).SetBytes(l.Data[64:96]).String()

	tx, err := u.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx,
		`UPDATE listings SET status='sold' WHERE collection=$1 AND token_id=$2 AND status='active'`,
		coll, tid); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO sales (collection, token_id, seller, buyer, price_wei, fee_wei, source, tx_hash, block_number, log_index)
		VALUES ($1,$2,$3,$4,$5,$6,'listing',$7,$8,$9)
		ON CONFLICT (tx_hash, log_index) DO NOTHING`,
		coll, tid, seller, buyer, price, fee, l.TxHash.Bytes(), int64(l.BlockNumber), int(l.Index)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (u *Upserter) applyOfferAccepted(ctx context.Context, l types.Log) error {
	coll   := addrFromTopic(l.Topics[1])
	tid    := uintFromTopic(l.Topics[2])
	seller := addrFromTopic(l.Topics[3])
	if len(l.Data) < 128 {
		return nil
	}
	bidder := common.BytesToAddress(l.Data[12:32]).Hex()
	amt    := new(big.Int).SetBytes(l.Data[48:64]).String()
	fee    := new(big.Int).SetBytes(l.Data[64:96]).String()
	nonce  := new(big.Int).SetBytes(l.Data[120:128]).String()

	tx, err := u.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx,
		`UPDATE offers SET status='accepted' WHERE bidder=$1 AND nonce=$2 AND status='active'`,
		bidder, nonce); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO sales (collection, token_id, seller, buyer, price_wei, fee_wei, source, tx_hash, block_number, log_index)
		VALUES ($1,$2,$3,$4,$5,$6,'offer',$7,$8,$9)
		ON CONFLICT (tx_hash, log_index) DO NOTHING`,
		coll, tid, seller, bidder, amt, fee, l.TxHash.Bytes(), int64(l.BlockNumber), int(l.Index)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
