# MagicWebb — system breakdown (v3.6)

The picture book. `ARCHITECTURE.md` is the map and the reference tables; this
document is every mechanism drawn out — where the code runs, what a request
touches, how the indexer and keeper loop turns chain events into a live page,
the exact state machines behind listings, auctions and offers, who may do what,
how control of the contracts moves and ends, how a badge is decided, and what
the product looks like. Diagrams are Mermaid; GitHub renders them, and the copy
served by the app at `/docs/system` renders them in the browser in the active
colour scheme. `app/src/pages/docs/system.md` mirrors this file minus the
screenshot section — edit both, or regenerate the mirror
(`python tools/mirror-system-doc.py`).

Companions: `USER_CAPABILITIES.md` / `/docs/capabilities` (what each user can do),
`NETWORKS.md` (provisioning), `MONITORING.md` (metrics + alerts),
`UPGRADE_RUNBOOK.md` (upgrades, admin rotation, Safe, sealing),
`DEPLOY_CHECKLIST.md`, `RUNBOOK_RESTORE.md`, `DESIGN.md`.

## 1. Deployment topology

One Docker image, built once per push to `main`, runs three times — one process
per network. Nothing is shared between networks except the image and the code
paths; every app has its own Postgres, its own optional Redis, its own keeper
wallet and its own contract set (or none, in read-only mode).

```mermaid
flowchart TB
  subgraph GH["GitHub · push to main"]
    CI["ci.yml<br/>go build/vet/race · forge · slither · gitleaks<br/>astro check · vitest · Playwright<br/>proto-drift · governance check"]
    DEP["deploy.yml<br/>build ONCE → image sha-{commit}<br/>matrix deploy · ghcr mirror (non-fatal)"]
  end
  subgraph FLY["Fly.io · region sin · one machine per app"]
    A1["magicwebb<br/>Coston2 · 114 · TRADING"]
    A2["magicwebb-songbird<br/>Songbird · 19 · read-only"]
    A3["magicwebb-flare<br/>Flare · 14 · read-only"]
  end
  DEP -->|"same image · env from deployments/*.json"| A1
  DEP --> A2 & A3
  A1 --- D1[("Neon<br/>still-mountain")]
  A2 --- D2[("Neon<br/>bitter-pond")]
  A3 --- D3[("Neon<br/>royal-paper")]
  A1 -.-> R1[("Redis (optional)<br/>mw:114:*")]
  A1 --> K1["keeper wallet<br/>KEEPER_KEY Fly secret"]
  A1 -->|"RPC rotation"| C2["Coston2 contracts · block 34905078<br/>Marketplace · AuctionHouse · OfferBook (UUPS)<br/>MarketplaceManager (plain)"]
  K1 -->|"settle · refundLosers<br/>forceCancel · fee sweep"| C2
  A1 <-.->|"NETWORK_URLS"| A2
  A1 <-.-> A3
```

| Network | Chain | App | Status | Contracts | Data |
|---|---|---|---|---|---|
| Coston2 (testnet) | 114 | `magicwebb.fly.dev` | trading | `deployments/coston2.json` (v3.5 set, 2026-09-04) | Neon `still-mountain-83246431` |
| Songbird | 19 | `magicwebb-songbird.fly.dev` | read-only | none yet (wave 9, owner-gated) | Neon `bitter-pond-73256956` |
| Flare | 14 | `magicwebb-flare.fly.dev` | read-only | none yet (wave 9, owner-gated) | Neon `royal-paper-82216877` |

What "read-only" means: the binary boots, serves the UI, API, wallet connect
and cross-network profiles; the indexer, keepers and verifier idle; the UI shows
a dismissible browse-only banner and empty states that point at the trading
origin. Filling `deployments/<network>.json` and setting `<NET>_ENABLED=true` in
CI is the entire enable procedure (`NETWORKS.md`).

