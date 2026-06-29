# svggen — Standalone Animated NFT SVG Art Generator

Generate high-quality, **animated SVG artwork** for NFT marketplace characters. Fully standalone — zero external dependencies, zero API keys, zero runtime costs.

```
          ╭──────────────────────────╮
          │  svggen                  │
          │  ─────────────────────── │
          │  • Animated SVG output   │
          │  • No dependencies       │
          │  • Fully portable        │
          │  • Go library + CLI      │
          ╰──────────────────────────╯
```

---

## Features

- 🎨 **4 original characters** with rich animated art (CSS keyframes + SVG animations)
- ✨ **Living artwork** — pulsing glows, floating particles, orbiting energy rings, flying crows, expanding shockwaves, and more
- 🚀 **Zero dependencies** — pure Go stdlib + hand-crafted SVGs
- 📦 **Two modes**: standalone CLI tool **or** embeddable Go library
- 🖼️ **Infinitely scalable** — vector SVGs render at any resolution (1080p, 4K, 8K)
- 🔄 **Portable** — copy the `svggen/` directory to any Go project and use immediately
- ⚡ **Tiny file size** — each SVG is 10-20 KB (vs 5-15 MB for 4K PNG)

---

## Characters

| # | Slug | Name | Theme | Animations |
|:--|:-----|:-----|:------|:-----------|
| 1 | `raven` | **Raven — Shadow of the Crimson Moon** | Dark ninja, blood moon, crows | Pulsing moon, 3 flying crows, floating shadow particles, blood mist, falling feathers, vortex ring |
| 2 | `titan` | **Titan — The Cosmic Awakening** | Cosmic warrior, nebula, energy | Core pulse, energy cracks, expanding power rings, orbiting particles, floating debris, nebula shift |
| 3 | `umbra` | **Umbra — The Eminence in the Dark** | Void mage, violet galaxy, mana | Galaxy spin, mana orb orbits, energy waves floating motes, cloaked figure sway, text glow |
| 4 | `blade` | **Blade — The Magicless Swordsman** | Swordsman, sky, light trails | Sword arc trail, motion lines, impact sparks, breathing figure, tower pulse, wind sweep |

---

## Quick Start (CLI)

```bash
# Clone or copy the svggen directory anywhere
cd svggen/

# Generate all 4 characters
go run ./cmd/svggen --all

# Output in ./out/
#   ├── raven.svg
#   ├── titan.svg
#   ├── umbra.svg
#   └── blade.svg

# Generate a single character
go run ./cmd/svggen --name raven

# Output to custom directory
go run ./cmd/svggen --all --output ./my-artworks

# Pipe SVG to stdout (for scripting)
go run ./cmd/svggen --name titan --stdout > titan.svg

# List available characters with descriptions
go run ./cmd/svggen --list
```

### Build a Binary

```bash
cd svggen/
go build -o svggen ./cmd/svggen/
./svggen --all
```

---

## Use as a Go Library

Add to any Go project:

```go
import "github.com/magicwebb/svggen"

// Generate SVG as a string
svg := svggen.GenerateSVG("raven")

// Save to file
path, _ := svggen.SaveToFile("titan", "./output")

// Generate all
results, _ := svggen.GenerateAll("./nft-art")
for slug, path := range results {
    fmt.Printf("%s → %s\n", slug, path)
}

// List characters
for _, c := range svggen.GetCharacters() {
    fmt.Printf("%s: %s\n", c.Slug, c.Name)
}
```

If you copy the `svggen/` directory into your project, update the module path in `go.mod`:

```
module your-project/svggen
```

Or reference it locally:

```
# go.mod
require github.com/magicwebb/svggen v0.0.0
replace github.com/magicwebb/svggen => ./svggen
```

---

## Integration with MagicWebb

The `svggen` tool is the artwork generator for the MagicWebb NFT marketplace. Here's the integration flow:

```
svggen/                          ← Standalone tool (this directory)
  └── outputs/                   ← Generated SVG files
        ├── raven.svg
        ├── titan.svg
        ├── umbra.svg
        └── blade.svg

MagicWebb backend:
  tools/seed-testnet/
    └── seed.sh                  ← Reads generated SVGs from svggen/outputs/
                                  → Stores in imagestore (Postgres BYTEA)
                                  → Serves at /api/v1/img/<sha256>

  Metadata JSONs now point to:
    "image": "/api/v1/img/<sha256>"    ← Self-hosted, no IPFS needed
```

### How to wire it in:

1. **Generate SVGs** — run `cd svggen && go run ./cmd/svggen --all --output ./outputs`
2. **Seed script stores them** — the updated `seed.sh` reads each SVG file, computes its SHA-256 hash, stores bytes in Postgres via the `imagestore`, and updates the metadata JSON's `image` field to `/api/v1/img/<hash>`
3. **Marketplace serves them** — the existing `/api/v1/img/<hash>` endpoint returns the SVG with `Content-Type: image/svg+xml`
4. **Done** — no IPFS, no external CDN, no hosting costs

> **Portability note:** The `svggen/` directory has no reference to MagicWebb internals. It's a fully independent Go module. To use it in another project, copy the directory, update `go.mod`, and import it.

---

## SVG Animation Details

Each SVG includes rich CSS and SVG-native animations:

| Animation Type | Implementation | Example |
|:---------------|:---------------|:--------|
| **Pulse/Glow** | CSS `@keyframes` on opacity + filter | Moon pulse, eye glow |
| **Orbit** | `animateTransform` rotation with translate | Crows flying, mana orbs |
| **Expand/Fade** | `<animate>` on `r` and `opacity` | Power rings, shockwaves |
| **Float** | `<animate>` on `cy` + `opacity` | Particles, debris |
| **Sway/Drift** | CSS `@keyframes` on transform | Cloak, mist, fog |
| **Motion trail** | CSS `@keyframes` on stroke-dashoffset | Sword arc, slash |
| **Spark burst** | `<animate>` on `cx`/`cy`/`opacity` | Impact sparks |

All animations run in-browser with no external JavaScript — pure SVG + CSS.

---

## License

This tool is provided as part of the MagicWebb NFT marketplace project.
The generated SVG artwork is original and copyright-free.
Use freely in any project, commercial or otherwise.
