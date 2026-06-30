package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// Valid 42-char hex addresses used as test fixtures.
const (
	testAddr1     = "0x0000000000000000000000000000000000000001"
	testAddr2     = "0x0000000000000000000000000000000000000002"
	testSeller    = "0x00000000000000000000000000000000000000bb"
	testBuyer     = "0x00000000000000000000000000000000000000cc"
	testBidder    = "0x00000000000000000000000000000000000000dd"
	testCollection = "0x000000000000000000000000000000000000000a"
	testTokenID   = "42"
)

// activityCols matches the ActivityRow scan columns from GetRecentTransactions.
var activityCols = []string{"type", "collection", "token_id", "amount_wei", "at", "tx_hash"}

// newActivityApp creates a Fiber app with the activity endpoint registered
// (including the ValidateQuery middleware) backed by a pgxmock pool.
func newActivityApp(t *testing.T, mock pgxmock.PgxPoolIface) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	svc := NewMetricsService(db.New(mock))
	app.Get("/api/v1/activity", ValidateQuery(QuerySchema{
		{Name: "limit", Type: ParamInt},
		{Name: "address", Type: ParamAddress},
		{Name: "collection", Type: ParamAddress},
		{Name: "token_id"},
	}), svc.handleRecentActivity)
	return app
}

// ── Global activity (no params) ──────────────────────────────────────────────

