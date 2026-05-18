package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	if os.Getenv("ENV") != "production" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info().
		Str("rpc", os.Getenv("RPC_URL")).
		Str("chain_id", os.Getenv("CHAIN_ID")).
		Msg("indexer starting")

	// TODO D4: initialise pgxpool, redis client, hot.DecodeLog
	// TODO D4: run watcher loop (eth_getLogs chunked, then eth_subscribe newHeads)
	// TODO D4: run auction worker (settle expired auctions)
	// TODO D4: run score worker (recompute trending every 60s via hot.ComputeScore)
	// TODO D4: run offer expiry sweeper

	<-ctx.Done()
	log.Info().Msg("indexer shutdown")
}
