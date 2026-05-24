#!/usr/bin/env bash
# MagicWebb Feature Configuration
# Run: bash configure.sh
# Outputs a feature summary and (optionally) patches frontend flags

set -euo pipefail

YELLOW='\033[1;33m'
GREEN='\033[0;32m'
RED='\033[0;31m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

ask() {
  local label=$1 default=${2:-y}
  local prompt; [[ $default == y ]] && prompt="[Y/n]" || prompt="[y/N]"
  printf "  ${CYAN}%-42s${NC} ${prompt} " "$label"
  read -r ans
  ans=${ans:-$default}
  [[ ${ans,,} == y || ${ans,,} == yes ]]
}

header() { printf "\n${BOLD}${YELLOW}==> %s${NC}\n" "$1"; }

echo ""
printf "${BOLD}${GREEN}MagicWebb Feature Configuration — Coston2 (chain 114)${NC}\n"
echo "  Answer y/n for each feature. Defaults shown in [brackets]."
echo ""

# ── Core Trading ──────────────────────────────────────────────────────────────
header "CORE TRADING (deployed on Coston2 — require contract redeployment to remove)"

FEAT_LISTINGS=y
FEAT_AUCTIONS=y
FEAT_OFFERS=y

printf "  ${GREEN}✓${NC}  Fixed-price listings (ERC-721 + ERC-1155)      [ALWAYS ON]\n"
printf "  ${GREEN}✓${NC}  1.5%% platform fee to creator wallet             [ALWAYS ON]\n"
printf "  ${GREEN}✓${NC}  No royalties                                     [ALWAYS OFF]\n"

if ask "Auctions (English-style, commit-reveal MEV protect)" y; then
  FEAT_AUCTIONS=y; printf "  ${GREEN}on${NC}\n"
else
  FEAT_AUCTIONS=n; printf "  ${RED}off${NC}  (hide auction UI; contracts still on-chain)\n"
fi

if ask "Off-chain EIP-712 Offers (collection + token-level)" y; then
  FEAT_OFFERS=y; printf "  ${GREEN}on${NC}\n"
else
  FEAT_OFFERS=n; printf "  ${RED}off${NC}  (hide offers UI; contracts still on-chain)\n"
fi

# ── Backend / Indexer ─────────────────────────────────────────────────────────
header "BACKEND FEATURES (Go API + indexer — toggle via env vars)"

if ask "Auction keeper bot (auto-settle ended auctions)" n; then
  FEAT_KEEPER=y; printf "  ${GREEN}on${NC}  Set KEEPER_KEY in Render dashboard\n"
else
  FEAT_KEEPER=n; printf "  ${RED}off${NC} Users must manually settle their auctions\n"
fi

if ask "Trending scores (Zig hot-path, 1h/24h/7d windows)" y; then
  FEAT_TRENDING=y; printf "  ${GREEN}on${NC}\n"
else
  FEAT_TRENDING=n; printf "  ${RED}off${NC}\n"
fi

if ask "Real-time events (GraphQL subscriptions + SSE)" y; then
  FEAT_REALTIME=y; printf "  ${GREEN}on${NC}\n"
else
  FEAT_REALTIME=n; printf "  ${RED}off${NC}\n"
fi

# ── Frontend Pages ────────────────────────────────────────────────────────────
header "FRONTEND PAGES (Next.js — hide/show routes)"

if ask "Metrics page (/metrics — platform volume, sales, listings)" y; then
  FEAT_METRICS=y; printf "  ${GREEN}on${NC}\n"
else
  FEAT_METRICS=n; printf "  ${RED}off${NC}\n"
fi

if ask "Search page (/search — wallet NFT scanner)" y; then
  FEAT_SEARCH=y; printf "  ${GREEN}on${NC}\n"
else
  FEAT_SEARCH=n; printf "  ${RED}off${NC}\n"
fi

if ask "User profiles (/profile/[addr])" y; then
  FEAT_PROFILES=y; printf "  ${GREEN}on${NC}\n"
else
  FEAT_PROFILES=n; printf "  ${RED}off${NC}\n"
fi

if ask "Favorites / watchlist toggle" y; then
  FEAT_FAVORITES=y; printf "  ${GREEN}on${NC}\n"
else
  FEAT_FAVORITES=n; printf "  ${RED}off${NC}\n"
fi

