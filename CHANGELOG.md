# Changelog

All notable changes to MagicWebb — contracts, backend, frontend,
docs — are tracked here. Versions follow the audit ledger cadence
(`v19` = wallet.js audit, `v20` = Solidity audit, `v21` = indexer +
DB + API + docs, `v22..v28` = iter-audit fixes rolled in from
multiple rounds, `v29` = full-stack chain-id + gas-cap + chunk-abort
hardening).

**Versioning changed after v29.** The `vNN` audit-ledger numbering
tracked audit rounds, not the product. From `v3.0` on, releases are
numbered by the deployed contract/protocol generation instead —
`v3.x` is the third contract generation, the one with
`MarketplaceManager`, timelocked upgrades and on-chain durations.
The two schemes do not overlap: `v29` precedes `v3.0` in time.

## v3.6 — 2026-09-07 — Governance trail, NFT badges, non-technical UX, ops hardening, both-scheme a11y gate

Live on Coston2 on the v3.5 contract set (block 34905078; Marketplace
`0xa6fbad08…`, AuctionHouse `0x0ADe5F5A…`, OfferBook `0x358d4504…`, manager
`0x14C3b1Ba…`). Songbird and Flare stay browse-only. Eight waves on `main`
(91f64b7 → this tag), each deployed on push — `X-MW-Build-SHA` verified on all
three apps after every wave. **Owner-gated, not in this release:** the Coston2
core upgrade to the 15-duration implementations (`tools/upgrade-cores.sh
coston2` + `cmd/reindexgov`) — until then a 10-minute duration reverts
`InvalidDuration` on-chain (decoded before the wallet opens) — and the wave 9
mainnet go-live (Safe owners, deployer funding, fee recipient, keeper secrets,
Neon snapshot schedule).

### Contracts / tooling (wave 2)

- **15 durations**: the 10-minute option is back (owner directive 2026-09-06);
  NatSpec, tests and the frontend agree; gas baseline regenerated.
- `tools/upgrade-cores.sh <network> [--dry-run|--verify|--rollback]`: deploys
  the three implementations with immutables read from the live proxies, queues
  and installs each with `ADMIN_KEY` (instant), verifies through the proxies
  and records `impls` / `superseded_impls` in `deployments/<network>.json`.

### Governance trail (wave 1)

- The watcher now listens to the manager and to every core's upgrade events
  (`KeeperSet`, `AdminTransferStarted/Cancelled/Transferred`, `AdminRenounced`,
  `UpgradeQueued/Cancelled`, ERC-1967 `Upgraded`) — migration 043
  `governance_events`, Prometheus counter, `governance` SSE / webhook event.
- `GET /api/v1/governance`: live admin / pending admin / keeper / upgrade delay
  (a Safe admin is labelled `admin_is_contract`), current implementations, last
  50 events; `{deployed:false}` on read-only networks.
- `/status` "Admin & upgrades" tiles + event table with a per-proxy
  pending-upgrade warning; a `system` notification to every opted-in wallet
  when control changes (`profiles.security_alerts`, `PUT /profile`,
  `GET /profile/:addr/prefs`); `cmd/reindexgov` backfill;
  `tools/check-governance.sh` as a CI job.

### Badges (waves 3–4)

- NFT-level **✓**: collection verified AND name AND image served from our store
  AND holder known, computed in one place (`db/badge.go`) for listings,
  auctions, offers, search, activity, collection tokens and token detail;
  unmet checks are named in `verified_reason[]`.
- **★ Creator** only where the owner decided (D3): holder == collection creator
  AND minted by that creator (migration 044 `nft_tokens.minter`, recorded from
  `Transfer` from `0x0`, backfilled by `reindexgov -collections`).
- `/token` gains `minter` + `creator {address, display_name, tag}`; `/profile`
  gains `creator_of` + `verified_creator`; verifier cadence from the profile.
- One `Badge.svelte` (check / pill / creator) replaces three components and a
  dead global; "Created by" links to the creator's profile on every surface.

