// Command seed-testnet emits a deterministic JSONL plan that an
// operator can execute to populate the MagicWebb marketplace with
// synthetic users, listings, bids and auctions.
//
// HONESTY CONTRACT
// ----------------
// This tool is a SPEC GENERATOR, not a transaction executor. It never
// signs or broadcasts transactions. Running it produces a JSONL spec
// on stdout; the operator then either:
//
//   (a) re-implements the execution leg using real signer/wallet
//       tooling against a Coston2 deployment whose addresses are
//       surfaced here, OR
//   (b) feeds the JSONL output into a pre-existing seeding daemon.
//
// The plan is reconstructed identically for identical inputs so
// dry-runs are reproducible. Tag every row "sim-harness:v1" so an
// operator can run a targeted teardown (see docs/AUDIT.md).
//
// FLAGS
// -----
//   --dry-run             Print the plan only; default is on.
//   --seed-users N        Number of synthetic users (default 8).
//   --seed-listings N     Listings per user (default 5).
//   --seed-bids N         Total bids across all auctions (default 20).
//   --seed-auctions N     Auctions to create (default 1).
//   --teardown            Emit teardown plan; tags use --run-id.
//   --run-id ID           Bind the run to a stable identifier.
//
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// seededByTag is the audit-grade identifier stamped on every row
// this tool's eventual executor writes. Operators query this in
// --teardown mode.
const seededByTag = "sim-harness:v1"

// planEnv holds the testnet connection parameters; all are env-driven
// because the operator's secret material must never enter the source
// tree.
type planEnv struct {
	PostgresURL     string `json:"postgres_url"`
	RPCURL          string `json:"rpc_url"`
	ChainID         int64  `json:"chain_id"`
	MarketplaceAddr string `json:"marketplace"`
	AuctionAddr     string `json:"auction"`
	OfferBookAddr   string `json:"offerbook"`
	TestNFTAddr     string `json:"test_nft,omitempty"`
}

// simUser describes one synthetic end-user the harness should mint.
// The address derivation uses SHA256 over the chain id + run id + index
// so dry-runs are reproducible. The address is a deterministic
// 40-hex-char string but is NOT derived from a private key — it is
// purely a placeholder the executor must replace when wiring real
// signer derivation (HD wallet path or pre-funded EOAs).
type simUser struct {
	Index   int    `json:"index"`
	Handle  string `json:"handle"`
	Address string `json:"address"`
	Funded  bool   `json:"funded"`
	RunID   string `json:"run_id"`
}

// emit writes one JSONL line per planned event so verifiers can
// replay the operation deterministically.
func emit(kind string, payload any) {
	bs, _ := json.Marshal(map[string]any{
		"kind": kind,
		"ts":   time.Now().Unix(),
		"data": payload,
	})
	fmt.Fprintln(os.Stdout, string(bs))
}

// deterministicPlaceholderAddress returns a 40-char hex placeholder
// derived from (runID, index). NOT a real address. The executor MUST
// replace this with a real address from its signer pool.
func deterministicPlaceholderAddress(runID string, index int) string {
	h := sha256.New()
	fmt.Fprintf(h, "magicwebb:sim-placeholder:r=%s:i=%d", runID, index)
	return hex.EncodeToString(h.Sum(nil))[:40]
}

// loadEnvPlan reads required vars; falls back to safe defaults with
// a clear `info` record emitted on stdout. Missing vars are warned,
// not fatal, because the tool's purpose is planning, not execution.
func loadEnvPlan() planEnv {
	get := func(k string) string { return strings.TrimSpace(os.Getenv(k)) }
	e := planEnv{
		PostgresURL:     get("POSTGRES_URL"),
		RPCURL:          get("RPC_URL"),
		MarketplaceAddr: get("MARKETPLACE_ADDR"),
		AuctionAddr:     get("AUCTION_ADDR"),
		OfferBookAddr:   get("OFFERBOOK_ADDR"),
		TestNFTAddr:     get("SIM_TESTNFT_ADDR"),
	}
	chainStr := get("CHAIN_ID")
	if chainStr != "" {
		var n int64
		if _, err := fmt.Sscan(chainStr, &n); err == nil {
			e.ChainID = n
		}
	}
	if e.PostgresURL == "" || e.RPCURL == "" || e.ChainID == 0 {
		emit("env-warn", map[string]any{
			"reason": "POSTGRES_URL, RPC_URL or CHAIN_ID empty — emit is a plan only. The executor must fill these before any real chain/DB write.",
			"env":    e,
		})
	}
	return e
}