Per-network tuning — finality depth, poll cadence, keeper tick, gas caps,
default RPCs, rate-limit tier, faucet, identity — lives in
`backend/internal/chain/profile/{coston2,songbird,flare}.go` and is validated at
boot (`Validate()` — bad values fail the process, not a request). The browser
mirror is `app/src/lib/chains/`; a Go test keeps the two and the deployment
records in agreement.

## 2. The request path (one origin)

```mermaid
flowchart TB
  UI["Browser · Astro static pages · Svelte 5 islands<br/>React wallet island (Reown AppKit · wagmi · viem)"]
  UI -->|"HTTPS · WSS"| SEC["Fly edge · TLS · Fly-Client-IP<br/>security headers · CSP · HSTS<br/>rate limit (Postgres counters, tier from profile)"]
  subgraph FACES["one Go process · one chain"]
    ST["embedded dist/<br/>pages · assets · /docs"]
    REST["/api/v1 REST<br/>listings · auctions · offers · token<br/>collections · search · trending · activity<br/>profile · wallet · server-time · governance"]
    IMG["/api/v1/img/:sha256<br/>content-addressed image store"]
    OBS["POST /api/v1/tx/observe<br/>instant lane"]
    AUTH["/auth · SIWE → JWT<br/>saved searches · notifications<br/>profile edits · webhooks"]
    WS["/ws hub<br/>token: collection: user: tx: activity<br/>retry {from_seq} replay"]
    GQL["/graphql · /graphql/ws<br/>gqlgen queries + subscriptions"]
    CN["Connect / gRPC MarketplaceService<br/>unary + Subscribe* streams"]
    HP["/healthz · /readyz<br/>/internal/metrics (METRICS_TOKEN)"]
    ST ~~~ OBS ~~~ GQL
    REST ~~~ AUTH ~~~ CN
    IMG ~~~ WS ~~~ HP
  end
  SEC --> FACES
  REST -->|"read cache · memory or Redis<br/>(cache.Key chain-namespaced)"| DB[("Neon Postgres<br/>reader pool · writer pool")]
  IMG & AUTH & GQL & CN --> DB
  OBS --> IDX["indexer.ObserveTx<br/>receipt logs → same handlers"] --> DB
  IDX --> BC[("sse.Broadcaster<br/>seq · replay ring · gRPC mesh")]
  BC --> WS & GQL & CN & WH["webhooks"]
```

Every write is a wallet transaction; the backend never holds a user key. The
page simulates the call first (custom errors decode to plain-language titles),
opens the wallet on a Review step, and after one confirmation posts the hash to
the instant lane. `ObserveTx` fetches the receipt and dispatches its logs
through the same idempotent handlers the watcher uses, publishes `tx-indexed`,
and the page's optimistic state settles with one refetch. The watcher
re-dispatches the same logs `ReorgSafety` blocks later — upserts, no-op.

```mermaid
sequenceDiagram
  participant P as Page (TxModal / runTx)
  participant W as Wallet
  participant C as Contract
  participant B as Backend
  participant S as /ws
  P->>P: requireWallet · simulateContract (errors decoded early)
  P->>P: Review step — you pay / seller receives / refundable
  P->>W: writeContract → "Confirm in your wallet"
  W-->>P: tx hash → "Pending on Coston2 (~2s)"
  P->>S: subscribe tx:{hash}
  C-->>P: receipt (1 confirmation) → "Done" · optimistic reducer applied
  P->>B: POST /api/v1/tx/observe {hash}
  B->>C: eth_getTransactionReceipt
  B->>B: handlers upsert (tx_hash, log_index) · Publish tx-indexed
  S-->>P: tx-indexed → settleAfterTx: one refetch → "Live on the marketplace"
```

## 3. Indexer + keeper loop

