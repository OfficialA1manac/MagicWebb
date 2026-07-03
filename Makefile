SHELL := /bin/bash
-include .env
export

BINARY  := bin/magicwebb
SERVER  := ./backend/cmd/server
HTTP_ADDR ?= :8080

.PHONY: help dev build run migrate migrate-status test lint contracts-build contracts-test deploy load-addrs regen-abi clean

help: ## show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-20s %s\n", $$1, $$2}'

# ── App ───────────────────────────────────────────────────────────────────────

dev: ## run backend (:8080) + Astro frontend (:4321) together
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@echo "  Go backend     → http://localhost:8080"
	@echo "  Astro frontend → http://localhost:4321"
	@echo "  Open :4321 — Astro hot-reloads + proxies API"
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@trap 'kill $$BACKEND_PID $$FRONTEND_PID 2>/dev/null' EXIT; \
	go run $(SERVER) & BACKEND_PID=$$!; \
	cd app && npm run dev & FRONTEND_PID=$$!; \
	wait $$BACKEND_PID $$FRONTEND_PID

build: ## compile single binary → bin/magicwebb (auto-rebuilds tailwind.css + appkit-bridge first)
	@mkdir -p bin
	cd backend && go run ./cmd/buildtailwindcss || echo 'warning: tailwind rebuild failed (offline?); using committed tailwind.css'
	# Build the self-hosted AppKit bridge bundle (no CDN dependency).
	# Outputs app/dist/static/appkit-bridge.js; copy to frontend/static/ for Go embed.
	# NOTE: bridge build FAILS the overall build when it cannot be produced,
	# matching the stricter Docker behavior. The tailwind step above tolerates
	# offline failures because a committed CSS fallback exists; the bridge must
	# be current for wallet pairing to function.
	cd app && npm run build:bridge
	@cp app/dist/static/appkit-bridge.js frontend/static/appkit-bridge.js
	# v23.1 — Inject git SHA via -ldflags so X-MW-Build-SHA on /healthz reports
	#         what's actually compiled in. Falls back to `unknown` when HEAD
	#         is detached/unreadable (e.g. an unpacked tarball); operators see
	#         the literal in /healthz and `make check-fly-sync` flags drift.
	$(eval MW_GIT_SHA := $(shell git rev-parse HEAD 2>/dev/null || echo unknown))
	cd backend && go build -ldflags "-X github.com/OfficialA1manac/MagicWebb/backend/internal/api.MWServerBuildSHA=$(MW_GIT_SHA)" -o ../$(BINARY) $(SERVER:./backend/%=./%)
	@echo "  built: $(BINARY)  sha=$(MW_GIT_SHA)"

run: build ## build then run
	./$(BINARY)

migrate: ## run DB migrations (goose up) — note: the server also auto-migrates on startup
	cd backend && go run github.com/pressly/goose/v3/cmd/goose -dir internal/db/migrations postgres "$(POSTGRES_URL)" up

migrate-status: ## show DB migration status
	cd backend && go run github.com/pressly/goose/v3/cmd/goose -dir internal/db/migrations postgres "$(POSTGRES_URL)" status

# ── Contracts ─────────────────────────────────────────────────────────────────

contracts-build: ## forge build
	cd contracts && forge build --sizes

contracts-test: ## forge test -vvv
	cd contracts && forge test -vvv

deploy: ## deploy contracts to Coston2
	@test -n "$(PRIVATE_KEY)" || { echo "FATAL: PRIVATE_KEY missing in .env"; exit 1; }
	cd contracts && forge script script/DeployCoston2.s.sol \
	  --rpc-url $(RPC_URL) --broadcast --private-key $(PRIVATE_KEY) --slow
	@$(MAKE) load-addrs

