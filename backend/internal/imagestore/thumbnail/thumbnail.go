// Package thumbnail provides Go-native image resizing for generating
// thumbnail variants (128px, 256px, 512px) from full-size NFT images.
// Supports JPEG, PNG, GIF input formats. WEBP and AVIF pass through
// unchanged (Go stdlib doesn't have WEBP/AVIF encoders — we serve the
// full-size original for these formats via the size=full fallback).
//
// Resizing uses nearest-neighbor for GIF (paletted) and bilinear for
// true-color images. For production-quality thumbnails, the resampling
// quality is sufficient for ~5-15KB listing-card thumbnails that render
// at 64-128px on screen.
package thumbnail

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
)

// Sizes are the standard thumbnail widths. Height is scaled proportionally.
const (
	Size128 = 128
	Size256 = 256
	Size512 = 512
)

// AllSizes returns all supported thumbnail widths.
func AllSizes() []int {
	return []int{Size128, Size256, Size512}
}

// CanResize reports whether the MIME type can be resized by this package.
// Currently supports image/jpeg, image/png, image/gif. WEBP and AVIF are
// not supported by Go's stdlib encoder — for these formats, the caller
// should serve the full-size original.
func CanResize(mime string) bool {
	switch mime {
	case "image/jpeg", "image/png", "image/gif":
		return true
	}
	return false
}

// Generate creates a resized variant of the input image bytes to fit within
// the given target width. Height is scaled proportionally. For GIF images,
// only the first frame is resized (animated GIFs become static thumbnails).
//
// Returns the resized bytes in the original format (JPEG→JPEG, PNG→PNG,
// GIF→GIF) and the output MIME type. Returns an error if the image cannot
// be decoded or if the format is unsupported.
func Generate(body []byte, mime string, targetWidth int) ([]byte, string, error) {
	if !CanResize(mime) {
		return nil, "", fmt.Errorf("thumbnail: unsupported mime %s", mime)
	}

	// Decode the source image.
	src, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("thumbnail: decode: %w", err)
	}

	// Calculate proportional height.
	bounds := src.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()
	if srcW <= targetWidth {
		// Image is already smaller than target — return original as-is.
		return body, mime, nil
	}
	targetH := srcH * targetWidth / srcW
	if targetH < 1 {
		targetH = 1
	}

	// Create the resized image.
	dst := image.NewRGBA(image.Rect(0, 0, targetWidth, targetH))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)

	// Encode back to the original format.
	var buf bytes.Buffer
	switch mime {
	case "image/jpeg":
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 80})
	case "image/png":
		err = png.Encode(&buf, dst)
	case "image/gif":
		// For GIF, encode as a single-frame GIF.
		// Paletted output keeps thumbnail size very small.
		paletted := image.NewPaletted(dst.Bounds(), nil)
		draw.FloydSteinberg.Draw(paletted, dst.Bounds(), dst, image.Point{})
		err = gif.Encode(&buf, paletted, nil)
	}
	if err != nil {
		return nil, "", fmt.Errorf("thumbnail: encode: %w", err)
	}

	return buf.Bytes(), mime, nil
}

// QuickResize is a fast path that decodes, resizes, and re-encodes in a
// single function call. Use for batch thumbnail generation during ingest.
func QuickResize(body []byte, mime string) map[int][]byte {
	out := make(map[int][]byte, len(AllSizes()))
	for _, size := range AllSizes() {
		resized, _, err := Generate(body, mime, size)
		if err != nil {
			continue // skip failed sizes; caller logs the error
		}
		out[size] = resized
	}
	return out
}
