# MagicWebb v29 — Post-Launch Monitoring & Operations Runbook

> Companion to `contracts/AUDIT_REPORT.md` Phase 4d → Phase 6.
> Applies to Coston2 now, mainnet on pivot.

## Event lore (chain-side monitoring)

| Event                | Severity tag     | What it means                           | Action                                                    |
|:---------------------|:-----------------|:----------------------------------------|:----------------------------------------------------------|
| `Listed` / `Bought`  | routine          | Marketplace activity                     | indexer handle                                            |
| `AuctionCreated`     | routine          | New auction live                         | indexer handle                                            |
| `BidPlaced`          | routine          | Cumulative bid landed                    | indexer handle                                            |
| `AuctionSettled`     | high             | Fund + NFT distribution                  | confirm indices in DB within 2 s                         |
| `AuctionReclaimed`   | **critical**     | 7-day safety-valve fired                 | **page on-call; audit the auction; this is the only path that can refund the winner's residual (no auto-resolve)** |
| `OfferMade` / `OfferAccepted` | routine | OfferBook activity               | indexer handle                                            |
| `PushFailed`         | **MEDIUM**       | A `.call{value: …}` reverted             | `runWithdrawalSweeper` will surface a banner via SSE; if PushFailed frequency >1/min investigate the receiver set |
| `AuctionStalled`     | **HIGH**         | Buyer-fault sandbox violation           | `settleUnstuck()` 7-day window opens; alert on-chain      |
| `EntriesHalted` / `EntriesUnpaused` | routine | Manager activity              | off-chain only (events are not stored on the cores)       |
| `AuditLog`           | informational    | Manager role + circuit-breaker writes   | audit only                                                |

## Backend health (Fly.io dashboard)

```bash
# Process / indexer liveness
curl -fsSL https://magicwebb.fly.dev/api/v1/healthz | jq .status
# → {"status":"ok","watcher":"alive","uptime_s":3124,"last_block":4781209}

# SSE fan-out
curl -fsS -N --max-time 5 https://magicwebb.fly.dev/events | head -c 64
# → ": connected\n\n"
```

| Alert class | Signal                          | Auto-resolve? |
|:------------|:--------------------------------|:--------------|
| Indexer stall | `last_block` 60 s behind head | yes; backfill picks up on next tick |
| Watcher poll 5xx | RPC upstream errors             | yes; rpcpool failover + sticky retry |
| Keeper advisory-lock lost | `<CONTRACT_ADMIN>` rotated or `<KEEPER_BOT>` lost | **NO** — on-call must investigate |
| Postgres replication lag | Fly metrics `pg_lsn` drift      | yes; backfill re-runs idempotent |
| Fly.io region failover | 2xx rate < 99% for 5 min      | yes; DNS failover + warm spare |

## PushFailed investigation playbook

```bash
# 1. Get the past 24 h of PushFailed events.
cast logs --rpc-url https://flare-api.flare.network/ext/C/rpc \
  --from-block $(($(cast block latest -n 86400 --rpc-url …) - 1)) \
  --to-block latest \
  --address $MARKETPLACE_ADDR,$AUCTION_ADDR,$OFFERBOOK_ADDR \
  --event "PushFailed(address,uint256)"

# 2. Cross-reference each `to` address against on-chain
#    pendingReturns().
cast call $AUCTION_ADDR "pendingReturns(address)(uint256)" $TO_ADDR \
  --rpc-url https://flare-api.flare.network/ext/C/rpc

# 3. If the receiver is a contract, simulate receive():
cast estimate $TO_ADDR --rpc-url …  # contract with empty value
```

If a receiver is a contract WITHOUT a payable `receive() / fallback()`
that has been holding escrow for >7 days, **page on-call**: this is
the only path for funds to be permanently trapped, and even there
the receiver owner can deploy a NEW contract that proxies to a
correct wallet and call `withdrawRefund()`.

## Keeper advisory-lock health

The keeper is single-flight via Postgres advisory-lock (`SELECT
pg_try_advisory_lock(<KEEPER_KEY_HASH>)`). If two replicas
simultaneously hold the lock (rare split-brain via network
partition), `WaitKeeperLock` rejects both, and the keepper logs
`keeper gate: acquisition failed`. 

```bash
# Check who currently holds the advisory lock.
psql "$DATABASE_URL" -c "SELECT * FROM pg_locks WHERE locktype='advisory' LIMIT 5;"

# Force-release if stuck (atomic; only if you're SURE no real keeper is running):
psql "$DATABASE_URL" -c "SELECT pg_advisory_unlock(<KEEPER_KEY_HASH>);"
```

## FTSO / State-Connector status

MagicWebb does NOT consume FTSO or State Connector feeds. The
contracts are pure escrow + auction/offer logic; the chain-truth
source is block timestamps (≥ 12 s granularity on Flare).

If a future protocol addition uses FTSO:

- Verify FTSO deliverer addresses against
  `https://gitlab.com/flarenetwork/flare-smart-contracts/-/tree/master/contract`
