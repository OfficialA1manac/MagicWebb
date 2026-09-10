#!/usr/bin/env bash
# go-live.sh — take one mainnet (Songbird or Flare) from read-only to trading
# with a single command, once the deployer wallet holds native funds.
#
#   tools/go-live.sh <songbird|flare> [--dry-run] [--no-push] [--no-keeper-fund]
#
# What it does, in order (every step is idempotent or refuses to repeat):
#   0. Preflight: tools present, deployments/<network>.json is read-only, the
#      RPC answers the expected chain id, the deployer balance covers the
#      deploy (+ the keeper top-up unless --no-keeper-fund).
#   1. DeployV34 (8 CREATEs, unsealed: instant admin-gated upgrades on every
#      contract — owner directive 2026-09-09) with --slow, --broadcast.
#   2. tools/record-deployment.py <network>  → deployments/<network>.json
#   3. tools/check-deployments.sh            → schema + stray-address gate
#   4. Fund the keeper from the deployer (KEEPER_FUND_NATIVE, default 50) so
#      settlements can start the moment the app boots trading.
#   5. git commit + push (push = deploy: CI redeploys the app with the
#      addresses; the staged KEEPER_KEY Fly secret applies; indexer + keepers
#      start). --no-push leaves the commit local.
#   6. Prints the live-verification commands.
#
# Inputs (env, all with defaults except the deployer key):
#   PRIVATE_KEY          deployer key. Default: PRIVATE_KEY= line of the repo
#                        root .env (gitignored). Holds no power after deploy.
#   ADMIN_ADDR           the network admin (single wallet, saved offline).
#                        Default: the owner's Coston2 admin wallet, so one
#                        wallet holds upgrade rights on all three networks.
#   FEE_RECIPIENT_ADDR   default: the platform fee wallet used on Coston2.
#   KEEPER_ADDR          default: the per-network keeper generated 2026-09-08
#                        (its key is the app's staged KEEPER_KEY Fly secret).
#   MIN_DEPLOYER_NATIVE  default 12 — 8 CREATEs ≈ 13M gas ≈ 8.5 native at the
#                        650 gwei the networks run at, plus headroom.
#   KEEPER_FUND_NATIVE   default 50 — sent deployer → keeper after the deploy.
#                        So the owner's ONLY action is one transfer of
#                        (MIN_DEPLOYER_NATIVE + KEEPER_FUND_NATIVE) ≈ 62 native
#                        to the deployer per network. --no-keeper-fund skips it
#                        (fund the keeper directly instead).
#   RPC_URL              default: deployments/<network>.json rpc.primary.
#
# Never prints a private key. Refuses to run on a network whose record is
# already "deployed" (use tools/upgrade-cores.sh for upgrades).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

NET="${1:?network required: songbird | flare}"; shift || true
DRY=0; PUSH=1; FUND_KEEPER=1
for a in "$@"; do case "$a" in
  --dry-run) DRY=1;; --no-push) PUSH=0;; --no-keeper-fund) FUND_KEEPER=0;;
  *) echo "unknown flag $a" >&2; exit 2;; esac; done

case "$NET" in
  songbird) CHAIN=19; CUR=SGB; DEFAULT_KEEPER=0x979Dd049B9C7952f768e753bae575c199f847E4c ;;
  flare)    CHAIN=14; CUR=FLR; DEFAULT_KEEPER=0x1F0aD251f923579781EcB7D8D76edE08a6A498FF ;;
  *) echo "go-live is for the mainnets only (songbird | flare); Coston2 is live" >&2; exit 2 ;;
esac

for t in jq cast forge python git; do command -v "$t" >/dev/null || { echo "missing tool: $t" >&2; exit 1; }; done

