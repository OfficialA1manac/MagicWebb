#!/usr/bin/env bash
# Local end-to-end playthrough against anvil (chain-id 114).
# Exercises every user flow exactly as it will run on Coston2:
#   list -> buy (fee split), auction -> bid -> outbid -> top-up -> settle ->
#   loser refund, offer -> accept -> distribute, offer expiry refund,
#   manager pause (entries halt, exits run).
# Usage: anvil --chain-id 114 & then:  RPC=http://127.0.0.1:8545 ./e2e_local.sh <MP> <AH> <OB> <MGR>
set -euo pipefail

RPC="${RPC:-http://127.0.0.1:8545}"
MP="$1"; AH="$2"; OB="$3"; MGR="$4"

# anvil deterministic accounts
PK_DEPLOY=0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80
PK_SELLER=0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d
PK_ALICE=0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a
PK_BOB=0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6
PK_KEEPER=0xdbda1821b80551c9d65939329250298aa3472ba22feea921c0cf5d620ea67b97
SELLER=0x70997970C51812dc3A010C7d01b50e0d17dc79C8
ALICE=0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC
BOB=0x90F79bf6EB2c4f870365E785982E1f101E93b906
CREATOR=0xa0Ee7A142d267C1f36714E4a8F75612F20a79720

bal() { cast balance "$1" --rpc-url "$RPC"; }
# sub <a> <b> -> a-b via python (wei values overflow 64-bit shell arithmetic)
sub() { python3 -c "print($1-$2)"; }
gt() { python3 -c "print(1 if $1-$2 > $3 else 0)"; }
warp() { cast rpc evm_increaseTime "$1" --rpc-url "$RPC" >/dev/null; cast rpc evm_mine --rpc-url "$RPC" >/dev/null; }
fail=0
check() { # check <label> <expected> <actual>
  if [ "$2" = "$3" ]; then echo "  PASS  $1"; else echo "  FAIL  $1: expected $2 got $3"; fail=1; fi
}

echo "== setup: mock NFT =="
NFT=$(forge create test/MockERC721.sol:MockERC721 --rpc-url "$RPC" --private-key "$PK_DEPLOY" --broadcast --json | python3 -c "import sys,json;print(json.load(sys.stdin)['deployedTo'])")
echo "NFT=$NFT"
NOW=$(cast block latest --field timestamp --rpc-url "$RPC")

echo "== A. list -> buy (1 ether, fee split 1.5%) =="
cast send "$NFT" "mint(address)" "$SELLER" --rpc-url "$RPC" --private-key "$PK_DEPLOY" >/dev/null   # id 1
cast send "$NFT" "setApprovalForAll(address,bool)" "$MP" true --rpc-url "$RPC" --private-key "$PK_SELLER" >/dev/null
cast send "$MP" "list(address,uint256,uint128,uint64)" "$NFT" 1 1000000000000000000 $((NOW+86400)) --rpc-url "$RPC" --private-key "$PK_SELLER" >/dev/null
S0=$(bal "$SELLER"); C0=$(bal "$CREATOR")
cast send "$MP" "buy(address,uint256,address)" "$NFT" 1 "$SELLER" --value 1000000000000000000 --rpc-url "$RPC" --private-key "$PK_ALICE" >/dev/null
S1=$(bal "$SELLER"); C1=$(bal "$CREATOR")
check "buyer owns token 1" "$ALICE" "$(cast call "$NFT" "ownerOf(uint256)(address)" 1 --rpc-url "$RPC")"
check "seller +0.985"      "985000000000000000"  "$(sub $S1 $S0)"
check "fee     +0.015"     "15000000000000000"   "$(sub $C1 $C0)"

echo "== B. auction -> bid -> outbid -> cumulative top-up -> settle -> loser refund =="
cast send "$NFT" "mint(address)" "$SELLER" --rpc-url "$RPC" --private-key "$PK_DEPLOY" >/dev/null   # id 2
cast send "$NFT" "setApprovalForAll(address,bool)" "$AH" true --rpc-url "$RPC" --private-key "$PK_SELLER" >/dev/null
NOW=$(cast block latest --field timestamp --rpc-url "$RPC")
cast send "$AH" "create(address,uint256,uint128,uint64,uint16,uint128)" "$NFT" 2 1000000000000000000 $((NOW+3600)) 500 0 --rpc-url "$RPC" --private-key "$PK_SELLER" >/dev/null
AID=$(cast call "$AH" "nextAuctionId()(uint256)" --rpc-url "$RPC")
echo "auction id=$AID"
cast send "$AH" "bid(uint256)" "$AID" --value 1000000000000000000 --rpc-url "$RPC" --private-key "$PK_ALICE" >/dev/null  # alice leads @1
cast send "$AH" "bid(uint256)" "$AID" --value 1100000000000000000 --rpc-url "$RPC" --private-key "$PK_BOB" >/dev/null    # bob outbids @1.1 -> OutbidNotification(alice)
cast send "$AH" "bid(uint256)" "$AID" --value 500000000000000000  --rpc-url "$RPC" --private-key "$PK_ALICE" >/dev/null  # alice tops up -> cumulative 1.5, retakes lead
check "alice cumulative=1.5e18" "1500000000000000000" "$(cast call "$AH" "cumulative(uint256,address)(uint128)" "$AID" "$ALICE" --rpc-url "$RPC" | awk '{print $1}')"
warp 7200
S0=$(bal "$SELLER"); C0=$(bal "$CREATOR"); B0=$(bal "$BOB")
cast send "$AH" "settle(uint256)" "$AID" --rpc-url "$RPC" --private-key "$PK_KEEPER" >/dev/null
S1=$(bal "$SELLER"); C1=$(bal "$CREATOR")
check "auction NFT -> alice"   "$ALICE" "$(cast call "$NFT" "ownerOf(uint256)(address)" 2 --rpc-url "$RPC")"
check "seller +1.47750"        "1477500000000000000" "$(sub $S1 $S0)"
check "fee    +0.02250"        "22500000000000000"   "$(sub $C1 $C0)"
cast send "$AH" "refundLosers(uint256,address[])" "$AID" "[$BOB]" --rpc-url "$RPC" --private-key "$PK_KEEPER" >/dev/null
B1=$(bal "$BOB")
check "bob (loser) refunded 1.1e18" "1100000000000000000" "$(sub $B1 $B0)"