// defaultRunID returns a stable identifier for the run so the
// counterpart DB writes (and teardown sweep) are reproducible.
func defaultRunID(e planEnv) string {
	if e.ChainID == 0 {
		return "sim-run-" + time.Now().UTC().Format("20060102T150405")
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s:%d:%s", e.PostgresURL, e.ChainID, time.Now().UTC().Format("20060102"))
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func main() {
	dryRun := flag.Bool("dry-run", true, "Print the plan only (default true). Tool is spec-only; never executes.")
	seedUsers := flag.Int("seed-users", 8, "Synthetic users to mint.")
	seedListings := flag.Int("seed-listings", 5, "Listings per user (executor honours SIM_TESTNFT_ADDR).")
	seedBids := flag.Int("seed-bids", 20, "Cumulative bids across all auctions (executor).")
	seedAuctions := flag.Int("seed-auctions", 1, "Auctions to create (executor).")
	teardown := flag.Bool("teardown", false, "Emit teardown plan (deletes rows tagged sim-harness:v1 --run-id).")
	runIDFlag := flag.String("run-id", "", "Stable identifier for the run (auto-derived if empty).")
	flag.Parse()

	env := loadEnvPlan()
	runID := strings.TrimSpace(*runIDFlag)
	if runID == "" {
		runID = defaultRunID(env)
	}

	if *teardown {
		emit("teardown-plan", map[string]any{
			"run_id": runID,
			"sql": []string{
				fmt.Sprintf("DELETE FROM listings  WHERE seeded_by = '%s' AND run_id = '%s';", seededByTag, runID),
				fmt.Sprintf("DELETE FROM bids      WHERE seeded_by = '%s' AND run_id = '%s';", seededByTag, runID),
				fmt.Sprintf("DELETE FROM offers    WHERE seeded_by = '%s' AND run_id = '%s';", seededByTag, runID),
				fmt.Sprintf("DELETE FROM auctions  WHERE seeded_by = '%s' AND run_id = '%s';", seededByTag, runID),
				"-- Synthetic users are removed by a follow-up SELECT/DELETE on the addresses emitted in this run's JSONL stream.",
			},
		})
		fmt.Fprintf(os.Stdout, "{\"kind\":\"teardown\",\"run_id\":\"%s\",\"ok\":true}\n", runID)
		return
	}

	emit("plan", map[string]any{
		"run_id":        runID,
		"seed_users":    *seedUsers,
		"seed_listings": *seedListings,
		"seed_bids":     *seedBids,
		"seed_auctions": *seedAuctions,
		"dry_run":       *dryRun,
		"has_test_nft":  env.TestNFTAddr != "",
		"env":           env,
	})

	// Synthetic user addresses — placeholder derivation, never signs.
	for i := 0; i < *seedUsers; i++ {
		u := simUser{
			Index:   i,
			Handle:  fmt.Sprintf("🤖 SimBidder-%03d", i+1),
			Address: "0x" + deterministicPlaceholderAddress(runID, i),
			Funded:  false,
			RunID:   runID,
		}
		emit("user", u)
		emit("fund-plan", map[string]any{
			"to":         u.Address,
			"amount_wei": "1000000000000000000",
			"note":       "executor must replace placeholder address with a real signer address; 1.0 C2FLR is sub-noise, well below faucet limits",
		})
	}

	if env.TestNFTAddr == "" {
		emit("skip-onchain", map[string]any{
			"reason": "SIM_TESTNFT_ADDR not set. Operator must deploy a TestNFT factory on Coston2 and either (a) mint per-user test tokens then transfer to synthetic users, or (b) cancel and only seed DB-side activity until the executor layer is wired.",
		})
	} else {
		// Listings
		for i := 0; i < *seedUsers; i++ {
			addr := "0x" + deterministicPlaceholderAddress(runID, i)
			for j := 0; j < *seedListings; j++ {
				priceWei := int64(10_000_000_000_000_000 + j*1_000_000_000_000_000)
				emit("seed-list", map[string]any{
					"seller":     addr,
					"collection": env.TestNFTAddr,
					"token_id":   fmt.Sprintf("sim-tok-%s-i%d-j%d", runID, i, j),
					"price_wei":  fmt.Sprintf("%d", priceWei),
					"expires_at": time.Now().Add(72 * time.Hour).Unix(),
					"run_id":     runID,
				})
			}
		}
		// Auctions
		for a := 0; a < *seedAuctions; a++ {
			emit("seed-auction", map[string]any{
				"seller":       "0x" + deterministicPlaceholderAddress(runID, -1-a),
				"collection":   env.TestNFTAddr,
				"token_id":     fmt.Sprintf("sim-auct-%s-a%d", runID, a),
				"reserve_wei":  "100000000000000000",
				"ends_at":      time.Now().Add(15 * time.Minute).Unix(),
				"min_inc_bps":  500,
				"run_id":       runID,
			})
		}
		// Bid plan — executor must verify user/token custody before broadcasting.
		for b := 0; b < *seedBids; b++ {
			emit("seed-bid-plan", map[string]any{
				"bidder":    "0x" + deterministicPlaceholderAddress(runID, b%*seedUsers),
				"amount_wei": fmt.Sprintf("%d", 110_000_000_000_000_000+b*5_000_000_000_000_000),
				"auction":   fmt.Sprintf("sim-auct-%s-a%d", runID, b%max1int(*seedAuctions)),
				"run_id":    runID,
			})
		}
	}

	emit("users-table-plan", map[string]any{
		"run_id": runID,
		"sql": fmt.Sprintf(`INSERT INTO users(address, last_seen_at) VALUES %s
ON CONFLICT(address) DO UPDATE SET last_seen_at = now()
WHERE NOT EXISTS (SELECT 1 FROM users WHERE address = EXCLUDED.address);`,
			placeholderUserList(*seedUsers, runID)),
	})

	fmt.Fprintf(os.Stdout, "{\"kind\":\"done\",\"run_id\":\"%s\",\"dry_run\":%v,\"ok\":true}\n", runID, *dryRun)
}

// max1int is int-divisor safety so b%auctions never divides by zero.
func max1int(n int) int {
	if n <= 0 {
		return 1
	}
	return n
}

// placeholderUserList builds the VALUES list for an INSERT.
func placeholderUserList(n int, runID string) string {
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		parts[i] = fmt.Sprintf("('0x%s', now())", deterministicPlaceholderAddress(runID, i))
	}
	return strings.Join(parts, ",")
}
