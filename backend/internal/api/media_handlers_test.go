package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/media"
)

// ── Sniffer tests (existing, snake-cased in suite above) ────────────────────

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

// ── /api/v1/img/retry endpoint ───────────────────────────────────────────────
//
// The handler is wired with a fetcher closure (imageRetryFetcher) so tests
// inject a stub that doesn't hit the network. The pgxmock DB substitutes
// for *db.Q.

var imgRetryPNGHeader = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}

// expectedGetTokenMetaRegex anchors on the GetTokenMeta SQL signature: a
// JOIN from nft_tokens LEFT JOIN nft_metadata. If a future refactor drops
// the JOIN or splits the function, this regex fails the test — the
// production handler's behavior depends on the join for fetching image_uri
// fallbacks when nft_tokens has no row.
var expectedGetTokenMetaRegex = `SELECT COALESCE\(m\.name, t\.name, ''\), COALESCE\(m\.image_uri, t\.image_uri, ''\)[\s\S]*FROM nft_tokens t\s+LEFT JOIN nft_metadata m`

// newRetryApp wires imageRetryNow against a pgxmock-backed *db.Q and a
// caller-supplied fetcher stub. Returns the Fiber app + the recorder-style
// helper the tests invoke.
func newRetryApp(t *testing.T, mock pgxmock.PgxPoolIface, fetch imageRetryFetcher) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
	})
	svc := NewMediaService(db.New(mock), nil, nil)
	svc.fetch = fetch
	app.Post("/api/v1/img/retry", svc.handleRetry)
	return app
}

func doRetry(t *testing.T, app *fiber.App, coll, id string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/img/retry?coll="+coll+"&id="+id, nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func readJSON(t *testing.T, r *http.Response) map[string]any {
	t.Helper()
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %q: %v", string(b), err)
	}
	return m
}

