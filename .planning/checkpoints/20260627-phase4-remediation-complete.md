# Phase 4: Remediation — Complete

## Date: June 27, 2026

## Fixes Implemented (2 Medium-severity from Phase 3)

### 1. Self-Hosted AppKit (V7.1) — No CDN dependency
- **Before:** `appkit-bridge.js` imported from `https://esm.sh/@reown/appkit@1.8.21` — compromised CDN = injected JS = wallet hijack
- **After:** Self-hosted via npm (`@reown/appkit` + `@reown/appkit-adapter-ethers` in `app/package.json`)
- **Files changed:**
  - `app/package.json` — added both Reown packages
  - `app/src/appkit-bridge.js` — new file, imports from npm, bundled by Vite
  - `app/astro.config.mjs` — rollupOptions.input for separate entry build
  - `frontend/templates/layout.html` — updated comment + `?v=35` cache-buster
  - `frontend/static/appkit-bridge.js` — replaced with stub (warns to build, sets `__MW_APPKIT__=undefined` for fallback)
- **Fallback:** If build not run, `wallet.js` falls through to self-hosted `wc-bundle.js`

### 2. Keeper Balance Check (V4.1)
- **Added:** `KeeperMinBalanceWei` config field (env: `KEEPER_MIN_BALANCE_WEI`, default 0.1 FLR)
- **Added:** `BalanceAt` to EthClient/ethNode/Pool interfaces
- **Added:** `checkKeeperBalance()` — one-shot balance check in `Run()` before keeper start
- **Added:** Startup validation in `Load()` — non-negative decimal integer required
- **Behavior:** Logs WARN when balance < threshold; non-fatal on RPC failure (5s timeout)
- **Files changed:**
  - `backend/internal/config/config.go` — KeeperMinBalanceWei field + Load() validation
  - `backend/internal/indexer/runner.go` — checkKeeperBalance() + Run() call
  - `backend/internal/rpcpool/pool.go` — BalanceAt on ethNode + Pool
  - `backend/internal/rpcpool/pool_test.go` — BalanceAt on fakeNode

## Verification
- `go build ./...` ✅
- `go vet ./...` ✅
- `go test ./internal/config/...` ✅
- `go test ./internal/rpcpool/...` ✅
- Code reviewer: all findings resolved (2 rounds)

## Prior Phase 1 fix
- `backend/internal/nonce/store_test.go` — 3 deprecated `s.Set()` → `SetIfFree` + goroutine loop var fix
