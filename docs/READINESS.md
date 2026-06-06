# MagicWebb — Mainnet Readiness Verdict

Dated **2026-06-05**, after the production-hardening + audit pass on branch
`chore/production-hardening`. Two separate verdicts: **(A) smart contracts** and
**(B) full application / operations**. Mainnet deployment was explicitly deferred — this is
an assessment, not a deploy.

---

## TL;DR

| Target | Verdict |
|--------|---------|
| Contracts on **Coston2 testnet** | ✅ **Ready** — strong test + static-analysis posture, locked-funds bug fixed. |
| Contracts on **Flare mainnet** | ⛔ **Not yet** — gated on an **independent professional audit** + a **multisig fee recipient**. Immutable code holding real funds has zero recourse; internal review is necessary but not sufficient. |
| App/ops for **real users, single instance** | 🟡 **Close** — functional and hardened; wire monitoring + an ops runbook first. |
| App/ops for **scale / multi-instance** | ⛔ **Not yet** — in-memory rate-limit/nonce/SSE are single-instance by design. |

## Evidence captured this pass

- **98 Foundry tests pass** (unit + a `buyerPaysPricePlusFee` fuzz + a new **escrow-solvency invariant**: 256 runs / 128k calls).
- **Slither**: 0 high / 0 medium (low/informational only: timestamp comparisons, checked low-level ETH calls).
- **govulncheck**: 0 reachable vulnerabilities (fixed 4 via go-ethereum v1.14.7 → v1.17.0).
- **go vet** clean; backend builds as a single binary.
- Backend test coverage lifted from ~5% (one file) to **9 packages** incl. the security-critical and money paths (auth/JWT, SIWE verify, nonce, rate-limit, config authz, SSE, indexer decode, atomic bid tx).

## Fixes landed

- **H1 (High) — locked auction funds:** `settle()` no longer reverts when the NFT can't be delivered; it refunds the winner in full and all payouts fall back to `pendingReturns`. A finished auction can never strand escrow. (3 regression tests.)
- **M1 (Med) — stale auction end time:** indexer now ingests `AuctionExtended` (anti-snipe), keeping the DB/UI close time correct.
- **L1 (Low) — OfferBook defense-in-depth:** `makeOffer*` now `nonReentrant` + CEI ordering.
- **DoS — SSE:** subscriber cap (10k) bounds memory against connection-bombing.
- **Auth — SIWE domain binding** now enforced (was parsed but unused).

---

## A. Smart-contract readiness

**Strengths:** immutable / zero-admin / non-custodial; taker-pays fee math is exact; reentrancy guards on every payable state-changer; ETH via checked `call`; solvency invariant proven; comprehensive test suite.

### Mainnet blockers (must do)
1. **Independent professional audit.** The contracts are immutable with no pause/upgrade/admin — a shipped bug is permanent and funds are at stake. An external audit (and ideally a public review/contest) is mandatory before mainnet. This is the single biggest gate.
2. **Multisig fee recipient.** Deploy with `CREATOR_ADDR` = a Gnosis Safe, not an EOA. (`DeployFlare.s.sol` already notes this.)
3. **Mainnet deploy dry-run** on a fork: verify `feeRecipient` sanity checks, chain-id guard (14), and gas.

### Recommended (strongly)
- **Document the keeper liveness model.** Settlement relies on a keeper, but `settle()` is **permissionless** — if the keeper dies, the winner or seller can call it themselves, so funds are recoverable (reinforced by the H1 fix). Make this explicit to users.
- Add an **AuctionExtended / RefundPushed** consumer check in monitoring (done for the indexer; surface in UI).
- Consider a small **bug-bounty** given immutability.

### Accepted / non-issues
- Slither timestamp warnings — windows are minutes/days; miner skew is bounded and non-exploitable.
- `ERC1155Holder` inherited but unused (non-custodial) — harmless.

---

## B. Application / operations readiness

### Blockers for mainnet real-money operation
1. **Observability not wired.** `SENTRY_DSN` / `OTEL_EXPORTER_OTLP_ENDPOINT` are read but not connected to a reporter. Wire error + trace reporting before taking real users/funds-adjacent traffic.
2. **Single-instance constraints.** Rate-limit, nonce store, and the SSE hub are in-memory: they reset on restart and don't share state across replicas. Running >1 instance silently breaks rate-limiting/nonce-replay protection and splits the SSE fan-out. Either commit to **one instance** or move these to a shared store (e.g. Postgres/Redis) before scaling.
3. **Hosting plan.** `render.yaml` is the free plan (sleeps, no SLA). Right-size before launch.

### Recommended
- **Trusted-proxy requirement:** `X-Forwarded-For` is trusted for client IP — only deploy behind a proxy that sets it, or rate-limiting is spoofable.
- **Frontend supply chain (see TECH_STACK.md):** replace the Tailwind dev-CDN with a built `static/tailwind.css`; pin Alpine's floating `3.x.x`; self-host or SRI-hash all CDN libs.
- **Indexer reorg handling:** the watcher advances the indexed block after processing a range with no explicit reorg rollback. On Flare reorgs this is low-risk but worth a confirmations buffer.
- **Ops runbook + alerts:** keeper health, indexer lag, DB health (`/readyz` exists), RPC failures.
- **Backups / RLS:** confirm Supabase RLS is enforced in prod (the service role bypasses it — protect that key) and DB backups are on.

### Ready now
- Single Go binary builds and runs; `/healthz` + `/readyz` liveness/readiness.
- Migrations auto-apply on boot; CI runs go test + forge + Slither + gitleaks.
- Real-time UX (SSE) works; `getLogs` chunking respects Flare RPC limits.

---

## Are real-time + real users achievable?

**Yes, at modest scale on a single instance, on testnet today.** The architecture is genuinely
real-time (SSE) and the core flows (list / buy / auction / offer) are implemented, tested, and
hardened. The constraints are operational (single-instance state, monitoring, hosting), not
architectural — and all are addressable without redesign.

## Bottom line

- **Keep building/operating on Coston2:** green.
- **Flare mainnet:** do **not** deploy until (1) an independent contract audit is complete and clean, and (2) the fee recipient is a multisig. Then clear the app blockers (observability, instance/store decision, hosting) and ship a single instance behind a trusted proxy with monitoring.

The codebase is in materially stronger shape than at the start of this pass; the remaining gates
are the right ones for an immutable, real-money system.
