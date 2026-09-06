package api

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gofiber/fiber/v2"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

const (
	govManager = "0x14c3b1bae9d9224e5456fa78e527adea48e3cda3"
	govMarket  = "0xa6fbad082bc73515a48bfc933014757461cf3f8e"
	govAdmin   = "0x987f10f49b35a8ef48b664a8dc457f61c6fe2105"
	govKeeper  = "0x16278eadd682b114e922cd25de9dd211fc93767f"
)

// govCaller answers admin()/pendingAdmin()/keeper() on the manager and
// upgradeDelay() on the marketplace, and reports the admin as a contract.
type govCaller struct {
	fail  bool
	calls int
}

func addrWord(hex string) []byte {
	return common.BytesToHash(common.HexToAddress(hex).Bytes()).Bytes()
}

func (g *govCaller) CallContract(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
	g.calls++
	if g.fail {
		return nil, errors.New("rpc down")
	}
	switch string(msg.Data) {
	case string(selAdmin):
		return addrWord(govAdmin), nil
	case string(selPendingAdmin):
		return addrWord(zeroAddr), nil
	case string(selKeeper):
		return addrWord(govKeeper), nil
	case string(selUpgradeDelay):
		return common.BigToHash(big.NewInt(0)).Bytes(), nil
	}
	return nil, errors.New("unexpected selector")
}
func (g *govCaller) BlockNumber(context.Context) (uint64, error) { return 100, nil }
func (g *govCaller) CodeAt(context.Context, common.Address, *big.Int) ([]byte, error) {
	return []byte{0x60, 0x80}, nil
}

var govCols = []string{"id", "chain_id", "block_number", "tx_hash", "log_index", "contract", "event", "actor", "subject", "extra", "block_time"}

func newGovApp(mock pgxmock.PgxPoolIface, eth *govCaller, manager string) *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	svc := NewGovernanceService(db.New(mock), eth, 114, manager, govMarket, "0x0ade5f5a4d4c836af5a5625b678846f66be3dd7d", "0x358d4504f4d1fa76e26d142d59f0369c5fd81f9f")
	svc.RegisterRoutes(app.Group("/api/v1"))
	return app
}

// Read-only networks: no manager configured → deployed:false, no DB, no RPC.
func TestGovernance_ReadOnlyNetwork(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	eth := &govCaller{}
	app := newGovApp(mock, eth, "")
	resp := doGet(t, app, "/api/v1/governance")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body governanceResp
	decodeJSON(t, resp, &body)
	if body.Deployed || body.Live != nil || len(body.Events) != 0 {
		t.Fatalf("read-only body: %+v", body)
	}
	if eth.calls != 0 {
		t.Fatal("read-only network must not call the RPC")
	}
}

func TestGovernance_LiveAndEvents(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	now := time.Now()
	mock.ExpectQuery(`SELECT id, chain_id, block_number, tx_hash, log_index, contract, event`).
		WithArgs(int64(114), 50).
		WillReturnRows(pgxmock.NewRows(govCols).
			AddRow(int64(2), int64(114), int64(34905100), "0xaa", int32(0), govMarket, "Upgraded", "", "0x00000000000000000000000000000000000000ee", "", now).
			AddRow(int64(1), int64(114), int64(34905078), "0xbb", int32(1), govManager, "KeeperSet", zeroAddr, govKeeper, "", now))
	mock.ExpectQuery(`SELECT DISTINCT ON \(contract\) contract`).
		WithArgs(int64(114)).
		WillReturnRows(pgxmock.NewRows([]string{"contract", "subject"}).AddRow(govMarket, "0x00000000000000000000000000000000000000ee"))

	eth := &govCaller{}
	app := newGovApp(mock, eth, govManager)
	resp := doGet(t, app, "/api/v1/governance")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body governanceResp
	decodeJSON(t, resp, &body)
	if !body.Deployed || body.Manager != govManager {
		t.Fatalf("header: %+v", body)
	}
	if body.Live == nil || body.Live.Admin != govAdmin || body.Live.Keeper != govKeeper || body.Live.PendingAdmin != zeroAddr {
		t.Fatalf("live: %+v", body.Live)
	}
	if body.Live.UpgradeDelay != 0 || body.Live.Renounced || !body.Live.AdminIsSafe || body.Live.Error != "" {
		t.Fatalf("live flags: %+v", body.Live)
	}
	if body.Implementations["marketplace"] != "0x00000000000000000000000000000000000000ee" {
		t.Fatalf("impls: %+v", body.Implementations)
	}
	if len(body.Events) != 2 || body.Events[0].Event != "Upgraded" || body.Events[1].Event != "KeeperSet" {
		t.Fatalf("events: %+v", body.Events)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// The live snapshot is cached for liveTTL: two requests inside the window make
// one set of RPC calls, so /status polling cannot become an RPC storm.
func TestGovernance_LiveIsCached(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	for i := 0; i < 2; i++ {
		mock.ExpectQuery(`SELECT id, chain_id, block_number`).WithArgs(int64(114), 50).WillReturnRows(pgxmock.NewRows(govCols))
		mock.ExpectQuery(`SELECT DISTINCT ON \(contract\) contract`).WithArgs(int64(114)).WillReturnRows(pgxmock.NewRows([]string{"contract", "subject"}))
	}
	eth := &govCaller{}
	app := newGovApp(mock, eth, govManager)
	doGet(t, app, "/api/v1/governance")
	first := eth.calls
	doGet(t, app, "/api/v1/governance")
	if eth.calls != first {
		t.Fatalf("second request re-read the chain: %d → %d calls", first, eth.calls)
	}
	if first != 4 {
		t.Fatalf("expected admin+pending+keeper+upgradeDelay = 4 calls, got %d", first)
	}
}

// RPC failure is reported in-band, never as a 5xx: the indexed trail still renders.
func TestGovernance_RPCFailureIsInBand(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	mock.ExpectQuery(`SELECT id, chain_id, block_number`).WithArgs(int64(114), 50).WillReturnRows(pgxmock.NewRows(govCols))
	mock.ExpectQuery(`SELECT DISTINCT ON \(contract\) contract`).WithArgs(int64(114)).WillReturnRows(pgxmock.NewRows([]string{"contract", "subject"}))
	eth := &govCaller{fail: true}
	app := newGovApp(mock, eth, govManager)
	resp := doGet(t, app, "/api/v1/governance")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body governanceResp
	decodeJSON(t, resp, &body)
	if body.Live == nil || body.Live.Error == "" || body.Live.Admin != "" {
		t.Fatalf("live on failure: %+v", body.Live)
	}
}
