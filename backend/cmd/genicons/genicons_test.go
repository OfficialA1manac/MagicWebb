package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// PNG signature per ISO/IEC 15948: §3.1. The 8-byte prefix followed by
// chunks is how every valid PNG begins; decode-tooling in the wild relies
// on this as a hard precondition.
var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// genIconPath resolves to the absolute path of the generated PNG for a
// given size, anchored on the backend/ module root (via runtime.Caller +
// go.mod walk) so the test passes regardless of cwd. Mirrors the path
// the generator writes — they MUST agree or the generator's output
// would be invisible to its own tests.
func genIconPath(t *testing.T, size int) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "internal", "ui", "static",
				fmt.Sprintf("icon-%d.png", size))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate go.mod above %s", thisFile)
		}
		dir = parent
	}
}

// readIcon reads and png-decodes a generated icon file.
func readIcon(t *testing.T, sz int) image.Image {
	t.Helper()
	path := genIconPath(t, sz)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.HasPrefix(b, pngSignature) {
		t.Fatalf("%s magic bytes = %x; want %x", path, b[:8], pngSignature)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("png.Decode %s: %v", path, err)
	}
	return img
}

// TestGeniconsOutputsExist pins both generated files at exactly the paths
// the Fiber filesystem middleware serves. A future refactor that renames
// either file without updating wallet.js's icons array would silently
// regress WC pairing back to "Unknown dApp".
func TestGeniconsOutputsExist(t *testing.T) {
	for _, sz := range outputSizes {
		path := genIconPath(t, sz)
		st, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if st.Size() <= 0 {
			t.Fatalf("%s size = %d, want > 0", path, st.Size())
		}
	}
}

// TestGeniconsHaveCorrectDimensions locks the WC-recommended sizes. 192
// covers the small WC mobile display surfaces; 512 covers the high-DPI
// reads on tablets / desktop wallets. A future regression that re-renders
// at "even numbers" (e.g. 200, 256) breaks mobile quality.
func TestGeniconsHaveCorrectDimensions(t *testing.T) {
	for _, sz := range outputSizes {
		img := readIcon(t, sz)
		b := img.Bounds()
		w, h := b.Dx(), b.Dy()
		if w != sz || h != sz {
			t.Fatalf("icon %d dimensions = %dx%d, want %dx%d", sz, w, h, sz, sz)
		}
	}
}

// TestGeniconsCenterIsMarkColor samples the dead-center pixel: must be the
// white mark (the diamond overlaps (size/2, size/2) for any size ≥ 6). If
// makeIcon's center formula ever drifts, the mark stops being centered
// and the logo reads unbalanced in WalletConnect's preview card.
func TestGeniconsCenterIsMarkColor(t *testing.T) {
	for _, sz := range outputSizes {
		img := readIcon(t, sz)
		b := img.Bounds()
		cp := img.At(b.Dx()/2+b.Min.X, b.Dy()/2+b.Min.Y)
		r, g, bl, a := cp.RGBA()
		mr, mg, mb, ma := brandMark.RGBA()
		if r != mr || g != mg || bl != mb || a != ma {
			t.Fatalf("icon %d center pixel = %d,%d,%d,%d; want white mark %d,%d,%d,%d",
				sz, r, g, bl, a, mr, mg, mb, ma)
		}
	}
}

// TestGeniconsCornerIsTransparent pins the rounded-corner alpha. The four
// true corners (0,0), (size-1,0), (0,size-1), (size-1,size-1) are the
// pixels furthest from the body's interior; the rounded-square algorithm
// must leave them transparent so PWA splash and mask-icon backgrounds
// don't show stray blue corners.
func TestGeniconsCornerIsTransparent(t *testing.T) {
	for _, sz := range outputSizes {
		img := readIcon(t, sz)
		b := img.Bounds()
		corners := []image.Point{
			{b.Min.X, b.Min.Y},
			{b.Max.X - 1, b.Min.Y},
			{b.Min.X, b.Max.Y - 1},
			{b.Max.X - 1, b.Max.Y - 1},
		}
		for _, c := range corners {
			r, g, bl, a := img.At(c.X, c.Y).RGBA()
			// The pixel must be fully transparent (alpha 0). RGB don't
			// matter because no rasterizer will write a pixel with alpha 0.
			if a != 0 {
				t.Fatalf("icon %d corner %v alpha = %d; want 0 (r,g,b=%d,%d,%d)",
					sz, c, a, r, g, bl)
			}
		}
	}
}

// TestGeniconsOffCornerIsBodyColor locks body pixels (slightly inset from
// straight edges). A future rounding regression that pushes the radius
// too large or removes it would expose non-blue pixels — the test fails
// loudly instead of shipping a logo WC displays without the brand color.
func TestGeniconsOffCornerIsBodyColor(t *testing.T) {
	for _, sz := range outputSizes {
		img := readIcon(t, sz)
		b := img.Bounds()
		cx, cy := b.Dx()/2+b.Min.X, b.Dy()/2+b.Min.Y
		samples := []image.Point{
			{cx, b.Min.Y + b.Dy()/8},     // top center, away from diamond
			{b.Min.X + b.Dx()/8, cy},     // left center
			{b.Max.X - 1 - b.Dx()/8, cy}, // right center
			{cx, b.Max.Y - 1 - b.Dy()/8}, // bottom center
		}
		skyR, skyG, skyB, skyA := brandSky.RGBA()
		for _, p := range samples {
			r, g, bl, a := img.At(p.X, p.Y).RGBA()
			if r != skyR || g != skyG || bl != skyB || a != skyA {
				t.Fatalf("icon %d pixel %v = %d,%d,%d,%d; want sky body %d,%d,%d,%d",
					sz, p, r, g, bl, a, skyR, skyG, skyB, skyA)
			}
		}
	}
}

// TestGeniconsPathsResolveViaStaticFS verifies the genicons outputs live
// under internal/ui/static so embed.go's `//go:embed all:static` catches
// them and the Fiber filesystem middleware can serve them at
// `/static/icon-{192,512}.png`. A future move to e.g. internal/ui/static/
// icons/ would silently break the wallet.js WC metadata wire without a
// test failure here.
func TestGeniconsPathsResolveViaStaticFS(t *testing.T) {
	const want = string(filepath.Separator) + "internal" +
		string(filepath.Separator) + "ui" +
		string(filepath.Separator) + "static" +
		string(filepath.Separator)
	for _, sz := range outputSizes {
		path := genIconPath(t, sz)
		if !bytes.Contains([]byte(path), []byte(want)) {
			t.Fatalf("icon %d path %q does not include %q; embed.FS will not pick it up",
				sz, path, want)
		}
	}
}
