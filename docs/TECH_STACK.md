# MagicWebb — Tech Stack Inventory

Every component, what it does, how MagicWebb uses it, and a **keep / update / replace** call.
Versions are pinned as of 2026-06-05 (`backend/go.mod`, `contracts/foundry.toml`,
`backend/internal/ui/templates/layout.html`).

## Smart contracts

| Component | Version | Role in MagicWebb | Verdict |
|-----------|---------|-------------------|---------|
| Solidity | 0.8.26 | Marketplace / AuctionHouse / OfferBook + MarketplaceCore base. Immutable, no admin. | **Keep** — modern, built-in overflow checks. |
| Foundry (forge) | current | Build, test (94 passing incl. 1 fuzz), deploy scripts, `forge coverage`. | **Keep** — best-in-class Solidity tooling. |
| OpenZeppelin Contracts | v5 | `ReentrancyGuard`, `IERC721`/`IERC1155` + `safeTransferFrom`, `Address`. | **Keep** — audited standard. Pin the exact tag (vendored, untracked in git). |

## Backend (Go single binary)

| Component | Version | Role | Verdict |
|-----------|---------|------|---------|
| Go | 1.25.0 | The whole server: HTTP, UI, SSE, indexer in one process. | **Keep**. |
| Fiber v2 | 2.52.13 | HTTP framework (routing, CORS, logger, rate-limit middleware, SSE streaming). | **Keep** — note Fiber **v3** exists; defer the major bump until post-launch. |
| go-ethereum | 1.14.7 | `ethclient` (getLogs, block polling), `crypto` (EIP-191 recover, keccak), tx signing for keepers. | **Keep**. |
| pgx/v5 | 5.6.0 | Postgres driver + pool; all queries parameterized (`$N`). | **Keep**. |
| goose/v3 | 3.21.1 | Embedded migrations, auto-run on startup (`db.Migrate`). | **Keep**. |
| zerolog | 1.33.0 | Structured logging (pretty in dev, JSON in prod). | **Keep**. |
| google/uuid | 1.6.0 | SSE client IDs. | **Keep**. |
| testify | 1.11.1 | Test assertions. | **Keep** — currently underused (≈5% coverage; Phase 6 fixes this). |
| sentry-go | 0.28.1 *(indirect)* | Present transitively; `SENTRY_DSN` is read by config but **not wired** to an active reporter. | **Update** — either wire Sentry properly or drop the config key. |

## Frontend (served from the Go binary via `embed.FS`)

| Component | Version | Role | Verdict |
|-----------|---------|------|---------|
| HTMX | 2.0.4 (unpkg) | Server-rendered partials, `hx-get` list refresh. | **Keep** (but self-host — see below). |
| htmx-ext-sse | 2.2.2 (unpkg) | Binds the `/events` SSE stream to DOM updates. | **Keep** (self-host). |
| Alpine.js | **3.x.x** (jsdelivr) | Client state: wallet store, modals, countdowns. | **Update — pin exact version.** A floating `3.x.x` is non-reproducible and a supply-chain risk. |
| ethers.js | 6.13.4 (cdnjs) | Wallet provider, contract calls, signing, `withFee()` math. | **Keep** (self-host). |
| WalletConnect ethereum-provider | 2.11.2 | Mobile-wallet connectivity. | **Keep** (self-host / verify still maintained). |
| Tailwind CSS | `cdn.tailwindcss.com` | Styling. | **Replace.** This is the **dev-only JIT CDN** — Tailwind explicitly says not for production (huge runtime payload, external dependency, no purge). A built `static/tailwind.css` already exists; compile at build time and drop the CDN `<script>`. |

### Cross-cutting frontend issue: third-party CDNs
HTMX, ethers, Alpine, Tailwind, and WalletConnect all load from external CDNs with **no SRI
hashes**. For an "unstoppable" marketplace this is a liveness + supply-chain weak point (a CDN
outage or compromise breaks or poisons the app). **Recommendation:** vendor these into the existing
`embed.FS` and serve from `/static`, or at minimum add Subresource Integrity hashes and pin exact
versions. This also removes the Tailwind-CDN problem above.

## Data & infrastructure

| Component | Role | Verdict |
|-----------|------|---------|
| PostgreSQL (Supabase) | Projected read model; pgvector (`004`) + full-text search (`005`); RLS policies (`003`). | **Keep** — confirm RLS is actually enforced in prod (service role bypasses it; see `READINESS.md`). |
| In-memory SSE hub (`internal/sse`) | Real-time fan-out (replaced Redis). | **Keep for single instance.** **Blocks horizontal scaling** — events only reach clients on the same process. Add a per-IP/global subscriber cap (DoS). |
| In-memory rate-limit + nonce stores | 60/min API, 20/min auth; single-use SIWE nonces. | **Keep for single instance.** Counters reset on restart and aren't shared across replicas; `X-Forwarded-For` is trusted (only safe behind a trusted proxy). Move to a shared store before scaling out. |
| Render.com (`render.yaml`) | Single Go web service, free plan, `/healthz`. | **Update for production** — free plan sleeps/has no SLA; size the plan and add monitoring before mainnet. |

## CI / tooling

| Component | Role | Verdict |
|-----------|------|---------|
| GitHub Actions | 4 jobs: Go build/test, forge build/test, Slither (fail-on-high), gitleaks. | **Keep** — add `govulncheck` + `golangci-lint` + `forge coverage` gate (Phase 6). |
| Slither | Contract static analysis (baseline 0 high / 0 medium). | **Keep**. |
| gitleaks | Secret scanning; `.gitleaksignore` documents one confirmed false positive. | **Keep**. |

## Summary of actions

- **Replace:** Tailwind CDN → built `static/tailwind.css`.
- **Update:** pin Alpine to an exact version; self-host or SRI-hash all CDN libs; wire or remove Sentry; right-size Render.
- **Architectural gates (before multi-instance / mainnet):** shared rate-limit/nonce/SSE store, SSE subscriber cap, confirm RLS enforcement, keeper high-availability (see `READINESS.md`).
- **Keep:** the entire contract + Go backend stack — it's modern, coherent, and appropriately minimal.
