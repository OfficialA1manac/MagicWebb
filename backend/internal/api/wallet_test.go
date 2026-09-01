package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

const testOwner = "0x00000000000000000000000000000000000000aa"

// walletNFTCols matches the OwnedNFT scan columns from WalletNFTs.
var walletNFTCols = []string{"collection", "token_id", "units", "standard", "name", "image_uri"}

// newWalletApp creates a Fiber app with the wallet NFTs endpoint registered.
func newWalletApp(t *testing.T, mock pgxmock.PgxPoolIface) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	// Empty explorerURL disables the Blockscout merge so tests exercise the
	// DB path deterministically; empty redisURL uses per-instance memory.
	svc := NewWalletService(db.New(mock), "", "")
	app.Get("/api/v1/wallet/:addr/nfts", svc.handleNFTs)
	return app
}

func TestWalletNFTs_Success(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT n.collection, n.token_id::text, n.units::text`).
		WithArgs(testOwner).
		WillReturnRows(pgxmock.NewRows(walletNFTCols).
			AddRow("0xcol1", "1", "1", "erc721", "Token One", "https://example.com/1.png").
			AddRow("0xcol2", "2", "5", "erc1155", "Token Two", "https://example.com/2.png"))

	app := newWalletApp(t, mock)
	resp := doGet(t, app, "/api/v1/wallet/"+testOwner+"/nfts")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var nfts []db.OwnedNFT
	decodeJSON(t, resp, &nfts)
	if len(nfts) != 2 {
		t.Fatalf("got %d nfts, want 2", len(nfts))
	}
	if nfts[0].Collection != "0xcol1" || nfts[1].Collection != "0xcol2" {
		t.Fatalf("unexpected nfts: %+v", nfts)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWalletNFTs_Empty(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT n.collection, n.token_id::text, n.units::text`).
		WithArgs(testOwner).
		WillReturnRows(pgxmock.NewRows(walletNFTCols))

	app := newWalletApp(t, mock)
	resp := doGet(t, app, "/api/v1/wallet/"+testOwner+"/nfts")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var nfts []db.OwnedNFT
	decodeJSON(t, resp, &nfts)
	if nfts == nil || len(nfts) != 0 {
		t.Fatalf("expected empty slice, got %+v", nfts)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWalletNFTs_EmptyDebugLog(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT n.collection, n.token_id::text, n.units::text`).
		WithArgs(testOwner).
		WillReturnRows(pgxmock.NewRows(walletNFTCols))

	// Capture zerolog global logger output
	var buf bytes.Buffer
	oldLogger := log.Logger
	log.Logger = zerolog.New(&buf).Level(zerolog.DebugLevel)
	defer func() { log.Logger = oldLogger }()

	app := newWalletApp(t, mock)
	resp := doGet(t, app, "/api/v1/wallet/"+testOwner+"/nfts")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	output := buf.String()
	if !strings.Contains(output, "wallet-nfts") {
		t.Fatalf("expected debug log to contain 'wallet-nfts', got: %s", output)
	}
	if !strings.Contains(output, testOwner) {
		t.Fatalf("expected debug log to contain owner address %s, got: %s", testOwner, output)
	}
	if !strings.Contains(output, "tracked_collections") {
		t.Fatalf("expected debug log to mention tracked_collections, got: %s", output)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWalletNFTs_DBError(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT n.collection, n.token_id::text, n.units::text`).
		WithArgs(testOwner).
		WillReturnError(fiber.ErrInternalServerError)

	app := newWalletApp(t, mock)
	resp := doGet(t, app, "/api/v1/wallet/"+testOwner+"/nfts")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestWalletNFTs_ExplorerMerge verifies the Blockscout union: explorer-only
// tokens are appended, (collection, token_id) conflicts keep the DB row, and
// the response advertises the merged source. A wallet whose NFTs predate
// indexFromBlock must still see them (the bug this merge fixes).
func TestWalletNFTs_ExplorerMerge(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT n.collection, n.token_id::text, n.units::text`).
		WithArgs(testOwner).
		WillReturnRows(pgxmock.NewRows(walletNFTCols).
			AddRow("0xcol1", "1", "1", "erc721", "DB Name Wins", "https://db.example/1.png"))

	explorer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/api/v2/addresses/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[
			{"id":"1","value":"1","image_url":"https://exp.example/dup.png","metadata":{"name":"Explorer Dup"},"token":{"address":"0xCOL1","name":"Col1","type":"ERC-721"}},
			{"id":"7","value":"3","image_url":"https://exp.example/7.png","metadata":{"name":"Explorer Only"},"token":{"address":"0xother","name":"Other","type":"ERC-1155"}}
		],"next_page_params":null}`))
	}))
	defer explorer.Close()

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	svc := NewWalletService(db.New(mock), explorer.URL, "")
	app.Get("/api/v1/wallet/:addr/nfts", svc.handleNFTs)

	resp := doGet(t, app, "/api/v1/wallet/"+testOwner+"/nfts")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-MW-Wallet-Source"); got != "db+explorer" {
		t.Fatalf("X-MW-Wallet-Source = %q, want db+explorer", got)
	}
	// A healthy merge is not degraded.
	if got := resp.Header.Get("X-MW-Degraded"); got != "" {
		t.Fatalf("X-MW-Degraded = %q, want empty on a healthy merge", got)
	}
	var nfts []db.OwnedNFT
	decodeJSON(t, resp, &nfts)
	if len(nfts) != 2 {
		t.Fatalf("got %d nfts, want 2 (1 db + 1 explorer-only, dup collapsed)", len(nfts))
	}
	if nfts[0].Name != "DB Name Wins" {
		t.Fatalf("db row should win the dedupe, got name %q", nfts[0].Name)
	}
	if nfts[1].Collection != "0xother" || nfts[1].Standard != "erc1155" || nfts[1].Units != "3" {
		t.Fatalf("explorer row mapped wrong: %+v", nfts[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestWalletNFTs_ExplorerDown verifies the wallet endpoint never breaks when
// the explorer is unreachable — it serves the DB view and says so.
func TestWalletNFTs_ExplorerDown(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT n.collection, n.token_id::text, n.units::text`).
		WithArgs(testOwner).
		WillReturnRows(pgxmock.NewRows(walletNFTCols).
			AddRow("0xcol1", "1", "1", "erc721", "Token One", "https://example.com/1.png"))

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	svc := NewWalletService(db.New(mock), "http://127.0.0.1:1", "") // nothing listens
	app.Get("/api/v1/wallet/:addr/nfts", svc.handleNFTs)

	resp := doGet(t, app, "/api/v1/wallet/"+testOwner+"/nfts")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-MW-Wallet-Source"); got != "db" {
		t.Fatalf("X-MW-Wallet-Source = %q, want db", got)
	}
	// Explorer configured but unreachable → degraded marker so the client
	// keeps its last-known-good grid instead of trusting the db-only list.
	if got := resp.Header.Get("X-MW-Degraded"); got != "explorer-unavailable" {
		t.Fatalf("X-MW-Degraded = %q, want explorer-unavailable", got)
	}
	var nfts []db.OwnedNFT
	decodeJSON(t, resp, &nfts)
	if len(nfts) != 1 {
		t.Fatalf("got %d nfts, want 1", len(nfts))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestWalletNFTs_CachedSourceHeader verifies the second (cached) response
// still carries X-MW-Wallet-Source.
func TestWalletNFTs_CachedSourceHeader(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	// Only ONE db query expected — the second request must be served from cache.
	mock.ExpectQuery(`SELECT n.collection, n.token_id::text, n.units::text`).
		WithArgs(testOwner).
		WillReturnRows(pgxmock.NewRows(walletNFTCols).
			AddRow("0xcol1", "1", "1", "erc721", "Token One", "https://example.com/1.png"))

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	svc := NewWalletService(db.New(mock), "", "")
	app.Get("/api/v1/wallet/:addr/nfts", svc.handleNFTs)

	first := doGet(t, app, "/api/v1/wallet/"+testOwner+"/nfts")
	if first.StatusCode != http.StatusOK || first.Header.Get("X-MW-Wallet-Source") != "db" {
		t.Fatalf("first: status=%d source=%q", first.StatusCode, first.Header.Get("X-MW-Wallet-Source"))
	}
	second := doGet(t, app, "/api/v1/wallet/"+testOwner+"/nfts")
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second status = %d", second.StatusCode)
	}
	if got := second.Header.Get("X-MW-Wallet-Source"); got != "db" {
		t.Fatalf("cached response lost X-MW-Wallet-Source: %q", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestWalletNFTs_ExplorerPagination verifies next_page_params is followed and
// an NFT that only appears on page two reaches the merged inventory.
func TestWalletNFTs_ExplorerPagination(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()

	mock.ExpectQuery(`SELECT n.collection, n.token_id::text, n.units::text`).
		WithArgs(testOwner).
		WillReturnRows(pgxmock.NewRows(walletNFTCols))

	explorer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("token_id") == "" {
			// page 1 with a continuation cursor
			_, _ = w.Write([]byte(`{"items":[
				{"id":"1","value":"1","metadata":{"name":"Page1"},"token":{"address":"0xaaa","type":"ERC-721"}}
			],"next_page_params":{"token_id":"1","token_type":"ERC-721","items_count":50}}`))
			return
		}
		// page 2, terminal
		_, _ = w.Write([]byte(`{"items":[
			{"id":"2","value":"1","metadata":{"name":"Page2"},"token":{"address":"0xbbb","type":"ERC-721"}}
		],"next_page_params":null}`))
	}))
	defer explorer.Close()

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	svc := NewWalletService(db.New(mock), explorer.URL, "")
	app.Get("/api/v1/wallet/:addr/nfts", svc.handleNFTs)

	resp := doGet(t, app, "/api/v1/wallet/"+testOwner+"/nfts")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var nfts []db.OwnedNFT
	decodeJSON(t, resp, &nfts)
	if len(nfts) != 2 {
		t.Fatalf("got %d nfts, want 2 (page 1 + page 2)", len(nfts))
	}
	if nfts[1].Collection != "0xbbb" || nfts[1].Name != "Page2" {
		t.Fatalf("page-2 NFT missing or mismapped: %+v", nfts[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
