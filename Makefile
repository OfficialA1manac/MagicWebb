SHELL := /bin/bash
-include .env
export

CHAIN_ID         ?= 114
COSTON2_RPC      ?= https://coston2-api.flare.network/ext/C/rpc
COSTON2_WS       ?= wss://coston2-api.flare.network/ext/bc/C/ws
DATABASE_URL     ?= postgres://postgres:postgres@localhost:5432/webbplace?sslmode=disable
HTTP_LISTEN      ?= :8080
GRPC_LISTEN      ?= :9090

.PHONY: help setup check-tools install-deps env db-up db-down migrate migrate-down \
        contracts-build contracts-test deploy-coston2 load-addrs \
        codegen codegen-graphql codegen-grpc codegen-sqlc codegen-abi \
        zig-build api indexer matcher web dev stop clean

help:
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-22s %s\n", $$1, $$2}'

# 0. one-shot bootstrap
setup: check-tools install-deps env db-up migrate contracts-build codegen zig-build ## first-time setup

check-tools: ## verify host has go, node, pnpm, forge, goose, sqlc, protoc, zig, psql
	@for t in go node pnpm forge goose sqlc protoc zig psql; do \
	  command -v $$t >/dev/null || { echo "missing: $$t"; exit 1; }; \
	done
	@echo "all tools present"

install-deps: ## install backend + frontend + foundry deps
	cd backend  && go mod download
	cd frontend && pnpm install
	cd contracts && forge install --no-commit \
	  OpenZeppelin/openzeppelin-contracts \
	  foundry-rs/forge-std

env: ## copy .env.example to .env if missing
	@test -f .env || cp .env.example .env
	@echo "review .env (PRIVATE_KEY, DATABASE_URL, addresses populated by 'make load-addrs')"

# 1. database
db-up: ## create local db if missing
	@psql "postgres://postgres:postgres@localhost:5432/postgres" -tAc \
	  "SELECT 1 FROM pg_database WHERE datname='webbplace'" | grep -q 1 \
	  || psql "postgres://postgres:postgres@localhost:5432/postgres" -c "CREATE DATABASE webbplace"

db-down: ## drop local db (DESTRUCTIVE)
	psql "postgres://postgres:postgres@localhost:5432/postgres" -c "DROP DATABASE IF EXISTS webbplace"

migrate: ## goose up
	cd backend && goose -dir migrations postgres "$(DATABASE_URL)" up

migrate-down: ## goose down 1
	cd backend && goose -dir migrations postgres "$(DATABASE_URL)" down

# 2. contracts
contracts-build: ## forge build
	cd contracts && forge build --sizes

contracts-test: ## forge test with gas report
	cd contracts && forge test -vvv --gas-report

deploy-coston2: ## deploy all contracts to Coston2 (PRIVATE_KEY funded via faucet)
	cd contracts && forge script script/DeployCoston2.s.sol \
	  --rpc-url $(COSTON2_RPC) \
	  --broadcast \
	  --private-key $(PRIVATE_KEY) \
	  --slow

load-addrs: ## parse Foundry broadcast JSON → .env + frontend/src/lib/contracts.ts
	./tools/load_contract_addrs.sh

# 3. codegen
codegen: codegen-graphql codegen-grpc codegen-sqlc codegen-abi ## run all generators

codegen-graphql:
	cd backend && go run github.com/99designs/gqlgen generate

codegen-grpc:
	cd backend && protoc --go_out=. --go-grpc_out=. proto/*.proto

codegen-sqlc:
	cd backend && sqlc generate

codegen-abi: ## abigen Go bindings from forge artifacts
	cd backend && for c in Marketplace AuctionHouse OfferBook FeeVault; do \
	  abigen --abi ../contracts/out/$$c.sol/$$c.json --pkg chain --type $$c \
	    --out internal/chain/$$c.go ; \
	done

zig-build: ## build libwebbplace_perf.a for CGo
	cd backend/zig && zig build -Doptimize=ReleaseFast

# 4. run services in foreground
api: ## run GraphQL gateway on :8080
	cd backend && go run ./cmd/api

indexer: ## run chain indexer
	cd backend && go run ./cmd/indexer

matcher: ## run gRPC matcher on :9090
	cd backend && go run ./cmd/matcher

web: ## run frontend on :5173
	cd frontend && pnpm dev

# 5. orchestrated dev (background)
dev: ## start matcher, indexer, api, web in background; logs in ./logs/
	@mkdir -p logs
	@echo "==> matcher"  ; (cd backend && go run ./cmd/matcher  > ../logs/matcher.log  2>&1 &)
	@sleep 1
	@echo "==> indexer"  ; (cd backend && go run ./cmd/indexer  > ../logs/indexer.log  2>&1 &)
	@echo "==> api"      ; (cd backend && go run ./cmd/api      > ../logs/api.log      2>&1 &)
	@sleep 1
	@echo "==> web"      ; (cd frontend && pnpm dev             > ../logs/web.log      2>&1 &)
	@echo "tail -f logs/*.log to follow. 'make stop' to kill."

stop: ## kill background dev processes
	-pkill -f "cmd/matcher"   || true
	-pkill -f "cmd/indexer"   || true
	-pkill -f "cmd/api"       || true
	-pkill -f "vite"          || true

clean: ## remove generated artifacts
	rm -rf backend/internal/graph/generated backend/internal/grpc/gen backend/internal/db/gen
	rm -rf contracts/out contracts/cache contracts/broadcast
	rm -rf backend/zig/zig-out backend/zig/.zig-cache
	rm -rf frontend/dist frontend/node_modules/.vite
	rm -rf logs