### UX for non-technical users (waves 5a–5c)

- P1 fixes: token page reads the indexed image first; cards use 256px
  renditions; deep links `/token#offer` and `/profile#nfts`; success cards +
  next actions for every refund path; disconnected-wallet wait 120s → 30s.
- **Light theme complete** (tokens only, light tints defined) + header toggle
  System → Light → Dark (`[data-theme]` wins over the OS scheme, applied before
  first paint).
- **TxModal Review step**: nothing reaches the wallet until Confirm; "Step X of
  N"; approval-row Hint; busy state explains why Cancel is disabled. Inline
  Hints at the decision point ("What is an offer?", "What happens to my bid?").
- No-wallet path (`NoWalletSheet`), `FirstRun` strip on home / token / auction /
  collection, poster hero, network switcher built from `networkStatuses()` with
  a HEAD probe + arrival toast, banner priority (wrong-network > browse-only >
  testnet).
- Optimistic UI: pure reducers (`lib/optimistic.ts`) + `settleAfterTx` (one
  refetch on `tx-indexed`, else after 2s) replace the 1.5s + 6s double timers;
  fixed the runner waiting on `hash` while the backend publishes `tx_hash`.
- WS scoping (NFTGrid listing-only, TokenPage token + collection channels), one
  `Skeleton.svelte`, the motion set, `--sticky-bar-h`, 44px touch targets on
  phones, `DurationPicker` two-row scroller, FAQ `#networks`.

### Backend layering + ops (waves 6a–6c)

- `chain/profile` split into per-network files with `Validate()` /
  `ValidateAll()` at boot; new `AllowanceModuleAddr` (fee sweeper probes the
  bytecode first) and `ConnectRateTier`; dead knobs wired — `KeeperTick` drives
  settlement (1s / 2s), `BlockTime` / `Confirmations` reach the UI via
  `/api/v1/server-time` + `window.MW_*`, `Mainnet` gates the testnet banner and
  refuses `RESET_ON_ADDRESS_CHANGE`, `DefaultRPCs` rotate so **`RPC_URL` is
  optional**, effective gas caps are validated. Activity cache keys are
  chain-namespaced; `internal/crypto` + `zigcrypto` deleted (zero callers);
  frontend `lib/chains/` directory mirror with a field-by-field parity test.
- **Ops hardening**: `internal/ops` snapshot; supervised `keeper-health`
  (balance levels, fee-cap watch, underpriced counter), `lag-alert` (> 30
  blocks for 2 min, resolved notice), `image-store` (gauge + LRU eviction of
  unreferenced blobs 80 % → 70 %, migration 045); Discord / Prometheus / SMTP
  alerts with hourly cooldown; nine new gauges; `/status` keeper-balance tile;
  `docs/RUNBOOK_RESTORE.md`; `NETWORKS.md` Neon ids corrected.
- **Connect subscriptions declared in the proto** (`SubscribeListings`,
  `SubscribeAuctions`, `SubscribeActivity`, `SubscribeNotifications`),
  regenerated with pinned protoc 28.3 / protoc-gen-go 1.36.11 /
  protoc-gen-connect-go 1.20.0; hand-rolled shims deleted; `make proto`,
  `proto-check`; CI `proto-drift` job. Hotfix b778394: the ws test mock
  implements the four streams (`go vet ./...` on the whole module before any
  push).

### Tests / CI (wave 7)

- Playwright: new `dark` project; the axe contrast gate runs in **both**
  schemes on nine pages; touch-target and 12px sweeps cover token / auction /
  offers / profile; new flows (token happy path, `#nfts` tab, sticky bar
  variable + toast stacking at 390px, no-wallet sheet focus return, banner
  priority); the "unreachable sibling" switch is `test.fixme` (flaky, ~1 in 3);
  token-page cases allow the CI runner's 15s wagmi timeout (bf7d1c0).
