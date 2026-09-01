package api

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/etag"
)

// etagTestApp mirrors the production middleware chain on the /api/v1 group:
// browserCacheReads (Cache-Control) BEFORE etag (validators), so a 304 still
// carries Cache-Control.
func etagTestApp() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	api := app.Group("/api/v1", browserCacheReads(), etag.New(etag.Config{
		Weak: true,
		Next: func(c *fiber.Ctx) bool {
			if c.Method() != fiber.MethodGet {
				return true
			}
			p := c.Path()
			return len(p) >= 13 && p[:13] == "/api/v1/media"
		},
	}))
	api.Get("/listings", func(c *fiber.Ctx) error { return c.JSON([]fiber.Map{{"id": 1}}) })
	api.Post("/listings", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })
	api.Get("/media/x", func(c *fiber.Ctx) error { return c.SendString("blob") })
	return app
}

func TestETag_304OnConditionalGet(t *testing.T) {
	app := etagTestApp()

	first, err := app.Test(httptest.NewRequest("GET", "/api/v1/listings", nil))
	if err != nil {
		t.Fatal(err)
	}
	if first.StatusCode != 200 {
		t.Fatalf("first GET status = %d, want 200", first.StatusCode)
	}
	et := first.Header.Get("ETag")
	if et == "" || et[:2] != "W/" {
		t.Fatalf("ETag = %q, want a weak validator (W/...)", et)
	}
	if cc := first.Header.Get("Cache-Control"); cc != "private, max-age=2, stale-while-revalidate=10" {
		t.Fatalf("Cache-Control = %q, want private/max-age=2/swr", cc)
	}

	req := httptest.NewRequest("GET", "/api/v1/listings", nil)
	req.Header.Set("If-None-Match", et)
	second, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if second.StatusCode != 304 {
		t.Fatalf("conditional GET status = %d, want 304", second.StatusCode)
	}
	body, _ := io.ReadAll(second.Body)
	if len(body) != 0 {
		t.Fatalf("304 body should be empty, got %d bytes", len(body))
	}
	// Cache-Control set before etag, so it rides the 304 too.
	if cc := second.Header.Get("Cache-Control"); cc != "private, max-age=2, stale-while-revalidate=10" {
		t.Fatalf("304 Cache-Control = %q, want it preserved", cc)
	}
}

func TestETag_SkippedForPOST(t *testing.T) {
	app := etagTestApp()
	resp, err := app.Test(httptest.NewRequest("POST", "/api/v1/listings", nil))
	if err != nil {
		t.Fatal(err)
	}
	if et := resp.Header.Get("ETag"); et != "" {
		t.Fatalf("POST should not carry an ETag, got %q", et)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "" {
		t.Fatalf("POST should not be browser-cached, got Cache-Control %q", cc)
	}
}

func TestETag_SkippedForMedia(t *testing.T) {
	app := etagTestApp()
	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/media/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	if et := resp.Header.Get("ETag"); et != "" {
		t.Fatalf("media should be skipped by etag, got %q", et)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "" {
		t.Fatalf("media is not in the browser-cache allowlist, got %q", cc)
	}
}
