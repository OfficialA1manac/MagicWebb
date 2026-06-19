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

dev: ## run server locally (go run, no build step)
	go run $(SERVER)

build: ## compile single binary → bin/magicwebb (auto-rebuilds tailwind.css first)
	@mkdir -p bin
	cd backend && go run ./cmd/buildtailwindcss || echo 'warning: tailwind rebuild failed (offline?); using committed tailwind.css'
	cd backend && go build -o ../$(BINARY) $(SERVER:./backend/%=./%)
	@echo "  built: $(BINARY)"

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
	@echo "  ABIs embedded in backend/internal/ui/static/wallet.js — update manually if needed"

# ── Quality ───────────────────────────────────────────────────────────────────

test: ## run Go test suite with the race detector
	cd backend && go test ./... -race -count=1 -timeout 120s

lint: ## run golangci-lint over the backend
	cd backend && golangci-lint run ./...

# ── Housekeeping ──────────────────────────────────────────────────────────────

clean: ## remove build artifacts
	rm -rf bin contracts/out contracts/cache contracts/broadcast
