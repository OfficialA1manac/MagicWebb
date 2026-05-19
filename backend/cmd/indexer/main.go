package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/indexer"
)

func main() {
	config.Load()

	if config.C.Env != "production" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info().
		Str("rpc", config.C.RPCURL).
		Uint64("chain_id", config.C.ChainID).
		Str("marketplace", config.C.MarketplaceAddr).
		Str("auction", config.C.AuctionAddr).
		Str("offerbook", config.C.OfferBookAddr).
		Msg("indexer starting")

	// DB migrations + pool.
	if err := db.Migrate(config.C.PostgresURL); err != nil {
		log.Fatal().Err(err).Msg("db migration failed")
	}
	pool, err := db.Connect(ctx, config.C.PostgresURL)
	if err != nil {
		log.Fatal().Err(err).Msg("db connect failed")
	}
	defer pool.Close()

	// Redis.
	rdb, err := cache.Connect(ctx, config.C.RedisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("redis connect failed")
	}
	defer rdb.Close()

	// Ethereum client.
	eth, err := ethclient.DialContext(ctx, config.C.RPCURL)
	if err != nil {
		log.Fatal().Err(err).Msg("eth client connect failed")
	}
	defer eth.Close()

	q := db.New(pool)
	runner := indexer.New(&config.C, q, rdb, eth)

	log.Info().Msg("indexer workers starting")
	runner.Run(ctx)
	log.Info().Msg("indexer shutdown complete")
}
