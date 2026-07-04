package main

import (
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/api"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// ── Mock Postgres pool (only Ping matters for healthz) ────────────────────

type mockPgxPool struct {
	pingErr error // if non-nil, Ping returns this error
}

func (m *mockPgxPool) Ping(ctx context.Context) error { return m.pingErr }
func (m *mockPgxPool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	panic("unused")
}
func (m *mockPgxPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	panic("unused")
}
func (m *mockPgxPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	panic("unused")
}
func (m *mockPgxPool) Begin(ctx context.Context) (pgx.Tx, error) { panic("unused") }
func (m *mockPgxPool) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	panic("unused")
}

// ── Mock RPC client (only BlockNumber matters for healthz) ────────────────

type mockEthClient struct {
	blockNum   uint64
	blockErr   error
}

func (m *mockEthClient) BlockNumber(ctx context.Context) (uint64, error) { return m.blockNum, m.blockErr }
func (m *mockEthClient) HeaderByNumber(ctx context.Context, n *big.Int) (*types.Header, error) {
	panic("unused")
}
func (m *mockEthClient) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	panic("unused")
}
func (m *mockEthClient) CallContract(ctx context.Context, msg ethereum.CallMsg, n *big.Int) ([]byte, error) {
	panic("unused")
}
func (m *mockEthClient) PendingNonceAt(ctx context.Context, addr common.Address) (uint64, error) {
	panic("unused")
}
func (m *mockEthClient) SuggestGasPrice(ctx context.Context) (*big.Int, error) { panic("unused") }
func (m *mockEthClient) SuggestGasTipCap(ctx context.Context) (*big.Int, error) { panic("unused") }
func (m *mockEthClient) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	panic("unused")
}
func (m *mockEthClient) TransactionReceipt(ctx context.Context, h common.Hash) (*types.Receipt, error) {
	panic("unused")
}
func (m *mockEthClient) BalanceAt(ctx context.Context, addr common.Address, n *big.Int) (*big.Int, error) {
	panic("unused")
}

// ── Test helpers ──────────────────────────────────────────────────────────

// setupSLOHealthApp creates a Fiber app with the SLO + healthz routes
// registered using the provided mock dependencies and lag function.
func setupSLOHealthApp(pool *mockPgxPool, eth *mockEthClient, getLag func() uint64) *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	q := db.New(pool)
	registerSLOHealthRoutes(app, q, eth, getLag)
	return app
}

// getReq is a shorthand for Fiber's app.Test.
func getReq(t *testing.T, app *fiber.App, path string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// ── Tests: /api/v1/indexer/slo (Prometheus gauge format) ──────────────────

func TestSLO_PrometheusFormat(t *testing.T) {
	pool := &mockPgxPool{}
	ethc := &mockEthClient{blockNum: 100}
	var lag atomic.Uint64

	app := setupSLOHealthApp(pool, ethc, lag.Load)

	resp := getReq(t, app, "/api/v1/indexer/slo")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/plain; charset=utf-8", ct)
	}

	// Verify Prometheus exposition format: HELP, TYPE, then the gauge line.
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	bodyStr := string(body[:n])

	if !strings.Contains(bodyStr, "# HELP head_lag_blocks") {
		t.Fatalf("missing HELP line in SLO response:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "# TYPE head_lag_blocks gauge") {
		t.Fatalf("missing TYPE line in SLO response:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "head_lag_blocks 0") {
		t.Fatalf("expected head_lag_blocks 0, got:\n%s", bodyStr)
	}
}

func TestSLO_ReflectsLag(t *testing.T) {
	pool := &mockPgxPool{}
	ethc := &mockEthClient{blockNum: 100}
	var lag atomic.Uint64

	app := setupSLOHealthApp(pool, ethc, lag.Load)

	tests := []struct {
		name string
		set  uint64
		want string
	}{
		{"startup zero lag", 0, "head_lag_blocks 0"},
		{"normal operation lag=2", 2, "head_lag_blocks 2"},
		{"stressed lag=100", 100, "head_lag_blocks 100"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lag.Store(tt.set)
			resp := getReq(t, app, "/api/v1/indexer/slo")
			defer resp.Body.Close()

			body := make([]byte, 4096)
			n, _ := resp.Body.Read(body)
			bodyStr := string(body[:n])

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200, got %d; body:\n%s", resp.StatusCode, bodyStr)
			}
			if !strings.Contains(bodyStr, tt.want) {
				t.Fatalf("expected %q in body:\n%s", tt.want, bodyStr)
			}
		})
	}
}