func TestImageRetryNowRejectsMissingQueryParams(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	app := newRetryApp(t, mock, func(ctx context.Context, uri, tokenID string) ([]byte, error) {
		t.Fatal("fetcher should not run when query is malformed")
		return nil, nil
	})
	resp := doRetry(t, app, "", "1")
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImageRetryNowIsNoOpWhenAlreadyLocal(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	mock.ExpectQuery(expectedGetTokenMetaRegex).
		WithArgs("0xc", "1").
		WillReturnRows(pgxmock.NewRows([]string{"name", "image_uri"}).
			AddRow("", "/api/v1/img/abc123def"))

	app := newRetryApp(t, mock, func(ctx context.Context, uri, tokenID string) ([]byte, error) {
		t.Fatal("fetcher must NOT be invoked when image_uri is already local")
		return nil, nil
	})

	resp := doRetry(t, app, "0xc", "1")
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readJSON(t, resp)
	if body["status"] != "already_local" {
		t.Fatalf("status = %v, want already_local", body["status"])
	}
	if body["image_uri"] != "/api/v1/img/abc123def" {
		t.Fatalf("image_uri = %v, want /api/v1/img/abc123def", body["image_uri"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImageRetryNowRejectsUnsupportedURI(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	mock.ExpectQuery(expectedGetTokenMetaRegex).
		WithArgs("0xc", "1").
		WillReturnRows(pgxmock.NewRows([]string{"name", "image_uri"}).
			AddRow("", "data:image/png;base64,AAAA"))

	app := newRetryApp(t, mock, func(ctx context.Context, uri, tokenID string) ([]byte, error) {
		t.Fatal("fetcher must NOT be invoked for non-retryable URIs")
		return nil, nil
	})

	resp := doRetry(t, app, "0xc", "1")
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body := readJSON(t, resp)
	if !strings.Contains(strings.ToLower(asString(body["error"])), "upstream") {
		t.Fatalf("error = %v, want a message mentioning 'upstream'", body["error"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImageRetryNowReturns404WhenNoImageURI(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	mock.ExpectQuery(expectedGetTokenMetaRegex).
		WithArgs("0xc", "1").
		WillReturnRows(pgxmock.NewRows([]string{"name", "image_uri"}).
			AddRow("", ""))

	app := newRetryApp(t, mock, func(ctx context.Context, uri, tokenID string) ([]byte, error) {
		t.Fatal("fetcher must NOT run when row exists but image_uri empty")
		return nil, nil
	})

	resp := doRetry(t, app, "0xc", "1")
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImageRetryNowReturns502WhenFetcherFails(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	mock.ExpectQuery(expectedGetTokenMetaRegex).
		WithArgs("0xc", "1").
		WillReturnRows(pgxmock.NewRows([]string{"name", "image_uri"}).
			AddRow("", "https://ipfs.io/ipfs/QmFakeHash"))

	app := newRetryApp(t, mock, func(ctx context.Context, uri, tokenID string) ([]byte, error) {
		return nil, errors.New("gateway timeout")
	})

	resp := doRetry(t, app, "0xc", "1")
	if resp.StatusCode != fiber.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImageRetryNowSelfHostsAndUpdatesAtomically(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	mock.ExpectQuery(expectedGetTokenMetaRegex).
		WithArgs("0xc", "1").
		WillReturnRows(pgxmock.NewRows([]string{"name", "image_uri"}).
			AddRow("", "https://ipfs.io/ipfs/QmFakeHash"))

	// Pre-compute the SHA so we can pin the imagestore arg with the literal hex
	// instead of relying on AnyArg (which would lose the structural pin if
	// someone accidentally introduces a Transform step or salt).
	sum := sha256.Sum256(imgRetryPNGHeader)
	expectedSHA := hex.EncodeToString(sum[:])
	expectedLocalPath := "/api/v1/img/" + expectedSHA

	// imagestore.Put flow:
	//   1) HasImage (pre-check) returns pgx.ErrNoRows → (false, nil)
	//   2) PutImage in a BEGIN/INSERT/COMMIT tx
	mock.ExpectQuery(`SELECT 1 FROM nft_image_blobs WHERE sha256=\$1`).
		WithArgs(expectedSHA).
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO nft_image_blobs`).
		WithArgs(expectedSHA, "image/png", len(imgRetryPNGHeader), "https://ipfs.io/ipfs/QmFakeHash", imgRetryPNGHeader).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	// UpdateImageURI atomic tx flow (BEGIN/update_nft_metadata/update_nft_tokens/COMMIT):
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE nft_metadata SET image_uri`).
		WithArgs("0xc", "1", expectedLocalPath).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec(`UPDATE nft_tokens SET image_uri`).
		WithArgs("0xc", "1", expectedLocalPath).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	app := newRetryApp(t, mock, func(ctx context.Context, uri, tokenID string) ([]byte, error) {
		if !strings.HasPrefix(uri, "https://") {
			t.Fatalf("fetcher saw unexpected uri %q", uri)
		}
		if tokenID != "1" {
			t.Fatalf("fetcher tokenID = %q, want 1", tokenID)
		}
		return imgRetryPNGHeader, nil
	})

	resp := doRetry(t, app, "0xc", "1")
	if resp.StatusCode != fiber.StatusOK {
		bd, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d (body %q), want 200", resp.StatusCode, string(bd))
	}
	body := readJSON(t, resp)
	if body["status"] != "ok" {
		t.Fatalf("status = %v, want ok", body["status"])
	}
	uri, ok := body["image_uri"].(string)
	if !ok || !strings.HasPrefix(uri, "/api/v1/img/") {
		t.Fatalf("image_uri = %v, want /api/v1/img/...", body["image_uri"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// isRetriableUpstream mirrors the production gate. Locked here so a future
// template / handler change can't drift.
func TestIsRetriableUpstream(t *testing.T) {
	yesIf := []string{"http://x", "https://x", "ipfs://x"}
	noIf := []string{"", "/api/v1/img/x", "data:image/png;base64,AAAA", "ftp://x"}
	for _, s := range yesIf {
		if !isRetriableUpstream(s) {
			t.Fatalf("%q should be retriable", s)
		}
	}
	for _, s := range noIf {
		if isRetriableUpstream(s) {
			t.Fatalf("%q should NOT be retriable", s)
		}
	}
}

// asString pulls a string out of a JSON-decoded `any` value, returning "" if
// the shape is unexpected — keeps assertion lines compact.
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