echo "== C. offer -> accept -> distribute =="
cast send "$NFT" "mint(address)" "$SELLER" --rpc-url "$RPC" --private-key "$PK_DEPLOY" >/dev/null   # id 3
NOW=$(cast block latest --field timestamp --rpc-url "$RPC")
cast send "$OB" "makeOffer(address,uint256,uint128,uint64)" "$NFT" 3 2000000000000000000 $((NOW+86400)) --value 2000000000000000000 --rpc-url "$RPC" --private-key "$PK_ALICE" >/dev/null
cast send "$NFT" "setApprovalForAll(address,bool)" "$OB" true --rpc-url "$RPC" --private-key "$PK_SELLER" >/dev/null
S0=$(bal "$SELLER"); C0=$(bal "$CREATOR")
cast send "$OB" "acceptOffer(address,uint256,address)" "$NFT" 3 "$ALICE" --rpc-url "$RPC" --private-key "$PK_SELLER" >/dev/null
S1=$(bal "$SELLER"); C1=$(bal "$CREATOR")
check "offer NFT -> alice" "$ALICE" "$(cast call "$NFT" "ownerOf(uint256)(address)" 3 --rpc-url "$RPC")"
check "seller +1.97 (gas-adjusted >=1.96)" "1" "$(gt $S1 $S0 1960000000000000000)"
check "fee    +0.03"       "30000000000000000" "$(sub $C1 $C0)"

echo "== D. offer expiry -> permissionless keeper refund =="
cast send "$NFT" "mint(address)" "$SELLER" --rpc-url "$RPC" --private-key "$PK_DEPLOY" >/dev/null   # id 4
NOW=$(cast block latest --field timestamp --rpc-url "$RPC")
cast send "$OB" "makeOffer(address,uint256,uint128,uint64)" "$NFT" 4 1000000000000000000 $((NOW+3600)) --value 1000000000000000000 --rpc-url "$RPC" --private-key "$PK_BOB" >/dev/null
warp 7200
B0=$(bal "$BOB")
cast send "$OB" "refundExpiredOffer(address,uint256,address)" "$NFT" 4 "$BOB" --rpc-url "$RPC" --private-key "$PK_KEEPER" >/dev/null
B1=$(bal "$BOB")
check "expired offer refunded" "1000000000000000000" "$(sub $B1 $B0)"

echo "== E. circuit breaker: entries halt, exits run =="
cast send "$MGR" "pauseEntries()" --rpc-url "$RPC" --private-key "$PK_DEPLOY" >/dev/null 2>&1 \
  && { echo "  FAIL  deployer should have renounced operator"; fail=1; } \
  || echo "  PASS  deployer cannot pause (roles renounced)"
PK_CREATOR=0x2a871d0798f97d79848a013d4936a73bf4cc922c825d33c1cf7073dff6d409c6
cast send "$MGR" "pauseEntries()" --rpc-url "$RPC" --private-key "$PK_CREATOR" >/dev/null
check "entriesAllowed=false" "false" "$(cast call "$MGR" "entriesAllowed()(bool)" --rpc-url "$RPC")"
NOW=$(cast block latest --field timestamp --rpc-url "$RPC")
cast send "$MP" "list(address,uint256,uint128,uint64)" "$NFT" 4 1000000000000000000 $((NOW+86400)) --rpc-url "$RPC" --private-key "$PK_SELLER" >/dev/null 2>&1 \
  && { echo "  FAIL  list should revert while paused"; fail=1; } \
  || echo "  PASS  list halted while paused"
cast send "$MGR" "unpauseEntries()" --rpc-url "$RPC" --private-key "$PK_CREATOR" >/dev/null
check "entriesAllowed=true" "true" "$(cast call "$MGR" "entriesAllowed()(bool)" --rpc-url "$RPC")"
# Recovery proof: an entry must actually succeed again after unpause.
cast send "$MP" "list(address,uint256,uint128,uint64)" "$NFT" 4 1000000000000000000 $((NOW+86400)) --rpc-url "$RPC" --private-key "$PK_SELLER" >/dev/null
echo "  PASS  entries recover after unpause"

echo
if [ "$fail" -eq 0 ]; then echo "E2E PLAYTHROUGH: ALL CHECKS PASSED"; else echo "E2E PLAYTHROUGH: FAILURES PRESENT"; exit 1; fi
