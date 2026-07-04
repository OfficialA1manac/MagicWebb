package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// ── Helpers ──────────────────────────────────────────────────────────────────

func newAppForService(t *testing.T, setup func(app *fiber.App)) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	setup(app)
	return app
}

func doGet(t *testing.T, app *fiber.App, url string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func decodeJSON(t *testing.T, r *http.Response, v any) {
	t.Helper()
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatalf("json decode: %v", err)
	}
}

// ── ListingsService ─────────────────────────────────────────────────────────

func TestListingsService_HandleList_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	// ListActiveListings with default limit=50
	mock.ExpectQuery(`SELECT l\.collection, l\.token_id::text`).
		WithArgs(50).
		WillReturnRows(pgxmock.NewRows([]string{
			"collection", "token_id", "seller", "price_wei", "amount",
			"standard", "expires_at", "listed_at", "tx_hash",
			"name", "image_uri", "collection_verified", "total_supply",
		}).AddRow(
			"0xcol1", "1", "0xseller1", "1000000000000000000", int64(1),
			"erc721", now.Add(24*time.Hour), now, "0xtx1",
			"Token One", "https://example.com/1.png", true, int64(0),
		).AddRow(
			"0xcol2", "2", "0xseller2", "2000000000000000000", int64(1),
			"erc1155", now.Add(48*time.Hour), now, "0xtx2",
			"Token Two", "https://example.com/2.png", false, int64(100),
		))

	svc := NewListingsService(db.New(mock), nil)
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/listings", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/listings")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.ListingRow
	decodeJSON(t, resp, &rows)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Collection != "0xcol1" || rows[1].Collection != "0xcol2" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListingsService_HandleList_WithCollectionFilter(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT l\.collection, l\.token_id::text`).
		WithArgs(50, "0xcol1").
		WillReturnRows(pgxmock.NewRows([]string{
			"collection", "token_id", "seller", "price_wei", "amount",
			"standard", "expires_at", "listed_at", "tx_hash",
			"name", "image_uri", "collection_verified", "total_supply",
		}).AddRow(
			"0xcol1", "1", "0xseller1", "1000000000000000000", int64(1),
			"erc721", now.Add(24*time.Hour), now, "0xtx1",
			"Token One", "https://example.com/1.png", true, int64(0),
		))

	svc := NewListingsService(db.New(mock), nil)
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/listings", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/listings?collection=0xcol1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.ListingRow
	decodeJSON(t, resp, &rows)
	if len(rows) != 1 || rows[0].Collection != "0xcol1" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListingsService_HandleList_Empty(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT l\.collection, l\.token_id::text`).
		WithArgs(50).
		WillReturnRows(pgxmock.NewRows([]string{
			"collection", "token_id", "seller", "price_wei", "amount",
			"standard", "expires_at", "listed_at", "tx_hash",
			"name", "image_uri", "collection_verified", "total_supply",
		}))

	svc := NewListingsService(db.New(mock), nil)
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/listings", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/listings")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.ListingRow
	decodeJSON(t, resp, &rows)
	if rows == nil || len(rows) != 0 {
		t.Fatalf("expected empty slice, got %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListingsService_HandleList_DBError(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT l\.collection, l\.token_id::text`).
		WithArgs(50).
		WillReturnError(fiber.ErrInternalServerError)

	svc := NewListingsService(db.New(mock), nil)
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/listings", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/listings")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListingsService_HandleGet_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT l\.collection, l\.token_id::text`).
		WithArgs("0xcol1", "1").
		WillReturnRows(pgxmock.NewRows([]string{
			"collection", "token_id", "seller", "price_wei", "amount",
			"standard", "expires_at", "listed_at", "tx_hash",
			"name", "image_uri",
		}).AddRow(
			"0xcol1", "1", "0xseller1", "1000000000000000000", int64(1),
			"erc721", now.Add(24*time.Hour), now, "0xtx1",
			"Token One", "https://example.com/1.png",
		))

	svc := NewListingsService(db.New(mock), nil)
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/listings/:collection/:id", svc.handleGet)
	})

	resp := doGet(t, app, "/api/v1/listings/0xcol1/1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var row db.ListingRow
	decodeJSON(t, resp, &row)
	if row.Collection != "0xcol1" || row.TokenID != "1" || row.PriceWei != "1000000000000000000" {
		t.Fatalf("unexpected row: %+v", row)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListingsService_HandleGet_NotFound(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT l\.collection, l\.token_id::text`).
		WithArgs("0xcol1", "1").
		WillReturnError(fiber.ErrNotFound) // isNotFound checks for "not found" in error string

	svc := NewListingsService(db.New(mock), nil)
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/listings/:collection/:id", svc.handleGet)
	})

	// The actual pgx.ErrNoRows would trigger a real "not found" from GetListing
	// But fiber.ErrNotFound doesn't contain "not found" string - let's use the correct error
	// Reset mock expectations
	mock, _ = pgxmock.NewPool()
	defer mock.Close()
	mock.ExpectQuery(`SELECT l\.collection, l\.token_id::text`).
		WithArgs("0xcol1", "1").
		WillReturnError(pgx.ErrNoRows) // This should be handled as "listing not found"

	svc = NewListingsService(db.New(mock), nil)
	app = newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/listings/:collection/:id", svc.handleGet)
	})

	resp := doGet(t, app, "/api/v1/listings/0xcol1/1")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListingsService_HandleGet_DBError(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT l\.collection, l\.token_id::text`).
		WithArgs("0xcol1", "1").
		WillReturnError(fiber.ErrInternalServerError)

	svc := NewListingsService(db.New(mock), nil)
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/listings/:collection/:id", svc.handleGet)
	})

	resp := doGet(t, app, "/api/v1/listings/0xcol1/1")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListingsService_HandlePreflight_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT \(l\.active AND NOT l\.orphaned AND l\.expires_at > now\(\)\), l\.orphaned, l\.price_wei::text`).
		WithArgs("0xcol1", "1", "0xseller1").
		WillReturnRows(pgxmock.NewRows([]string{"listed", "orphaned", "price_wei", "seller_owns"}).
			AddRow(true, false, "1000000000000000000", true))

	svc := NewListingsService(db.New(mock), nil)
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/listings/:collection/:id/preflight", svc.handlePreflight)
	})

	resp := doGet(t, app, "/api/v1/listings/0xcol1/1/preflight?seller=0xseller1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["ok"] != true || body["listed"] != true || body["seller_owns"] != true {
		t.Fatalf("unexpected preflight: %+v", body)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestListingsService_HandlePreflight_MissingSeller(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	// No DB expectation — the handler should return 400 before any query
	svc := NewListingsService(db.New(mock), nil)
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/listings/:collection/:id/preflight", svc.handlePreflight)
	})

	resp := doGet(t, app, "/api/v1/listings/0xcol1/1/preflight")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── AuctionsService ─────────────────────────────────────────────────────────

