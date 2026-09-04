package api

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// ── ProfilesService ─────────────────────────────────────────────────────────

func newProfilesServiceForTest(t *testing.T, mock pgxmock.PgxPoolIface) *ProfilesService {
	t.Helper()
	// Empty RedisURL → per-instance memory cache; no Networks → no siblings.
	return NewProfilesService(db.New(mock), &config.Config{ChainID: 114})
}

func TestProfilesService_HandleGet_TagAndSourceChain(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT display_name, COALESCE\(tag,''\), bio`).
		WithArgs("0xabc").
		WillReturnRows(pgxmock.NewRows([]string{
			"display_name", "tag", "bio", "avatar_uri", "banner_uri", "twitter", "website",
		}).AddRow("Alice", "OG Minter", "hi", "", "", "", ""))

	svc := newProfilesServiceForTest(t, mock)
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/profile/:addr", svc.handleGet)
	})

	resp := doGet(t, app, "/api/v1/profile/0xABC")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got struct {
		DisplayName string `json:"display_name"`
		Tag         string `json:"tag"`
		SourceChain uint64 `json:"source_chain"`
	}
	decodeJSON(t, resp, &got)
	if got.Tag != "OG Minter" || got.DisplayName != "Alice" || got.SourceChain != 114 {
		t.Fatalf("body = %+v; want tag 'OG Minter', display_name 'Alice', source_chain 114", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// The 5s merged cache serves the second hit without touching the DB — the
// single ExpectQuery above would fail ExpectationsWereMet if it ran twice,
// so here we assert a cached repeat works with NO expectations queued.
func TestProfilesService_HandleGet_ServesFromCache(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT display_name, COALESCE\(tag,''\), bio`).
		WithArgs("0xabc").
		WillReturnRows(pgxmock.NewRows([]string{
			"display_name", "tag", "bio", "avatar_uri", "banner_uri", "twitter", "website",
		}).AddRow("Alice", "", "", "", "", "", ""))

	svc := newProfilesServiceForTest(t, mock)
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/profile/:addr", svc.handleGet)
	})

	for i := 0; i < 2; i++ {
		resp := doGet(t, app, "/api/v1/profile/0xabc")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, resp.StatusCode)
		}
		resp.Body.Close()
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("second request should have been served from cache: %v", err)
	}
}

func TestIsValidProfileTag(t *testing.T) {
	for tag, want := range map[string]bool{
		"":          true, // unset
		"OG Minter": true,
		"crew_42-x": true,
		"héros":     true,  // unicode letters allowed
		"a<script>": false, // punctuation rejected
		"tab\tted":  false,
		"emoji ❤":   false,
	} {
		if got := isValidProfileTag(tag); got != want {
			t.Errorf("isValidProfileTag(%q) = %v, want %v", tag, got, want)
		}
	}
}
