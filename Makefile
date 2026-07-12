SHELL := /bin/bash
-include .env
export

BINARY  := bin/magicwebb
SERVER  := ./backend/cmd/server
HTTP_ADDR ?= :8080

.PHONY: help dev build run migrate migrate-status test lint contracts-build contracts-test deploy load-addrs regen-abi clean

help: ## show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-20s %s\n", $$1, $$2}'

# ---- App ----

dev: ## run backend (:8080) + Astro frontend (:4321) together
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@echo "  Go backend     -> http://localhost:8080"
	@echo "  Astro frontend -> http://localhost:4321"
	@echo "  Open :4321 - Astro hot-reloads + proxies API"
	@echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	@trap 'kill $$BACKEND_PID $$FRONTEND_PID 2>/dev/null' EXIT; \
	go run $(SERVER) & BACKEND_PID=$$!; \
	cd app && npm run dev & FRONTEND_PID=$$!; \
	wait $$BACKEND_PID $$FRONTEND_PID

build: ## compile single binary -> bin/magicwebb (auto-rebuilds tailwind.css + appkit-bridge first)
	@mkdir -p bin
	cd backend && go run ./cmd/buildtailwindcss || echo 'warning: tailwind rebuild failed (offline?); using committed tailwind.css'
	cd app && npm run build:bridge
	@cp app/dist/static/appkit-bridge.js frontend/static/appkit-bridge.js
	$(eval MW_GIT_SHA := $(shell git rev-parse HEAD 2>/dev/null || echo unknown))
	cd backend && go build -ldflags "-X github.com/OfficialA1manac/MagicWebb/backend/internal/api.MWServerBuildSHA=$(MW_GIT_SHA)" -o ../$(BINARY) $(SERVER:./backend/%=./%)
	@echo "  built: $(BINARY)  sha=$(MW_GIT_SHA)"

run: build ## build then run
	./$(BINARY)

migrate: ## run DB migrations (goose up)
	cd backend && go run github.com/pressly/goose/v3/cmd/goose -dir internal/db/migrations postgres "$(POSTGRES_URL)" up

migrate-status: ## show DB migration status
	cd backend && go run github.com/pressly/goose/v3/cmd/goose -dir internal/db/migrations postgres "$(POSTGRES_URL)" status

# ---- Contracts ----

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
	  test -f "$$bc" || { echo "FATAL: $$bc not found - run 'make deploy' first"; exit 1; }; \
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

regen-abi: ## regenerate wallet.js ABIs from forge build
	@test -d contracts/out || { echo "FATAL: run 'make contracts-build' first"; exit 1; }
	@echo "  ABIs embedded in frontend/static/wallet.js - update manually if needed"

# ---- Zig Accelerated Libraries (sha256, keccak256, image sniffing) --------

ZIG_LIBS = zigsha256 zigcrypto zignsniff

zigmedia-build: ## compile all Zig libraries + build Go binary with zigmedia tag
	@mkdir -p bin
	@echo "  Compiling Zig libraries..."
	cd backend/zigsha256 && zig build-lib -O ReleaseFast -dynamic zigsha256.zig
	cd backend/zigcrypto && zig build-lib -O ReleaseFast -dynamic zigcrypto.zig
	cd backend/zigsniff && zig build-lib -O ReleaseFast -dynamic zignsniff.zig
	@echo "  Building Go binary with -tags zigmedia..."
	$(eval MW_GIT_SHA := $(shell git rev-parse HEAD 2>/dev/null || echo unknown))
	cd backend && CGO_ENABLED=1 go build -tags zigmedia -ldflags "-X github.com/OfficialA1manac/MagicWebb/backend/internal/api.MWServerBuildSHA=$(MW_GIT_SHA)" -o ../$(BINARY) $(SERVER:./backend/%=./%)
	@echo "  built: $(BINARY)  [zigmedia]  sha=$(MW_GIT_SHA)"

# Individual library build targets (for incremental compilation)
zigsha256-lib: ## compile only the Zig SHA-256 library
	@echo "  Compiling Zig SHA-256 library..."
	cd backend/zigsha256 && zig build-lib -O ReleaseFast -dynamic zigsha256.zig

zigcrypto-lib: ## compile only the Zig Keccak256 library
	@echo "  Compiling Zig crypto library..."
	cd backend/zigcrypto && zig build-lib -O ReleaseFast -dynamic zigcrypto.zig

zignsniff-lib: ## compile only the Zig image-sniffing library
	@echo "  Compiling Zig image-sniffing library..."
	cd backend/zigsniff && zig build-lib -O ReleaseFast -dynamic zignsniff.zig

zigmedia-test: ## run Zig unit tests for all libraries
	cd backend/zigsha256 && zig test zigsha256.zig
	cd backend/zigcrypto && zig test zigcrypto.zig
	cd backend/zigsniff && zig test zignsniff.zig

zigmedia-bench: ## benchmark Zig vs Go
	@echo "  Go benchmark: go test -bench=BenchmarkHash -benchmem ./internal/imagestore/"
	@echo "  Zig benchmark: cd backend/zigsha256 && zig build -Doptimize=ReleaseFast"

.PHONY: zigmedia-build zigsha256-lib zigcrypto-lib zignsniff-lib zigmedia-test zigmedia-bench

clean-zig: ## remove compiled Zig library artifacts
	rm -f backend/zigsha256/*.so backend/zigsha256/*.dylib backend/zigsha256/*.dll
	rm -f backend/zigcrypto/*.so backend/zigcrypto/*.dylib backend/zigcrypto/*.dll
	rm -f backend/zigsniff/*.so backend/zigsniff/*.dylib backend/zigsniff/*.dll

# ---- Quality ----

setup-hooks: ## install Git hooks via core.hooksPath -> .githooks/
	@git config core.hooksPath .githooks
	@chmod +x .githooks/pre-commit 2>/dev/null || true
	@echo "  Git hooks installed - .githooks/pre-commit runs on every commit."
.PHONY: setup-hooks

check-test-files: ## fail if .test.* files exist in app/src/pages/
	@bad=$$(find app/src/pages -maxdepth 1 -name '*.test.*' 2>/dev/null); \
	if [ -n "$$bad" ]; then \
		echo "ERROR: .test.* files found in app/src/pages/ - Astro will try to render them as routes:"; \
		echo "$$bad" | sed 's/^/  /'; \
		echo "Move them to app/src/__tests__/ and update the import path."; \
		exit 1; \
	fi
	@echo "  OK - no .test.* files in app/src/pages/"

test: ## run Go test suite with the race detector
	cd backend && go test ./... -race -count=1 -timeout 120s

check-fly-sync: ## verify magicwebb.fly.dev X-MW-Build-SHA matches origin/main
	@./tools/check-fly-sync.sh
.PHONY: check-fly-sync

vulncheck: ## run govulncheck over the backend
	cd backend && govulncheck ./...

lint: ## run golangci-lint over the backend
	cd backend && golangci-lint run ./...

# ---- Housekeeping ----

clean: ## remove build artifacts
	rm -rf bin contracts/out contracts/cache contracts/broadcast
	rm -f backend/zigsha256/*.o backend/zigsha256/*.so
	rm -f backend/zigcrypto/*.o backend/zigcrypto/*.so
	rm -f backend/zigsniff/*.o backend/zigsniff/*.so
