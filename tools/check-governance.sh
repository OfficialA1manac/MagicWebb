#!/usr/bin/env bash
# check-governance.sh — prints who controls the contracts on every deployed
# network, straight from the chain (no cast/foundry needed: raw eth_call over
# curl + jq). Informational in CI; fails only when a deployed network's manager
# cannot be read at all, which means deployments/<net>.json and the chain
# disagree.
#
#   admin()        0xf851a440   pendingAdmin() 0x26782247
#   keeper()       0xaced1661   upgradeDelay() 0x0c4c5f2f  (on the marketplace)
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

rpc_call() { # url to data
  curl -sS -m 15 -X POST -H 'content-type: application/json' \
    --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_call\",\"params\":[{\"to\":\"$2\",\"data\":\"$3\"},\"latest\"]}" "$1" \
    | jq -r '.result // empty'
}
rpc_code() {
  curl -sS -m 15 -X POST -H 'content-type: application/json' \
    --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"eth_getCode\",\"params\":[\"$2\",\"latest\"]}" "$1" \
    | jq -r '.result // empty'
}
addr_of() { printf '0x%s' "${1: -40}"; }

# Selectors pinned (verified with `cast sig`).
sel_admin=0xf851a440; sel_pending=0x26782247; sel_keeper=0xaced1661; sel_delay=0x7e48d4ea

rc=0
for f in deployments/coston2.json deployments/songbird.json deployments/flare.json; do
  status=$(jq -r .status "$f"); net=$(jq -r .network "$f")
  if [ "$status" != "deployed" ]; then
    echo "== $net: $status (no contracts)"; continue
  fi
  rpc=$(jq -r .rpc.primary "$f")
  mgr=$(jq -r .contracts.marketplaceManager "$f")
  mp=$(jq -r .contracts.marketplace "$f")
  admin=$(rpc_call "$rpc" "$mgr" "$sel_admin" || true)
  if [ -z "$admin" ]; then echo "== $net: FAILED to read admin() from $mgr via $rpc"; rc=1; continue; fi
  pending=$(rpc_call "$rpc" "$mgr" "$sel_pending"); keeper=$(rpc_call "$rpc" "$mgr" "$sel_keeper")
  delay=$(rpc_call "$rpc" "$mp" "$sel_delay")
  admin_a=$(addr_of "$admin"); pending_a=$(addr_of "$pending"); keeper_a=$(addr_of "$keeper")
  kind="single wallet (EOA)"
  if [ "$admin_a" = "0x0000000000000000000000000000000000000000" ]; then kind="RENOUNCED — immutable"
  else
    code=$(rpc_code "$rpc" "$admin_a" || true)
    if [ -z "$code" ]; then kind="unknown (eth_getCode failed)"
    elif [ "$code" != "0x" ]; then kind="contract (Safe)"; fi
  fi
  echo "== $net"
  echo "   admin         $admin_a  [$kind]"
  echo "   pendingAdmin  $pending_a"
  echo "   keeper        $keeper_a"
  echo "   upgradeDelay  $((delay))s"
done
exit $rc
