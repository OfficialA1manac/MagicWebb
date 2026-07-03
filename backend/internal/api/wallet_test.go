package api

import (
	"bytes"
	"net/http"
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
	svc := NewWalletService(db.New(mock))
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