if ask "WalletConnect (mobile wallet support)" y; then
  FEAT_WALLETCONNECT=y; printf "  ${GREEN}on${NC}  Needs NEXT_PUBLIC_WALLETCONNECT_PROJECT_ID\n"
else
  FEAT_WALLETCONNECT=n; printf "  ${RED}off${NC} MetaMask/injected wallets only\n"
fi

# ── Roadmap Features (not yet built) ─────────────────────────────────────────
header "ROADMAP FEATURES (not yet implemented — plan for future)"

printf "\n  These features exist on OpenSea/Blur/LooksRare but NOT yet in MagicWebb:\n\n"

FUTURE=()
ask "Collection stats (floor price, 24h volume, holder count)" n && FUTURE+=("collection-stats")    && printf "  ${YELLOW}+queued${NC}\n" || printf "  skip\n"
ask "Price history chart (sales over time per token/collection)" n && FUTURE+=("price-history")     && printf "  ${YELLOW}+queued${NC}\n" || printf "  skip\n"
ask "Trait/attribute filtering on collection pages" n            && FUTURE+=("trait-filter")         && printf "  ${YELLOW}+queued${NC}\n" || printf "  skip\n"
ask "NFT metadata refresh (force re-fetch tokenURI)" n          && FUTURE+=("metadata-refresh")      && printf "  ${YELLOW}+queued${NC}\n" || printf "  skip\n"
ask "Batch listing (list multiple NFTs in one session)" n       && FUTURE+=("batch-listing")          && printf "  ${YELLOW}+queued${NC}\n" || printf "  skip\n"
ask "Floor sweep (buy multiple cheapest listings at once)" n    && FUTURE+=("floor-sweep")            && printf "  ${YELLOW}+queued${NC}\n" || printf "  skip\n"
ask "Creator verification badges on collections" n              && FUTURE+=("creator-verification")   && printf "  ${YELLOW}+queued${NC}\n" || printf "  skip\n"

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
printf "${BOLD}${GREEN}============================================================${NC}\n"
printf "${BOLD}  Configuration Summary${NC}\n"
printf "${BOLD}${GREEN}============================================================${NC}\n"
echo ""
printf "  Core:        listings=ON  fee=1.5%%  royalties=NONE\n"
printf "  Auctions:    %s\n" "$([[ $FEAT_AUCTIONS == y ]] && echo ON || echo OFF)"
printf "  Offers:      %s\n" "$([[ $FEAT_OFFERS == y ]] && echo ON || echo OFF)"
printf "  Keeper bot:  %s\n" "$([[ $FEAT_KEEPER == y ]] && echo "ON  (set KEEPER_KEY in Render)" || echo OFF)"
printf "  Trending:    %s\n" "$([[ $FEAT_TRENDING == y ]] && echo ON || echo OFF)"
printf "  Realtime:    %s\n" "$([[ $FEAT_REALTIME == y ]] && echo ON || echo OFF)"
printf "  Metrics pg:  %s\n" "$([[ $FEAT_METRICS == y ]] && echo ON || echo OFF)"
printf "  Search pg:   %s\n" "$([[ $FEAT_SEARCH == y ]] && echo ON || echo OFF)"
printf "  Profiles:    %s\n" "$([[ $FEAT_PROFILES == y ]] && echo ON || echo OFF)"
printf "  Favorites:   %s\n" "$([[ $FEAT_FAVORITES == y ]] && echo ON || echo OFF)"
printf "  WalletConn:  %s\n" "$([[ $FEAT_WALLETCONNECT == y ]] && echo ON || echo OFF)"

if [[ ${#FUTURE[@]} -gt 0 ]]; then
  echo ""
  printf "  Roadmap queued: %s\n" "${FUTURE[*]}"
  echo "  Tell Claude: 'build [feature-name]' to implement any of these."
fi

echo ""
printf "  ${CYAN}Network:${NC} Coston2 (chain 114) -- production-equivalent testnet\n"
printf "  ${CYAN}Upgrade:${NC} Deploy new contracts to Flare mainnet, update NEXT_PUBLIC_* vars\n"
echo ""
printf "${BOLD}${GREEN}============================================================${NC}\n"
printf "  To start the full system:  make start\n"
printf "  To stop:                   make stop\n"
printf "  To check status:           make status\n"
printf "${BOLD}${GREEN}============================================================${NC}\n"
echo ""
