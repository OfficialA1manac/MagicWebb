// chainwipe resets every chain-derived table after a contract redeploy, so the
// indexer rebuilds clean state from the new contract set (old contracts' rows
// would otherwise collide on auction IDs and listing keys).
//
// User-owned data (users, profiles, notifications, reports) is untouched.
//
// Usage: POSTGRES_URL=... go run ./cmd/chainwipe --yes
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// chainTables is everything the indexer rebuilds from chain events, in an
// order that respects FK references (children first).
var chainTables = []string{
	"trending_scores",
	"bids",
	"sales",
	"offers",
	"listings",
	"auctions",
	"nft_attributes",
	"nft_metadata",
	"nft_ownership",
	"nft_tokens",
	"tracked_collections",
	"collections",
	"indexer_state",
	// The startup guard in cmd/server compares deployment_config against the
	// env addresses and refuses to boot on a mismatch, telling the operator to
	// "truncate on-chain data first". Leaving this row behind meant doing
	// exactly that still left the server blocked — the wipe cleared the data
	// the row describes but not the row. Empty is the correct post-wipe state:
	// the server re-inserts it from the current env on the ErrNoRows path.
	"deployment_config",
}

func main() {
	yes := flag.Bool("yes", false, "confirm wipe of all chain-derived tables")
	flag.Parse()
	if !*yes {
		fmt.Fprintln(os.Stderr, "refusing to run without --yes (wipes all chain-derived tables)")
		os.Exit(1)
	}
	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "POSTGRES_URL not set")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	defer pool.Close()

	for _, t := range chainTables {
		if _, err := pool.Exec(ctx, "TRUNCATE TABLE "+t+" CASCADE"); err != nil {
			fmt.Fprintf(os.Stderr, "truncate %s: %v\n", t, err)
			os.Exit(1)
		}
		fmt.Println("wiped", t)
	}
	fmt.Println("chain-derived state reset; indexer will rebuild from INDEX_FROM_BLOCK")
}
