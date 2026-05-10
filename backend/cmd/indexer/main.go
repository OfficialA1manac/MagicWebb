package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/webbplace/backend/internal/indexer"
)

func main() {
	_ = godotenv.Load("../.env")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	httpC, err := ethclient.Dial(os.Getenv("COSTON2_RPC"))
	if err != nil {
		log.Fatalf("rpc: %v", err)
	}
	wsC, err := ethclient.Dial(os.Getenv("COSTON2_WS"))
	if err != nil {
		log.Fatalf("ws: %v", err)
	}

	chainID, _ := strconv.Atoi(envOr("CHAIN_ID", "114"))

	addrs := nonEmpty(
		os.Getenv("MARKETPLACE_ADDR"),
		os.Getenv("AUCTION_ADDR"),
		os.Getenv("OFFER_ADDR"),
	)
	if len(addrs) == 0 {
		log.Fatal("no contract addresses set; run `make deploy-coston2 && make load-addrs` first")
	}

	r := &indexer.Runner{
		Pool:      pool,
		HTTP:      httpC,
		WS:        wsC,
		ChainID:   chainID,
		Addresses: toAddresses(addrs),
	}
	log.Printf("indexer starting on chain %d, %d addresses", chainID, len(addrs))
	if err := r.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("indexer: %v", err)
	}
}

func nonEmpty(in ...string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func toAddresses(ss []string) []common.Address {
	out := make([]common.Address, len(ss))
	for i, s := range ss {
		out[i] = common.HexToAddress(s)
	}
	return out
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