DEP="deployments/$NET.json"
STATUS=$(jq -r .status "$DEP")
[ "$STATUS" = "read-only" ] || { echo "$DEP status is '$STATUS' — go-live only runs on a read-only network" >&2; exit 1; }
# The deploy commit must land on main (push = deploy); refuse a dirty tree so
# nothing unrelated rides along in the go-live commit.
[ "$(git rev-parse --abbrev-ref HEAD)" = main ] || { echo "check out main first (push = deploy)" >&2; exit 1; }
[ -z "$(git status --porcelain -- "$DEP")" ] || { echo "$DEP has uncommitted changes — commit or discard them first" >&2; exit 1; }
[ "$(jq -r .chainId "$DEP")" = "$CHAIN" ] || { echo "$DEP chainId != $CHAIN" >&2; exit 1; }

RPC="${RPC_URL:-$(jq -r .rpc.primary "$DEP")}"
GOT_CHAIN=$(cast chain-id --rpc-url "$RPC")
[ "$GOT_CHAIN" = "$CHAIN" ] || { echo "RPC $RPC answers chain id $GOT_CHAIN, expected $CHAIN" >&2; exit 1; }

# Deployer key: env, else the repo-root .env (never echoed).
if [ -z "${PRIVATE_KEY:-}" ]; then
  [ -f .env ] || { echo "PRIVATE_KEY not set and no root .env" >&2; exit 1; }
  PRIVATE_KEY=$(grep -E '^PRIVATE_KEY=' .env | head -1 | cut -d= -f2- | tr -d '"\r ')
fi
[ -n "$PRIVATE_KEY" ] || { echo "PRIVATE_KEY empty" >&2; exit 1; }
DEPLOYER=$(cast wallet address --private-key "$PRIVATE_KEY")

ADMIN_ADDR="${ADMIN_ADDR:-0x987f10f49b35a8ef48b664a8dc457f61c6fe2105}"
FEE_RECIPIENT_ADDR="${FEE_RECIPIENT_ADDR:-0x78993B71051de91C2D2595BC3475F07748927dc0}"
KEEPER_ADDR="${KEEPER_ADDR:-$DEFAULT_KEEPER}"
MIN_DEPLOYER_NATIVE="${MIN_DEPLOYER_NATIVE:-12}"
KEEPER_FUND_NATIVE="${KEEPER_FUND_NATIVE:-50}"
[ "$FUND_KEEPER" = 1 ] || KEEPER_FUND_NATIVE=0

[ "${ADMIN_ADDR,,}" != "${KEEPER_ADDR,,}" ] || { echo "ADMIN_ADDR must differ from KEEPER_ADDR" >&2; exit 1; }
[ "${ADMIN_ADDR,,}" != "${DEPLOYER,,}" ] || { echo "ADMIN_ADDR must not be the deployer (a hot key must not hold root)" >&2; exit 1; }

wei_to_native() { cast from-wei "$1"; }
BAL_WEI=$(cast balance "$DEPLOYER" --rpc-url "$RPC")
BAL=$(wei_to_native "$BAL_WEI")
NEED=$(python -c "print($MIN_DEPLOYER_NATIVE + $KEEPER_FUND_NATIVE)")
BASEFEE_GWEI=$(cast base-fee --rpc-url "$RPC" 2>/dev/null | awk '{printf "%.0f", $1/1e9}' || echo "?")

echo "== go-live $NET (chain $CHAIN) via $RPC"
echo "   deployer        $DEPLOYER   balance $BAL $CUR   (need ≥ $NEED: deploy $MIN_DEPLOYER_NATIVE + keeper top-up $KEEPER_FUND_NATIVE)"
echo "   admin           $ADMIN_ADDR   (unsealed: instant upgrades on all four contracts until renounceAdmin)"
echo "   fee recipient   $FEE_RECIPIENT_ADDR"
echo "   keeper          $KEEPER_ADDR   balance $(wei_to_native "$(cast balance "$KEEPER_ADDR" --rpc-url "$RPC")") $CUR"
echo "   base fee        ${BASEFEE_GWEI} gwei   (profile caps 2000/200)"

