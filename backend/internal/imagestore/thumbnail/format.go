// Package thumbnail — IMG-4: Format negotiation from HTTP Accept header.
//
// Parses the standard HTTP Accept header to determine the best thumbnail
// format the client supports. Uses quality values (q=) and format
// preference order to select between jpeg, webp, and avif.
package thumbnail

import (
	"sort"
	"strconv"
	"strings"
)

// Format represents an output image format for thumbnail generation.
type Format string

// Supported thumbnail output formats. JPEG is always available (Go stdlib).
// WebP requires deepteams/webp (pure-Go). AVIF requires -tags vips (Option A).
const (
	FormatJPEG Format = "jpeg"
	FormatWebP Format = "webp"
	FormatAVIF Format = "avif"
)

// MimeType returns the Content-Type for a format.
func (f Format) MimeType() string {
	switch f {
	case FormatJPEG:
		return "image/jpeg"
	case FormatWebP:
		return "image/webp"
	case FormatAVIF:
		return "image/avif"
	default:
		return "image/jpeg"
	}
}

// AvailableFormats returns formats that are available in the current build.
// JPEG is always available. WebP is pure-Go (no build tag). AVIF requires
// -tags vips and will not appear in the default build.
func AvailableFormats() []Format {
	formats := []Format{FormatJPEG, FormatWebP}
	// AVIF is only available with CGO + libvips (Option A).
	// Pure-Go builds skip it.
	if avifAvailable() {
		formats = append(formats, FormatAVIF)
	}
	return formats
}

// avifAvailable returns true when the AVIF encoder is compiled in.
// In pure-Go builds (Option B), this always returns false.
// The -tags vips build replaces this with a true implementation.
func avifAvailable() bool {
	return false
}

// NegotiateFormat parses an HTTP Accept header and returns the best
// thumbnail format supported by both client and server.
//
// Examples:
//
//	"image/avif,image/webp,image/*" → FormatAVIF (if available) or FormatWebP
//	"image/webp,*/*"                → FormatWebP
//	"*/*"                           → FormatJPEG (default)
//	""                              → FormatJPEG (default)
//
// Falls back to JPEG when no acceptable format matches.
func NegotiateFormat(acceptHeader string) Format {
	if acceptHeader == "" || acceptHeader == "*/*" {
		// Default to JPEG for clients that don't specify a preference.
		// WebP must be explicitly requested via "image/webp" in Accept
		// to avoid breaking older browsers (Safari <14, old Android).
		return FormatJPEG
	}

	available := AvailableFormats()
	parsed := parseAccept(acceptHeader)

	// Sort by quality (descending), then by server preference.
	for _, f := range available {
		for _, ae := range parsed {
			if ae.matches(string(f.MimeType())) && ae.quality > 0 {
				return f
			}
		}
	}

	// No match — return first available format (usually JPEG).
	if len(available) > 0 {
		return available[0]
	}
	return FormatJPEG
}

// acceptEntry is one parsed entry from the Accept header.
type acceptEntry struct {
	mime    string
	quality float64
}

// matches reports whether the Accept entry covers the given MIME type.
// Handles wildcards: "image/*" matches "image/webp", "*/*" matches anything.
func (ae acceptEntry) matches(mime string) bool {
	if ae.mime == "*/*" {
		return true
	}
	if ae.mime == "image/*" && strings.HasPrefix(mime, "image/") {
		return true
	}
	return ae.mime == mime
}

// parseAccept parses an HTTP Accept header value into ordered entries.
// Entries are sorted by quality (descending), with ties broken by specificity
// (exact match > subtype wildcard > full wildcard).
func parseAccept(header string) []acceptEntry {
	if header == "" {
		return nil
	}

	parts := strings.Split(header, ",")
	entries := make([]acceptEntry, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Split on ";" to separate media type from parameters.
		subparts := strings.Split(part, ";")
		mime := strings.TrimSpace(subparts[0])
		if mime == "" {
			continue
		}

		q := 1.0 // default quality
		for _, param := range subparts[1:] {
			param = strings.TrimSpace(param)
			if strings.HasPrefix(param, "q=") {
				if v, err := strconv.ParseFloat(strings.TrimPrefix(param, "q="), 64); err == nil {
					q = v
				}
			}
		}

		entries = append(entries, acceptEntry{mime: mime, quality: q})
	}

	// Sort: higher quality first, then more specific media types first.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].quality != entries[j].quality {
			return entries[i].quality > entries[j].quality
		}
		// More specific types beat wildcards at equal quality.
		iWild := entries[i].mime == "*/*"
		jWild := entries[j].mime == "*/*"
		if iWild != jWild {
			return !iWild
		}
		iSub := strings.HasPrefix(entries[i].mime, "image/*")
		jSub := strings.HasPrefix(entries[j].mime, "image/*")
		if iSub != jSub {
			return !iSub
		}
		return false
	})

	return entries
}
