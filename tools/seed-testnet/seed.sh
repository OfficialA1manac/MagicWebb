#!/usr/bin/env bash
# MagicWebb Phase 5 — Seed Coston2 testnet with 4 anime NFT listings
#
# Prerequisites:
#   1. forge installed (Foundry)
#   2. PRIVATE_KEY set to a Coston2 wallet with C2FLR
#   3. Metadata JSONs in ./metadata/ edited with real image URIs
#   4. Contracts deployed (MARKETPLACE_ADDR, etc.)
#
# Usage:
#   export PRIVATE_KEY=0x...
#   bash tools/seed-testnet/seed.sh

set -euo pipefail

RPC="${RPC_URL:-https://coston2-api.flare.network/ext/C/rpc}"
CHAIN_ID=114

# ── Contract addresses (Coston2 v2 deploy) ──
MARKETPLACE="${MARKETPLACE_ADDR:-0xf9355C77F4Dba5CecA217ceB4D762A33aB7EFE37}"
NFT="${NFT_ADDR:-0x0E513BfE29E00E160ADE7516AD9363F070a101bF}"

# ── Deployer / seller ──
: "${PRIVATE_KEY:?PRIVATE_KEY env required (Coston2 wallet with gas)}"
SELLER=$(cast wallet address --private-key "$PRIVATE_KEY")
echo "Seller: $SELLER"
echo "Marketplace: $MARKETPLACE"
echo "NFT contract: $NFT"
echo ""

# ── Check chain ──
CHAIN=$(cast chain-id --rpc-url "$RPC")
if [ "$CHAIN" != "$CHAIN_ID" ]; then
    echo "ERROR: wrong chain — expected $CHAIN_ID, got $CHAIN" >&2
    exit 1
fi

# ── Check seller has C2FLR ──
BAL=$(cast balance "$SELLER" --rpc-url "$RPC" | awk '{print int($1/1e14)/10000}')
echo "Seller balance: $BAL C2FLR"
if [ "$(cast balance "$SELLER" --rpc-url "$RPC" | awk '{print $1}')" -lt 100000000000000000 ]; then
    echo "WARNING: seller balance < 0.1 C2FLR. Gas may be insufficient." >&2
fi
echo ""

# ── Price: 5 C2FLR each ──
PRICE_WEI=5000000000000000000

# ── Verify contracts exist ──
echo "== Contract verification =="
MP_CODE=$(cast codesize "$MARKETPLACE" --rpc-url "$RPC")
NFT_CODE=$(cast codesize "$NFT" --rpc-url "$RPC")
if [ "$MP_CODE" -eq 0 ]; then echo "ERROR: no contract at MARKETPLACE_ADDR"; exit 1; fi
if [ "$NFT_CODE" -eq 0 ]; then echo "ERROR: no contract at NFT_ADDR"; exit 1; fi
NFT_TOTAL=$(cast call "$NFT" "totalSupply()(uint256)" --rpc-url "$RPC" 2>/dev/null || echo "?")
echo "  Marketplace: $MP_CODE bytes code at $MARKETPLACE"
echo "  NFT: $NFT_CODE bytes code at $NFT (totalSupply=$NFT_TOTAL)"
echo ""

# ── Mint 4 tokens (derive IDs from contract state, not hardcoded) ──
echo "== Minting 4 NFTs =="
TOKEN_IDS=""

# Read current totalSupply before minting to derive real token IDs.
PRE_TOTAL=$(cast call "$NFT" "totalSupply()(uint256)" --rpc-url "$RPC" 2>/dev/null || echo "0")
PRE_TOTAL=${PRE_TOTAL:-0}

for i in $(seq 0 3); do
    echo "  Minting token $((PRE_TOTAL + i + 1))..."
    cast send "$NFT" "mint(address)" "$SELLER" \
        --private-key "$PRIVATE_KEY" --rpc-url "$RPC" --gas-limit 150000 >/dev/null
    # Read the actual minted token ID from on-chain totalSupply after mint.
    POST_TOTAL=$(cast call "$NFT" "totalSupply()(uint256)" --rpc-url "$RPC" 2>/dev/null || echo "0")
    TID=$((POST_TOTAL))
    TOKEN_IDS="$TOKEN_IDS $TID"
    echo "  ✓ Token $TID minted to $SELLER"
    PRE_TOTAL=$POST_TOTAL
done
echo ""

# ── Approve marketplace ──
echo "== Approving marketplace =="
cast send "$NFT" "setApprovalForAll(address,bool)" "$MARKETPLACE" true \
    --private-key "$PRIVATE_KEY" --rpc-url "$RPC" --gas-limit 100000 >/dev/null
echo "  ✓ Marketplace approved"
echo ""

# ── List for 5 C2FLR each ──
NOW=$(cast block latest --field timestamp --rpc-url "$RPC")
EXPIRES=$((NOW + 7776000))  # 90 days

echo "== Listing NFTs @ 5 C2FLR =="
NFT_NAMES=(
    "Itachi Uchiha — Tears of the Crow"
    "Garou — The Hero Hunter Awakened"
    "Cid Kagenou — I Am Atomic"
    "Will Serfort — The Sword That Defies Magic"
)

i=0
for TID in $TOKEN_IDS; do
    NAME="${NFT_NAMES[$i]}"
    echo "  Listing token $TID: $NAME"
    cast send "$MARKETPLACE" "list(address,uint256,uint128,uint64)" \
        "$NFT" "$TID" "$PRICE_WEI" "$EXPIRES" \
        --private-key "$PRIVATE_KEY" --rpc-url "$RPC" --gas-limit 200000 >/dev/null
    echo "  ✓ Listed for 5 C2FLR (expires $(date -d @$EXPIRES '+%Y-%m-%d' 2>/dev/null || echo "@$EXPIRES"))"
    echo ""
    i=$((i + 1))
done

# ── Verify ──
echo "== Verification =="
ALL_OK=true
for TID in $TOKEN_IDS; do
    OWNER=$(cast call "$NFT" "ownerOf(uint256)(address)" "$TID" --rpc-url "$RPC" 2>/dev/null)
    if [ "$OWNER" = "$SELLER" ]; then
        echo "  ✓ Token $TID owned by seller"
    else
        echo "  ✗ Token $TID NOT owned by seller (got $OWNER)"
        ALL_OK=false
    fi
    # Verify listing is active on marketplace
    LISTING=$(cast call "$MARKETPLACE" "listings(address,uint256)(address,uint128,uint64)" "$NFT" "$TID" --rpc-url "$RPC" 2>/dev/null || echo "")
    if [ -n "$LISTING" ]; then
        L_OWNER=$(echo "$LISTING" | awk '{print $1}')
        if [ "$L_OWNER" = "$SELLER" ]; then
            echo "  ✓ Token $TID listing active on marketplace"
        else
            echo "  ✗ Token $TID listing owner mismatch (expected $SELLER got $L_OWNER)"
            ALL_OK=false
        fi
    else
        echo "  ✗ Token $TID listing not found — marketplace verification FAILED"
        ALL_OK=false
    fi
done
echo ""
if [ "$ALL_OK" = "true" ]; then
    echo "All 4 NFTs listed at 5 C2FLR on Coston2."
    echo ""
    echo "=== Phase 5 seeding complete ==="
else
    echo "Some verifications failed — review above before proceeding."
    exit 1
fi
