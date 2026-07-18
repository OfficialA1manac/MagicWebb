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

// generateThumb creates a resized variant of body at targetWidth pixels.
// Returns the thumbnail bytes and MIME type, or an error if generation fails.
//
// For JPEG, PNG, and GIF inputs, thumbnails preserve the original format.
// For WebP inputs, thumbnails are transcoded to JPEG (WebP can be decoded
// via golang.org/x/image/webp but Go stdlib cannot re-encode to WebP).
// Bridges the StoreThumbnails function to the thumbnail package.
func generateThumb(body []byte, mime string, targetWidth int) ([]byte, string, error) {
	// Try same-format resize first (works for JPEG, PNG, GIF).
	if resized, thumbMime, err := thumbnail.Generate(body, mime, targetWidth); err == nil {
		return resized, thumbMime, nil
	}

	// Fallback: transcode to JPEG for formats that decode but can't re-encode
	// in their original format (WebP, AVIF, etc.). GenerateAsJPEG uses
	// image.Decode (which recognises WebP via golang.org/x/image/webp) and
	// re-encodes as JPEG at quality 80.
	if !thumbnail.CanResize(mime) {
		return thumbnail.GenerateAsJPEG(body, targetWidth)
	}

	return nil, "", errUnsupportedThumb
}

var errUnsupportedThumb = &thumbErr{msg: "thumbnail: unsupported source format"}

type thumbErr struct{ msg string }

func (e *thumbErr) Error() string { return e.msg }