All long-lived workers run inside the single server process under
`supervise()` — a panicking worker is logged with its stack and restarted
after a short delay; a worker that returns cleanly (no keeper key, read-only
network) stays stopped. The keeper workers additionally pass through a
**single-flight gate**: with two or more machines, `internal/keeper`'s gRPC
election (lowest instance UUID leads, 1 s heartbeat, three missed beats fail
over) guarantees exactly one broadcaster per network.

```mermaid
flowchart TB
  RPC["rpcpool · failover rotation · eth_getLogs"]
  subgraph WATCH["watcher (poll = profile.PollInterval, head − profile.ReorgSafety)"]
    F["filter: 3 cores + manager + tracked collections<br/>topics: trading · governance · Transfer*"]
    H["handlers · idempotent upserts keyed (tx_hash, log_index)"]
  end
  RPC --> F --> H
  H --> DB[("Postgres · 44 goose migrations at boot<br/>(numbered to 045, 031 skipped)")]
  H --> BC[("sse.Broadcaster")]
  BC --> OUT["/ws · GraphQL subs · Connect streams · webhooks · notifications"]
  subgraph WORK["supervised workers"]
    META["metadata → tokenURI → fetch (SSRF-safe)<br/>→ sniff → sha256 → image store → image_uri"]
    RETRY["image retry"]
    VER["verifier · ERC-165 + metadata<br/>⇒ collections.verified"]
    SCORE["score → trending"]
    LEXP["listing expiry sweeper · active=false<br/>(off-chain; buy reverts Expired on-chain)"]
    OEXP["offer expiry sweeper"]
    WDR["withdrawal sweeper · pendingReturns()<br/>→ pending_withdrawals + notification"]
    OWN["ownership repair<br/>stale nft_ownership vs on-chain holder"]
    GOV["governance events → governance_events<br/>counter · SSE governance · security alerts"]
    META ~~~ SCORE ~~~ WDR
    RETRY ~~~ LEXP ~~~ OWN
    VER ~~~ OEXP ~~~ GOV
  end
  DB --> WORK
  subgraph KEEP["keeper workers (KEEPER_KEY · single-flight)"]
    AK["auction keeper · every KeeperTick (1s / 2s)<br/>settle ended auctions · forceCancel after 3 days"]
    LR["loser refund sweeper<br/>refundLosers in batches"]
    FEE["fee sweeper · Safe Allowance Module<br/>(probe bytecode first)"]
    OPS["ops health · keeper balance + fee cap<br/>head-lag alert · image-store gauge + LRU eviction"]
    CLEAN["expired-listing clean · every 2nd KeeperTick<br/>Marketplace.cleanExpired per row (chain_cleaned=false)"]
    AK ~~~ FEE
    LR ~~~ OPS
  end
  DB --> KEEP
  WORK ~~~ KEEP
  KEEP -->|"signed tx via keeper key"| RPC
  OPS --> AL["alerts · Discord · Prometheus Alertmanager · SMTP · hourly cooldown"]
```

| Worker | Cadence | Source |
|---|---|---|
| watcher | `PollInterval` (profile), `ReorgSafety` behind head | `indexer/runner.go` `runWatcher` |
| instant lane | on demand (`POST /tx/observe`) | `indexer/observe.go` |
| metadata / image retry | continuous / periodic | `indexer/metadata.go` |
| verifier | `VerifierTick` (profile) | `internal/verifier` |
| score (trending) | periodic | `runner.go` `runScoreWorker` |
| listing / offer expiry sweepers | 1 s (DB flips only) | `runner.go` |
| withdrawal sweeper | `RefundTick` (profile; default 30 s) | `runner.go` `runWithdrawalSweeper` |
| ownership repair | periodic | `indexer/ownership_repair.go` |
| auction keeper | `KeeperTick` (1 s / 2 s) | `runner.go` `runAuctionKeeper` |
| expired-listing clean (on-chain `cleanExpired`) | every 2nd `KeeperTick` | `runner.go` `cleanExpiredListings` |
| loser refunds | `RefundTick` (profile; default 45 s) | `indexer/keeper_refund.go` |
| fee sweeper | `FeeSweepTick` (5 min / 10 min) | `indexer/keeper_feesweep.go` |
| keeper-health · lag-alert · image-store | `FeeSweepTick` · 15 s · 5 min | `indexer/ops_health.go` |
| governance | with the watcher | `indexer/governance.go` |