enough=$(python -c "print(1 if float('$BAL') >= float('$NEED') else 0)")
if [ "$enough" != 1 ]; then
  if [ "$DRY" = 1 ]; then
    echo "   (dry run) deployer underfunded — simulation continues, nothing is signed"
  else
    echo "NOT FUNDED: send $NEED $CUR to $DEPLOYER on $NET, then rerun. Nothing was signed." >&2
    exit 3
  fi
fi

export PRIVATE_KEY ADMIN_ADDR FEE_RECIPIENT_ADDR KEEPER_ADDR
unset SEAL   # never seal at deploy time (owner directive 2026-09-02 / 2026-09-09)

if [ "$DRY" = 1 ]; then
  echo "== simulating DeployV34 (no --broadcast)"
  SIM_LOG=$(cd contracts && forge script script/DeployV34.s.sol --rpc-url "$RPC" -vv --sender "$DEPLOYER" 2>&1) || {
    echo "$SIM_LOG" | tail -40 >&2; echo "simulation FAILED — nothing signed" >&2; exit 1; }
  echo "$SIM_LOG" | grep -E "MANAGER_ADDR|MARKETPLACE_ADDR|AUCTION_ADDR|OFFERBOOK_ADDR|verified" || true
  echo "dry run complete — nothing signed. Fund $DEPLOYER with $NEED $CUR and rerun without --dry-run."
  exit 0
fi

echo "== 1/6 deploying DeployV34 on $NET (8 CREATEs, --slow)"
(cd contracts && forge script script/DeployV34.s.sol --rpc-url "$RPC" --broadcast --slow -vv --private-key "$PRIVATE_KEY")

echo "== 2/6 recording deployments/$NET.json"
python tools/record-deployment.py "$NET"

echo "== 3/6 check-deployments"
bash tools/check-deployments.sh

if [ "$FUND_KEEPER" = 1 ]; then
  echo "== 4/6 funding keeper $KEEPER_ADDR with $KEEPER_FUND_NATIVE $CUR from the deployer"
  cast send "$KEEPER_ADDR" --value "$(cast to-wei "$KEEPER_FUND_NATIVE")" --rpc-url "$RPC" --private-key "$PRIVATE_KEY" >/dev/null
  echo "   keeper balance now $(wei_to_native "$(cast balance "$KEEPER_ADDR" --rpc-url "$RPC")") $CUR"
else
  echo "== 4/6 keeper funding skipped (--no-keeper-fund): fund $KEEPER_ADDR with ≈50 $CUR yourself"
fi

BLOCK=$(jq -r .indexFromBlock "$DEP")
MGR=$(jq -r .contracts.marketplaceManager "$DEP")
echo "== 5/6 commit"
git add "$DEP"
git commit -q -m "$NET: contracts live at block $BLOCK (v3.7 set, admin $ADMIN_ADDR, keeper $KEEPER_ADDR)

DeployV34 via tools/go-live.sh: four UUPS proxies (manager $MGR), unsealed —
instant admin-gated upgrades until the owner orders renounceAdmin()."
if [ "$PUSH" = 1 ]; then
  BRANCH=$(git rev-parse --abbrev-ref HEAD)
  [ "$BRANCH" = main ] || { echo "on branch '$BRANCH', not main — the commit is local; merge it to main to deploy" >&2; exit 1; }
  git push origin HEAD:main
  echo "   pushed — CI redeploys the app with the addresses (≈10 min)"
else
  echo "   --no-push: commit is local; push when ready"
fi

ORIGIN=$(jq -r .app.origin "$DEP")
cat <<EOF
== 6/6 verify once the deploy lands (x-mw-build-sha = $(git rev-parse --short HEAD)):
   curl -sI $ORIGIN/healthz | grep -i x-mw-build-sha
   curl -s  $ORIGIN/readyz
   curl -s  $ORIGIN/api/v1/governance      # deployed:true, admin $ADMIN_ADDR, keeper $KEEPER_ADDR, upgrade_delay 0
   curl -s  $ORIGIN/api/v1/indexer/slo     # head_lag_blocks < 30
   bash tools/check-governance.sh
Then one list → buy round-trip between two wallets on $ORIGIN.
EOF
