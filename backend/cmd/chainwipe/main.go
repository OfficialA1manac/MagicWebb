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

	"github.com/OfficialA1manac/MagicWebb/backend/cmd/internal/chaintables"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

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

	// One TRUNCATE statement over the shared chaintables list: atomic (a
	// failure leaves everything intact — never a half-wiped DB with a stale
	// deployment_config row) and guaranteed to match cmd/server's
	// RESET_ON_ADDRESS_CHANGE path.
	if _, err := pool.Exec(ctx, chaintables.TruncateStmt()); err != nil {
		fmt.Fprintf(os.Stderr, "truncate: %v\n", err)
		os.Exit(1)
	}
	for _, t := range chaintables.Tables {
		fmt.Println("wiped", t)
	}
	fmt.Println("chain-derived state reset; indexer will rebuild from INDEX_FROM_BLOCK")
}
