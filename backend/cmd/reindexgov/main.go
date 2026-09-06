// reindexgov replays the governance trail (MarketplaceManager admin/keeper
// events + every core's UpgradeQueued/UpgradeCancelled/Upgraded) into
// governance_events (migration 043). Run once per network after the v3.6
// deploy so history that predates the watcher — the deploy-time KeeperSet and
// each proxy's construction-time Upgraded — becomes visible on /status, and
// again after any upgrade the watcher might have missed. Idempotent.
//
// Usage (same env as the server):
//
//	POSTGRES_URL=… RPC_URL=… CHAIN_ID=114 MARKETPLACE_ADDR=… AUCTION_ADDR=… \
//	OFFERBOOK_ADDR=… MARKETPLACE_MANAGER_ADDR=… go run ./cmd/reindexgov -from 34905078
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/indexer"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/rpcpool"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

func main() {
	from := flag.Uint64("from", 0, "first block to scan (default: INDEX_FROM_BLOCK)")
	flag.Parse()

	env := func(k string) string { return strings.TrimSpace(os.Getenv(k)) }
	need := func(k string) string {
		v := env(k)
		if v == "" {
			fmt.Fprintln(os.Stderr, k, "not set")
			os.Exit(1)
		}
		return v
	}
	chainID, err := strconv.ParseUint(need("CHAIN_ID"), 10, 64)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bad CHAIN_ID:", err)
		os.Exit(1)
	}
	if *from == 0 {
		if v := env("INDEX_FROM_BLOCK"); v != "" {
			*from, _ = strconv.ParseUint(v, 10, 64)
		}
	}
	if *from == 0 {
		fmt.Fprintln(os.Stderr, "-from or INDEX_FROM_BLOCK required")
		os.Exit(1)
	}
	urls := strings.Split(need("RPC_URL"), ",")
	if v := env("RPC_URLS"); v != "" {
		urls = strings.Split(v, ",")
	}

	cfg := &config.Config{
		ChainID:                chainID,
		MarketplaceAddr:        strings.ToLower(need("MARKETPLACE_ADDR")),
		AuctionAddr:            strings.ToLower(need("AUCTION_ADDR")),
		OfferBookAddr:          strings.ToLower(need("OFFERBOOK_ADDR")),
		MarketplaceManagerAddr: strings.ToLower(need("MARKETPLACE_MANAGER_ADDR")),
		GetLogsChunk:           30,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	pool, err := db.Connect(ctx, need("POSTGRES_URL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	defer pool.Close()
	eth, err := rpcpool.New(ctx, urls, rpcpool.DefaultTimeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rpc:", err)
		os.Exit(1)
	}

	var serverTimeMs int64
	r := indexer.New(cfg, db.New(pool), sse.New(), eth, &serverTimeMs)
	n, err := r.ReindexGovernance(ctx, *from)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reindexgov:", err)
		os.Exit(1)
	}
	fmt.Printf("governance trail replayed: %d events recorded from block %d\n", n, *from)
}