## 4. State machines

Money never depends on the keeper: every terminal state has a self-service exit
(`withdrawRefund`, `refundLosers` — permissionless after settlement,
`withdrawLoserFunds` — non-leaders before settlement, `refundExpiredOffer`,
`settle`, `forceCancel`). The keeper only makes those exits happen sooner.

### 4.1 Listing (non-custodial — the NFT stays in the seller's wallet)

```mermaid
stateDiagram-v2
  [*] --> Active: list · list1155 · batchList ≤50 (duration → expiresAt on-chain, free)
  Active --> Active: editPrice (seller)
  Active --> Sold: buy (msg.value == price · NFT → buyer · 98% → seller · 1.5% platform · 0.5% keeper)
  Active --> Cancelled: cancel (seller)
  Active --> Expired: block.timestamp > expiresAt (buy reverts Expired · keeper calls cleanExpired)
  Sold --> [*]
  Cancelled --> [*]
  Expired --> [*]
```

Off-chain the expiry sweeper flips `listings.active=false` every second so the
UI stops showing it, and the keeper drives the on-chain `Marketplace.cleanExpired`
(keeper-gated) every second `KeeperTick` for rows with `chain_cleaned=false` —
owner decision 2026-08-31, everything that expires is handled instantly; the
resulting `Cancelled` event marks the row so it is never re-sent. A stale
ownership row that would block `buy` preflight is repaired by the ownership
worker against the live holder. Transfers of a listed NFT (seen via `Transfer`)
deactivate the seller's own listing.

### 4.2 Auction (escrowed, cumulative bids, flat +1 native increment)

```mermaid
stateDiagram-v2
  [*] --> Live: create · create1155 (reserve ≥ 1 native · duration)
  Live --> Live: bid (must beat reserve or leader + 1 native, else BidTooLow reverts — nothing is parked)
  Live --> Live: anti-snipe — a lead-changing bid in the last 3 min resets endsAt to now + 3 min (leader top-ups do not extend) · hard cap originalEndsAt + 30 min (AuctionExtended)
  Live --> Live: withdrawLoserFunds (non-leader self-service, only before settlement)
  Live --> Cancelled: cancelEarly (seller, only while no bid leads)
  Live --> Ended: endsAt reached (no transaction)
  Ended --> Settled: settle — keeper within ~1 tick · or seller · or winner
  Ended --> ForceCancelled: forceCancel after endsAt + 3 days — keeper · seller · winner (everyone refunded)
  Settled --> Settled: refundLosers batches — permissionless (the keeper drives it every RefundTick; any loser can call it with their own address)
  Settled --> [*]
  Cancelled --> [*]
  ForceCancelled --> [*]
```

Payout is pull-safe: if pushing proceeds to the seller fails (`PushFailed`) the
amount lands in `pendingReturns`, which the profile's Refunds card reads
directly on-chain (the withdrawal sweeper separately re-verifies bidders seeded
by `LoserRefunded` / `RefundPushed` and sends a "refund" notification).
`AuctionSettlementFailed` is the other outcome: the NFT could not be delivered
(seller moved it or revoked approval), so no fee is taken, the seller is paid
nothing, the winner's escrow is pushed back (pull fallback) and the row is
marked `cancelled`. A losing bidder can pull escrow with `withdrawLoserFunds`
while the auction is live and is never locked behind a position that cannot
win. DB `auction_status`: `active → settled | cancelled`, plus
`losers_refunded`.

