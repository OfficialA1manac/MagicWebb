# Phase 5 — NFT Creation & Marketplace Seeding

This directory contains the metadata and deployment scripts for seeding
MagicWebb's Coston2 testnet with 4 original-character NFTs.

## NFTs

| ID (example) | Name | Price |
|-------------|------|-------|
| totalSupply+1 | Raven — Shadow of the Crimson Moon | 5 C2FLR |
| totalSupply+2 | Titan — The Cosmic Awakening | 5 C2FLR |
| totalSupply+3 | Umbra — The Eminence in the Dark | 5 C2FLR |
| totalSupply+4 | Blade — The Magicless Swordsman | 5 C2FLR |

> **Token IDs are not fixed.** The seed script reads `totalSupply()` from the NFT contract before minting, so the actual IDs depend on the contract's current state. The table above shows IDs relative to the pre-mint total supply.

## Prerequisites

1. **Foundry** installed (`forge` + `cast`)
2. **PRIVATE_KEY** for a Coston2 wallet with C2FLR
3. **4K character images** generated and pinned to IPFS/Arweave
4. **Metadata JSONs** updated with real `image` URIs

## Image Generation (4K, 3840×2160)

Generate or commission the 4 character images:
- **Style:** Anime, cel-shaded, dramatic lighting
- **Resolution:** 3840×2160 (4K)
- **Format:** PNG (lossless) or WebP (lossy, smaller)
- **Hosting:** Upload to any public HTTP server (or self-host via the app's imagestore)

Recommended prompts (Midjourney / Stable Diffusion / manual art):

- **Raven:** "Cloaked shinobi with glowing eyes, standing under a crimson moon, crows circling overhead, dark cel-shaded anime style, 4K, dramatic lighting"
- **Titan:** "A warrior in awakened form with cosmic energy flowing through veins, shattered ruined colosseum background, dynamic action pose, anime style, 4K"
- **Umbra:** "Mysterious figure in dark bodysuit with purple mana swirling around, galaxy nebula background, ethereal glow, dramatic stance, anime style, 4K"
- **Blade:** "Sword-wielding warrior mid-strike with light trails, magical Endless Spire background, determined expression, motion effect, anime style, 4K"

## Usage

1. Upload images to any public HTTP server (or host them anywhere accessible via http/https).
2. Update each `metadata/*.json` `"image"` field with the real URL.
3. The backend media proxy will fetch and self-host images on first access — no Pinata, no IPFS needed.

**Metadata handling:** The MagicWebb indexer resolves NFT metadata
off-chain via tokenURI → `nft_metadata` table. The indexer fetches the
metadata JSON, extracts the `image` field, downloads the image bytes,
stores them in the local imagestore (Postgres BYTEA), and rewrites
`image_uri` to `/api/v1/img/<sha256>`. From that point on, the image
is served from the local database — no upstream dependency at render time.

## Minting with Metadata URIs

The current NFT contract on Coston2 may or may not expose a public
`setTokenURI(uint256,string)` function. The seed script handles both cases:

- **If METADATA_BASE is set:** After each mint, the script calls
  `setTokenURI(tokenId, METADATA_BASE/<filename>.json)`. If the contract
  doesn't support this selector, the call silently fails and a warning
  is logged — the token is still minted and listed, just without an
  on-chain metadata link.
- **If METADATA_BASE is empty (default):** The script skips `setTokenURI`
  calls entirely. Tokens are minted and listed but the indexer won't
  resolve metadata via `tokenURI()`.

```bash
# Set up environment
export PRIVATE_KEY=0x_your_coston2_private_key
export RPC_URL=https://coston2-api.flare.network/ext/C/rpc

# Optional: base URL for metadata JSONs hosted on any public HTTP endpoint
# Each file in ./metadata/ will be available as:
#   $METADATA_BASE/itachi-uchiha.json
#   $METADATA_BASE/garou.json
#   $METADATA_BASE/cid-kagenou.json
#   $METADATA_BASE/will-serfort.json
# Host all 4 files at a public HTTP URL first, then set:
export METADATA_BASE=https://example.com/metadata

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
- `image`: URI to the 4K artwork (public HTTP URL) — **must be set to a real image URI before minting**
- `attributes[]`: 8 trait slots covering anime, character, affiliation, power, technique, rarity, art style, and background
