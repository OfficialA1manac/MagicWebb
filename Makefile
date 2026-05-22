SHELL := /bin/bash

# MagicWebb — marketplace app (frontend + contracts). See docs/PLATFORM.md.
# Backend runs on Render; frontend runs locally pointed at Render API.
ENV_FILE := frontend/.env.local
-include $(ENV_FILE)

CHAIN_ID      := $(if $(strip $(NEXT_PUBLIC_CHAIN_ID)),$(strip $(NEXT_PUBLIC_CHAIN_ID)),114)
DEPLOY_RPC    := $(if $(strip $(NEXT_PUBLIC_RPC_URL)),$(strip $(NEXT_PUBLIC_RPC_URL)),https://coston2-api.flare.network/ext/C/rpc)
RENDER_API_URL := $(if $(strip $(NEXT_PUBLIC_API_URL)),$(strip $(NEXT_PUBLIC_API_URL)),https://magicwebb-api.onrender.com)

PORT     ?= 3000
PID_FILE := frontend/.next/.web.pid
LOG_FILE := frontend/.next/web.log

export

.PHONY: help install build start stop restart status health \
        contracts-build contracts-test deploy load-addrs clean

help: ## show targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-18s %s\n", $$1, $$2}'

# --- App lifecycle (Coston2) --------------------------------------------------
#
#  make start  — wakes Render backend (free-tier spin-up), builds frontend if
#                needed, starts local Next.js prod server on PORT=3000
#  make stop   — stops local frontend; Render services continue running

start: ## start marketplace: wake Render backend + serve frontend locally
	@test -f $(ENV_FILE) || { echo "FATAL: $(ENV_FILE) missing — cp frontend/.env.example frontend/.env.local"; exit 1; }
	@mkdir -p frontend/.next
	@echo ""
	@echo "==> [1/3] Waiting for Render backend at $(RENDER_API_URL)/healthz"
	@echo "    (free-tier cold start can take up to 5 min — be patient)"
	@for i in $$(seq 1 30); do \
	  if curl -sf --max-time 10 "$(RENDER_API_URL)/healthz" >/dev/null 2>&1; then \
	    echo "    backend is up"; break; \
	  fi; \
	  printf "    attempt $$i/30 ..."; sleep 10; printf "\r"; \
	  if [ "$$i" -eq 30 ]; then \
	    echo ""; echo "FATAL: Render backend did not respond after 5 min"; \
	    echo "  check: $(RENDER_API_URL)/healthz"; exit 1; \
	  fi; \
	done
	@echo ""
	@echo "==> [2/3] Building frontend (skipped if .next/BUILD_ID exists)"
	@cd frontend && ( [ -f .next/BUILD_ID ] || npm run build )
	@echo ""
	@echo "==> [3/3] Starting local frontend"
	@if [ -f $(PID_FILE) ] && kill -0 $$(cat $(PID_FILE)) 2>/dev/null; then \
	  echo "    already running pid=$$(cat $(PID_FILE))"; \
	else \
	  cd frontend && setsid nohup npm run start -- -p $(PORT) >> ../$(LOG_FILE) 2>&1 & echo $$! > ../$(PID_FILE); \
	  sleep 2; \
	  echo "    started pid=$$(cat $(PID_FILE))"; \
	fi
	@echo ""
	@echo "  frontend  http://127.0.0.1:$(PORT)"
	@echo "  backend   $(RENDER_API_URL)"
	@echo "  chain     Coston2 (chain_id=$(CHAIN_ID))"
	@echo "  logs      $(LOG_FILE)"
	@echo ""

stop: ## stop marketplace (kills local frontend; Render keeps running)
	@echo ""
	@echo "==> Stopping local frontend"
	@if [ -f $(PID_FILE) ]; then \
	  pid=$$(cat $(PID_FILE)); \
	  pgid=$$(ps -o pgid= -p $$pid 2>/dev/null | tr -d ' '); \
	  if [ -n "$$pgid" ]; then \
	    kill -TERM -$$pgid 2>/dev/null || true; sleep 2; kill -KILL -$$pgid 2>/dev/null || true; \
	  else \
	    kill -TERM $$pid 2>/dev/null || true; sleep 2; kill -KILL $$pid 2>/dev/null || true; \
	  fi; \
	  rm -f $(PID_FILE); echo "    stopped"; \
	else \
	  echo "    not running"; \
	fi
	@echo "    note: Render backend still running at $(RENDER_API_URL)"
	@echo ""

restart: stop start ## restart the marketplace

status: ## show local pid + port + Render backend + Coston2 RPC health
	@echo ""
	@if [ -f $(PID_FILE) ] && kill -0 $$(cat $(PID_FILE)) 2>/dev/null; then \
	  echo "  frontend  pid=$$(cat $(PID_FILE)) (up)"; \
	else \
	  echo "  frontend  (down)"; \
	fi
	@(echo > /dev/tcp/127.0.0.1/$(PORT)) 2>/dev/null && echo "  port      $(PORT) open" || echo "  port      $(PORT) closed"
	@curl -sf --max-time 5 "$(RENDER_API_URL)/healthz" >/dev/null 2>&1 \
	  && echo "  backend   $(RENDER_API_URL) (up)" \
	  || echo "  backend   $(RENDER_API_URL) (down or sleeping)"
	@cast block-number --rpc-url $(DEPLOY_RPC) >/dev/null 2>&1 && echo "  coston2   rpc ok" || echo "  coston2   rpc down"
	@echo ""

health: ## HTTP health check for both local frontend and Render backend
	@echo ""
	@curl -sf "http://127.0.0.1:$(PORT)/" >/dev/null \
	  && echo "  frontend  http://127.0.0.1:$(PORT)  ok" \
	  || echo "  frontend  http://127.0.0.1:$(PORT)  down"
	@curl -sf --max-time 10 "$(RENDER_API_URL)/healthz" >/dev/null \
	  && echo "  backend   $(RENDER_API_URL)/healthz  ok" \
	  || echo "  backend   $(RENDER_API_URL)/healthz  down"
	@cast block-number --rpc-url $(DEPLOY_RPC) >/dev/null 2>&1 \
	  && echo "  coston2   rpc ok" \
	  || echo "  coston2   rpc down"
	@echo ""

# --- Contracts ----------------------------------------------------------------

contracts-build: ## forge build
	cd contracts && forge build --sizes

contracts-test: ## forge test
	cd contracts && forge test -vvv

deploy: ## deploy contracts to Coston2 (uses NEXT_PUBLIC_RPC_URL + PRIVATE_KEY from $(ENV_FILE))
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

install: ## one-time: node modules + forge libs
	cd frontend && npm install --no-audit --no-fund
	cd contracts && forge install --no-commit OpenZeppelin/openzeppelin-contracts foundry-rs/forge-std

build: ## production build of the Next.js app
	cd frontend && npm run build

clean: ## remove all build artifacts
	rm -rf contracts/out contracts/cache contracts/broadcast
	rm -rf frontend/.next frontend/dist