### 4.3 Offer (escrowed native, fee only at accept)

```mermaid
stateDiagram-v2
  [*] --> Gate: collection owner (ERC-173) setOfferEligible(true) — otherwise OffersNotEligible
  Gate --> Pending: makeOffer · makeOffer1155 (escrow + duration)
  Pending --> Pending: makeOffer again edits the position — old principal refunded, new one escrowed, original expiry kept
  Pending --> Accepted: acceptOffer (owner · NFT → bidder · escrow − 2% → owner)
  Pending --> Cancelled: cancelOffer (bidder · full refund)
  Pending --> Rejected: rejectOffer (owner · full refund)
  Pending --> Expired: expiresAt passed → refundExpiredOffer (bidder self-service, or the keeper sweep)
  Accepted --> [*]
  Cancelled --> [*]
  Rejected --> [*]
  Expired --> [*]
```

`acceptOffer` re-checks that the offer's principal still matches
(`PrincipalChanged`) and that the acceptor still owns and has approved the
token. DB `offer_status`: `pending → accepted | cancelled | expired`.

## 5. Role × action matrix

Three visitors, and a person is all three at different moments: **Viewer** (no
wallet), **Buyer** (wallet on this network, does not own the NFT), **Seller /
owner** (owns the NFT or created the listing / auction). Every write is a wallet
transaction on the network you are viewing.

| Action | Viewer | Buyer (wallet) | Seller / owner |
|---|---|---|---|
| Browse, search, view token / collection / profile / activity | ✓ | ✓ | ✓ |
| Buy a listing | connect prompt | ✓ | hidden on own |
| List (721 / 1155 units), 15 durations, min 1 native | — | — | ✓ owner |
| Batch list up to 50 | — | — | ✓ 721 only |
| Change price / cancel listing | — | — | ✓ seller |
| Start auction (721 / 1155) | — | — | ✓ owner |
| Bid (+1 native over the lead, cumulative) | connect prompt | ✓ (not seller) | — |
| Withdraw when outbid | — | ✓ any time | — |
| Cancel auction | — | — | ✓ only with no bids |
| Settle after end | — | ✓ winner | ✓ seller (+ keeper auto, 1 s Coston2 / 2 s mainnets) |
| Cancel & refund everyone (3 d after end) | — | ✓ winner | ✓ seller (+ keeper auto) |
| Make offer (if the collection allows) | connect prompt | ✓ | — |
| Raise / withdraw own offer (full refund) | — | ✓ | — |
| Accept / decline an offer | — | — | ✓ owner |
| Reclaim own expired offer (full refund) | — | ✓ bidder (+ keeper auto) | — |
| Enable offers for a collection | — | — | ✓ ERC-173 owner |
| Search, trending, activity, collection traits | ✓ | ✓ | ✓ |
| Save search / notifications / security alerts (SIWE) | disabled + hint | ✓ | ✓ |
| Edit own profile — name, bio, links, tag (SIWE, shared across networks) | — | ✓ | ✓ |
| See who controls the contracts (`/status`, `/api/v1/governance`) | ✓ | ✓ | ✓ |
| Switch network (keeps the path; reconnect on arrival) | ✓ | ✓ | ✓ |
| Theme: System / Light / Dark | ✓ | ✓ | ✓ |
| Anything admin | none exists | none | none |

No admin page, no login form, no roles in the product. The only privileged
key is the per-network on-chain **admin** (upgrades + keeper rotation) held by
the owner — see §6.

## 6. Governance lifecycle

One `MarketplaceManager` per network holds exactly two addresses: `keeper`
(settle · refund · clean — can never move or block funds) and `admin`
(`setKeeper`, instant UUPS upgrades with `upgradeDelay() == 0`, two-step
rotation, `renounceAdmin`). There is no grant path and nothing is pausable.

