#!/usr/bin/env bash
# upgrade-cores.sh — install new Marketplace / AuctionHouse / OfferBook
# implementations on one network through the admin-gated UUPS path.
#
# Upgrades are INSTANT on every network (upgradeDelay()==0): queueUpgrade and
# upgradeTo run back-to-back, signed by that network's ADMIN key. The queue is
# exact-match and one-shot, and a queued entry expires after 7 days, so a
# half-run leaves nothing dangerous behind (cancelUpgrade() is also available).
#
# The three implementations bake two immutables — feeRecipient and manager —
# which this script READS FROM THE LIVE PROXIES so an upgrade can never change
# them by accident. Change them on purpose with FEE_RECIPIENT_ADDR / MANAGER_ADDR.
#
# Usage:
#   ADMIN_KEY=0x… DEPLOYER_KEY=0x… tools/upgrade-cores.sh <coston2|songbird|flare> [--dry-run]
#   ADMIN_KEY=0x… tools/upgrade-cores.sh <network> --rollback     # reinstall superseded_impls
#   tools/upgrade-cores.sh <network> --verify                     # read-only: print current state
#
# Keys are read from the environment only; nothing is written to disk. The
# admin key is the network's root credential: use it from an offline-held
# wallet, in one terminal, and clear the variable afterwards.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

NET="${1:?network required: coston2 | songbird | flare}"; shift || true
MODE="upgrade"
for a in "$@"; do case "$a" in --dry-run) MODE=dry;; --rollback) MODE=rollback;; --verify) MODE=verify;; *) echo "unknown flag $a"; exit 2;; esac; done

DEP="deployments/$NET.json"
[ "$(jq -r .status "$DEP")" = "deployed" ] || { echo "$NET is not deployed"; exit 1; }
RPC="${RPC_URL:-$(jq -r .rpc.primary "$DEP")}"
MP=$(jq -r .contracts.marketplace "$DEP")
AH=$(jq -r .contracts.auctionHouse "$DEP")
OB=$(jq -r .contracts.offerBook "$DEP")
MGR_LIVE=$(cast call "$MP" "manager()(address)" --rpc-url "$RPC")
FEE_LIVE=$(cast call "$MP" "feeRecipient()(address)" --rpc-url "$RPC")
FEE="${FEE_RECIPIENT_ADDR:-$FEE_LIVE}"
MGR="${MANAGER_ADDR:-$MGR_LIVE}"
DELAY=$(cast call "$MP" "upgradeDelay()(uint64)" --rpc-url "$RPC")
ADMIN=$(cast call "$MGR" "admin()(address)" --rpc-url "$RPC")

impl_of() { # ERC-1967 implementation slot
  cast storage "$1" 0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc --rpc-url "$RPC" | sed 's/^0x0\{24\}/0x/'
}

