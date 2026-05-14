SHELL := /bin/bash

# MagicWebb — marketplace app (frontend + contracts). See docs/PLATFORM.md.
ENV_FILE := frontend/.env.local
-include $(ENV_FILE)

CHAIN_ID := $(if $(strip $(NEXT_PUBLIC_CHAIN_ID)),$(strip $(NEXT_PUBLIC_CHAIN_ID)),114)
DEPLOY_RPC := $(if $(strip $(NEXT_PUBLIC_RPC_URL)),$(strip $(NEXT_PUBLIC_RPC_URL)),https://coston2-api.flare.network/ext/C/rpc)

PORT        ?= 3000
PID_FILE    := frontend/.next/.web.pid
LOG_FILE    := frontend/.next/web.log

export

.PHONY: help install build start stop restart status health \
        contracts-build contracts-test deploy load-addrs clean

help: ## show targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-18s %s\n", $$1, $$2}'

# --- App lifecycle ------------------------------------------------------------

install: ## one-time: node modules + forge libs
	cd frontend && npm install --no-audit --no-fund
	cd contracts && forge install --no-commit OpenZeppelin/openzeppelin-contracts foundry-rs/forge-std

build: ## production build of the Next.js app
	cd frontend && npm run build

start: ## start the marketplace (prod build, background)
	@test -f $(ENV_FILE) || { echo "FATAL: $(ENV_FILE) missing — cp frontend/.env.example frontend/.env.local"; exit 1; }
	@mkdir -p frontend/.next
	@if [ -f $(PID_FILE) ] && kill -0 $$(cat $(PID_FILE)) 2>/dev/null; then \
	  echo "  already running pid=$$(cat $(PID_FILE))"; exit 0; \
	fi
	@cd frontend && ( [ -d .next ] && [ -f .next/BUILD_ID ] || npm run build )
	@cd frontend && setsid nohup npm run start -- -p $(PORT) >> ../$(LOG_FILE) 2>&1 & echo $$! > ../$(PID_FILE)
	@sleep 2
	@echo "  web pid=$$(cat $(PID_FILE))  url=http://127.0.0.1:$(PORT)  log=$(LOG_FILE)"

stop: ## stop the marketplace
	@if [ -f $(PID_FILE) ]; then \
	  pid=$$(cat $(PID_FILE)); \
	  pgid=$$(ps -o pgid= -p $$pid 2>/dev/null | tr -d ' '); \
	  if [ -n "$$pgid" ]; then kill -TERM -$$pgid 2>/dev/null || true; sleep 2; kill -KILL -$$pgid 2>/dev/null || true; \
	  else kill -TERM $$pid 2>/dev/null || true; sleep 2; kill -KILL $$pid 2>/dev/null || true; fi; \
	  rm -f $(PID_FILE); echo "  stopped"; \
	else echo "  not running"; fi

restart: stop start ## restart the marketplace

status: ## show pid + port + RPC health
	@if [ -f $(PID_FILE) ] && kill -0 $$(cat $(PID_FILE)) 2>/dev/null; then echo "  pid=$$(cat $(PID_FILE)) (up)"; else echo "  pid=- (down)"; fi
	@(echo > /dev/tcp/127.0.0.1/$(PORT)) 2>/dev/null && echo "  port $(PORT) open" || echo "  port $(PORT) closed"
	@cast block-number --rpc-url $(DEPLOY_RPC) >/dev/null 2>&1 && echo "  rpc ok" || echo "  rpc down"

health: ## HTTP + RPC checks (expects app on $(PORT))
	@command -v curl >/dev/null || { echo "FATAL: curl required for health"; exit 1; }
	@curl -sf "http://127.0.0.1:$(PORT)/" >/dev/null && echo "  http $(PORT) ok" || echo "  http $(PORT) down"
	@cast block-number --rpc-url $(DEPLOY_RPC) >/dev/null 2>&1 && echo "  rpc ok" || echo "  rpc down"

# --- Contracts ----------------------------------------------------------------

contracts-build: ## forge build
	cd contracts && forge build --sizes

contracts-test: ## forge test
	cd contracts && forge test -vvv

deploy: ## deploy contracts (uses NEXT_PUBLIC_RPC_URL + PRIVATE_KEY from $(ENV_FILE))
	@test -n "$(PRIVATE_KEY)" || { echo "FATAL: PRIVATE_KEY missing in $(ENV_FILE)"; exit 1; }
	@addr=$$(cast wallet address --private-key "$(PRIVATE_KEY)"); \
	  bal=$$(cast balance $$addr --rpc-url $(DEPLOY_RPC)); \
	  echo "  deployer=$$addr balance=$$bal wei"; \
	  [ "$$bal" != "0" ] || { echo "FATAL: deployer unfunded — https://faucet.flare.network"; exit 1; }
	cd contracts && forge script script/DeployCoston2.s.sol --rpc-url $(DEPLOY_RPC) --broadcast --private-key $(PRIVATE_KEY) --slow
	@"$(MAKE)" load-addrs

load-addrs: ## sync deployed addresses from broadcast → $(ENV_FILE)
	@bc=contracts/broadcast/DeployCoston2.s.sol/$(CHAIN_ID)/run-latest.json; \
	  test -f "$$bc" || { echo "FATAL: $$bc not found — run 'make deploy' first"; exit 1; }; \
	  command -v jq >/dev/null || { echo "FATAL: jq required"; exit 1; }; \
	  m=$$(jq -r '.transactions[]|select(.transactionType=="CREATE" and .contractName=="Marketplace")|.contractAddress' "$$bc" | head -n1); \
	  a=$$(jq -r '.transactions[]|select(.transactionType=="CREATE" and .contractName=="AuctionHouse")|.contractAddress' "$$bc" | head -n1); \
	  o=$$(jq -r '.transactions[]|select(.transactionType=="CREATE" and .contractName=="OfferBook")|.contractAddress'    "$$bc" | head -n1); \
	  test -n "$$m" -a -n "$$a" -a -n "$$o" || { echo "FATAL: missing address(es) in broadcast"; exit 1; }; \
	  for kv in NEXT_PUBLIC_MARKETPLACE_ADDR=$$m NEXT_PUBLIC_AUCTION_ADDR=$$a NEXT_PUBLIC_OFFER_ADDR=$$o; do \
	    k=$${kv%%=*}; v=$${kv#*=}; \
	    if grep -qE "^$$k=" $(ENV_FILE); then sed -i "s|^$$k=.*|$$k=$$v|" $(ENV_FILE); \
	    else printf '%s=%s\n' "$$k" "$$v" >> $(ENV_FILE); fi; \
	    echo "  $$k=$$v"; \
	  done

# --- Housekeeping -------------------------------------------------------------

clean: ## remove all build artifacts
	rm -rf contracts/out contracts/cache contracts/broadcast
	rm -rf frontend/.next frontend/dist