```mermaid
stateDiagram-v2
  [*] --> AdminHeld: DeployV34 — ADMIN_ADDR (EOA on testnet, Safe on mainnet) · KEEPER_ADDR · FEE_RECIPIENT_ADDR
  AdminHeld --> AdminHeld: setKeeper (KeeperSet)
  AdminHeld --> AdminHeld: queueUpgrade → upgradeTo back-to-back (UpgradeQueued · Upgraded) · cancelUpgrade · 7-day queue expiry
  AdminHeld --> PendingTransfer: transferAdmin(new) (AdminTransferStarted)
  PendingTransfer --> AdminHeld: acceptAdmin from the NEW key (AdminTransferred) — e.g. hand-off to a Safe
  PendingTransfer --> AdminHeld: cancelAdminTransfer (AdminTransferCancelled)
  AdminHeld --> Sealed: renounceAdmin (AdminRenounced) — one way, wipes any pending offer
  Sealed --> Sealed: no upgrades · no keeper rotation · users keep every exit
```

The trail is public. The watcher indexes nine governance topics into
`governance_events`; `GET /api/v1/governance` returns the live `admin`,
`pending_admin`, `keeper`, `upgrade_delay`, `admin_is_contract`, `renounced`,
the current implementation per proxy, the last 50 events and `keeper_health`;
`/status` shows "Admin & upgrades" tiles with a per-proxy pending-upgrade
warning; every signed-in wallet that has not opted out of `security_alerts`
receives a notification when control changes; operators get Discord /
Prometheus / SMTP alerts. `tools/check-governance.sh` reads the chain in CI,
`cmd/reindexgov` backfills the trail, and `tools/upgrade-cores.sh <network>`
performs a full three-core upgrade (deploy impls with immutables read from the
proxies → queue + install with `ADMIN_KEY` → verify → record `impls` /
`superseded_impls`). Mainnets deploy with a Gnosis Safe as admin from block one
(`UPGRADE_RUNBOOK.md` → "Safe as admin").

## 7. Badge decision tree

Two NFT-level signals, computed in one place (`backend/internal/db/badge.go`)
for every row type, plus the collection tier on detail headers. No badge is
ever granted by hand.

```mermaid
flowchart TB
  N["NFT row (listing · auction · offer · search · collection token · token page)"]
  N --> Q1{"collection verified?<br/>verifier: ERC-165 says 721/1155 AND metadata resolved"}
  Q1 -->|no| R1["reason unverified_collection"]
  Q1 -->|yes| Q2{"token has a name?"}
  Q2 -->|no| R2["reason no_metadata"]
  Q2 -->|yes| Q3{"image served from our store?<br/>/api/v1/img/{sha256} or inline data:"}
  Q3 -->|no| R3["reason image_missing"]
  Q3 -->|yes| Q4{"holder known?"}
  Q4 -->|no| R4["reason owner_unknown"]
  Q4 -->|yes| V["✓ Verified — verified=true, verified_reason=[]"]
  R1 & R2 & R3 & R4 --> NV["no checkmark — verified_reason names each unmet check so the UI can say why"]
  N --> S1{"holder == collection creator?<br/>(ERC-173 owner / deployer)"}
  S1 -->|no| NS["no ★"]
  S1 -->|yes| S2{"nft_tokens.minter == creator?<br/>(first Transfer from 0x0, migration 044)"}
  S2 -->|no| NS
  S2 -->|yes| ST["★ Creator — creator_is_owner=true"]
```

| Signal | Where it shows | Source fields |
|---|---|---|
| ✓ (sky check) | glyph on every card, text + Hint on token / auction / collection headers | `verified`, `verified_reason[]` |
| ★ Creator | creator's profile header ("Creator of …"), collection header next to "Created by", an NFT whose holder is the creator and minted it | `creator_is_owner`, `/token → minter, creator{address, display_name, tag}` |
| Collection pill (Listed collection → Verified → Authentic) | detail headers | `collections.verified`, `verified_reason{standard_ok, metadata_ok, creator_known}` |
| Holder tag | token page | profile `tag`, else derived from the address |

