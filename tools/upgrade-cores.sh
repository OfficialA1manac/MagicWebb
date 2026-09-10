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
# v3.7: the MarketplaceManager is a UUPS proxy too. `--manager` upgrades ITS
# implementation in place (admin-signed upgradeTo, instant, no queue) and
# leaves the cores alone. Passing MANAGER_ADDR=<new proxy> (from
# script/DeployManager.s.sol) migrates a network that still runs the v3.4
# plain manager: the new core impls bake the new manager, the OLD manager's
# admin authorizes the install, and deployments/<network>.json gets the new
# contracts.marketplaceManager + impls.marketplaceManager.
#
# Usage:
#   ADMIN_KEY=0x… DEPLOYER_KEY=0x… tools/upgrade-cores.sh <coston2|songbird|flare> [--dry-run]
#   ADMIN_KEY=0x… DEPLOYER_KEY=0x… tools/upgrade-cores.sh <network> --manager      # new manager impl only
#   ADMIN_KEY=0x… tools/upgrade-cores.sh <network> --rollback     # reinstall superseded_impls (cores)
#   tools/upgrade-cores.sh <network> --verify                     # read-only: print current state
#
# Keys are read from the environment only; nothing is written to disk. The
# admin key is the network's root credential: use it from an offline-held
# wallet, in one terminal, and clear the variable afterwards.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

NET="${1:?network required: coston2 | songbird | flare}"; shift || true
MODE="upgrade"
for a in "$@"; do case "$a" in --dry-run) MODE=dry;; --rollback) MODE=rollback;; --verify) MODE=verify;; --manager) MODE=manager;; *) echo "unknown flag $a"; exit 2;; esac; done

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

MGR_IMPL=$(impl_of "$MGR")
ZERO=0x0000000000000000000000000000000000000000
echo "== $NET via $RPC"
echo "   admin          $ADMIN"
echo "   manager        $MGR   (live: $MGR_LIVE)"
if [ "$MGR_IMPL" = "$ZERO" ]; then
  echo "   manager impl   — (v3.4 plain bytecode: NOT upgradeable in place; migrate with script/DeployManager.s.sol + MANAGER_ADDR=<new proxy>)"
else
  echo "   manager impl   $MGR_IMPL   (v3.7 UUPS proxy: upgrade with --manager)"
