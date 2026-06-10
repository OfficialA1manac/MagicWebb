#!/usr/bin/env bash
# LIVE Coston2 end-to-end playthrough against the v2 deployment, with the
# backend's keeper handling settlement/refunds AUTONOMOUSLY (this script never
# calls settle/refundLosers — it only watches them happen).
# Run AFTER the backend is up with KEEPER_KEY set.
set -euo pipefail

RPC="${RPC:-https://coston2-api.flare.network/ext/C/rpc}"
MP=0x78ECa1F6c3B03653c409e7bF8F4c4aF7f7ddED96
AH=0x48A1410B2F1B51fFae643fAfF64fcC399f366036
OB=0xeC4C29b2809C8485baF214ed9Be16716756660B7
NFT=0xD34FAED2F5F9Bd4b5C0687A0dEb5b0e00cCbd7cc

PK_SELLER=$DEPLOYER_KEY                      # deployer doubles as seller (testnet)
PK_C=0x1155d467e2746415ca792f62d2170ac3d9bd746cc5370d0d242f1af6b4608bff
PK_D=0x30c0778ab9ee9a827d71e2aef8a866d896cb3625c35aa735d341d06b0dcc7733
SELLER=0x2e1F4ac2980c556ad8b677961e1d485b82457440
C=0x675c0da0957BEfeb9f874C3347F5305207Fe88EC
D=0x87C694B3AbC8Df9599C0ACC87AE9Af0C2cD90b63

send() { local pk=$1; shift; cast send "$@" --private-key "$pk" --rpc-url "$RPC" >/dev/null; }
bal()  { cast balance "$1" --rpc-url "$RPC"; }
sub()  { python3 -c "print($1-$2)"; }
fail=0
check() { if [ "$2" = "$3" ]; then echo "  PASS  $1"; else echo "  FAIL  $1: expected $2 got $3"; fail=1; fi; }

echo "== fund bidders =="
send "$PK_SELLER" "$C" --value 3ether
send "$PK_SELLER" "$D" --value 3ether
echo "funded C=$(bal $C) D=$(bal $D)"

echo "== mint + approvals =="
for i in 1 2 3 4; do send "$PK_SELLER" "$NFT" "mint(address)" "$SELLER"; done
send "$PK_SELLER" "$NFT" "setApprovalForAll(address,bool)" "$MP" true
send "$PK_SELLER" "$NFT" "setApprovalForAll(address,bool)" "$AH" true
send "$PK_SELLER" "$NFT" "setApprovalForAll(address,bool)" "$OB" true

NOW=$(cast block latest --field timestamp --rpc-url "$RPC")

echo "== A. list token1 @0.05 -> C buys =="
send "$PK_SELLER" "$MP" "list(address,uint256,uint128,uint64)" "$NFT" 1 50000000000000000 $((NOW+86400))
send "$PK_C" "$MP" "buy(address,uint256,address)" "$NFT" 1 "$SELLER" --value 50000000000000000
check "token1 -> C" "$C" "$(cast call "$NFT" "ownerOf(uint256)(address)" 1 --rpc-url "$RPC")"

echo "== B. auction token2 (reserve 0.05, ends +480s): C 0.05 -> D 0.06 -> C +0.02 =="
NOW=$(cast block latest --field timestamp --rpc-url "$RPC")
send "$PK_SELLER" "$AH" "create(address,uint256,uint128,uint64,uint16,uint128)" "$NFT" 2 50000000000000000 $((NOW+480)) 500 0
AID=$(cast call "$AH" "nextAuctionId()(uint256)" --rpc-url "$RPC")
echo "auction id=$AID ends $(date -d @$((NOW+480)) 2>/dev/null || echo +480s)"
send "$PK_C" "$AH" "bid(uint256)" "$AID" --value 50000000000000000
send "$PK_D" "$AH" "bid(uint256)" "$AID" --value 60000000000000000
send "$PK_C" "$AH" "bid(uint256)" "$AID" --value 20000000000000000
check "C cumulative 0.07" "70000000000000000" "$(cast call "$AH" "cumulative(uint256,address)(uint128)" "$AID" "$C" --rpc-url "$RPC" | awk '{print $1}')"

echo "== C. offer 0.05 on token3 from C -> seller accepts =="
NOW=$(cast block latest --field timestamp --rpc-url "$RPC")
send "$PK_C" "$OB" "makeOffer(address,uint256,uint128,uint64)" "$NFT" 3 50000000000000000 $((NOW+86400)) --value 50000000000000000
send "$PK_SELLER" "$OB" "acceptOffer(address,uint256,address)" "$NFT" 3 "$C"
check "token3 -> C" "$C" "$(cast call "$NFT" "ownerOf(uint256)(address)" 3 --rpc-url "$RPC")"

echo "== D. offer 0.05 on token4 from D, expires +90s (offer keeper must refund) =="
NOW=$(cast block latest --field timestamp --rpc-url "$RPC")
send "$PK_D" "$OB" "makeOffer(address,uint256,uint128,uint64)" "$NFT" 4 50000000000000000 $((NOW+90)) --value 50000000000000000
D_AFTER_OFFER=$(bal "$D")

echo "== E. WAIT: backend keeper must settle auction $AID + refund loser D + refund expired offer =="
echo "auction ends in ~8min; polling chain for settled flag + D refunds (no manual settle calls)..."
DEADLINE=$(( $(date +%s) + 900 ))
SETTLED=""
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  SETTLED=$(cast call "$AH" "auctions(uint256)(address,uint64,uint16,bool,uint8,address,uint64,uint256,uint128,uint128,address,uint128,uint128)" "$AID" --rpc-url "$RPC" | sed -n 4p)
  [ "$SETTLED" = "true" ] && break
  sleep 20
done
check "keeper auto-settled auction" "true" "$SETTLED"
check "token2 -> C (auction winner)" "$C" "$(cast call "$NFT" "ownerOf(uint256)(address)" 2 --rpc-url "$RPC")"

# loser refund: D's escrow (0.06) must come back without anyone calling refundLosers
DEADLINE=$(( $(date +%s) + 300 ))
DREF=0
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  CUM=$(cast call "$AH" "cumulative(uint256,address)(uint128)" "$AID" "$D" --rpc-url "$RPC" | awk '{print $1}')
  if [ "$CUM" = "0" ]; then DREF=1; break; fi
  sleep 15
done
check "keeper auto-refunded loser D (escrow zeroed)" "1" "$DREF"

# expired offer refund: D's position on token4 must be deleted by the offer keeper
DEADLINE=$(( $(date +%s) + 300 ))
OREF=0
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  P=$(cast call "$OB" "positions(address,uint256,address)(uint128,uint128,uint64,uint8)" "$NFT" 4 "$D" --rpc-url "$RPC" | sed -n 1p | awk '{print $1}')
  if [ "$P" = "0" ]; then OREF=1; break; fi
  sleep 15
done
check "offer keeper auto-refunded expired offer" "1" "$OREF"

echo
if [ "$fail" -eq 0 ]; then echo "COSTON2 LIVE E2E: ALL CHECKS PASSED"; else echo "COSTON2 LIVE E2E: FAILURES"; exit 1; fi