// ── Tests: /healthz ───────────────────────────────────────────────────────

func TestHealthz_Healthy(t *testing.T) {
	pool := &mockPgxPool{}
	ethc := &mockEthClient{blockNum: 12345}
	var lag atomic.Uint64
	lag.Store(2) // normal operation: 2 blocks behind

	// Set MWServerBuildSHA so the header appears on 200 responses.
	api.MWServerBuildSHA = "test-sha-abc123"
	t.Cleanup(func() { api.MWServerBuildSHA = "" })

	app := setupSLOHealthApp(pool, ethc, lag.Load)
	resp := getReq(t, app, "/healthz")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-MW-Build-SHA"); got != "test-sha-abc123" {
		t.Fatalf("X-MW-Build-SHA = %q, want test-sha-abc123", got)
	}
}

func TestHealthz_LagAtThreshold_Boundary(t *testing.T) {
	pool := &mockPgxPool{}
	ethc := &mockEthClient{blockNum: 100}
	var lag atomic.Uint64

	app := setupSLOHealthApp(pool, ethc, lag.Load)

	// Exactly 15 is healthy (threshold is > 15, not >= 15).
	lag.Store(15)
	resp := getReq(t, app, "/healthz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lag=15 should be healthy (200), got %d", resp.StatusCode)
	}

	// 16 is unhealthy.
	lag.Store(16)
	resp = getReq(t, app, "/healthz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("lag=16 should be 503, got %d", resp.StatusCode)
	}
}

func TestHealthz_ZeroLag(t *testing.T) {
	// Startup scenario: before the watcher's first tick, HeadLagBlocks() = 0.
	pool := &mockPgxPool{}
	ethc := &mockEthClient{blockNum: 100}
	var lag atomic.Uint64 // default 0

	app := setupSLOHealthApp(pool, ethc, lag.Load)
	resp := getReq(t, app, "/healthz")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("zero lag at startup should be 200, got %d", resp.StatusCode)
	}
}

func TestHealthz_DbUnhealthy(t *testing.T) {
	pool := &mockPgxPool{pingErr: errMock{"db failure"}}
	ethc := &mockEthClient{blockNum: 100}
	var lag atomic.Uint64

	app := setupSLOHealthApp(pool, ethc, lag.Load)
	resp := getReq(t, app, "/healthz")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("DB unhealthy should be 503, got %d", resp.StatusCode)
	}
}

// errMock implements the error interface for injecting controlled failures.
type errMock struct{ msg string }
func (e errMock) Error() string { return e.msg }

func TestHealthz_RpcUnhealthy(t *testing.T) {
	pool := &mockPgxPool{}
	ethc := &mockEthClient{blockNum: 0, blockErr: errMock{"rpc failure"}}
	var lag atomic.Uint64

	app := setupSLOHealthApp(pool, ethc, lag.Load)
	resp := getReq(t, app, "/healthz")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("RPC unhealthy should be 503, got %d", resp.StatusCode)
	}
}

func TestHealthz_LagExceedsThreshold(t *testing.T) {
	pool := &mockPgxPool{}
	ethc := &mockEthClient{blockNum: 100}
	var lag atomic.Uint64
	lag.Store(42) // far above 15-block threshold

	app := setupSLOHealthApp(pool, ethc, lag.Load)
	resp := getReq(t, app, "/healthz")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("lag=42 should be 503, got %d", resp.StatusCode)
	}

	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	bodyStr := string(body[:n])
	if !strings.Contains(bodyStr, "42") {
		t.Fatalf("503 body should mention the lag value (42), got:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "blocks behind head") {
		t.Fatalf("503 body should say 'blocks behind head', got:\n%s", bodyStr)
	}
}

// ── Integration: both endpoints registered together ───────────────────────

func TestSLOAndHealthz_RegisteredConcurrently(t *testing.T) {
	pool := &mockPgxPool{}
	ethc := &mockEthClient{blockNum: 100}
	var lag atomic.Uint64

	app := setupSLOHealthApp(pool, ethc, lag.Load)

	// SLO should always return 200 regardless of lag.
	lag.Store(999)
	resp := getReq(t, app, "/api/v1/indexer/slo")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SLO with lag=999 should be 200, got %d", resp.StatusCode)
	}

	// Healthz should be 503 with this high lag.
	resp = getReq(t, app, "/healthz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("healthz with lag=999 should be 503, got %d", resp.StatusCode)
	}
}
