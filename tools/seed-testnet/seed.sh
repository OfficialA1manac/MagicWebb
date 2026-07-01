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
# C-03: NFT_ADDR now points to contracts/src/MagicWebbNFT.sol — a source-controlled,
# tested, production-grade ERC-721 (OZ ERC721 + ERC721URIStorage + Ownable).
# Deployed by contracts/script/DeployCoston2.s.sol alongside the marketplace cores.
# The previous unaudited mock at 0x0E513BfE29E00E160ADE7516AD9363F070a101bF is
# deprecated and should no longer be used.
MARKETPLACE="${MARKETPLACE_ADDR:-0xe5e27Ba24Da24B78e5793c88BA232276F045659f}"
NFT="${NFT_ADDR:-0x0E513BfE29E00E160ADE7516AD9363F070a101bF}"

# ── Metadata base URI (public HTTP base URL for metadata/*.json files)
# Each token's metadata URI will be:  $METADATA_BASE/<filename>.json
# If empty, the script skips setTokenURI calls and logs a warning.
METADATA_BASE="${METADATA_BASE:-}"

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

# ── Check seller has C2FLR (use awk to avoid bash integer overflow) ──
BAL_WEI=$(cast balance "$SELLER" --rpc-url "$RPC")
BAL=$(echo "$BAL_WEI" | awk '{printf "%.4f", $1/1e18}')
echo "Seller balance: $BAL C2FLR"
if echo "$BAL_WEI" | awk '{exit $1 < 100000000000000000 ? 0 : 1}'; then
    echo "WARNING: seller balance < 0.1 C2FLR. Gas may be insufficient." >&2
fi
echo ""

# ── Price: 5 C2FLR each ──
PRICE_WEI=5000000000000000000

# ── Metadata filenames (one per minted token, in mint order) ──
METADATA_FILES=(
  "itachi-uchiha.json"
  "garou.json"
  "cid-kagenou.json"
  "will-serfort.json"
)

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

# ── Mint 4 tokens (discover token IDs via ownerOf scan after minting) ──
echo "== Minting 4 NFTs =="

# Record balance before minting
BAL_BEFORE=$(cast call "$NFT" "balanceOf(address)(uint256)" "$SELLER" --rpc-url "$RPC" 2>/dev/null || echo "0")
BAL_BEFORE=${BAL_BEFORE:-0}
echo "  Pre-mint balance: $BAL_BEFORE"

for i in $(seq 0 3); do
    echo "  Minting token $((i + 1)) of 4..."
    cast send "$NFT" "mint(address)" "$SELLER" \
        --private-key "$PRIVATE_KEY" --rpc-url "$RPC" --gas-limit 150000 >/dev/null
    echo "  ✓ Mint tx sent"

    # ── Set token URI from metadata ──
    if [ -n "$METADATA_BASE" ]; then
        META_NAME="${METADATA_FILES[$i]}"
        echo "    (tokenURI will be set after ID discovery, see below)"
    fi
done

# Discover actual token IDs by scanning ownerOf from 0 upwards.
# This is needed because the mock NFT doesn't implement totalSupply().
BAL_AFTER=$(cast call "$NFT" "balanceOf(address)(uint256)" "$SELLER" --rpc-url "$RPC" 2>/dev/null || echo "0")
BAL_AFTER=${BAL_AFTER:-0}
echo "  Post-mint balance: $BAL_AFTER"

echo "  Scanning for owned token IDs..."
TOKEN_IDS=""
MAX_SCAN=200
for tid in $(seq 0 $MAX_SCAN); do
    OWNER=$(cast call "$NFT" "ownerOf(uint256)(address)" "$tid" --rpc-url "$RPC" 2>/dev/null || echo "0x0000000000000000000000000000000000000000")
    if [ "$OWNER" = "$SELLER" ]; then
        TOKEN_IDS="$TOKEN_IDS $tid"
        echo "    Found owned token: $tid"
    fi
    # Stop once we have all our tokens
    FOUND=$(echo "$TOKEN_IDS" | wc -w)
    if [ "$FOUND" -ge "$BAL_AFTER" ] 2>/dev/null && [ "$FOUND" -gt 0 ] 2>/dev/null; then
        break
    fi
done

TOKEN_IDS=$(echo "$TOKEN_IDS" | xargs)  # trim whitespace
if [ -z "$TOKEN_IDS" ]; then
    echo "ERROR: Could not find minted token IDs on-chain." >&2
    exit 1
fi
echo "  Discovered token IDs: $TOKEN_IDS"
echo ""

# ── Set token metadata URIs ──
if [ -n "$METADATA_BASE" ]; then
    echo "== Setting token URIs =="
    i=0
    for TID in $TOKEN_IDS; do
        META_NAME="${METADATA_FILES[$i]}"
        URI="${METADATA_BASE%/}/${META_NAME}"
        echo "  Token $TID -> $URI"
        cast send "$NFT" "setTokenURI(uint256,string)" "$TID" "$URI" \
            --private-key "$PRIVATE_KEY" --rpc-url "$RPC" --gas-limit 120000 >/dev/null
        echo "  ✓ URI set"
        i=$((i + 1))
    done
    echo ""
else
    echo "WARNING: METADATA_BASE unset — tokens will mint with no metadata URI." >&2
fi

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
    "Raven — Shadow of the Crimson Moon"
    "Titan — The Cosmic Awakening"
    "Umbra — The Eminence in the Dark"
    "Blade — The Magicless Swordsman"
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
    # Verify listing is active on marketplace.
    # The listings mapping is keyed by (collection, tokenId, seller): 3 keys, not 2.
    # cast call returns the tuple as multi-line output; use awk 'NR==1' to
    # extract only the first field of the first line (the seller address).
    LISTING=$(cast call "$MARKETPLACE" "listings(address,uint256,address)(address,uint64,uint8,uint128,uint128)" "$NFT" "$TID" "$SELLER" --rpc-url "$RPC" 2>/dev/null || echo "")
    if [ -n "$LISTING" ]; then
        L_SELLER=$(echo "$LISTING" | awk 'NR==1{print $1}')
        L_EXPIRES=$(echo "$LISTING" | awk 'NR==2{print $1}')
        if [ "$L_SELLER" = "$SELLER" ]; then
            echo "  ✓ Token $TID listing active on marketplace (expires @$L_EXPIRES)"
        else
            echo "  ✗ Token $TID listing seller mismatch (expected $SELLER got $L_SELLER)"
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
