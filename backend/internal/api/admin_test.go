package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

const (
	testJWTSecret    = "test-secret-at-least-32-characters-long!!"
	testAdminAddr    = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testNonAdminAddr = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// issueTestToken issues a JWT for the given address using a fixed test secret.
func issueTestToken(t *testing.T, addr string) string {
	t.Helper()
	tok, err := auth.Issue(addr, testJWTSecret, auth.DefaultAudience, 1*time.Hour)
	if err != nil {
		t.Fatalf("failed to issue test JWT: %v", err)
	}
	return tok
}

// newAdminTestConfig creates a config with the test secret and an admin allowlist.
func newAdminTestConfig() *config.Config {
	return &config.Config{
		JWTSecret:      testJWTSecret,
		AdminAllowlist: []string{testAdminAddr},
	}
}

// newAdminApp creates a Fiber app with the admin debug routes registered.
func newAdminApp(t *testing.T, mock pgxmock.PgxPoolIface) *fiber.App {
	t.Helper()
	cfg := newAdminTestConfig()
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	svc := NewAdminService(db.New(mock), cfg)
	svc.RegisterRoutes(app, cfg)
	return app
}

func doGetWithAuth(t *testing.T, app *fiber.App, url, token string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

// ── /admin/debug/tracked-collections ───────────────────────────────────────────

func TestTrackedCollections_Unauthenticated(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	app := newAdminApp(t, mock)
	resp := doGetWithAuth(t, app, "/admin/debug/tracked-collections", "")

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (unauthenticated)", resp.StatusCode)
	}

	var body map[string]string
	decodeJSON(t, resp, &body)
	if !strings.Contains(body["error"], "missing token") {
		t.Fatalf("error = %q, want 'missing token'", body["error"])
	}
}

func TestTrackedCollections_NonAdmin(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	app := newAdminApp(t, mock)
	token := issueTestToken(t, testNonAdminAddr)
	resp := doGetWithAuth(t, app, "/admin/debug/tracked-collections", token)

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (non-admin)", resp.StatusCode)
	}
}

func TestTrackedCollections_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	// ListTrackedCollections returns addresses
	mock.ExpectQuery(`SELECT address FROM tracked_collections`).
		WillReturnRows(pgxmock.NewRows([]string{"address"}).
			AddRow("0xe96Afb7b664Ab90d74b778AdFE47D8342495807F").
			AddRow("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))

	app := newAdminApp(t, mock)
	token := issueTestToken(t, testAdminAddr)
	resp := doGetWithAuth(t, app, "/admin/debug/tracked-collections", token)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	decodeJSON(t, resp, &body)

	addrs, ok := body["tracked_collections"].([]interface{})
	if !ok {
		t.Fatalf("tracked_collections is not an array, got %T", body["tracked_collections"])
	}
	if len(addrs) != 2 {
		t.Fatalf("got %d tracked collections, want 2", len(addrs))
	}

	count, ok := body["count"].(float64)
	if !ok || count != 2 {
		t.Fatalf("count = %v, want 2", body["count"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTrackedCollections_Empty(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT address FROM tracked_collections`).
		WillReturnRows(pgxmock.NewRows([]string{"address"}))

	app := newAdminApp(t, mock)
	token := issueTestToken(t, testAdminAddr)
	resp := doGetWithAuth(t, app, "/admin/debug/tracked-collections", token)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	decodeJSON(t, resp, &body)

	addrs, ok := body["tracked_collections"].([]interface{})
	if !ok {
		t.Fatalf("tracked_collections is not an array, got %T", body["tracked_collections"])
	}
	if len(addrs) != 0 {
		t.Fatalf("expected empty tracked_collections, got %d entries", len(addrs))
	}

	count, ok := body["count"].(float64)
	if !ok || count != 0 {
		t.Fatalf("count = %v, want 0", body["count"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTrackedCollections_DBError(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT address FROM tracked_collections`).
		WillReturnError(fiber.ErrInternalServerError)

	app := newAdminApp(t, mock)
	token := issueTestToken(t, testAdminAddr)
	resp := doGetWithAuth(t, app, "/admin/debug/tracked-collections", token)

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}

	var body map[string]string
	decodeJSON(t, resp, &body)
	if body["error"] != "internal error" {
		t.Fatalf("error = %q, want 'internal error'", body["error"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
