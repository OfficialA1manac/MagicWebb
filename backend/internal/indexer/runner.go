package indexer

import (
	"context"
	"log"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/jackc/pgx/v5/pgxpool"
)

const backfillChunk = 2000

type Runner struct {
	Pool      *pgxpool.Pool
	HTTP      *ethclient.Client
	WS        *ethclient.Client
	ChainID   int
	Addresses []common.Address
}

func (r *Runner) Run(ctx context.Context) error {
	if err := r.backfill(ctx); err != nil {
		return err
	}
	return r.subscribe(ctx)
}

func (r *Runner) backfill(ctx context.Context) error {
	var last int64
	if err := r.Pool.QueryRow(ctx,
		`SELECT last_block FROM indexer_cursor WHERE chain_id=$1`, r.ChainID).Scan(&last); err != nil {
		return err
	}
	head, err := r.HTTP.BlockNumber(ctx)
	if err != nil {
		return err
	}
	up := &Upserter{Pool: r.Pool}
	for from := last + 1; from <= int64(head); from += backfillChunk {
		to := from + backfillChunk - 1
		if to > int64(head) {
			to = int64(head)
		}
		q := ethereum.FilterQuery{
			FromBlock: big.NewInt(from),
			ToBlock:   big.NewInt(to),
			Addresses: r.Addresses,
		}
		logs, err := r.HTTP.FilterLogs(ctx, q)
		if err != nil {
			return err
		}
		for _, lg := range logs {
			if err := up.Apply(ctx, lg); err != nil {
				log.Printf("upsert: %v", err)
			}
		}
		if _, err = r.Pool.Exec(ctx,
			`UPDATE indexer_cursor SET last_block=$2 WHERE chain_id=$1`, r.ChainID, to); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) subscribe(ctx context.Context) error {
	up := &Upserter{Pool: r.Pool}
	for {
		ch := make(chan types.Log, 64)
		sub, err := r.WS.SubscribeFilterLogs(ctx, ethereum.FilterQuery{Addresses: r.Addresses}, ch)
		if err != nil {
			log.Printf("WS subscribe failed, falling back to poll: %v", err)
			r.poll(ctx, up)
			continue
		}
		for {
			select {
			case <-ctx.Done():
				sub.Unsubscribe()
				return ctx.Err()
			case err := <-sub.Err():
				log.Printf("WS sub error: %v", err)
				sub.Unsubscribe()
			case lg := <-ch:
				if err := up.Apply(ctx, lg); err != nil {
					log.Printf("upsert: %v", err)
				}
				if _, err := r.Pool.Exec(ctx,
					`UPDATE indexer_cursor SET last_block=$2 WHERE chain_id=$1 AND last_block < $2`,
					r.ChainID, int64(lg.BlockNumber)); err != nil {
					log.Printf("cursor: %v", err)
				}
			}
		}
	}
}

func (r *Runner) poll(ctx context.Context, up *Upserter) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = r.backfill(ctx)
		}
	}
}
