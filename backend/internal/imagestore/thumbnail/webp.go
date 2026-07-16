// Package thumbnail — IMG-4: WebP encoder using pure-Go deepteams/webp.
//
// This file provides a format-aware encoder that converts decoded images
// to WebP (lossy VP8) at configurable quality. WebP typically achieves
// 25-35% smaller file sizes than JPEG at equivalent quality, making it
// the best bang-for-buck format for listing-card thumbnails.
//
// AVIF encoding requires CGO (libavif) and is NOT available in the pure-Go
// code path. Use -tags vips for Option A (libvips).
package thumbnail

import (
	"bytes"
	"fmt"
	"image"

	"github.com/deepteams/webp"
	"golang.org/x/image/draw"
)

// WebPQuality is the default lossy WebP quality (0-100). 80 matches the
// JPEG default in this package and provides good quality/size balance
// for NFT listing thumbnails.
const WebPQuality = 80

// EncodeWebP encodes an image.Image to lossy WebP bytes at the given
// quality (0-100, higher = better quality + larger file).
func EncodeWebP(img image.Image, quality float32) ([]byte, error) {
	if quality < 0 {
		quality = 0
	}
	if quality > 100 {
		quality = 100
	}

	var buf bytes.Buffer
	opts := &webp.Options{
		Quality: quality,
	}
	if err := webp.Encode(&buf, img, opts); err != nil {
		return nil, fmt.Errorf("webp encode: %w", err)
	}
	return buf.Bytes(), nil
}

// EncodeWebPFromBytes decodes the input bytes (JPEG/PNG/GIF), resizes to
// targetWidth, and encodes as WebP. Uses a shared resize helper so all
// format encoders benefit from the same bilinear scaling logic.
//
// Returns (webpBytes, "image/webp", nil) on success.
func EncodeWebPFromBytes(body []byte, targetWidth int, quality float32) ([]byte, string, error) {
	if quality <= 0 {
		quality = WebPQuality
	}
	if targetWidth <= 0 {
		return nil, "", fmt.Errorf("webp: invalid target width %d", targetWidth)
	}

	resized, err := resizeDecoded(body, targetWidth)
	if err != nil {
		return nil, "", fmt.Errorf("webp: %w", err)
	}

	webpBytes, err := EncodeWebP(resized, quality)
	if err != nil {
		return nil, "", err
	}
	return webpBytes, "image/webp", nil
}

// ResizeImage scales an image to the target dimensions using bilinear
// interpolation. Exported for use by the benchmark tool.
func ResizeImage(src image.Image, width, height int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

// resizeDecoded decodes image bytes and resizes to targetWidth with
// proportional height scaling. Returns the original-size image when it
// already fits within the target width. Shared by all format encoders.
func resizeDecoded(body []byte, targetWidth int) (image.Image, error) {
	src, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	bounds := src.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return nil, fmt.Errorf("zero-dimension image")
	}
	if srcW <= targetWidth {
		return src, nil
	}

	targetH := srcH * targetWidth / srcW
	if targetH < 1 {
		targetH = 1
	}
	return ResizeImage(src, targetWidth, targetH), nil
}