fi
echo "   feeRecipient   $FEE   (live: $FEE_LIVE)"
echo "   upgradeDelay   ${DELAY}s"
for pair in "Marketplace:$MP" "AuctionHouse:$AH" "OfferBook:$OB"; do
  echo "   ${pair%%:*} proxy ${pair#*:} → impl $(impl_of "${pair#*:}")"
done
[ "$MODE" = verify ] && exit 0

if [ "$MODE" != dry ]; then
  # (no apostrophe in this message: bash parses one inside "${…:?…}" as an open quote)
  : "${ADMIN_KEY:?ADMIN_KEY required (the network admin wallet)}"
  SIGNER=$(cast wallet address --private-key "$ADMIN_KEY")
  # The cores' queueUpgrade/upgradeTo consult the manager that is LIVE in the
  # installed implementation (MGR_LIVE), not the one being baked into the new
  # impls — during a MANAGER_ADDR migration those differ.
  ADMIN_LIVE=$(cast call "$MGR_LIVE" "admin()(address)" --rpc-url "$RPC")
  [ "${SIGNER,,}" = "${ADMIN_LIVE,,}" ] || { echo "ADMIN_KEY signs as $SIGNER but the live manager admin() is $ADMIN_LIVE"; exit 1; }
  [ "${ADMIN_LIVE,,}" = "${ADMIN,,}" ] || echo "   note: new manager admin $ADMIN differs from live admin $ADMIN_LIVE - after this install only $ADMIN can upgrade"
fi

# ── Storage-layout gate (v3.7 CSO finding, verified 9/10) ───────────────────
# Instant upgrades on proxies holding escrow: an implementation whose layout
# drifted from contracts/storage-layout/*.json would make every mapping read
# zero on install. Refuse before anything is deployed or signed.
if [ "$MODE" != rollback ]; then
  python tools/check-storage-layout.py || { echo "storage layout drift - refusing to build/install implementations"; exit 1; }
fi

# ── --manager: replace the MarketplaceManager implementation in place ────────
if [ "$MODE" = manager ]; then
  [ "$MGR_IMPL" != "$ZERO" ] || { echo "$MGR is the v3.4 plain manager — nothing to upgrade in place; run the DeployManager migration"; exit 1; }
  : "${DEPLOYER_KEY:?DEPLOYER_KEY required to deploy the new manager implementation}"
  (cd contracts && forge build >/dev/null)
  NEW_MGR_IMPL=$(cd contracts && forge create "src/MarketplaceManager.sol:MarketplaceManager" --rpc-url "$RPC" --private-key "$DEPLOYER_KEY" --broadcast --json | jq -r .deployedTo)
  echo "== deploying manager implementation → $NEW_MGR_IMPL"
  # Instant, admin-only, no queue (the manager holds no escrow). upgradeTo,
  # not upgradeToAndCall(impl,"") — OZ 4.9.6 force-calls empty calldata.
  cast send "$MGR" "upgradeTo(address)" "$NEW_MGR_IMPL" --rpc-url "$RPC" --private-key "$ADMIN_KEY" >/dev/null
  got=$(impl_of "$MGR")
  [ "${got,,}" = "${NEW_MGR_IMPL,,}" ] || { echo "   FAILED: manager impl is $got, expected $NEW_MGR_IMPL"; exit 1; }
  echo "   installed $NEW_MGR_IMPL on $MGR"
  # State lives on the proxy: authority must be untouched.
  [ "$(cast call "$MGR" "admin()(address)"  --rpc-url "$RPC")" = "$ADMIN" ] || { echo "admin changed across the upgrade"; exit 1; }
  echo "   admin/keeper intact on the manager proxy"
  tmp=$(mktemp)
  jq --arg new "$NEW_MGR_IMPL" --arg old "$MGR_IMPL" --arg at "$(date -u +%Y-%m-%d)" \
     '.impls.marketplaceManager = $new | .impls.upgradedAt = $at | .superseded_impls.marketplaceManager = $old' "$DEP" > "$tmp" && mv "$tmp" "$DEP"
  echo "== $DEP updated (impls.marketplaceManager). Commit it; the watcher indexes the Upgraded + AuditLog(UPGRADE) events."
  exit 0
fi

declare -A NEW
if [ "$MODE" = rollback ]; then
  for k in marketplace auctionHouse offerBook; do
    NEW[$k]=$(jq -r ".superseded_impls.$k // empty" "$DEP")
    [ -n "${NEW[$k]}" ] || { echo "no superseded_impls.$k recorded in $DEP"; exit 1; }
    # A superseded impl bakes its own manager/feeRecipient immutables. After a
    # MANAGER_ADDR migration they point at the OLD manager: installing them
    # would re-point the cores at a manager that may be abandoned (admin()==0
    # → cores frozen forever). Check BEFORE signing anything; the immutable
    # getters answer on the implementation itself.
    im=$(cast call "${NEW[$k]}" "manager()(address)" --rpc-url "$RPC")
    ifee=$(cast call "${NEW[$k]}" "feeRecipient()(address)" --rpc-url "$RPC")
    [ "${im,,}" = "${MGR_LIVE,,}" ] || { echo "refusing rollback: superseded $k impl ${NEW[$k]} bakes manager $im, live manager is $MGR_LIVE"; exit 1; }
    [ "${ifee,,}" = "${FEE_LIVE,,}" ] || { echo "refusing rollback: superseded $k impl ${NEW[$k]} bakes feeRecipient $ifee, live is $FEE_LIVE"; exit 1; }
  done
  echo "   rollback impls bake the live manager + feeRecipient — OK"
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
# A manager change (MANAGER_ADDR migration) also moves contracts.marketplaceManager
# and records the new manager's implementation; the old manager is archived.
tmp=$(mktemp)
jq --arg mp "${NEW[marketplace]}" --arg ah "${NEW[auctionHouse]}" --arg ob "${NEW[offerBook]}" \
   --arg omp "$OLD_MP" --arg oah "$OLD_AH" --arg oob "$OLD_OB" --arg at "$(date -u +%Y-%m-%d)" \
   --arg mgr "$MGR" --arg mgrlive "$MGR_LIVE" --arg mgrimpl "$MGR_IMPL" --arg zero "$ZERO" \
   '.impls = ((.impls // {}) + {marketplace:$mp, auctionHouse:$ah, offerBook:$ob, upgradedAt:$at})
    | .superseded_impls = ((.superseded_impls // {}) + {marketplace:$omp, auctionHouse:$oah, offerBook:$oob})
    | if ($mgr | ascii_downcase) != ($mgrlive | ascii_downcase) then
        .superseded = ([{note: ("manager " + $mgrlive + " replaced " + $at + " by the v3.7 UUPS manager " + $mgr + " (cores re-pointed in place)"),
                         marketplace: .contracts.marketplace, auctionHouse: .contracts.auctionHouse, offerBook: .contracts.offerBook,
                         marketplaceManager: $mgrlive, nft: .contracts.nft}] + (.superseded // []))
        | .contracts.marketplaceManager = $mgr
      else . end
    | if $mgrimpl != $zero then .impls.marketplaceManager = $mgrimpl else . end' "$DEP" > "$tmp" && mv "$tmp" "$DEP"
echo "== $DEP updated (impls + superseded_impls). Commit it, then backfill the trail from backend/ (the Go module root):"
echo "   cd backend && CHAIN_ID=... POSTGRES_URL=... RPC_URL=... MARKETPLACE_ADDR=... AUCTION_ADDR=... OFFERBOOK_ADDR=... MARKETPLACE_MANAGER_ADDR=... go run ./cmd/reindexgov -from <upgrade block>"
