# MagicWebb v29 — Pre-Mainnet Deployment Checklist

> **Status of this doc:** v29 working tree (uncommitted per user directive).
> Last reviewer pass: code-reviewer-minimax-m3 — APPROVED (with v29 cosmetic
> residual noted in `contracts/AUDIT_REPORT.md` §Phase 4d cos-1).

## Layer 1 — Smart Contracts (Foundry + Slither)

```bash
cd contracts
forge build                          # solc 0.8.26, via_ir, optimizer 1M
forge test                           # 146 tests + 1 invariant — all pass
forge test --match-path test/AuditFuzz.t.sol -vv   # fuzz regression set
slither . --filter-paths 'lib/|test/'              # zero findings
```

- [x] `MarketplaceCore.sol`, `Marketplace.sol`, `AuctionHouse.sol`,
      `OfferBook.sol`, `MarketplaceManager.sol` compile clean.
- [x] `withdrawRefund()` `virtual`/`override` chain holds across cores.
- [x] `_seenBidder` mapping v28 fixverified by
      `test_bidders_uniqueAcrossRefundAndRebid`.
- [x] `batchList` `nonReentrant` v28 fix verified by
      `test_batchList_protectedByNonReentrant` (`ReentrantBatchColl` mock
      re-entry raises inner revert; outer listings preserved).
- [x] Push-fail event coverage on every `pendingReturns += X` site
      (Round 2 L-05 + M-03 fix).

### Constructor args (per network)

| Network  | Chain ID | Required env        | Constructor `manager_`            |
|:---------|:--------:|:--------------------|:----------------------------------|
| Coston2  |   114    | `RPC_URL=https://coston2-api.flare.network/ext/C/rpc` | `address(0)` (ungated) — production-grade fallback |
| Mainnet  |    14    | `RPC_URL=https://flare-api.flare.network/ext/C/rpc`     | `<GNOSIS_SAFE_ADDR>` multisig |

`feeRecipient` MUST point to a multisig BEFORE mainnet deploy.

## Layer 2 — Backend (Go + Postgres + Fly.io)

```bash
cd backend
go build ./...                                            # clean
go test ./internal/{ui,config,auth,nonce,indexer}/        # pass
go test ./internal/ui/ -run TestHomePageInjectsAllRuntimeGlobals
fly deploy --strategy canary                              # Fly canary:
                                                            # 0% → 25% → 100%,
                                                            # 5 min on each step,
                                                            # auto-rollback on
                                                            # 5xx rate >0.5%
```

- [x] v24.0.1 chain-metadata wiring: layout.html injects
      `window.MW_{NETWORK_NAME,NETWORK_ID,RPC_URL,EXPLORER,NATIVE_CURRENCY}`
      from server config; wallet.js / templates / labels read from globals.
- [x] v28.0.2 `{{.NativeCurrency}}` template injection replaces all
      27 hardcoded FLR literals across pages + partials.
- [x] v29 F-01 SIWE Chain ID substring check in `verifyHandler`.
- [x] v29 F-02 `processTransfers` chunk abort on HeaderByNumber miss.
- [x] v29 F-03 keeper gas cap + EIP-1559 invariant lift.
- [x] 16 Postgres migrations auto-applied on first launch
      (`backend/internal/db/migrations/001..015_*.sql`).
- [x] SSE live updates + Fly.io LISTEN/NOTIFY cross-instance fan-out.
- [x] Keeper advisory-lock single-flight (`WaitKeeperLock`).
- [x] WalletConnect v2 self-hosted UMD SDK (`static/wc-bundle.js`,
      no third-party CDN at runtime).

### Required env (Fly.io secrets, set via `flyctl secrets set`)

```
RPC_URL=https://flare-api.flare.network/ext/C/rpc
RPC_URLS=https://flare-api.flare.network/ext/C/rpc,...      # rotation
CHAIN_ID=14
NETWORK_NAME=Flare
NATIVE_CURRENCY=FLR
EXPLORER_URL=https://flare-explorer.flare.network
MARKETPLACE_ADDR=0x…   # post-deploy address; auto-injected to template
AUCTION_ADDR=0x…
OFFERBOOK_ADDR=0x…
ROYALTY_ADDR=
POSTGRES_URL=postgres://…  # Fly Postgres + IP allowlist
JWT_SECRET=                 # 32+ chars; rotate via secret swap + restart
SIWE_DOMAIN=magicwebb.xyz   # binds SIWE signature to legit origin
FRONTEND_URL=https://magicwebb.xyz
WC_PROJECT_ID=…              # from cloud.walletconnect.com
KEEPER_KEY=                  # hex, no 0x prefix; multisig-tier wallet
KEEPER_MAX_FEE_CAP_GWEI=100
KEEPER_MAX_TIP_CAP_GWEI=5
ADMIN_ALLOWLIST=             # CSV of admin addresses (off-chain admin)
SERVICE_TOKEN=               # 32+ chars; gates IndexerService.Reindex RPC
```

## Layer 3 — Frontend (Alpine.js + HTMX + WC Reown)

- [x] Self-hosted WC SDK (v23.6) — `static/wc-bundle.js`; CSP
      `script-src 'self'` blocks remote SDK injection.
- [x] EIP-1193 listeners (`chainChanged`, `accountsChanged`,
      `disconnect`) registered on the WC session object only — no
      legacy `window.ethereum` re-introduction.
- [x] `state: 'idle'|'connecting'|'connected'|'error'` driving navbar
      pill + AED (auto-reconnect removed in v23.2).
- [x] Action modal gated on `opts.userInitiated` (v23.1) — no
      modal auto-show without explicit user click.
- [x] Native `onclick="window.MW_CONNECT_WALLET()"` on the Connect
      Wallet chip (v23.9) — bypasses Alpine AST silent-drop class.
- [x] `MW_HIDE_ALL()` global kill-switch on tab `visibilitychange`
      (v17 wedged-transition fix).

## Live verification (smoke test matrix)

```bash
# 1. HTML template resolution
curl -fsSL https://magicwebb.fly.dev/ | grep -F '{{' ; \
  [ "$(curl -s https://magicwebb.fly.dev/ | grep -cF '{{')" = "0" ] && echo PASS

# 2. Native currency injection (Coston2 default = C2FLR)
curl -fsSL https://magicwebb.fly.dev/ | grep -cF 'C2FLR'  # expect ≥4

# 3. Chain ID injection
curl -fsSL https://magicwebb.fly.dev/ | grep -cF 'window.MW_NETWORK_ID'  # 1

# 4. SSE preamble (events endpoint)
curl -fsS -N https://magicwebb.fly.dev/events | head -c 32  # `: connected\n\n`

# 5. SIWE nonce issuance
curl -fsSL 'https://magicwebb.fly.dev/auth/nonce?address=0x000…000' \
  | jq -r '.nonce | length'   # 32 (16-byte hex)

# 6. SIWE chain-id mismatch rejection (paste test: see
#    contracts/AUDIT_REPORT.md §Phase 4d F-01 for the curl payload)
curl -fsS -X POST https://magicwebb.fly.dev/auth/verify \
  -H 'Content-Type: application/json' \
  -d '{"message":"Sign in to MagicWebb\nChain ID: 14\nAddress: 0x…\nNonce: …",
       "signature":"…","address":"…"}' | jq .error  # expect "chain id mismatch"
```

## Post-deploy immutability transition

See [`IMMUTABILITY_TRANSITION.md`](./IMMUTABILITY_TRANSITION.md).

## Post-launch monitoring

See [`MONITORING.md`](./MONITORING.md).
