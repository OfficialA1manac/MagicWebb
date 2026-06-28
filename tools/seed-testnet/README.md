# Phase 5 — NFT Creation & Marketplace Seeding

This directory contains the metadata and deployment scripts for seeding
MagicWebb's Coston2 testnet with 4 production-quality anime NFTs.

## NFTs

| ID | Name | Anime | Price |
|----|------|-------|-------|
| 5  | Itachi Uchiha — Tears of the Crow | Naruto Shippuden | 5 C2FLR |
| 6  | Garou — The Hero Hunter Awakened | One Punch Man | 5 C2FLR |
| 7  | Cid Kagenou — I Am Atomic | The Eminence in Shadow | 5 C2FLR |
| 8  | Will Serfort — The Sword That Defies Magic | Wistoria: Wand and Sword | 5 C2FLR |

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

- **Itachi Uchiha:** "Itachi Uchiha from Naruto, Akatsuki cloak, Mangekyo Sharingan active, crow on finger, crimson moon background, amaterasu flames at edges, dark cel-shaded anime style, 4K, dramatic lighting"
- **Garou:** "Garou awakened form from One Punch Man, cosmic energy veins, silver hair, cracked monster skin, shattered Hero Association HQ background, dynamic action pose, anime style, 4K"
- **Cid Kagenou:** "Cid Kagenou / Shadow from Eminence in Shadow, slime bodysuit, violet mana swirling, atomic pose with raised hand, galaxy nebula background, ethereal glow, anime style, 4K"
- **Will Serfort:** "Will Serfort from Wistoria Wand and Sword, mid-sword-strike pose leaving light trails, magical tower background stretching infinitely upward, determined expression, sword trail motion effect, anime style, 4K"

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
metadata fetch cycle. The seed script uses `mint(address)` which
auto-increments the token ID without setting an on-chain tokenURI —
the metadata association happens through the indexer's metadata
resolution, not on-chain.

```bash
# Set up environment
export PRIVATE_KEY=0x_your_coston2_private_key
export RPC_URL=https://coston2-api.flare.network/ext/C/rpc

# Run seed script
bash tools/seed-testnet/seed.sh
```

The script will:
1. Verify both contracts exist on-chain (code size + totalSupply)
2. Mint token IDs 5-8 on the Coston2 MockERC721
3. Approve the marketplace contract
4. List each NFT for exactly 5 C2FLR (5,000,000,000,000,000,000 wei)
5. Verify ownership and active marketplace listing for each token

## Metadata Structure

Each `metadata/*.json` follows the OpenSea/NFT standard:
- `name`: Display name (formatted with series reference)
- `description`: Rich, lore-accurate character description
- `image`: URI to the 4K artwork (IPFS/Arweave)
- `attributes[]`: 8 trait slots covering anime, character, affiliation, power, technique, rarity, art style, and background