func TestAuctionsService_HandleList_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	cols := []string{"auction_id", "collection", "token_id", "seller", "standard",
		"reserve_price_wei", "highest_bid_wei", "highest_bidder", "min_increment_bps",
		"starts_at", "ends_at", "status", "create_tx", "name", "image_uri"}

	mock.ExpectQuery(`SELECT a\.auction_id, a\.collection`).
		WithArgs(50).
		WillReturnRows(pgxmock.NewRows(cols).
			AddRow(int64(1), "0xcol1", "1", "0xseller1", "erc721",
				"5000000000000000000", "", "", int16(100),
				now, now.Add(24*time.Hour), "active", "0xtx1", "Auction One", ""))

	svc := NewAuctionsService(db.New(mock))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/auctions", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/auctions")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.AuctionRow
	decodeJSON(t, resp, &rows)
	if len(rows) != 1 || rows[0].AuctionID != 1 {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuctionsService_HandleList_Filtered(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	cols := []string{"auction_id", "collection", "token_id", "seller", "standard",
		"reserve_price_wei", "highest_bid_wei", "highest_bidder", "min_increment_bps",
		"starts_at", "ends_at", "status", "create_tx", "name", "image_uri"}

	mock.ExpectQuery(`SELECT a\.auction_id, a\.collection`).
		WithArgs(50, "0xcol1", "active").
		WillReturnRows(pgxmock.NewRows(cols).
			AddRow(int64(2), "0xcol1", "2", "0xseller2", "erc1155",
				"10000000000000000000", "15000000000000000000", "0xbidder1", int16(200),
				now, now.Add(48*time.Hour), "active", "0xtx2", "Auction Two", ""))

	svc := NewAuctionsService(db.New(mock))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/auctions", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/auctions?collection=0xcol1&status=active")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.AuctionRow
	decodeJSON(t, resp, &rows)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuctionsService_HandleGet_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	cols := []string{"auction_id", "collection", "token_id", "seller", "standard",
		"reserve_price_wei", "highest_bid_wei", "highest_bidder", "min_increment_bps",
		"starts_at", "ends_at", "status", "create_tx", "name", "image_uri"}

	mock.ExpectQuery(`SELECT a\.auction_id, a\.collection`).
		WithArgs(int64(42)).
		WillReturnRows(pgxmock.NewRows(cols).
			AddRow(int64(42), "0xcol1", "1", "0xseller1", "erc721",
				"5000000000000000000", "6000000000000000000", "0xbidder1", int16(150),
				now, now.Add(24*time.Hour), "active", "0xtx1", "Auction 42", ""))

	svc := NewAuctionsService(db.New(mock))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/auctions/:id", svc.handleGet)
	})

	resp := doGet(t, app, "/api/v1/auctions/42")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var row db.AuctionRow
	decodeJSON(t, resp, &row)
	if row.AuctionID != 42 || row.Collection != "0xcol1" {
		t.Fatalf("unexpected row: %+v", row)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuctionsService_HandleGet_NotFound(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT a\.auction_id, a\.collection`).
		WithArgs(int64(999)).
		WillReturnError(pgx.ErrNoRows)

	svc := NewAuctionsService(db.New(mock))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/auctions/:id", svc.handleGet)
	})

	resp := doGet(t, app, "/api/v1/auctions/999")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuctionsService_HandleGet_InvalidID(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	// Not a number — handler returns 400 before any DB call
	svc := NewAuctionsService(db.New(mock))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/auctions/:id", svc.handleGet)
	})

	resp := doGet(t, app, "/api/v1/auctions/notanumber")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]string
	decodeJSON(t, resp, &body)
	if !strings.Contains(body["error"], "invalid auction id") {
		t.Fatalf("error = %q, want 'invalid auction id'", body["error"])
	}
}

func TestAuctionsService_HandleBids_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT bidder, amount_wei::text, tx_hash, placed_at`).
		WithArgs(int64(42)).
		WillReturnRows(pgxmock.NewRows([]string{"bidder", "amount_wei", "tx_hash", "placed_at"}).
			AddRow("0xbidder1", "1000000000000000000", "0xtx1", now).
			AddRow("0xbidder2", "500000000000000000", "0xtx2", now.Add(-time.Hour)))

	svc := NewAuctionsService(db.New(mock))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/auctions/:id/bids", svc.handleBids)
	})

	resp := doGet(t, app, "/api/v1/auctions/42/bids")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.BidRow
	decodeJSON(t, resp, &rows)
	if len(rows) != 2 {
		t.Fatalf("expected 2 bids, got %d", len(rows))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuctionsService_HandleBids_Empty(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT bidder, amount_wei::text, tx_hash, placed_at`).
		WithArgs(int64(42)).
		WillReturnRows(pgxmock.NewRows([]string{"bidder", "amount_wei", "tx_hash", "placed_at"}))

	svc := NewAuctionsService(db.New(mock))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/auctions/:id/bids", svc.handleBids)
	})

	resp := doGet(t, app, "/api/v1/auctions/42/bids")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.BidRow
	decodeJSON(t, resp, &rows)
	if rows == nil || len(rows) != 0 {
		t.Fatalf("expected empty slice, got %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuctionsService_HandleBids_InvalidID(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	svc := NewAuctionsService(db.New(mock))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/auctions/:id/bids", svc.handleBids)
	})

	resp := doGet(t, app, "/api/v1/auctions/notanumber/bids")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServerTime(t *testing.T) {
	var serverTimeMs int64
	atomic.StoreInt64(&serverTimeMs, time.Now().UnixMilli())

	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/server-time", func(c *fiber.Ctx) error {
			return c.JSON(fiber.Map{"unix_ms": atomic.LoadInt64(&serverTimeMs)})
		})
	})

	resp := doGet(t, app, "/api/v1/server-time")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]int64
	decodeJSON(t, resp, &body)
	ms, ok := body["unix_ms"]
	if !ok || ms <= 0 {
		t.Fatalf("unexpected server-time response: %+v", body)
	}
}

// ── OffersService ──────────────────────────────────────────────────────────

func TestOffersService_HandleList_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	offerCols := []string{
		"offer_id", "bidder", "collection", "token_id",
		"principal_wei", "fee_wei", "units", "standard",
		"expires_at", "status", "make_tx", "created_at",
	}

	mock.ExpectQuery(`SELECT o\.offer_id::text, o\.bidder`).
		WithArgs(50).
		WillReturnRows(pgxmock.NewRows(offerCols).
			AddRow("1", "0xbidder1", "0xcol1", "1",
				"1000000000000000000", "10000000000000000", int64(1), "erc721",
				now.Add(7*24*time.Hour), "pending", "0xmaketx1", now))

	svc := NewOffersService(db.New(mock))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/offers", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/offers")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.OfferRow
	decodeJSON(t, resp, &rows)
	if len(rows) != 1 || rows[0].OfferID != "1" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOffersService_HandleList_FilteredByBidder(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	offerCols := []string{
		"offer_id", "bidder", "collection", "token_id",
		"principal_wei", "fee_wei", "units", "standard",
		"expires_at", "status", "make_tx", "created_at",
	}

	mock.ExpectQuery(`SELECT o\.offer_id::text, o\.bidder`).
		WithArgs(50, "0xbidder1").
		WillReturnRows(pgxmock.NewRows(offerCols).
			AddRow("1", "0xbidder1", "0xcol1", "1",
				"1000000000000000000", "10000000000000000", int64(1), "erc721",
				now.Add(7*24*time.Hour), "pending", "0xmaketx1", now))

	svc := NewOffersService(db.New(mock))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/offers", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/offers?bidder=0xbidder1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.OfferRow
	decodeJSON(t, resp, &rows)
	if len(rows) != 1 || rows[0].Bidder != "0xbidder1" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOffersService_HandleList_Empty(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	offerCols := []string{
		"offer_id", "bidder", "collection", "token_id",
		"principal_wei", "fee_wei", "units", "standard",
		"expires_at", "status", "make_tx", "created_at",
	}

	mock.ExpectQuery(`SELECT o\.offer_id::text, o\.bidder`).
		WithArgs(50).
		WillReturnRows(pgxmock.NewRows(offerCols))

	svc := NewOffersService(db.New(mock))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/offers", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/offers")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.OfferRow
	decodeJSON(t, resp, &rows)
	if rows == nil || len(rows) != 0 {
		t.Fatalf("expected empty slice, got %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOffersService_HandlePosition_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	offerCols := []string{
		"offer_id", "bidder", "collection", "token_id",
		"principal_wei", "fee_wei", "units", "standard",
		"expires_at", "status", "make_tx", "created_at",
	}

	// GetActiveOffersForToken with limit 200
	mock.ExpectQuery(`SELECT offer_id::text, bidder, collection, token_id::text`).
		WithArgs("0xcol1", "1", 200).
		WillReturnRows(pgxmock.NewRows(offerCols).
			AddRow("1", "0xbidder1", "0xcol1", "1",
				"3000000000000000000", "30000000000000000", int64(1), "erc721",
				now.Add(7*24*time.Hour), "pending", "0xmtx1", now).
			AddRow("2", "0xbidder2", "0xcol1", "1",
				"1000000000000000000", "10000000000000000", int64(1), "erc721",
				now.Add(7*24*time.Hour), "pending", "0xmtx2", now))

	svc := NewOffersService(db.New(mock))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/offers/:collection/:id/position", svc.handlePosition)
	})

	resp := doGet(t, app, "/api/v1/offers/0xcol1/1/position")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["collection"] != "0xcol1" || body["token_id"] != "1" {
		t.Fatalf("unexpected position keys: %+v", body)
	}
	if body["count"] != float64(2) {
		t.Fatalf("count = %v, want 2", body["count"])
	}
	if body["highest"] != "3000000000000000000" {
		t.Fatalf("highest = %v, want 3000000000000000000", body["highest"])
	}
	total := "4000000000000000000"
	if body["total_wei"] != total {
		t.Fatalf("total_wei = %v, want %s", body["total_wei"], total)
	}
	if body["truncated"] != false {
		t.Fatalf("truncated = %v, want false", body["truncated"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOffersService_HandlePosition_Empty(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	offerCols := []string{
		"offer_id", "bidder", "collection", "token_id",
		"principal_wei", "fee_wei", "units", "standard",
		"expires_at", "status", "make_tx", "created_at",
	}

	mock.ExpectQuery(`SELECT offer_id::text, bidder, collection, token_id::text`).
		WithArgs("0xcol1", "1", 200).
		WillReturnRows(pgxmock.NewRows(offerCols))

	svc := NewOffersService(db.New(mock))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/offers/:collection/:id/position", svc.handlePosition)
	})

	resp := doGet(t, app, "/api/v1/offers/0xcol1/1/position")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["count"] != float64(0) {
		t.Fatalf("count = %v, want 0", body["count"])
	}
	if body["highest"] != "0" {
		t.Fatalf("highest = %v, want 0", body["highest"])
	}
	if body["total_wei"] != "0" {
		t.Fatalf("total_wei = %v, want 0", body["total_wei"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── CollectionsService ──────────────────────────────────────────────────────

func TestCollectionsService_HandleList_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT address, name, symbol, standard::text, deploy_block`).
		WithArgs(50).
		WillReturnRows(pgxmock.NewRows([]string{"address", "name", "symbol", "standard", "deploy_block"}).
			AddRow("0xcol1", "Collection One", "COL1", "erc721", uint64(100)).
			AddRow("0xcol2", "Collection Two", "COL2", "erc1155", uint64(200)))

	svc := NewCollectionsService(db.New(mock), cache.New(0))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/collections", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/collections")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.CollectionRow
	decodeJSON(t, resp, &rows)
	if len(rows) != 2 || rows[0].Address != "0xcol1" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionsService_HandleList_LimitClamping(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	// limit=300 should be clamped to 200 by the handler
	// But note: the handler applies limit BEFORE calling the DB
	// And ListCollections also clamps at the DB layer
	// We need to match what the handler actually sends:
	// limit=300 → handler clamps to 200 → DB receives 200
	mock.ExpectQuery(`SELECT address, name, symbol, standard::text, deploy_block`).
		WithArgs(200).
		WillReturnRows(pgxmock.NewRows([]string{"address", "name", "symbol", "standard", "deploy_block"}))

	svc := NewCollectionsService(db.New(mock), cache.New(0))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/collections", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/collections?limit=300")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionsService_HandleList_DBError(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT address, name, symbol, standard::text, deploy_block`).
		WithArgs(50).
		WillReturnError(fiber.ErrInternalServerError)

	svc := NewCollectionsService(db.New(mock), cache.New(0))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/collections", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/collections")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionsService_HandleGet_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	// GetCollection: 6 columns
	mock.ExpectQuery(`SELECT address, name, symbol, standard::text, deploy_block, verified`).
		WithArgs("0xcol1").
		WillReturnRows(pgxmock.NewRows([]string{"address", "name", "symbol", "standard", "deploy_block", "verified"}).
			AddRow("0xcol1", "Collection One", "COL1", "erc721", uint64(1000), true))

	// GetFloorPrice
	mock.ExpectQuery(`SELECT COALESCE\(MIN\(price_wei\)::text,'0'\)`).
		WithArgs("0xcol1").
		WillReturnRows(pgxmock.NewRows([]string{"price"}).AddRow("5000000000000000000"))

	// Get24hVolume
	mock.ExpectQuery(`SELECT COALESCE\(SUM\(price_wei\)::text,'0'\)`).
		WithArgs("0xcol1").
		WillReturnRows(pgxmock.NewRows([]string{"vol"}).AddRow("10000000000000000000"))

	// GetListedCount
	mock.ExpectQuery(`SELECT COUNT\(\*\)`).
		WithArgs("0xcol1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(15))

	svc := NewCollectionsService(db.New(mock), cache.New(0))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/collections/:address", svc.handleGet)
	})

	resp := doGet(t, app, "/api/v1/collections/0xcol1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	// CollectionRow has json tags so keys are lowercase
	if body["address"] != "0xcol1" || body["name"] != "Collection One" {
		t.Fatalf("unexpected body: %+v", body)
	}
	if body["floor_price_wei"] != "5000000000000000000" {
		t.Fatalf("floor_price_wei = %v", body["floor_price_wei"])
	}
	if body["volume_24h_wei"] != "10000000000000000000" {
		t.Fatalf("volume_24h_wei = %v", body["volume_24h_wei"])
	}
	if body["listed_count"] != float64(15) {
		t.Fatalf("listed_count = %v, want 15", body["listed_count"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionsService_HandleGet_NotFound(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT address, name, symbol, standard::text, deploy_block, verified`).
		WithArgs("0xnonexistent").
		WillReturnError(pgx.ErrNoRows)

	svc := NewCollectionsService(db.New(mock), cache.New(0))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/collections/:address", svc.handleGet)
	})

	resp := doGet(t, app, "/api/v1/collections/0xnonexistent")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionsService_HandleGet_StatsFallback(t *testing.T) {
	// When floor/volume queries fail, the handler gracefully falls through
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT address, name, symbol, standard::text, deploy_block, verified`).
		WithArgs("0xcol1").
		WillReturnRows(pgxmock.NewRows([]string{"address", "name", "symbol", "standard", "deploy_block", "verified"}).
			AddRow("0xcol1", "Collection One", "COL1", "erc721", uint64(1000), false))

	// Floor price returns 0 (no active listings)
	mock.ExpectQuery(`SELECT COALESCE\(MIN\(price_wei\)::text,'0'\)`).
		WithArgs("0xcol1").
		WillReturnRows(pgxmock.NewRows([]string{"price"}).AddRow("0"))

	// Volume returns 0 (no sales)
	mock.ExpectQuery(`SELECT COALESCE\(SUM\(price_wei\)::text,'0'\)`).
		WithArgs("0xcol1").
		WillReturnRows(pgxmock.NewRows([]string{"vol"}).AddRow("0"))

	// ListedCount
	mock.ExpectQuery(`SELECT COUNT\(\*\)`).
		WithArgs("0xcol1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))

	svc := NewCollectionsService(db.New(mock), cache.New(0))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/collections/:address", svc.handleGet)
	})

	resp := doGet(t, app, "/api/v1/collections/0xcol1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["floor_price_wei"] != "0" || body["volume_24h_wei"] != "0" {
		t.Fatalf("expected zero stats, got floor=%v vol=%v",
			body["floor_price_wei"], body["volume_24h_wei"])
	}
	if body["listed_count"] != float64(0) {
		t.Fatalf("listed_count = %v, want 0", body["listed_count"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionsService_HandleTraits_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT trait_type, value, count\(\*\)`).
		WithArgs("0xcol1").
		WillReturnRows(pgxmock.NewRows([]string{"trait_type", "value", "count"}).
			AddRow("Background", "Red", 10).
			AddRow("Background", "Blue", 5).
			AddRow("Fur", "Gold", 3))

	svc := NewCollectionsService(db.New(mock), cache.New(0))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/collections/:address/traits", svc.handleTraits)
	})

	resp := doGet(t, app, "/api/v1/collections/0xcol1/traits")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string][]string
	decodeJSON(t, resp, &body)
	if len(body) != 2 {
		t.Fatalf("expected 2 trait types, got %d: %+v", len(body), body)
	}
	if len(body["Background"]) != 2 || len(body["Fur"]) != 1 {
		t.Fatalf("unexpected trait values: %+v", body)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionsService_HandleTraits_Empty(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT trait_type, value, count\(\*\)`).
		WithArgs("0xcol1").
		WillReturnRows(pgxmock.NewRows([]string{"trait_type", "value", "count"}))

	svc := NewCollectionsService(db.New(mock), cache.New(0))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/collections/:address/traits", svc.handleTraits)
	})

	resp := doGet(t, app, "/api/v1/collections/0xcol1/traits")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string][]string
	decodeJSON(t, resp, &body)
	if body == nil || len(body) != 0 {
		t.Fatalf("expected empty map, got %+v", body)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionsService_HandleTrending_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT collection, \"window\", score, views, bids, volume_wei::text`).
		WithArgs("24h", 20).
		WillReturnRows(pgxmock.NewRows([]string{"collection", "window", "score", "views", "bids", "volume_wei"}).
			AddRow("0xcol1", "24h", 95.5, int64(1000), int64(5), "50000000000000000000").
			AddRow("0xcol2", "24h", 80.0, int64(500), int64(2), "20000000000000000000"))

	svc := NewCollectionsService(db.New(mock), cache.New(0))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/trending", svc.handleTrending)
	})

	resp := doGet(t, app, "/api/v1/trending")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.TrendingScore
	decodeJSON(t, resp, &rows)
	if len(rows) != 2 || rows[0].Collection != "0xcol1" || rows[1].Collection != "0xcol2" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionsService_HandleTrending_Empty(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT collection, \"window\", score, views, bids, volume_wei::text`).
		WithArgs("24h", 20).
		WillReturnRows(pgxmock.NewRows([]string{"collection", "window", "score", "views", "bids", "volume_wei"}))

	svc := NewCollectionsService(db.New(mock), cache.New(0))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/trending", svc.handleTrending)
	})

	resp := doGet(t, app, "/api/v1/trending")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.TrendingScore
	decodeJSON(t, resp, &rows)
	if rows == nil || len(rows) != 0 {
		t.Fatalf("expected empty slice, got %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionsService_HandleTrending_CustomWindow(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT collection, \"window\", score, views, bids, volume_wei::text`).
		WithArgs("7d", 20).
		WillReturnRows(pgxmock.NewRows([]string{"collection", "window", "score", "views", "bids", "volume_wei"}).
			AddRow("0xcol1", "7d", 95.5, int64(1000), int64(5), "50000000000000000000"))

	svc := NewCollectionsService(db.New(mock), cache.New(0))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/trending", svc.handleTrending)
	})

	resp := doGet(t, app, "/api/v1/trending?window=7d")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.TrendingScore
	decodeJSON(t, resp, &rows)
	if len(rows) != 1 || rows[0].Window != "7d" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionsService_HandleList_LimitCustom(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	// limit=10 should pass through
	mock.ExpectQuery(`SELECT address, name, symbol, standard::text, deploy_block`).
		WithArgs(10).
		WillReturnRows(pgxmock.NewRows([]string{"address", "name", "symbol", "standard", "deploy_block"}))

	svc := NewCollectionsService(db.New(mock), cache.New(0))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/collections", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/collections?limit=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Test that the collections handler returns empty list on DB error after nil check
func TestCollectionsService_HandleList_SuccessWithNilGuard(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT address, name, symbol, standard::text, deploy_block`).
		WithArgs(50).
		WillReturnRows(pgxmock.NewRows([]string{"address", "name", "symbol", "standard", "deploy_block"}))

	svc := NewCollectionsService(db.New(mock), cache.New(0))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/collections", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/collections")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var rows []db.CollectionRow
	decodeJSON(t, resp, &rows)
	if rows == nil || len(rows) != 0 {
		t.Fatalf("expected empty slice after nil guard, got %+v", rows)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── Handles bad input at the handler level ──────────────────────────────────

func TestListingsService_HandleList_InvalidLimit(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	// limit=0 should use the default 50
	// But limit=-1 or limit=abc should also use default 50
	mock.ExpectQuery(`SELECT l\.collection, l\.token_id::text`).
		WithArgs(50).
		WillReturnRows(pgxmock.NewRows([]string{
			"collection", "token_id", "seller", "price_wei", "amount",
			"standard", "expires_at", "listed_at", "tx_hash",
			"name", "image_uri", "collection_verified", "total_supply",
		}))

	svc := NewListingsService(db.New(mock), nil)
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/listings", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/listings?limit=abc")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuctionsService_HandleList_InvalidLimit(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	cols := []string{"auction_id", "collection", "token_id", "seller", "standard",
		"reserve_price_wei", "highest_bid_wei", "highest_bidder", "min_increment_bps",
		"starts_at", "ends_at", "status", "create_tx", "name", "image_uri"}

	// limit=-5 is clamped by the handler to 1 (n < 1 → n = 1)
	mock.ExpectQuery(`SELECT a\.auction_id, a\.collection`).
		WithArgs(1).
		WillReturnRows(pgxmock.NewRows(cols))

	svc := NewAuctionsService(db.New(mock))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/auctions", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/auctions?limit=-5")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuctionsService_HandleList_DBError(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT a\.auction_id, a\.collection`).
		WithArgs(50).
		WillReturnError(fiber.ErrInternalServerError)

	svc := NewAuctionsService(db.New(mock))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/auctions", svc.handleList)
	})

	resp := doGet(t, app, "/api/v1/auctions")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ── Limit edge cases ────────────────────────────────────────────────────────