func TestActivity_Global_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT type, collection, token_id::text, amount_wei::text, at, tx_hash`).
		WithArgs(50).
		WillReturnRows(pgxmock.NewRows(activityCols).
			AddRow("Listed", testAddr1, "1", "1000000000000000000", now, "0xtx1").
			AddRow("Sold", testAddr2, "2", "2000000000000000000", now.Add(-time.Hour), "0xtx2"))

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.ActivityRow
	decodeJSON(t, resp, &rows)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Type != "Listed" || rows[1].Type != "Sold" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestActivity_Global_Empty(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT type, collection, token_id::text, amount_wei::text, at, tx_hash`).
		WithArgs(50).
		WillReturnRows(pgxmock.NewRows(activityCols))

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.ActivityRow
	decodeJSON(t, resp, &rows)
	if rows == nil || len(rows) != 0 {
		t.Fatalf("expected empty slice, got %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestActivity_Global_DBError(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT type, collection, token_id::text, amount_wei::text, at, tx_hash`).
		WithArgs(50).
		WillReturnError(fiber.ErrInternalServerError)

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── Address-only activity ────────────────────────────────────────────────────

func TestActivity_ByAddress_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT type, collection, token_id::text, amount_wei::text, at, tx_hash`).
		WithArgs(testAddr1, 50).
		WillReturnRows(pgxmock.NewRows(activityCols).
			AddRow("Listed", testAddr1, "1", "1000000000000000000", now, "0xtx1"))

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity?address="+testAddr1)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.ActivityRow
	decodeJSON(t, resp, &rows)
	if len(rows) != 1 || rows[0].Type != "Listed" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestActivity_ByAddress_Empty(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT type, collection, token_id::text, amount_wei::text, at, tx_hash`).
		WithArgs(testAddr1, 50).
		WillReturnRows(pgxmock.NewRows(activityCols))

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity?address="+testAddr1)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.ActivityRow
	decodeJSON(t, resp, &rows)
	if rows == nil || len(rows) != 0 {
		t.Fatalf("expected empty slice, got %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── Collection + token_id activity ──────────────────────────────────────────

// tokenActivityCols matches the TokenActivityRow scan columns.
var tokenActivityCols = []string{"type", "amount_wei", "from_addr", "to_addr", "ts", "tx_hash"}

func TestActivity_ByToken_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT 'Sold' AS type, price_wei::text, seller, buyer, occurred_at AS ts, tx_hash`).
		WithArgs(testCollection, testTokenID).
		WillReturnRows(pgxmock.NewRows(tokenActivityCols).
			AddRow("Sold", "5000000000000000000", testSeller, testBuyer, now, "0xtx1"))

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity?collection="+testCollection+"&token_id="+testTokenID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.ActivityRow
	decodeJSON(t, resp, &rows)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Type != "Sold" || rows[0].Collection != testCollection || rows[0].TokenID != testTokenID {
		t.Fatalf("unexpected row: %+v", rows[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestActivity_ByToken_Empty(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT 'Sold' AS type, price_wei::text, seller, buyer, occurred_at AS ts, tx_hash`).
		WithArgs(testCollection, testTokenID).
		WillReturnRows(pgxmock.NewRows(tokenActivityCols))

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity?collection="+testCollection+"&token_id="+testTokenID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.ActivityRow
	decodeJSON(t, resp, &rows)
	if rows == nil || len(rows) != 0 {
		t.Fatalf("expected empty slice, got %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestActivity_ByToken_DBError(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT 'Sold' AS type, price_wei::text, seller, buyer, occurred_at AS ts, tx_hash`).
		WithArgs(testCollection, testTokenID).
		WillReturnError(fiber.ErrInternalServerError)

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity?collection="+testCollection+"&token_id="+testTokenID)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── All three ANDed (collection + token_id + address) ───────────────────────

func TestActivity_ByTokenAndAddress_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT 'Sold' AS type, price_wei::text, seller, buyer, occurred_at AS ts, tx_hash`).
		WithArgs(testCollection, testTokenID, testAddr1).
		WillReturnRows(pgxmock.NewRows(tokenActivityCols).
			AddRow("Sold", "5000000000000000000", testAddr1, testBuyer, now, "0xtx1"))

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity?collection="+testCollection+"&token_id="+testTokenID+"&address="+testAddr1)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.ActivityRow
	decodeJSON(t, resp, &rows)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Type != "Sold" || rows[0].Collection != testCollection || rows[0].TokenID != testTokenID {
		t.Fatalf("unexpected row: %+v", rows[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestActivity_ByTokenAndAddress_Empty(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT 'Sold' AS type, price_wei::text, seller, buyer, occurred_at AS ts, tx_hash`).
		WithArgs(testCollection, testTokenID, testAddr1).
		WillReturnRows(pgxmock.NewRows(tokenActivityCols))

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity?collection="+testCollection+"&token_id="+testTokenID+"&address="+testAddr1)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.ActivityRow
	decodeJSON(t, resp, &rows)
	if rows == nil || len(rows) != 0 {
		t.Fatalf("expected empty slice, got %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestActivity_ByAddress_DBError(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT type, collection, token_id::text, amount_wei::text, at, tx_hash`).
		WithArgs(testAddr1, 50).
		WillReturnError(fiber.ErrInternalServerError)

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity?address="+testAddr1)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestActivity_ByAddress_WithCustomLimit(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT type, collection, token_id::text, amount_wei::text, at, tx_hash`).
		WithArgs(testAddr1, 5).
		WillReturnRows(pgxmock.NewRows(activityCols).
			AddRow("Sold", testAddr1, "1", "1000000000000000000", now, "0xtx1"))

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity?address="+testAddr1+"&limit=5")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.ActivityRow
	decodeJSON(t, resp, &rows)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── Collection + token_id activity (multi-event) ───────────────────────────

func TestActivity_ByToken_MultiEventTypes(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	// The UNION query returns multiple event types for one token.
	mock.ExpectQuery(`SELECT 'Sold' AS type, price_wei::text, seller, buyer, occurred_at AS ts, tx_hash`).
		WithArgs(testCollection, testTokenID).
		WillReturnRows(pgxmock.NewRows(tokenActivityCols).
			AddRow("Sold", "5000000000000000000", testSeller, testBuyer, now, "0xtx1").
			AddRow("BidPlaced", "3000000000000000000", testBidder, "", now.Add(-time.Minute), "0xtx2").
			AddRow("OfferMade", "1000000000000000000", testBidder, "", now.Add(-2*time.Minute), "0xtx3"))

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity?collection="+testCollection+"&token_id="+testTokenID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.ActivityRow
	decodeJSON(t, resp, &rows)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0].Type != "Sold" || rows[1].Type != "BidPlaced" || rows[2].Type != "OfferMade" {
		t.Fatalf("unexpected event types: %+v", rows)
	}
	// Verify collection and token_id are filled in from query params
	for _, r := range rows {
		if r.Collection != testCollection {
			t.Fatalf("expected collection %s, got %s", testCollection, r.Collection)
		}
		if r.TokenID != testTokenID {
			t.Fatalf("expected token_id %s, got %s", testTokenID, r.TokenID)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── All three ANDed — DB error ─────────────────────────────────────────────

func TestActivity_ByTokenAndAddress_DBError(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT 'Sold' AS type, price_wei::text, seller, buyer, occurred_at AS ts, tx_hash`).
		WithArgs(testCollection, testTokenID, testAddr1).
		WillReturnError(fiber.ErrInternalServerError)

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity?collection="+testCollection+"&token_id="+testTokenID+"&address="+testAddr1)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── Validation middleware (unknown param rejection) ─────────────────────────

func TestActivity_RejectsUnknownParam(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	// No DB expectations — the middleware should reject before any query.
	app := newActivityApp(t, mock)

	// ?uri= is NOT in the schema — should be rejected as unknown.
	resp := doGet(t, app, "/api/v1/activity?uri=https://example.com/img.png")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown param", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestActivity_RejectsInvalidAddress(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity?address=notanaddress")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for invalid address", resp.StatusCode)
	}
}

func TestActivity_RejectsInvalidLimit(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity?limit=abc")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for invalid limit", resp.StatusCode)
	}
}

// ── Limit parameter ─────────────────────────────────────────────────────────

func TestActivity_WithCustomLimit(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT type, collection, token_id::text, amount_wei::text, at, tx_hash`).
		WithArgs(10).
		WillReturnRows(pgxmock.NewRows(activityCols).
			AddRow("Listed", testAddr1, "1", "1000000000000000000", now, "0xtx1"))

	app := newActivityApp(t, mock)
	resp := doGet(t, app, "/api/v1/activity?limit=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
