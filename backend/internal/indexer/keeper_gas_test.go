package indexer

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// Hardcoded test ECDSA key (64 hex chars = 32 bytes). Deterministic across
// test runs so tests are reproducible and don't incur key generation cost.
const testKeeperKeyHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// gasLogRecorder is a minimal PgxPool that records whether Exec was called.
// Used to verify that logKeeperGas skips InsertGasLog when EffectiveGasPrice
// is nil — pgxmock can't detect "was NOT called" because it returns errors
// on unexpected calls, which logKeeperGas swallows.
type gasLogRecorder struct {
	execCalled bool
}

func (r *gasLogRecorder) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	r.execCalled = true
	return pgconn.CommandTag{}, nil
}
func (r *gasLogRecorder) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}
func (r *gasLogRecorder) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return nil
}
func (r *gasLogRecorder) Begin(ctx context.Context) (pgx.Tx, error) {
	return nil, nil
}
func (r *gasLogRecorder) BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error) {
	return nil, nil
}
func (r *gasLogRecorder) Ping(ctx context.Context) error {
	return nil
}

// TestKeeperAddress verifies keeperAddress() error handling and the
// valid-key derivation path directly, without involving logKeeperGas.
func TestKeeperAddress(t *testing.T) {
	// Computed offline from testKeeperKeyHex via crypto.HexToECDSA + PubkeyToAddress.
	const wantAddr = "0xFCAd0B19bB29D4674531d6f115237E16AfCE377c"

	tt := []struct {
		name    string
		key     string
		wantErr bool
		want    string // expected address if !wantErr
	}{
		{name: "empty key", key: "", wantErr: true},
		{name: "too short", key: "abcdef", wantErr: true},
		{name: "non-hex", key: "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", wantErr: true},
		{name: "valid", key: testKeeperKeyHex, wantErr: false, want: wantAddr},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.KeeperKey = tc.key
			r := &Runner{cfg: cfg}

			addr, err := r.keeperAddress()

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if addr != "" {
					t.Fatalf("expected empty address on error, got %q", addr)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if addr != tc.want {
					t.Fatalf("address = %q, want %q", addr, tc.want)
				}
			}
		})
	}
}

// TestLogKeeperGas_NilEffectiveGasPrice verifies that when a receipt's
// EffectiveGasPrice is nil, logKeeperGas logs a warning and skips the
// InsertGasLog call instead of writing a fabricated zero-cost row.
func TestLogKeeperGas_NilEffectiveGasPrice(t *testing.T) {
	cfg := &config.Config{}
	cfg.KeeperKey = testKeeperKeyHex

	pool := &gasLogRecorder{}
	r := &Runner{cfg: cfg, q: db.New(pool)}

	// Receipt with nil EffectiveGasPrice (zero-value for *big.Int).
	rec := &types.Receipt{
		GasUsed: 50000,
		Status:  types.ReceiptStatusSuccessful,
		// EffectiveGasPrice is nil
	}
	txHash := common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")

	// Capture zerolog output to verify the warning message is emitted.
	var buf bytes.Buffer
	prevLevel := zerolog.GlobalLevel()
	prevLogger := log.Logger
	zerolog.SetGlobalLevel(zerolog.WarnLevel)
	log.Logger = zerolog.New(&buf).With().Timestamp().Logger()
	defer func() {
		zerolog.SetGlobalLevel(prevLevel)
		log.Logger = prevLogger
	}()

	// logKeeperGas should detect nil EffectiveGasPrice, log a warning,
	// and return without calling InsertGasLog (which would call Exec).
	r.logKeeperGas(context.Background(), txHash, "settle", rec)

	// Verify InsertGasLog was NOT called.
	if pool.execCalled {
		t.Fatal("InsertGasLog was called despite nil EffectiveGasPrice — expected early return")
	}

	// Verify the warning was logged.
	output := buf.String()
	if output == "" {
		t.Fatal("expected warning log message but no output was produced")
	}
	if !bytes.Contains(buf.Bytes(), []byte("EffectiveGasPrice is nil")) {
		t.Fatalf("warning log missing expected text, got: %s", output)
	}
}

