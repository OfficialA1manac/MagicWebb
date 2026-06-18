package api

import (
	"testing"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/media"
)

func TestMediaSniffImageAllowsBitmapImages(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want string
	}{
		{"png", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, "image/png"},
		{"jpeg", []byte{0xff, 0xd8, 0xff, 0xdb}, "image/jpeg"},
		{"gif", []byte("GIF89a"), "image/gif"},
		{"webp", []byte("RIFFxxxxWEBPVP8 "), "image/webp"},
		{"avif", []byte("\x00\x00\x00\x18ftypavif"), "image/avif"},
	}
	for _, tt := range tests {
		got, ok := media.SniffImage(tt.body)
		if !ok || got != tt.want {
			t.Fatalf("%s: media.SniffImage = %q,%v; want %q,true", tt.name, got, ok, tt.want)
		}
	}
}

func TestMediaSniffImageRejectsActiveContent(t *testing.T) {
	// SVGs are now accepted (many NFT collections use SVG images). They are
	// safe when served as image/svg+xml via an <img> tag — browsers block
	// script execution in that context. Raw HTML and JSON must still be rejected.
	for _, body := range [][]byte{
		[]byte(`{"image":"https://example.com/nft.png"}`),
		[]byte(`<!doctype html><script>alert(1)</script>`),
	} {
		if got, ok := media.SniffImage(body); ok {
			t.Fatalf("media.SniffImage(%q) = %q,true; want false", body, got)
		}
	}
}

func TestMediaSniffImageAcceptsSVG(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{"bare svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"></svg>`)},
		{"xml preamble", []byte(`<?xml version="1.0"?><svg viewBox="0 0 100 100"></svg>`)},
		{"bom + xml + svg", append([]byte{0xEF, 0xBB, 0xBF}, []byte(`<?xml version="1.0"?><svg></svg>`)...)},
		{"uppercase SVG", []byte(`<SVG viewBox="0 0 100 100"></SVG>`)},
		{"svg with script tag", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)},
	}
	for _, tt := range tests {
		got, ok := media.SniffImage(tt.body)
		if !ok || got != "image/svg+xml" {
			t.Fatalf("%s: media.SniffImage = %q,%v; want image/svg+xml,true", tt.name, got, ok)
		}
	}
}

func TestMediaSniffImageRejectsNonSVGThatLooksLikeSVG(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`<svgTHISISNOTVALID`),
		[]byte(`<svgical data>`),
	} {
		if got, ok := media.SniffImage(body); ok {
			t.Fatalf("media.SniffImage(%q) = %q,true; want false", body, got)
		}
	}
}