- vitest durations parity: the app's fifteen durations equal the `DURATION_*`
  constants in `MarketplaceCore.sol`.
- Contrast fixes forced by the gate: TokenPage tokens-only, `--white-40` now
  tracks `--text-3`, new `--text-on-gold`, captions ≥ 12px.
- Nightly / audit gitleaks steps pass `GITHUB_TOKEN` (the anonymous owner
  lookup 403s on the runner's shared IP and reports "missing gitleaks license").

### Docs (wave 8)

- README rewritten for v3.6 (three networks, 2 % split, no admin, badges,
  stack, tests, deploy).
- `docs/SYSTEM_BREAKDOWN.md` + `/docs/system` (mermaid rendered client-side by
  a lazy-loaded mermaid 11 chunk, theme-aware; `wide` docs layout): deployment
  topology, request path + instant lane, indexer + keeper loop, listing /
  auction / offer state machines, role × action matrix, governance lifecycle,
  badge decision tree, real-time faces, ops health, screenshots. Mirror script
  `tools/mirror-system-doc.py`.
- Screenshots of every page — desktop + mobile, light + dark — in
  `docs/images/v3.6/` from `npm run shots` (`playwright.shots.config.ts`).
- `ARCHITECTURE.md` §6 / §7 / §8 (+ badges, governance, ops), `capabilities.md`
  (badges, who controls the contracts), `UPGRADE_RUNBOOK.md` "Safe as admin",
  `DEPLOY_CHECKLIST.md` mainnet rows, `DESIGN.md` v3.6 tokens / motion /
  components / shell; the "MagicWebb System Atlas" artifact updated to v3.6.

## v3.5 — 2026-09-04 — Keeper-mandatory protocol, UX-spec frontend, no-admin surface

Live on Coston2 from block 34905078 (fresh set: Marketplace `0xa6fbad08…`,
AuctionHouse `0x0ADe5F5A…`, OfferBook `0x358d4504…`, manager (unproxied)
`0x14C3b1Ba…`; NFT unchanged; v3.2/v3.3 set superseded). Songbird and Flare
stay read-only (no contracts) — browse-only banner, trading disabled per
profile. Nine waves, all on `main`, each deployed on push.

### Protocol

- **14 durations** (10-minute option dropped): 1m 3m 5m 15m 30m 45m 1h 2h
  4h 8h 12h 16h 20h 24h.
- **Keeper is mandatory.** Cores refuse a zero manager and `_payFee` reverts
  `NoKeeper` when `manager.keeper()` is unset — the 0.5% keeper share can never
  fall back to `feeRecipient`. `NoManager` / `manager != 0` guards removed.
- Tests rebuilt around a real manager everywhere (175 green), FeeSplit
  fake-manager coverage, gas snapshot refreshed, frontend ABI regenerated.

### Backend / API

- **Per-network isolation**: Redis keys namespaced by chain (`cache.Key`),
  boot refuses a Redis already bound to another chain. `chain/profile` is the
  only per-network table (`knownNetworks` deleted); new knobs `WSCoalesceMs`,
  `ImageProxyConcurrency`, `GraphQLMaxCost`, `RateLimitTier` (testnet 120/min,
  mainnet 60/min), `FaucetURL`, `AuditNote`, `TradingStatus` (live |
  browse-only). `deploy.yml` derives `NETWORK_TRADING` from `deployments/*.json`.
- API shaped for the UX spec: card rows carry `collection_tracked` / name /
  standard / image; `GET /collections/:addr/tokens` (paginated);
  `verified_reason` on collections; `owner` / `last_sale` / `indexed_at` on
  tokens; `collections_tracked` + `updated_at` on stats; `ts` / `status` /
  `token_url` on activity. Unknown token / collection → 404 (was fabricated
  rows). `auctions?status=all|ended|settled|cancelled` no longer 500s; listings
  honour `min_price` / `max_price` / `sort=ending` / `collection` / `page`.
- Token not-found decision races the on-chain fallback against a 6s timeout so
  a dead RPC can never hang it.
- Health probe refresh is single-flight and never caches a result from a
  cancelled context; indexer observe set hard-capped at 4096 with eviction;
  migration 042 audits 039's lowercase merge read-only and fails loudly if it
  would ever be lossy.
- **No-admin surface**: API keys are verify-only (Create/Revoke/List issuance
  and its audit events deleted; migrations untouched). `/internal/metrics`
  gains an optional `METRICS_TOKEN` gate (public when unset).
- Infra: image also mirrored to ghcr.io (non-fatal) for the Kubernetes path,
  kustomize image pins + a CI job rendering all three overlays, Zig 0.13.0
  tarball pinned by sha256, goose migrations bounded by `MIGRATE_TIMEOUT`
  (default 5m). `RESET_ON_ADDRESS_CHANGE` wipes chain tables on boot when the
  contract set changes.

### Frontend (UX spec B0–B5)

- Design system: `tokens.css` (surfaces, text tiers ≥ 4.5:1, accents, type
  scale, motion), one `.btn` system, focus-visible rings, 44px targets,
  self-hosted fonts, SVG icon set, `docs/DESIGN.md`.
- Global shell: header/nav, keyboard network menu that keeps the path, drawer
  with focus trap, testnet banner with faucet link, browse-only banner, skip
  link, toasts, Hint popover, branded 404, AppKit lazy-loaded.
- **Three-tier badges** from one source (`lib/badge.ts`): Listed collection /
  Verified / Authentic + ★ Creator; cards say "1 of 1" / "Multi-edition".
- **Honest tx modal**: plan summary before signing (you pay / seller receives
  (2% fee) / held safely / refundable…), estimated network fee, success card
  with one next action, plain error titles with `[Switch to <chain>]`
  (`wallet_addEthereumChain` fallback on 4902) and `[Get test FLR]` on Coston2.
- Pages rebuilt to spec: Listings (URL-driven filters, chips, Load more,
  distinct empty / no-match / error states), Token (always-present action
  zone = full status × role matrix with disabled+reason cells, outbid and
  expired-offer refunds on-page, mobile sticky bar), Collection (Items tab
  default, badge tier tooltip, in-place offers toggle), Auctions (Live /
  Ending soon / Ended, bid panel as phase × role matrix incl. 3-day
  cancel-and-refund), Home (hero, first-run strip, honest "Right now" line),
  Offers (teach-first viewer state, Received/Sent, net-proceeds accept
  summary), Search (grouped + pluralised, recent searches), Profile (980-line
  inline script → `ProfilePage.svelte`, ERC-721-only batch list, refunds card).
- `/status` page: automation costs, indexer lag, running build SHA
  (`X-MW-Build-SHA`). `/metrics/gas` redirects there.
- Docs: registry-driven nav, 2-minute start-here guide with badge legend and
  fee line, `capabilities.md` carries the capability matrix verbatim (mirrored
  in `docs/ARCHITECTURE.md` §8).
- **Accessibility debt paid**: sub-12px text raised, muted card text to
  4.5:1, badge pills 12px with a light-scheme palette; exemption lists empty.

### CI / tests

- New required job **Frontend e2e (Playwright)**: 13 wallet-less smoke tests
  (desktop + mobile) against the built site with API/RPC mocked — branded 404,
  URL filters, collection Items tab, token 404, keyboard network switcher,
  mobile tab-bar padding + no horizontal scroll, 44px / 12px sweeps, axe
  contrast gate.
- 298 vitest green, `astro check` 0 errors, full backend suite green,
  CodeRabbit full-sweep (10 findings, 9 fixed, 1 skipped: migration 028
  renumbering — append-only rule).
- Seed: MagicWebb Genesis open-mint collection on Coston2 with a live
  round-trip proof.

## v3.4 — 2026-09-02 — Gas repack + instant upgrades on every network

The generation Songbird and Flare launch with. Fresh deployment on all
three networks (no storage compatibility owed to v3.2/v3.3 proxies).

### Protocol / gas (measured targets vs v3.2 baseline)

- **Platform fee 1.5% → 2% total**, split **1.5% → `feeRecipient`** (owner's
  platform wallet) and **0.5% → the network keeper** (`manager.keeper()`, gas
  replenishment for instant settlement). Falls back to 100% `feeRecipient` when
  no keeper is set. Still seller-pays, still only on a successful sale. New
  `FeeSplit(feeRecipient, platformShare, keeper, keeperShare)` event.
- **`Auction` struct repacked 6 → 4 slots** (`create` −66k, −42%): dropped
  vestigial `minIncrementBps`/`minIncrementFlat`/`active`/`startsAt`;
  `originalEndsAt` folded in from its mapping; `leaderTotal` is now DERIVED
  from `cumulative[id][leader]` (compat view kept). `activateAuction()`
  removed (creation = activation since v3.3).
- **Bidder registry deleted** (first bid −44k, −31%): `_bidders` /
  `_seenBidder` / `bidderCount` / `getBidder` were write-only; the indexer
  reconstructs bidders from `BidPlaced`, `refundLosers` takes calldata.
- **`OfferBook.Position` packed 2 → 1 slot** (`makeOffer` −22k, −40%).
- **`MarketplaceManager` is UNPROXIED plain bytecode** and the cores hold
  `manager` + `feeRecipient` as implementation **immutables** (keeper-path
  consults −6.8k…−9.8k; fee reads −2.1k). Replacing either = new core impl
  via the normal upgrade path.
- **Transient-storage reentrancy guard** (EIP-1153 TSTORE/TLOAD, −2k per
  guarded call), gated on a per-chain Cancun probe before deploy.
- `buy()` uses `transferFrom` for ERC-721 (buyer is the caller; −2.6k);
  `cancel()` reads a storage pointer; loops hoisted/unchecked.
- Width bounds: auction `reserve`/`amount` → uint96, offer `units` → uint80,
  timestamps → uint40 — external ABI unchanged (uint128/uint64), guarded.

### Deployment / governance (owner directives 2026-09-02)

- **Every network deploys UNSEALED and instantly upgradeable**:
  `upgradeDelay()` is 0 on every chain; queue+upgrade run back-to-back. A
  fresh, per-network admin wallet (saved offline by the owner) is the whole
  upgrade authority until the owner orders that network immutable
  (`renounceAdmin()` — see docs/UPGRADE_RUNBOOK.md, new).
- `DeployV34.s.sol` replaces `DeployV32.s.sol` (7 CREATEs, manager plain,
  no-arg initializers). `keeperrotate -grant` (dead `addKeeper`) replaced
  by `-set` (`setKeeper`, admin-signed). foundry.toml gains songbird/flare
  rpc + verify entries; the CI verify job loops all deployed networks.
- **2-step admin rotation on the manager**: `transferAdmin(new)` →
  `acceptAdmin()` from the new wallet, `cancelAdminTransfer()` aborts a
  pending handover. The admin key is rotatable on every network until that
  network's `renounceAdmin()`.

### Backend / frontend / tooling

- Indexer now indexes `AuctionForceCancelled`; the keeper auto-`forceCancel`s
  unsettled auctions at `endsAt + 3d` (never-stuck completeness — no escrow
  waits on a human).
- Creator / Authentic badges now render on auction detail, offers, search
  and profile tabs; offers + search API rows gained `collection_verified` /
  `collection_creator`.
- Gas snapshot baseline committed (`contracts/.gas-snapshot`); CI runs
  `forge snapshot --check` against it.
- Frontend ABI regenerated for v3.4.

## v3.3 — 2026-08-31 — Flat bid increment, instant expiry

- Marketplace-wide flat **+1-token bid increment** (seller increment knobs
  removed); instant expiry handling and cleanExpired sweep integration.
- Shipped as source + Coston2 app deploy; the on-chain Coston2 impls
  remained v3.2 bytecode (resolved by the v3.4 fresh deploy).

## v3.2 — 2026-08-31 — Single welded keeper, no grants

- `MarketplaceManager` rebuilt: exactly ONE keeper (`setKeeper` replaces,
  never adds), exactly ONE admin until `renounceAdmin()`; the v3.1
  self-replenishing keeper fleet and all AccessControl grant paths deleted.
- Deployed to Coston2 (block 34729709) behind UUPS proxies with timelocked
  upgrades (0 on Coston2, 48h mainnets — superseded by v3.4's instant-everywhere).

## v3.1 — 2026-08-29 — Rules overhaul, badges, unpausable protocol

Live on Coston2 from block 34619862. Songbird and Flare run the same
build in read-only network mode (UI, wallet, profiles; no contracts).

### Protocol

- **Settle authority is closed to three parties**: `KEEPER_ROLE`
  (the instant `endsAt` passes), the seller, and the auction winner.
  No third party can settle someone else's auction. Escrow is still
  never trapped: non-leading escrow is withdrawable at any time and
  `forceCancel()` at `endsAt + 3d` is callable by the same
  keeper/seller/winner set (v3.2 removed the permissionless tier).
- **Nothing is pausable — entries included.** No entry or exit path
  (`list`, `bid`, `withdrawLoserFunds`, `refundExpiredOffer`,
  `cancelOffer`, `withdrawRefund`, `forceCancel`) can be halted by
  anyone. (An earlier entry here said "pausable entries" — no pause
  machinery exists anywhere in the contract set.)
- **`refundExpiredOffer` is no longer keeper-only** — a bidder can
  always reclaim their own expired escrow, so a dead keeper cannot
  strand it. `KEEPER_ROLE` is required only to refund someone else.
- **15 auction durations**, validated and expiry-computed on chain.
  Anti-snipe extensions are hard-capped at 30 minutes past the
  original end, so no auction can be kept alive indefinitely.
- **Sub-threshold bids revert** instead of accumulating: escrow that
  can never lead only burned gas and let a griefer push the timer.
- **Keeper fleet is self-replenishing** — keepers may add and remove
  keepers, so the fleet survives admin renunciation. Deploy scripts
  now *require* `KEEPER_ADDR`; deploying without one and then
  renouncing admin would have sealed `KEEPER_ROLE` forever.

### Frontend

- **Badges**: Verified NFT (ERC-165 + metadata resolved), upgraded to
  **Authentic** when the collection's ERC-173 creator is known, plus
  a Creator badge on profiles.
- **Editable profile tag**, cross-network profile carry-over,
  profile pagination, and an on-chain fallback so a token page still
  renders for NFTs the indexer has never seen.

### Safety / operations

- Server shutdown no longer loses data: teardown cancels first, then
  drains, then closes sinks; `log.Fatal` in the run path was replaced
  with returned errors so the keeper's Postgres advisory lock is
  always released.
- Real invariant suites for the marketplace, auction house and offer
  book — the previous ones asserted properties that could not fail.
- Deployment drift detector: every network's live build SHA is
  compared against `origin/main`.

## v3.0 — 2026-08 — Third contract generation, multi-network

- **`MarketplaceManager`** as the roles registry (admin, keeper,
  fee-manager) and trust anchor for **timelocked UUPS upgrades**.
  The 1.5% fee stays immutable in the cores.
- **Durations computed on chain** — the client sends a duration, not
  an absolute expiry it could lie about.
- **Frontend rebuilt on Astro + Svelte**; the server-rendered HTMX UI
  was removed entirely.
- **Per-network chain profiles** with a read-only network mode: a
  network with no contracts serves UI, wallet and profiles while
  running no indexer or keepers.
- **Deploy matrix over `deployments/*.json`** — one Fly template per
  network, addresses from a single source of truth, and a network
  switcher that links only to networks that actually exist.
- **Realtime event catalog** as the single list of event types shared
  by every transport (WS, SSE, gRPC bridge).

## v29 — 2026-06-24 — Full-stack Round 4 (cross-layer)

The **$75k+ full-stack audit** engagement surfaced six findings
across chain / backend / frontend. Three fixed in this release;
two deferred as LOW; one MEDIUM cosmetic pending.

### Fixed (Round 4)

- 🔴 **F-01 SIWE Chain ID binding (HIGH)** — wallet.js SIWE
  template now signs `Chain ID: ${chainId}` line;
  `cmd/server/main.go verifyHandler` parses `"Chain ID: 114"` and
  rejects payloads whose chainId != `config.C.ChainID` (401 chain
  id mismatch). Closes the cross-chain replay vector: a Coston2
  signed payload no longer authenticates on any other chain
  because the `Chain ID:` line embedded in the signed message
  differs and EIP-191 verify rejects the mismatched message.
  The chain ID is server-injected via `window.MW_NETWORK_ID =
  {{.ChainID}}`.
- 🟠 **F-02 transfers-chunk abort (HIGH)** — `backend/internal/
  indexer/runner.go processTransfers` now returns `err` on
  `HeaderByNumber` failure instead of silently `continue`-ing.
  Mirrors `processRange`'s abort-on-miss policy. Prevents
  orphaned ownership events from being lost when a transient RPC
  failure leaves a tracked-collection Transfer log without a
  header in the current chunk — the chunk retries next tick.
- 🟠 **F-03 Keeper gas cap (MEDIUM)** — `runner.go sendRaw`
  clamps `feeCap` / `tipCap` to `KEEPER_MAX_FEE_CAP_GWEI` (default
  100 gwei) / `KEEPER_MAX_TIP_CAP_GWEI` (default 5 gwei). New
  `MaxFeeCapWei()` / `MaxTipCapWei()` helpers in `config.go`;
  `.env.example` documents both. **EIP-1559 invariant
  `feeCap >= tipCap` lifted** when clamping produced a mismatch
  (logs warning) so the keeper never broadcasts an un-mineable
  `DynamicFeeTx`.

### Deferred (Round 4, non-blocking)

- 🟡 **F-04** Indexer overlapping DB writes (advisory-lock belt)
  — deferred as LOW; existing handlers are idempotent upserts.
- 🟡 **F-05** wallet.js `window.ethereum` reference comments —
  deferred as LOW; no live calls, only historical documentation.
- 🟡 **cos-1** wallet.js `URI: ${origin}` line is informational
  only — deferred as MEDIUM cosmetic; SIWEDomain is the actual
  cross-site binding. Future pass: drop or add server-side parse.

### Working tree state (v29)

- 24 modified files: contracts + backend + frontend all at parite.
- 2 untracked files: `claude-code-prompt-enhancer/`, `contracts/
  AUDIT_REPORT.md`.
- `git push` NOT executed per user directive; `origin/main` is the
  source-of-truth that the user later chooses to publish when ready.
- Build clean for backend (`go build ./internal/{config,indexer}/
  ./...` PASS); tests pass for affected packages.

### Phase 6 deliverables

- `contracts/AUDIT_REPORT.md` — updated to v29 with Phase 4d
  full-stack findings, before/after rationale, and cross-layer
  verification commands.
- `docs/DEPLOY_CHECKLIST.md` — Coston2 deployment checklist.
- `docs/IMMUTABILITY_TRANSITION.md` — immutability notes for Coston2.
- `docs/MONITORING.md` — post-launch operational runbook
  (PushFailed events, pendingReturns sweep, keeper advisory-lock
  health, FTSO/State-Connector status) (NEW, untracked).


## v21 — 2026-06-22 — Priority Stack unlock

This release closes every item in the audit Priority Stack
(`docs/AUDIT.md`) and ships the seed-testnet harness for live
verification. Backend compile + vet + pg-less unit tests pass;
live site https://magicwebb.fly.dev returns 200 OK across every
public route and `/events` SSE remains streaming-clean.

### Fixed (Priority Stack — all items now FIXED)

- 🔴 **P0 `onTransferBatch`** — indexer no longer OOMs on hostile
  TransferBatch logs. Bound `idsOff`/`valsOff` by data footprint
  AND `maxBatchLength = 1024` BEFORE the inner loop; cross-validate
  `idsLen == valsLen` and array-extends-past-boundary.
  Anchor: `backend/internal/indexer/handlers.go::maxBatchLength`.
- 🟠 **P1 `processTransfersWallClock`** — processTransfers no
  longer poisons rows with `time.Now().Unix()` when the core-event
  FilterLogs didn't return a log for that block. Per-block header
  fetch with 2 s context timeout; log+skip on failure; memoize so
  the next Transfer log in the same chunk reuses the cached
  timestamp without an extra RPC.
- 🟠 **P1 `getRecentTxnsLimit`** — LIMIT pushed into each
  UNION ALL branch via parens so Postgres can honour per-branch
  indexed scans; outer wrapper caps the merge.
- 🟠 **P1 `getEffectiveBidsLimit`** — hard-cap at `LIMIT 200` so
  contested 10k-bid auctions no longer OOM the page renderer.
- 🟠 **P1 `clientIpSpoof`** — `clientIP` trusts `Fly-Client-IP`
  first (mathematically unspoofable from outside), then RFC 7239
  `Forwarded` (with `stripAddrPort` for bracketed IPv6 + port
  stripping), then `X-Forwarded-For` rightmost, then fasthttp
  `c.IP()`. Fiber config: `EnableTrustedProxyCheck: false` +
  `ProxyHeader: "Fly-Client-IP"`.
- 🟡 **P2 `parseWeiHelper`** — central `ParseWei(s)` +
  `ParseWeiOrZero(s)` helper. Five prior `big.Int.SetString`
  sites rewritten to route through it; malformed input is now a
  WARN log instead of a silent `0`.

### SSE belt-and-braces

- `sse/cancel` scope moved from handler-scope (which fired
  prematurely) into the writer callback; an additional
  sync.Once-guarded `vctx.Done()` goroutine ensures
  cancel-on-early-disconnect even when SetBodyStreamWriter's
  callback never runs.
- Sentinel `: connected\n\n` first-byte flush so fasthttp commits
  response headers + first chunk in the same TCP write.

### Documentation

- `docs/AUDIT.md` — v21 section awaits priority stack unlock with
  verification evidence; feature-flow appendix A–K added.
- `docs/USER_GUIDE.md` — full user-flow walkthrough A–P, what-to-
  do-when table, event-to-feature map, live test matrix.
- `tools/seed-testnet/` — new Go CLI harness (`main.go` + README)
  with `--dry-run`, `--seed-*` per-feature-count flags,
  `--teardown` flag, audit-grade `seeded_by` row tagging.

### Tooling

- `tools/seed-testnet` ships a `go.mod` and uses `crypto.Keccak256`
  (EVM-canonical) for calldata selectors. ABI packing via
  `accounts/abi` for `setApprovalForAll`, `list`,
  `create` calls.
- `tools/seed-testnet/main.go` synthesises per-run addresses via
  HD derivation from a SHA256-seed so dry-runs produce stable
  keys for QA reproduction.

## v20 — contracts (handover from prior release)

The audit ledger's v20 row is unchanged here — those fixes
(C-01 anti-snipe, C-02 stalled-state recovery, C-03 offer withdraw
fallback, C-04 refundLosers gas-bounds) are all FIXED via the
existing AuditFuzz harnesses under `contracts/test/`. See
`docs/AUDIT.md` for the per-row evidence.

## v19 — wallet.js (handover from prior release)

The v19 rows F-01/F-02/F-03 are landed in
`frontend/static/wallet.js`: chainChanged /
accountsChanged listeners on both eip1193 kinds, SIWE
typed-error path, no silent auto-reconnect. Manual live
verification on https://magicwebb.fly.dev confirms the fixes.