// TestLogKeeperGas_InvalidKeeperKey verifies that when KEEPER_KEY is
// empty, logKeeperGas logs a warning and skips the InsertGasLog call.
func TestLogKeeperGas_InvalidKeeperKey(t *testing.T) {
	cfg := &config.Config{}
	cfg.KeeperKey = "" // invalid: empty key

	pool := &gasLogRecorder{}
	r := &Runner{cfg: cfg, q: db.New(pool)}

	rec := &types.Receipt{
		GasUsed:            50000,
		Status:             types.ReceiptStatusSuccessful,
		EffectiveGasPrice:  big.NewInt(1000000000),
	}
	txHash := common.HexToHash("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")

	// Capture zerolog output.
	var buf bytes.Buffer
	prevLevel := zerolog.GlobalLevel()
	prevLogger := log.Logger
	zerolog.SetGlobalLevel(zerolog.WarnLevel)
	log.Logger = zerolog.New(&buf).With().Timestamp().Logger()
	defer func() {
		zerolog.SetGlobalLevel(prevLevel)
		log.Logger = prevLogger
	}()

	r.logKeeperGas(context.Background(), txHash, "settle", rec)

	// Verify InsertGasLog was NOT called.
	if pool.execCalled {
		t.Fatal("InsertGasLog was called despite invalid KEEPER_KEY — expected early return")
	}

	// Verify the key-parse-failure warning was logged.
	output := buf.String()
	if output == "" {
		t.Fatal("expected warning log message but no output was produced")
	}
	if !bytes.Contains(buf.Bytes(), []byte("key parse failed")) {
		t.Fatalf("warning log missing expected text, got: %s", output)
	}
}

// TestLogKeeperGas_InsertError verifies that when InsertGasLog returns an
// error (e.g. DB down), logKeeperGas does not panic and logs a warning.
func TestLogKeeperGas_InsertError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	cfg := &config.Config{}
	cfg.KeeperKey = testKeeperKeyHex

	r := &Runner{cfg: cfg, q: db.New(mock)}

	keeperAddr, err := r.keeperAddress()
	if err != nil {
		t.Fatalf("keeperAddress() failed: %v", err)
	}

	effectiveGasPrice := big.NewInt(1000000000)
	rec := &types.Receipt{
		GasUsed:            50000,
		Status:             types.ReceiptStatusSuccessful,
		EffectiveGasPrice:  effectiveGasPrice,
	}
	txHash := common.HexToHash("0xcafecafecafecafecafecafecafecafecafecafecafecafecafecafecafecafe")

	// Simulate a DB write failure.
	mock.ExpectExec(`INSERT INTO keeper_gas_logs`).
		WithArgs(keeperAddr, "settle", txHash.Hex(), int64(50000), "1000000000", "50000000000000", int64(0)).
		WillReturnError(fmt.Errorf("connection refused"))

	// Capture zerolog output.
	var buf bytes.Buffer
	prevLevel := zerolog.GlobalLevel()
	prevLogger := log.Logger
	zerolog.SetGlobalLevel(zerolog.WarnLevel)
	log.Logger = zerolog.New(&buf).With().Timestamp().Logger()
	defer func() {
		zerolog.SetGlobalLevel(prevLevel)
		log.Logger = prevLogger
	}()

	// logKeeperGas should not panic on insert failure.
	r.logKeeperGas(context.Background(), txHash, "settle", rec)

	// Verify the insert-failure warning was logged.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
	output := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("gas log insert failed")) {
		t.Fatalf("warning log missing expected text, got: %s", output)
	}
}

// TestLogKeeperGas_ValidReceipt verifies that when EffectiveGasPrice is
// populated, logKeeperGas calls InsertGasLog with the correct values.
func TestLogKeeperGas_ValidReceipt(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	cfg := &config.Config{}
	cfg.KeeperKey = testKeeperKeyHex

	r := &Runner{cfg: cfg, q: db.New(mock)}

	// Derive the keeper address via the extracted helper (same method
	// logKeeperGas uses) for expectation matching.
	keeperAddr, err := r.keeperAddress()
	if err != nil {
		t.Fatalf("keeperAddress() failed: %v", err)
	}

	effectiveGasPrice := big.NewInt(1000000000) // 1 gwei
	rec := &types.Receipt{
		GasUsed:            50000,
		Status:             types.ReceiptStatusSuccessful,
		EffectiveGasPrice:  effectiveGasPrice,
	}
	txHash := common.HexToHash("0xabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefab")

	// Expected: gasCost = 50000 * 1000000000 = 50000000000000
	expectedGasCost := "50000000000000"
	expectedGasPrice := "1000000000"

	mock.ExpectExec(`INSERT INTO keeper_gas_logs`).
		WithArgs(keeperAddr, "settle", txHash.Hex(), int64(50000), expectedGasPrice, expectedGasCost, int64(0)).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	r.logKeeperGas(context.Background(), txHash, "settle", rec)

	// Verify all expected calls were made.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
