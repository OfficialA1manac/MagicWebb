# tools/seed-testnet — MagicWebb testnet population SPEC emitter

This tool emits a deterministic JSONL specification of how the
MagicWebb marketplace would be populated with synthetic users,
listings, bids and auctions.

## What this tool IS

- A SPEC generator — produces a reproducible JSONL plan on stdout.
- Deterministic — same inputs ⇒ identical output, byte for byte.
- Audit-grade — every record carries `seeded_by = "sim-harness:v1"`
  and a stable `run_id` so an operator can identify, reproduce, or
  sweep the corresponding DB rows.
- Self-contained — stdlib only, no third-party deps, builds in <1 s.

## What this tool IS NOT

- **Not a transaction executor.** It never signs or broadcasts txs.
- **Not connected to a real signer pool.** Placeholder addresses are
  SHA256-derived 40-char hex strings, NOT a real EOA. Operators MUST
  replace every `address` field with a real signer address before
  any on-chain leg of the plan runs.
- **Not a faucet.** It does not direct any real chain or DB writes.
  An executor implementation is out of scope for this seed-testnet
  tool — see "Wiring the executor" below.

## Flags

| Flag              | Default | Purpose                                                |
|-------------------|---------|--------------------------------------------------------|
| --dry-run         | true    | Print plan only (default behaviour — never executes).  |
| --seed-users      | 8       | Synthetic users to mint.                               |
| --seed-listings   | 5       | Listings per user (requires `--no-test-nft` removal).  |
| --seed-bids       | 20      | Cumulative bids across all auctions.                   |
| --seed-auctions   | 1       | Auctions to create.                                    |
| --teardown        | false   | Emit a teardown plan (DELETE rows tagged sim-harness). |
| --run-id          | auto    | Stable identifier for the run.                         |

## Environment

The tools reads these env vars (none required — the tool runs even
with zero env, in which case the executor must fill the gaps before
any real write):

- `POSTGRES_URL` — Postgres connection string (operator's secret).
- `RPC_URL` — Coston2 RPC endpoint.
- `CHAIN_ID` — numeric (114 for Coston2).
- `MARKETPLACE_ADDR`, `AUCTION_ADDR`, `OFFERBOOK_ADDR` — deployed contracts.
- `SIM_TESTNFT_ADDR` — when set, the plan includes on-chain listing /
  bid legs; when unset, the plan emits a `skip-onchain` record so
  the operator knows to wire those steps themselves.

## Quick start

```bash
cd tools/seed-testnet
go build .           # builds with NO third-party deps

# Plan only (default)
./seed-testnet --dry-run --seed-users=12 | tee plan.jsonl

# Teardown plan
./seed-testnet --teardown --run-id=<the-id-from-logs>
```

Run output is JSONL on stdout — one record per planned event:

```
{"kind":"plan","ts":...,"data":{"run_id":"...","seed_users":12, ...}}
{"kind":"user","ts":...,"data":{"index":0,"handle":"🤖 SimBidder-001","address":"0x...","run_id":"..."}}
{"kind":"fund-plan","ts":...,"data":{"to":"0x...","amount_wei":"1000000000000000000",...}}
{"kind":"seed-list","ts":...,"data":{"seller":"0x...", ...}}
{"kind":"seed-auction","ts":...,"data":{"seller":"0x...", ...}}
{"kind":"seed-bid-plan","ts":...,"data":{"bidder":"0x...","amount_wei":"...",...}}
{"kind":"users-table-plan","ts":...,"data":{"run_id":"...","sql":"INSERT INTO users..."}}
{"kind":"done","ts":...,"data":{"run_id":"...","dry_run":true,"ok":true}}
```

## Wiring the executor

Once you have a real signer pool and a real Postgres URL, the
operator writes a daemon (separate repo) that:

1. Reads JSONL line-by-line on stdin.
2. For each `fund-plan` record, replaces the placeholder address
   with a real EOA from the signer pool and broadcasts `value=1.0
   C2FLR` to it.
3. For each `seed-list` record, signs and broadcasts the
   `MARKETPLACE.list(coll, tokenId, priceWei, expiresAt)` call with
   the executor's own signer (synthetic users cannot be owners yet).
4. For each `seed-auction` record, signs and broadcasts the
   auction-create call.
5. For each `seed-bid-plan` record, signs and broadcasts a `bid()` call
   once the synthetic user has a real signer + token custody.
6. For each `users-table-plan`, runs the embedded SQL with
   `address` rewritten via the real signer pool assignments.

Out of scope for `seed-testnet` because:
- Real signer keys must NEVER enter this repo's source tree.
- The executor requires a wallet framework (cast, ethers, ledger)
  that this tool deliberately does not import to keep build times
  sub-second on CI.

## Audit-grade teardown

Each `--teardown --run-id=<id>` emits a SQL plan marked with
`sim-harness:v1`. Operators stamp every row their daemon writes with
the same tag so a single `--teardown` sweep can be verified end-to-end:

```sql
DELETE FROM listings  WHERE seeded_by = 'sim-harness:v1' AND run_id = '<id>';
DELETE FROM bids      WHERE seeded_by = 'sim-harness:v1' AND run_id = '<id>';
DELETE FROM offers    WHERE seeded_by = 'sim-harness:v1' AND run_id = '<id>';
DELETE FROM auctions  WHERE seeded_by = 'sim-harness:v1' AND run_id = '<id>';
```

Synthetic `users` rows are stamp-free (the column is reserved for
entity data the indexer owns); the executor's run-time JSONL stream
carries the addresses so a targeted `DELETE FROM users WHERE
address IN (...)` finishes the sweep.

## See also

- `docs/USER_GUIDE.md` for the user-flow walkthrough the harness
  exercises once the executor is wired.
- `docs/AUDIT.md` for the v21 Priority Stack unlock that ensures
  the indexer + DB layer is robust against hostile synthetic input.