Activity rows carry no `collection_verified` column, so the checkmark is withheld
there rather than guessed; offers imply a holder, so ✓ answers but ★ never
applies.

## 8. Real-time: one spine, three faces

`sse.Broadcaster` is the only publisher (sequence-numbered, replay ring, fanned
across machines over the gRPC mesh). Consumers:

| Face | For | Shape |
|---|---|---|
| `/ws` | the product UI | channels `token:` `collection:` `user:` `tx:` `activity`, `retry {from_seq}` replay from the seq-numbered ring; islands subscribe only to what they show (TokenPage: its token + collection; NFTGrid: listing changes, page 1 refetch) |
| GraphQL subscriptions (`/graphql/ws`) | third-party dashboards | hydrated objects, cost-limited |
| Connect server streams | bots and keepers | `SubscribeListings` `SubscribeAuctions` `SubscribeActivity` `SubscribeNotifications` (SIWE), declared in `marketplace.proto`, regenerated with a pinned toolchain, CI `proto-drift` gate |
| Webhooks | integrations | per-type subscriptions incl. `governance` |

`window.MW_BLOCK_TIME_MS` / `MW_CONFIRMATIONS` (from `/api/v1/server-time`, per
profile) drive the pending ETA copy; optimistic reducers (`app/src/lib/optimistic.ts`)
apply the expected row change at "Done" and `settleAfterTx` refetches once when
`tx-indexed` arrives (or after 2 s).

## 9. Ops health and observability

| Endpoint | What it answers |
|---|---|
| `/healthz` | liveness |
| `/readyz` | DB + indexer readiness |
| `/api/v1/indexer/slo` | head lag in blocks (text) |
| `/api/v1/governance` | control + `keeper_health` |
| `/internal/metrics` | Prometheus (optional `METRICS_TOKEN`) — `magicwebb_keeper_balance_wei`, `_balance_level{level}`, `_underpriced_total`, `_feecap_below_network_ticks`, `magicwebb_head_lag_blocks`, `_head_lag_alerting`, `magicwebb_imagestore_bytes` / `_cap_bytes` / `_evicted_total`, governance counters |
| `/status` | the human page: build SHA (`X-MW-Build-SHA`), indexer lag, automation costs, keeper balance, admin & upgrades |

Alerts (`MONITORING.md` → "Ops health"): `KeeperBalanceLow` (warning below
`KEEPER_MIN_BALANCE_WEI`, critical below 20 % of it), `KeeperFeeCapBelowNetwork`
(three ticks with `eth_gasPrice` above the keeper's cap — the 2026-08-31
starvation signature), `IndexerHeadLag` (> 30 blocks for 2 min, with a
resolved notice), `ImageStoreNearCap` (only after LRU eviction of unreferenced
blobs from 80 % down to 70 %). Backups and the quarterly restore drill:
`RUNBOOK_RESTORE.md`.

## 10. The product (v3.6 screenshots)

Captured from the live Coston2 app with `npm run shots` (Playwright,
`app/playwright.shots.config.ts`; 1440×900 and 390×844, light and dark;
the listings page uses the e2e sample fixtures because the live testnet had no
open listings on capture day). Files live in `docs/images/v3.6/`.

