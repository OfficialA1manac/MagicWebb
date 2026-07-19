package api

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
)

// testPayload returns an HTML page large enough to trigger compression.
// Generates the specified number of listing-card divs; each is ~200 bytes.
func testPayload(cardCount int) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><head><title>Test</title></head><body><div class="container">`)
	for i := 0; i < cardCount; i++ {
		b.WriteString(`<div class="card"><img src="/img/test.png" alt="Card"><p class="title">NFT #`)
		b.WriteByte(byte('0' + i%10))
		b.WriteString(`</p><p class="price">1.5 FLR</p></div>`)
	}
	b.WriteString(`</div></body></html>`)
	return b.String()
}

// newCompressApp creates a minimal Fiber app with the compress middleware
// at level 5 (our production setting) and a single HTML route.
func newCompressApp() *fiber.App {
	app := fiber.New()
	app.Use(compress.New(compress.Config{Level: 5}))
	app.Get("/", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/html; charset=utf-8")
		return c.SendString(testPayload(10)) // ~2KB, well above compression threshold
	})
	return app
}

func TestCompressBrotliEndToEnd(t *testing.T) {
	app := newCompressApp()

	req, _ := http.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "br")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// The compressed body should be smaller than the uncompressed payload.
	uncompressed := testPayload(10)
	if len(body) >= len(uncompressed) {
		t.Errorf("brotli body size %d >= uncompressed %d — compression may not have triggered", len(body), len(uncompressed))
	}

	enc := resp.Header.Get("Content-Encoding")
	if enc != "br" {
		t.Errorf("Content-Encoding: want 'br', got %q", enc)
	}

	vary := resp.Header.Get("Vary")
	if !strings.Contains(vary, "Accept-Encoding") {
		t.Errorf("Vary: want to contain 'Accept-Encoding', got %q", vary)
	}

	if resp.StatusCode != 200 {
		t.Errorf("status: want 200, got %d", resp.StatusCode)
	}
}

func TestCompressGzipEndToEnd(t *testing.T) {
	app := newCompressApp()

	req, _ := http.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	uncompressed := testPayload(10)
	if len(body) >= len(uncompressed) {
		t.Errorf("gzip body size %d >= uncompressed %d — compression may not have triggered", len(body), len(uncompressed))
	}

	enc := resp.Header.Get("Content-Encoding")
	if enc != "gzip" {
		t.Errorf("Content-Encoding: want 'gzip', got %q", enc)
	}

	vary := resp.Header.Get("Vary")
	if !strings.Contains(vary, "Accept-Encoding") {
		t.Errorf("Vary: want to contain 'Accept-Encoding', got %q", vary)
	}
}

func TestCompressVaryPresentOnCompressedResponses(t *testing.T) {
	app := newCompressApp()

	// Vary: Accept-Encoding is set when compression is applied.
	// It is NOT set when no Accept-Encoding header is sent and the response
	// is served uncompressed (Fiber's compress middleware only sets Vary when
	// it processes an Accept-Encoding header, per its source).
	tests := []struct {
		name           string
		acceptEncoding string
		expectEnc      string
		expectVary     bool
	}{
		{"brotli", "br", "br", true},
		{"gzip", "gzip", "gzip", true},
		{"no encoding", "", "", false}, // no compression → no Vary
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/", nil)
			if tt.acceptEncoding != "" {
				req.Header.Set("Accept-Encoding", tt.acceptEncoding)
			}

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			resp.Body.Close()

			vary := resp.Header.Get("Vary")
			if tt.expectVary && !strings.Contains(vary, "Accept-Encoding") {
				t.Errorf("Vary: want to contain 'Accept-Encoding', got %q (encoding=%q)", vary, tt.name)
			}
			if !tt.expectVary && vary != "" {
				t.Errorf("Vary: want empty for no-encoding response, got %q", vary)
			}

			if tt.expectEnc != "" {
				enc := resp.Header.Get("Content-Encoding")
				if enc != tt.expectEnc {
					t.Errorf("Content-Encoding: want %q, got %q", tt.expectEnc, enc)
				}
			}
		})
	}
}

func TestCompressContentTypePreserved(t *testing.T) {
	app := newCompressApp()

	req, _ := http.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "br")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type: want 'text/html', got %q", ct)
	}
}

func TestCompressSmallPayloadNotCompressed(t *testing.T) {
	app := fiber.New()
	app.Use(compress.New(compress.Config{Level: 5}))
	app.Get("/small", func(c *fiber.Ctx) error {
		return c.SendString("ok") // 2 bytes — below compression threshold
	})

	req, _ := http.NewRequest("GET", "/small", nil)
	req.Header.Set("Accept-Encoding", "br")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// Small payloads should be served uncompressed.
	if string(body) != "ok" {
		t.Errorf("body: want 'ok', got %q", body)
	}

	enc := resp.Header.Get("Content-Encoding")
	if enc != "" {
		t.Errorf("Content-Encoding: want empty for small payloads, got %q", enc)
	}

	// Vary is NOT set for payloads below the compression threshold —
	// Fiber only sets it when compression is actually applied or when
	// the response was compressible but served uncompressed due to
	// client preference. Sub-threshold payloads skip the compress
	// middleware entirely.
	if resp.Header.Get("Vary") != "" {
		t.Errorf("Vary: want empty for sub-threshold payloads, got %q", resp.Header.Get("Vary"))
	}
}

func TestCompressBrotliPriorityOverGzip(t *testing.T) {
	// When both br and gzip are offered, brotli should win.
	app := newCompressApp()

	req, _ := http.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")

	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	enc := resp.Header.Get("Content-Encoding")
	if enc != "br" {
		t.Errorf("Content-Encoding: want 'br' (brotli priority over gzip), got %q", enc)
	}

	vary := resp.Header.Get("Vary")
	if !strings.Contains(vary, "Accept-Encoding") {
		t.Errorf("Vary: want to contain 'Accept-Encoding', got %q", vary)
	}
}