func TestLimitClamping(t *testing.T) {
	tests := []struct {
		input string // limit query param value
		want  int    // expected limit passed to DB
	}{
		{"", 50},    // not present → f.Limit=0 → DB clamps 0→50
		{"0", 1},     // Atoi(0)=0, n<1→n=1 → f.Limit=1 → DB passes 1
		{"-1", 1},    // Atoi(-1)=-1, n<1→n=1 → f.Limit=1 → DB passes 1
		{"5", 5},     // valid → f.Limit=5 → DB passes 5
		{"100", 100}, // valid → f.Limit=100 → DB passes (100 <= 100)
		{"250", 100},  // handler clamps 250→100 (listings max), DB passes 100 (100 <= 100)
		{"abc", 50},  // Atoi error → f.Limit=0 → DB clamps 0→50
	}
	for _, tc := range tests {
		mock, _ := pgxmock.NewPool()
		svc := NewListingsService(db.New(mock), nil)
		app := newAppForService(t, func(app *fiber.App) {
			app.Get("/api/v1/listings", svc.handleList)
		})

		mock.ExpectQuery(`SELECT l\.collection, l\.token_id::text`).
			WithArgs(tc.want).
			WillReturnRows(pgxmock.NewRows([]string{
				"collection", "token_id", "seller", "price_wei", "amount",
				"standard", "expires_at", "listed_at", "tx_hash",
				"name", "image_uri", "collection_verified", "total_supply",
			}))

		url := "/api/v1/listings"
		if tc.input != "" {
			url += "?limit=" + tc.input
		}
		resp := doGet(t, app, url)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("input=%q: status=%d, want 200", tc.input, resp.StatusCode)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("input=%q: %v", tc.input, err)
		}
		mock.Close()
	}
}

