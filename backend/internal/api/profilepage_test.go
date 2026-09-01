package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

const ppOwner = "0x00000000000000000000000000000000000000bb"

func newProfilePageApp(t *testing.T, mock pgxmock.PgxPoolIface) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	q := db.New(mock)
	metrics := NewMetricsService(q, cache.NewRedisOrMemory("", time.Second), nil)
	NewProfilePageService(q, metrics).RegisterRoutes(app.Group("/api/v1"))
	return app
}

func TestProfilePage_InvalidAddress(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	app := newProfilePageApp(t, mock)

	resp := doGet(t, app, "/api/v1/profile-page/not-an-address")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// NOTE: the happy path (all sections populated / [] shape) is intentionally
// not unit-tested through this handler: it fans the six queries out via
// errgroup, and pgxmock is not safe under concurrent Query calls (it reports
// "call to method Query() was not expected" nondeterministically). Each
// underlying query is covered by the listings/auctions/offers/activity/
// collections service tests, and the composite's assembly is exercised
// end-to-end against a real database in the profile-page e2e.
