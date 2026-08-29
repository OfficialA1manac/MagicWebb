// Package chaintables is the single source of truth for the set of
// chain-derived tables that a contract redeploy must wipe. Two callers
// perform the same truncation and must never drift:
//
//   - cmd/chainwipe — the operator-run reset (mainnets, deliberate)
//   - cmd/server    — the RESET_ON_ADDRESS_CHANGE=true automatic reset
//     (testnet template only)
//
// A table added to one wipe but missed by the other would leave stale
// chain rows behind after a redeploy — exactly the collision the
// deployment_config guard exists to prevent.
package chaintables

import "strings"

// Tables is everything the indexer rebuilds from chain events, in an order
// that respects FK references (children first).
var Tables = []string{
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

// TruncateStmt returns one TRUNCATE statement covering every table.
// A single statement runs in a single implicit transaction, so the wipe is
// all-or-nothing: the process can never die with the data tables emptied
// but deployment_config still describing the old contract set.
func TruncateStmt() string {
	return "TRUNCATE TABLE " + strings.Join(Tables, ", ") + " CASCADE"
}