load-addrs: ## sync deployed contract addresses into .env
	@bc=contracts/broadcast/DeployCoston2.s.sol/114/run-latest.json; \
	  test -f "$$bc" || { echo "FATAL: $$bc not found — run 'make deploy' first"; exit 1; }; \
	  command -v jq >/dev/null || { echo "FATAL: jq required"; exit 1; }; \
	  m=$$(jq -r '.transactions[]|select(.transactionType=="CREATE" and .contractName=="Marketplace")|.contractAddress' "$$bc" | head -n1); \
	  a=$$(jq -r '.transactions[]|select(.transactionType=="CREATE" and .contractName=="AuctionHouse")|.contractAddress'  "$$bc" | head -n1); \
	  o=$$(jq -r '.transactions[]|select(.transactionType=="CREATE" and .contractName=="OfferBook")|.contractAddress'     "$$bc" | head -n1); \
	  test -n "$$m" -a -n "$$a" -a -n "$$o" || { echo "FATAL: missing address(es) in broadcast"; exit 1; }; \
	  for kv in MARKETPLACE_ADDR=$$m AUCTION_ADDR=$$a OFFERBOOK_ADDR=$$o; do \
	    k=$${kv%%=*}; v=$${kv#*=}; \
	    if grep -qE "^$$k=" .env 2>/dev/null; then sed -i "s|^$$k=.*|$$k=$$v|" .env; \
	    else printf '%s=%s\n' "$$k" "$$v" >> .env; fi; \
	    echo "  $$k=$$v"; \
	  done

regen-abi: ## regenerate wallet.js ABIs from forge build (updates static/wallet.js constants)
	@test -d contracts/out || { echo "FATAL: run 'make contracts-build' first"; exit 1; }
	@echo "  ABIs embedded in frontend/static/wallet.js — update manually if needed"

# ── Zig Accelerated SHA-256 ──────────────────────────────────────────────────

zigmedia-build: ## compile Zig SHA-256 library + build Go binary with zigmedia tag
	@mkdir -p bin
	@echo "  Compiling Zig SHA-256 library..."
	cd backend/zigsha256 && zig build-lib -O ReleaseFast -dynamic zigsha256.zig
	@echo "  Building Go binary with -tags zigmedia..."
	$(eval MW_GIT_SHA := $(shell git rev-parse HEAD 2>/dev/null || echo unknown))
	cd backend && CGO_ENABLED=1 go build -tags zigmedia -ldflags "-X github.com/OfficialA1manac/MagicWebb/backend/internal/api.MWServerBuildSHA=$(MW_GIT_SHA)" -o ../$(BINARY) $(SERVER:./backend/%=./%)
	@echo "  built: $(BINARY)  [zigmedia]  sha=$(MW_GIT_SHA)"

zigmedia-test: ## run Zig unit tests (no Go needed)
	cd backend/zigsha256 && zig test zigsha256.zig

zigmedia-bench: ## benchmark Zig vs Go SHA-256
	@echo "  Run Go benchmark: go test -bench=BenchmarkHash -benchmem ./internal/imagestore/"
	@echo "  Run Zig benchmark: cd backend/zigsha256 && zig build -Doptimize=ReleaseFast"
	@echo "  (Add a benchmark main.zig to backend/zigsha256/ for isolated Zig bench)"

.PHONY: zigmedia-build zigmedia-test zigmedia-bench

# ── Quality ───────────────────────────────────────────────────────────────────

setup-hooks: ## install Git hooks via core.hooksPath → .githooks/
	@git config core.hooksPath .githooks
	@chmod +x .githooks/pre-commit 2>/dev/null || true
	@echo "  Git hooks installed — .githooks/pre-commit runs on every commit."
.PHONY: setup-hooks

check-test-files: ## fail if .test.* files exist in app/src/pages/ (Astro picks them up as routes)
	@bad=$$(find app/src/pages -maxdepth 1 -name '*.test.*' 2>/dev/null); \
	if [ -n "$$bad" ]; then \
		echo "ERROR: .test.* files found in app/src/pages/ — Astro will try to render them as routes:"; \
		echo "$$bad" | sed 's/^/  /'; \
		echo "Move them to app/src/__tests__/ and update the import path."; \
		exit 1; \
	fi
	@echo "  OK — no .test.* files in app/src/pages/"

test: ## run Go test suite with the race detector
	cd backend && go test ./... -race -count=1 -timeout 120s

# v23.1 — verify Fly.io is serving the exact SHA in origin/main.
# Catches the v74-class deploy-drift bug: Fly records a new release
# successfully but the Docker layer cache pinned the previous binary's
# static assets, so the served wallet.js / tailwind.css / templates
# silently fall out of sync with what's in the git repo. The contract
# is: live X-MW-Build-SHA header must equal origin/main SHA. Any drift
# exit-codes 1 with a one-line actionable diff so a CI step can fail
# loudly instead of silently shipping a stale frontend.
check-fly-sync: ## verify magicwebb.fly.dev X-MW-Build-SHA matches origin/main
	@./tools/check-fly-sync.sh
.PHONY: check-fly-sync

lint: ## run golangci-lint over the backend
	cd backend && golangci-lint run ./...

# ── Housekeeping ──────────────────────────────────────────────────────────────

clean: ## remove build artifacts
	rm -rf bin contracts/out contracts/cache contracts/broadcast
