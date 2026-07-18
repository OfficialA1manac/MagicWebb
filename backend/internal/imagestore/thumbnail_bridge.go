// Package imagestore — IMG-1: Bridge to the thumbnail generation package.
//
// The thumbnail package lives under imagestore/thumbnail/ to signal that it's
// a sub-package of the blob store. This bridge file lives in the parent
// imagestore package so StoreThumbnails (defined in imagestore.go) can call
// thumbnail generation. Separating the import into its own file avoids any
// risk of circular imports if the thumbnail package later needs to reference
// imagestore types.
package imagestore

import "github.com/OfficialA1manac/MagicWebb/backend/internal/imagestore/thumbnail"

// generateThumb creates a resized variant of body at targetWidth pixels,
// always outputting JPEG (universally supported, small file size). Uses
// GenerateFormat which handles JPEG, PNG, GIF, and WebP inputs — any
// format that image.Decode can read. WebP inputs are transcoded to JPEG.
//
// Bridges the StoreThumbnails function to the thumbnail package.
func generateThumb(body []byte, mime string, targetWidth int) ([]byte, string, error) {
	return thumbnail.GenerateFormat(body, mime, targetWidth, thumbnail.FormatJPEG)
}

// generateThumbWebP creates a WebP variant at targetWidth pixels. WebP
// typically achieves 25-35% smaller files than JPEG at equivalent quality,
// making it the best format for listing-card thumbnails. Uses the pure-Go
// deepteams/webp encoder (no CGO required).
//
// Falls back gracefully: if the WebP encoder is unavailable or the input
// can't be decoded, returns an error. Caller treats this as best-effort.
func generateThumbWebP(body []byte, mime string, targetWidth int) ([]byte, string, error) {
	return thumbnail.GenerateFormat(body, mime, targetWidth, thumbnail.FormatWebP)
}
