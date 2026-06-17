package api

import "testing"

func TestSafeImageContentTypeAllowsBitmapImages(t *testing.T) {
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
		got, ok := safeImageContentType(tt.body)
		if !ok || got != tt.want {
			t.Fatalf("%s: safeImageContentType = %q,%v; want %q,true", tt.name, got, ok, tt.want)
		}
	}
}

func TestSafeImageContentTypeRejectsActiveContent(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		[]byte(`{"image":"https://example.com/nft.png"}`),
		[]byte(`<!doctype html><script>alert(1)</script>`),
	} {
		if got, ok := safeImageContentType(body); ok {
			t.Fatalf("safeImageContentType(%q) = %q,true; want false", body, got)
		}
	}
}
