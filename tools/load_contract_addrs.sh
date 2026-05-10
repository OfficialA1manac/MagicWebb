#!/usr/bin/env bash
# Parses contracts/broadcast/DeployCoston2.s.sol/114/run-latest.json,
# extracts deployed addresses by contract name, updates ./.env, and writes
# frontend/.env.local with NEXT_PUBLIC_* vars for Next.js.
#
# Note: production deploy does NOT include FeeVault — it routes fees directly
#       to CREATOR_ADDR. If FeeVault is deployed separately, it is ignored here.
#
# Requires: jq

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BROADCAST="$ROOT/contracts/broadcast/DeployCoston2.s.sol/114/run-latest.json"
ENVFILE="$ROOT/.env"
FRONTEND_ENV="$ROOT/frontend/.env.local"

if [[ ! -f "$BROADCAST" ]]; then
  echo "broadcast file not found: $BROADCAST" >&2
  echo "run 'make deploy-coston2' first." >&2
  exit 1
fi
command -v jq >/dev/null || { echo "missing: jq"; exit 1; }

extract() {
  jq -r --arg name "$1" \
    '.transactions[] | select(.transactionType=="CREATE" and .contractName==$name) | .contractAddress' \
    "$BROADCAST" | head -n1
}

MARKETPLACE=$(extract Marketplace)
AUCTION=$(extract AuctionHouse)
OFFER=$(extract OfferBook)

[[ -n "$MARKETPLACE" ]] || { echo "missing Marketplace address"; exit 1; }
[[ -n "$AUCTION"     ]] || { echo "missing AuctionHouse address"; exit 1; }
[[ -n "$OFFER"       ]] || { echo "missing OfferBook address";  exit 1; }

echo "Marketplace  = $MARKETPLACE"
echo "AuctionHouse = $AUCTION"
echo "OfferBook    = $OFFER"

if [[ ! -f "$ENVFILE" ]]; then
  cp "$ROOT/.env.example" "$ENVFILE"
fi

set_kv() {
  local file="$1" key="$2" val="$3"
  if [[ -f "$file" ]] && grep -qE "^${key}=" "$file"; then
    if [[ "$(uname -s)" == "Darwin" ]]; then
      sed -i '' "s|^${key}=.*|${key}=${val}|" "$file"
    else
      sed -i "s|^${key}=.*|${key}=${val}|" "$file"
    fi
  else
    printf '%s=%s\n' "$key" "$val" >> "$file"
  fi
}

# Backend .env
set_kv "$ENVFILE" MARKETPLACE_ADDR "$MARKETPLACE"
set_kv "$ENVFILE" AUCTION_ADDR     "$AUCTION"
set_kv "$ENVFILE" OFFER_ADDR       "$OFFER"

# Frontend .env.local (Next.js public vars)
mkdir -p "$ROOT/frontend"
if [[ ! -f "$FRONTEND_ENV" && -f "$ROOT/frontend/.env.local.example" ]]; then
  cp "$ROOT/frontend/.env.local.example" "$FRONTEND_ENV"
fi
set_kv "$FRONTEND_ENV" NEXT_PUBLIC_MARKETPLACE_ADDR "$MARKETPLACE"
set_kv "$FRONTEND_ENV" NEXT_PUBLIC_AUCTION_ADDR     "$AUCTION"
set_kv "$FRONTEND_ENV" NEXT_PUBLIC_OFFER_ADDR       "$OFFER"
set_kv "$FRONTEND_ENV" NEXT_PUBLIC_CHAIN_ID         "114"
set_kv "$FRONTEND_ENV" NEXT_PUBLIC_RPC_URL          "https://coston2-api.flare.network/ext/C/rpc"

echo "wrote addresses → .env and frontend/.env.local"