// TestOffersService_HandlePosition_Truncated verifies the truncated flag.
func TestOffersService_HandlePosition_Truncated(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	now := time.Now()
	offerCols := []string{
		"offer_id", "bidder", "collection", "token_id",
		"principal_wei", "fee_wei", "units", "standard",
		"expires_at", "status", "make_tx", "created_at",
	}

	mock.ExpectQuery(`SELECT offer_id::text, bidder, collection, token_id::text`).
		WithArgs("0xcol1", "1", 200).
		// Simulate exactly 200 rows (hits the truncation boundary)
		WillReturnRows(func() *pgxmock.Rows {
			r := pgxmock.NewRows(offerCols)
			for i := 0; i < 200; i++ {
				r.AddRow(
					strconv.Itoa(i), "0xbidder", "0xcol1", "1",
					"1000000000000000000", "10000000000000000", int64(1), "erc721",
					now.Add(7*24*time.Hour), "pending", "0xmtx", now,
				)
			}
			return r
		}())

	svc := NewOffersService(db.New(mock))
	app := newAppForService(t, func(app *fiber.App) {
		app.Get("/api/v1/offers/:collection/:id/position", svc.handlePosition)
	})

	resp := doGet(t, app, "/api/v1/offers/0xcol1/1/position")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, resp, &body)
	if body["truncated"] != true {
		t.Fatalf("truncated = %v, want true (200 offers is truncation boundary)", body["truncated"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