- Read feed prices via `FtsoV2Interface.getFeedById` only — NEVER
  inline a feed address; that was the FTSO-spoof attack class in
  FIP-12 era.
- For State Connector attestation (cross-chain NFT bridge use cases):
  validate finality window (≥ 60 s on Flare) before acting on a
  proof.

## MEV sandwich detection

AuctionHouse applies `MIN_BID_INCREMENT` (0.5% / 5% floor configurable
per auction) as the leading-bid floor. Front-running attackers must
outbid AND be outbid, costing capital on every failed attempt.

```sql
-- Detect potential sandwich patterns on a single auction:
SELECT block_number, encoded_bidder, tx_hash, wei
FROM auction_bids
WHERE auction_id = $AUCTION_ID
ORDER BY block_number, log_index;
-- Pattern: same leading bidder address → outbid → re-leader in <2 blocks
-- is a sandwich if a MEV-bot tx sits between; surface for human review.
```

## Runbook: per-alert class

| Alert                   | Who pages         | First action                                                |
|:------------------------|:------------------|:-------------------------------------------------------------|
| `AuctionReclaimed`      | on-call (P0)     | Check the auction was actually stalled — false positives if a seller pre-emptively `settleUnstuck()` triggers the 7-day window |
| `PushFailed` rate >1/min| indexer operator | Enumerate receivers, identify non-payable contracts, cohort by deployer |
| Indexer 60 s behind     | indexer (auto)    | Wait one tick; if persistent, rotate `RPC_URLS` priority    |
| Keeper lock lost        | on-call (P1)     | Confirm the rotate was intentional (`<CONTRACT_ADMIN>` grantRole trace) |
| Postgres trailing       | DBA (P2)         | Check `pg_stat_replication`; if application-side pool saturated, deploy a fresh instance |

## === v29-Specific Monitoring ===

### F-01 SIWE chain-id mismatch spike

If the v29 chain-id substring check in `verifyHandler` increases
rejection rate above ~0.1% of total verifications:

- Investigate whether the `WALLET_MISCONFIG` herd is using a sticker
  wallet that auto-flips `chainId` between testnet + mainnet.
- Or whether `CHAIN_ID` env changed mid-flight without coordinated
  Redis cache clear (the page would otherwise re-render with stale
  chain metadata).
- Or whether a phishing ring is now replaying testnet-signed
  payloads.

### F-02 transfers-chunk abort

If `runWatcher` logs `watcher: range failed, will retry` more than
once every 30 s, an RPC is stalling on header lookups; rotate
`RPC_URLS` priority.

### F-03 keeper gas cap clamp

If `log.Warn("keeper: feeCap above max; clamping")` fires more than
~3 times per day, network fees might be spiking. Either:
- Reconsider `KEEPER_MAX_FEE_CAP_GWEI` (raise if economically OK).
- Hold the keeper off-air until fees normalize.
- Or accept that the keeper queue is backed up and gas spend is
  capped.

## === Keepalive: tests + audits replay nightly ===

A nightly cron in Fly-sidekick runs (advised):

```bash
0 4 * * * cd /app && go test ./internal/... 2>&1 | tee nightly-test.log
```

And a weekly auditor-side rerun (per external tester):

```bash
cd contracts && forge test --match-path test/AuditFuzz.t.sol -vv
slither . --filter-paths 'lib/|test/'
```

Any regression fails the cron -> emails on-call.

---

## Appendix — Full event signature cheat-sheet

```
Listed(address indexed seller, address indexed collection, uint256 indexed tokenId, uint128 price, uint64 endsAt, uint8 standard)
Bought(address indexed seller, address indexed buyer, address indexed collection, uint256 tokenId, uint128 price)
Cancelled(address indexed seller, address indexed collection, uint256 indexed tokenId)
AuctionCreated(uint256 indexed id, address indexed seller, address indexed collection, uint256 tokenId, uint128 reserve, uint64 endsAt)
BidPlaced(uint256 indexed id, address indexed bidder, uint256 cumulative, bool newLeader)
AuctionSettled(uint256 indexed id, address indexed winner, address seller, uint128 amount)
AuctionStalled(uint256 indexed id, address indexed buyer, uint64 stalledAt)
AuctionReclaimed(uint256 indexed id, address indexed winner, uint256 refundWei)
OfferMade(address indexed collection, uint256 indexed tokenId, address indexed bidder, uint128 principal, uint128 units, uint64 expiresAt, uint8 standard)
OfferAccepted(address indexed collection, uint256 indexed tokenId, address indexed bidder, address seller)
OfferRejected(address indexed collection, uint256 indexed tokenId, address indexed bidder)
PushFailed(address indexed to, uint256 amount)
WithdrawRefunded(address indexed to, uint256 amount)   // emitted on auction-side settle losers
RefundedExpired(address indexed collection, uint256 indexed tokenId, address indexed bidder, uint128 principal)
```

Indices are designed so an off-chain indexer can join cheaply with
Postgres WITHOUT recomputing ABI fields — see
`backend/internal/db/migrations/002_indexes.sql`.

