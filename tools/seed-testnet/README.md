# Phase 5 — NFT Creation & Marketplace Seeding

This directory contains the metadata and deployment scripts for seeding
MagicWebb's Coston2 testnet with 4 production-quality anime NFTs.

## NFTs

| ID (example) | Name | Price |
|-------------|------|-------|
| totalSupply+1 | Itachi Uchiha — Tears of the Crow | 5 C2FLR |
| totalSupply+2 | Garou — The Hero Hunter Awakened | 5 C2FLR |
| totalSupply+3 | Cid Kagenou — I Am Atomic | 5 C2FLR |
| totalSupply+4 | Will Serfort — The Sword That Defies Magic | 5 C2FLR |

> **Token IDs are not fixed.** The seed script reads `totalSupply()` from the NFT contract before minting, so the actual IDs depend on the contract's current state. The table above shows IDs relative to the pre-mint total supply.

## Prerequisites

1. **Foundry** installed (`forge` + `cast`)
2. **PRIVATE_KEY** for a Coston2 wallet with C2FLR
3. **4K anime images** generated and pinned to IPFS/Arweave
4. **Metadata JSONs** updated with real `image` URIs

## Image Generation (4K, 3840×2160)

Generate or commission the 4 character images:
- **Style:** Anime, cel-shaded, dramatic lighting
- **Resolution:** 3840×2160 (4K)
- **Format:** PNG (lossless) or WebP (lossy, smaller)
- **Hosting:** Pin to IPFS via Pinata / web3.storage, or Arweave

Recommended prompts (Midjourney / Stable Diffusion / manual art):

- **Ninja:** "Cloaked shinobi with glowing eyes, standing under a crimson moon, crows circling overhead, dark cel-shaded anime style, 4K, dramatic lighting"
- **Hero:** "A warrior in awakened form with cosmic energy flowing through veins, shattered building background, dynamic action pose, anime style, 4K"
- **Shadow:** "Mysterious figure in dark bodysuit with purple mana swirling around, galaxy nebula background, ethereal glow, dramatic stance, anime style, 4K"
- **Mage:** "Sword-wielding warrior mid-strike with light trails, magical tower background, determined expression, motion effect, anime style, 4K"

## Usage

1. Generate/pin images to IPFS or Arweave. Use public HTTP gateway URLs
   (e.g. `https://ipfs.io/ipfs/Qm...` or `https://arweave.net/...`).
   The backend media proxy and CSP allow `ipfs.io`, `dweb.link`, and
   `gateway.pinata.cloud` — so prefer those gateways for `image` URIs.
2. Update each `metadata/*.json` `"image"` field with the real URI
3. Pin the updated metadata JSONs to IPFS/Arweave — record the URIs

**Metadata handling:** The MagicWebb indexer resolves NFT metadata
off-chain via tokenURI → `nft_metadata` table. Once metadata JSONs
are live at public URIs, the indexer will pick them up on the next
metadata fetch cycle. The seed script should set a token URI during
minting so the indexer can associate each token with its metadata.

If the NFT contract supports `setTokenURI(tokenId, uri)`, update the
seed script to call it after each `mint()`. The metadata JSONs in
`./metadata/` should be pinned to IPFS and their URIs recorded as
environment variables or passed as script arguments.

## Minting with Metadata URIs

The current NFT contract on Coston2 may or may not expose a public `setTokenURI(uint256,string)` function. The seed script handles both cases:

- **If METADATA_BASE is set:** After each mint, the script calls `setTokenURI(tokenId, METADATA_BASE/<filename>.json)`. If the contract doesn't support this selector, the call silently fails and a warning is logged — the token is still minted and listed, just without an on-chain metadata link.
- **If METADATA_BASE is empty (default):** The script skips `setTokenURI` calls entirely. Tokens are minted and listed but the indexer won't resolve metadata via `tokenURI()`.

```bash
# Set up environment
export PRIVATE_KEY=0x_your_coston2_private_key
export RPC_URL=https://coston2-api.flare.network/ext/C/rpc

# Optional: base URL for pinned metadata JSONs
# Each file in ./metadata/ will be available as:
#   $METADATA_BASE/itachi-uchiha.json
#   $METADATA_BASE/garou.json
#   $METADATA_BASE/cid-kagenou.json
#   $METADATA_BASE/will-serfort.json
# Pin all 4 files to IPFS/Arweave first, then set:
export METADATA_BASE=https://ipfs.io/ipfs/QmYourMetadataCID

# Run seed script
bash tools/seed-testnet/seed.sh
```

The script will:
1. Verify both contracts exist on-chain (code size + totalSupply)
2. Mint token IDs derived from contract state (not hardcoded)
3. Set each token's on-chain URI from `METADATA_BASE` + metadata filename
4. Approve the marketplace contract
5. List each NFT for exactly 5 C2FLR (5,000,000,000,000,000,000 wei)
6. Verify ownership and active marketplace listing for each token

## Metadata Structure

Each `metadata/*.json` follows the OpenSea/NFT standard:
- `name`: Display name (formatted with series reference)
- `description`: Rich, lore-accurate character description
- `image`: URI to the 4K artwork (IPFS/Arweave) — **must be set to a real image URI before minting**
- `attributes[]`: 8 trait slots covering anime, character, affiliation, power, technique, rarity, art style, and background