echo "== $NET via $RPC"
echo "   admin          $ADMIN"
echo "   manager        $MGR   (live: $MGR_LIVE)"
echo "   feeRecipient   $FEE   (live: $FEE_LIVE)"
echo "   upgradeDelay   ${DELAY}s"
for pair in "Marketplace:$MP" "AuctionHouse:$AH" "OfferBook:$OB"; do
  echo "   ${pair%%:*} proxy ${pair#*:} → impl $(impl_of "${pair#*:}")"
done
[ "$MODE" = verify ] && exit 0

if [ "$MODE" != dry ]; then
  : "${ADMIN_KEY:?ADMIN_KEY required (the network's admin wallet)}"
  SIGNER=$(cast wallet address --private-key "$ADMIN_KEY")
  [ "${SIGNER,,}" = "${ADMIN,,}" ] || { echo "ADMIN_KEY signs as $SIGNER but admin() is $ADMIN"; exit 1; }
fi

declare -A NEW
if [ "$MODE" = rollback ]; then
  for k in marketplace auctionHouse offerBook; do
    NEW[$k]=$(jq -r ".superseded_impls.$k // empty" "$DEP")
    [ -n "${NEW[$k]}" ] || { echo "no superseded_impls.$k recorded in $DEP"; exit 1; }
  done
else
  : "${DEPLOYER_KEY:?DEPLOYER_KEY required to deploy the new implementations}"
  (cd contracts && forge build >/dev/null)
  deploy() { # contract name, constructor args
    if [ "$MODE" = dry ]; then echo "0xDRYRUN000000000000000000000000000000$RANDOM"; return; fi
    (cd contracts && forge create "src/$1.sol:$1" --rpc-url "$RPC" --private-key "$DEPLOYER_KEY" --broadcast \
       --constructor-args "$FEE" "$MGR" --json | jq -r .deployedTo)
  }
  echo "== deploying implementations (feeRecipient=$FEE manager=$MGR)"
  NEW[marketplace]=$(deploy Marketplace);  echo "   Marketplace  → ${NEW[marketplace]}"
  NEW[auctionHouse]=$(deploy AuctionHouse); echo "   AuctionHouse → ${NEW[auctionHouse]}"
  NEW[offerBook]=$(deploy OfferBook);       echo "   OfferBook    → ${NEW[offerBook]}"
fi

OLD_MP=$(impl_of "$MP"); OLD_AH=$(impl_of "$AH"); OLD_OB=$(impl_of "$OB")

install() { # proxy newImpl
  if [ "$MODE" = dry ]; then echo "   (dry) queueUpgrade+upgradeTo $2 on $1"; return; fi
  cast send "$1" "queueUpgrade(address)" "$2" --rpc-url "$RPC" --private-key "$ADMIN_KEY" >/dev/null
  cast send "$1" "upgradeTo(address)"    "$2" --rpc-url "$RPC" --private-key "$ADMIN_KEY" >/dev/null
  got=$(impl_of "$1")
  [ "${got,,}" = "${2,,}" ] || { echo "   FAILED: $1 impl is $got, expected $2"; exit 1; }
  echo "   installed $2 on $1"
}
echo "== installing (instant: queueUpgrade → upgradeTo)"
install "$MP" "${NEW[marketplace]}"
install "$AH" "${NEW[auctionHouse]}"
install "$OB" "${NEW[offerBook]}"
[ "$MODE" = dry ] && { echo "dry run complete — nothing signed"; exit 0; }

echo "== verifying through the proxies"
for p in "$MP" "$AH" "$OB"; do
  [ "$(cast call "$p" "manager()(address)" --rpc-url "$RPC")" = "$MGR" ] || { echo "manager mismatch on $p"; exit 1; }
  [ "$(cast call "$p" "feeRecipient()(address)" --rpc-url "$RPC")" = "$FEE" ] || { echo "feeRecipient mismatch on $p"; exit 1; }
done
echo "   immutables intact on all three proxies"

# Record the swap so --rollback can undo it and check-deployments knows the impls.
tmp=$(mktemp)
jq --arg mp "${NEW[marketplace]}" --arg ah "${NEW[auctionHouse]}" --arg ob "${NEW[offerBook]}" \
   --arg omp "$OLD_MP" --arg oah "$OLD_AH" --arg oob "$OLD_OB" --arg at "$(date -u +%Y-%m-%d)" \
   '.impls = {marketplace:$mp, auctionHouse:$ah, offerBook:$ob, upgradedAt:$at}
    | .superseded_impls = {marketplace:$omp, auctionHouse:$oah, offerBook:$oob}' "$DEP" > "$tmp" && mv "$tmp" "$DEP"
echo "== $DEP updated (impls + superseded_impls). Commit it, then backfill the trail from backend/ (the Go module root):"
echo "   cd backend && CHAIN_ID=... POSTGRES_URL=... RPC_URL=... MARKETPLACE_ADDR=... AUCTION_ADDR=... OFFERBOOK_ADDR=... MARKETPLACE_MANAGER_ADDR=... go run ./cmd/reindexgov -from <upgrade block>"
