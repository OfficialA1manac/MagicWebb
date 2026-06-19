// Command genicons renders the MagicWebb app icons used by WalletConnect v2
// pairing screens (and any other browser/PWA surface that wants a branded
// 512×512 or 192×192 PNG).
//
// Run:
//
//	go run ./cmd/genicons
//
// Output:
//
//	internal/ui/static/icon-192.png
//	internal/ui/static/icon-512.png
//
// The output paths are picked up by the embedded static FS
// (internal/ui/embed.go's `//go:embed all:static`) and served at
// `/static/icon-NNN.png` by Fiber's filesystem middleware.
//
// The wire is already in place on the front-end: wallet.js builds the
// WalletConnect metadata as
//
//	icons: [`${window.location.origin}/static/icon-512.png`],
//
// so once these files exist, mobile wallets display "MagicWebb" instead of
// "Unknown dApp" during the QR handshake. Run this command once after a
// redesign; the bytes are committed next to it for embed-time serving.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"runtime"
)

// Brand palette mirrors internal/ui/templates/layout.html tailwind.config:
//
//	sky:   { 400: '#38bdf8', 500: '#0ea5e9' }
//	ink:   { 950: '#050507', 900: '#0a0a0d' }
//
// Both sizes use these constants verbatim — magicwebb's palette is
// the single source of truth for icon and page colors.
var (
	brandSky  = color.NRGBA{R: 0x38, G: 0xbd, B: 0xf8, A: 0xff}
	brandInk  = color.NRGBA{R: 0x05, G: 0x05, B: 0x07, A: 0xff}
	brandMark = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	// cleared marks pixels OUTSIDE the rounded-square body — PWA splash
	// and Android adaptive-icon backgrounds respect the alpha channel.
	cleared = color.NRGBA{R: 0, G: 0, B: 0, A: 0}
)

// makeIcon renders a rounded sky-blue square with a centered white diamond
// (rotated square). Corner radius and diamond half-diagonal scale with `size`
// so the same proportions read correctly at any resolution.
//
// Visual:
//   - Body: filled rounded square (sky-400) with corner radius = size/8
//   - Mark: centered white diamond, half-diagonal = size/3
//
// No font dependency — the diamond is a pure math shape drawn with
// |dx| + |dy| ≤ r, which renders sharp at any pixel size without aliasing
// artifacts from rasterizing an actual glyph.
func makeIcon(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	cx, cy := size/2, size/2
	cornerR := size / 8
	diamondR := size / 3

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if !insideRoundedSquare(x, y, size, cornerR) {
				img.SetNRGBA(x, y, cleared)
				continue
			}
			// Centered diamond: |dx| + |dy| ≤ diamondR.
			if absI(x-cx)+absI(y-cy) <= diamondR {
				img.SetNRGBA(x, y, brandMark)
			} else {
				img.SetNRGBA(x, y, brandSky)
			}
		}
	}
	return img
}

// insideRoundedSquare reports whether (x, y) is on the interior of a square
// of side `size` with rounded corners of radius `cornerR`. The seam point
// for each corner is `cornerR` pixels in from the adjacent edges; pixels
// within `cornerR` of the seam (chebyshev distance) are kept.
//
// Straight-edge region (where one or both of dx, dy ≥ cornerR) is kept
// unconditionally. Corner-zone pixels are kept iff they fall inside the
// circle of radius `cornerR` centered on the corner's seam point.
func insideRoundedSquare(x, y, size, cornerR int) bool {
	dx := x
	if size-1-x < dx {
		dx = size - 1 - x
	}
	dy := y
	if size-1-y < dy {
		dy = size - 1 - y
	}
	if dx >= cornerR || dy >= cornerR {
		return true
	}
	ddx := cornerR - dx
	ddy := cornerR - dy
	return ddx*ddx+ddy*ddy <= cornerR*cornerR
}

func absI(i int) int {
	if i < 0 {
		return -i
	}
	return i
}

// min is intentionally not pulled from a newer-language stdlib: we target
// Go 1.21. The tokenizer is fine with a home-grown helper here.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// render uses brandInk for an alt palette swap if we ever need a dark-mode
// companion. Currently unused but kept available behind a flag for future
// PWA variants.
func render(size int, palette ...color.NRGBA) *image.NRGBA {
	_ = palette // reserved
	return makeIcon(size)
}

// output is a single emit (size + path) for the wallet-connect-recommended
// icon set. WalletConnect v2 docs call for 192×192 and 512×512; smaller
// variants for favicons can be added later if a browser-side requirement
// surfaces. Paths are anchored on the backend/ module root (via
// runtime.Caller), so the generator works no matter what cwd the user
// invokes from — both for `go run ./cmd/genicons/...` and `go test
// ./cmd/genicons/...`.
var outputSizes = []int{192, 512}

// repoRoot returns the absolute path of the backend/ module root that
// contains this generator. Walked up from runtime.Caller(0) by locating
// the directory that owns go.mod — robust against the generator moving
// to cmd/genicons/, tools/genicons/, or any other depth in future
// restructuring.
func repoRoot() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		log.Fatalf("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			log.Fatalf("could not locate go.mod above %s", thisFile)
		}
		dir = parent
	}
}

func main() {
	root := repoRoot()
	for _, sz := range outputSizes {
		img := makeIcon(sz)
		outPath := filepath.Join(root, "internal", "ui", "static",
			fmt.Sprintf("icon-%d.png", sz))
		f, err := os.Create(outPath)
		if err != nil {
			log.Fatalf("create %s: %v", outPath, err)
		}
		if err := png.Encode(f, img); err != nil {
			_ = f.Close()
			log.Fatalf("encode %s: %v", outPath, err)
		}
		if err := f.Close(); err != nil {
			log.Fatalf("close %s: %v", outPath, err)
		}
		st, err := os.Stat(outPath)
		if err != nil {
			log.Fatalf("stat %s: %v", outPath, err)
		}
		fmt.Printf("wrote %s (%d bytes, %dx%d)\n", outPath, st.Size(), sz, sz)
	}
}