| Page | Desktop · light | Desktop · dark | Mobile · light | Mobile · dark |
|---|---|---|---|---|
| Home | ![](images/v3.6/home-desktop-light.jpg) | ![](images/v3.6/home-desktop-dark.jpg) | ![](images/v3.6/home-mobile-light.jpg) | ![](images/v3.6/home-mobile-dark.jpg) |
| Listings (sample data) | ![](images/v3.6/listings-desktop-light.jpg) | ![](images/v3.6/listings-desktop-dark.jpg) | ![](images/v3.6/listings-mobile-light.jpg) | ![](images/v3.6/listings-mobile-dark.jpg) |
| Token | ![](images/v3.6/token-desktop-light.jpg) | ![](images/v3.6/token-desktop-dark.jpg) | ![](images/v3.6/token-mobile-light.jpg) | ![](images/v3.6/token-mobile-dark.jpg) |
| Collection | ![](images/v3.6/collection-desktop-light.jpg) | ![](images/v3.6/collection-desktop-dark.jpg) | ![](images/v3.6/collection-mobile-light.jpg) | ![](images/v3.6/collection-mobile-dark.jpg) |
| Auctions | ![](images/v3.6/auctions-desktop-light.jpg) | ![](images/v3.6/auctions-desktop-dark.jpg) | ![](images/v3.6/auctions-mobile-light.jpg) | ![](images/v3.6/auctions-mobile-dark.jpg) |
| Auction #1 (settled) | ![](images/v3.6/auction-desktop-light.jpg) | ![](images/v3.6/auction-desktop-dark.jpg) | ![](images/v3.6/auction-mobile-light.jpg) | ![](images/v3.6/auction-mobile-dark.jpg) |
| Offers | ![](images/v3.6/offers-desktop-light.jpg) | ![](images/v3.6/offers-desktop-dark.jpg) | ![](images/v3.6/offers-mobile-light.jpg) | ![](images/v3.6/offers-mobile-dark.jpg) |
| Profile (creator) | ![](images/v3.6/profile-desktop-light.jpg) | ![](images/v3.6/profile-desktop-dark.jpg) | ![](images/v3.6/profile-mobile-light.jpg) | ![](images/v3.6/profile-mobile-dark.jpg) |
| Search | ![](images/v3.6/search-desktop-light.jpg) | ![](images/v3.6/search-desktop-dark.jpg) | ![](images/v3.6/search-mobile-light.jpg) | ![](images/v3.6/search-mobile-dark.jpg) |
| Status | ![](images/v3.6/status-desktop-light.jpg) | ![](images/v3.6/status-desktop-dark.jpg) | ![](images/v3.6/status-mobile-light.jpg) | ![](images/v3.6/status-mobile-dark.jpg) |
| Docs | ![](images/v3.6/docs-desktop-light.jpg) | ![](images/v3.6/docs-desktop-dark.jpg) | ![](images/v3.6/docs-mobile-light.jpg) | ![](images/v3.6/docs-mobile-dark.jpg) |
| Start here | ![](images/v3.6/docs-start-here-desktop-light.jpg) | ![](images/v3.6/docs-start-here-desktop-dark.jpg) | ![](images/v3.6/docs-start-here-mobile-light.jpg) | ![](images/v3.6/docs-start-here-mobile-dark.jpg) |
| System breakdown (this page in the app) | ![](images/v3.6/docs-system-desktop-light.jpg) | ![](images/v3.6/docs-system-desktop-dark.jpg) | ![](images/v3.6/docs-system-mobile-light.jpg) | ![](images/v3.6/docs-system-mobile-dark.jpg) |

## 11. Where the code is

See `ARCHITECTURE.md` §7 for the repository map. The short version: `contracts/`
(Foundry, UUPS cores + plain manager, `script/DeployV34.s.sol`, `DeploySafe.s.sol`),
`backend/` (Go 1.26 + Fiber; `cmd/server` boots migrate → connect → guard →
indexer → http; `internal/chain/profile` is the per-network table; `internal/ops`
the health snapshot), `app/` (Astro 7 + Svelte 5 islands + one React wallet
island; `src/lib/tx` write flows, `src/lib/ws`, `src/lib/chains`, `src/lib/optimistic.ts`),
`deployments/` (address records — the source of truth), `tools/` (upgrade-cores,
check-governance, check-deployments), `.github/workflows` (ci · deploy · nightly ·
audit · codeql).
