package api

import (
	"context"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/chain"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/ops"
)

// GovernanceService serves GET /api/v1/governance (v3.6 wave 1): who controls
// the contracts right now (read live from the manager and a core), plus the
// indexed trail of admin / keeper / upgrade events. No admin surface exists —
// this is the public window onto the one privileged key per network.
type GovernanceService struct {
	q       *db.Q
	eth     chain.Caller
	chainID int64
	manager string
	cores   map[string]string // name → proxy address (lowercase)

	mu      sync.Mutex
	cached  *governanceLive
	cacheAt time.Time
}

// governanceLive is the eth_call snapshot, cached for liveTTL so /status polling
// (every 30s per viewer) never turns into an RPC storm.
type governanceLive struct {
	Admin        string `json:"admin"`
	PendingAdmin string `json:"pending_admin"`
	Keeper       string `json:"keeper"`
	UpgradeDelay uint64 `json:"upgrade_delay"`
	AdminIsSafe  bool   `json:"admin_is_contract"`
	Renounced    bool   `json:"renounced"`
	ReadAt       int64  `json:"read_at"`
	Error        string `json:"error,omitempty"`
}

const liveTTL = 5 * time.Second

type governanceResp struct {
	Deployed        bool                    `json:"deployed"`
	ChainID         int64                   `json:"chain_id"`
	Manager         string                  `json:"manager,omitempty"`
	Live            *governanceLive         `json:"live,omitempty"`
	Implementations map[string]string       `json:"implementations,omitempty"`
	Events          []db.GovernanceEventRow `json:"events"`
	// KeeperHealth (v3.6 wave 6): the keeper wallet's last balance sample and
	// level, so /status can warn below KEEPER_MIN_BALANCE_WEI. Omitted until
	// the keeper-health worker has sampled once.
	KeeperHealth *ops.KeeperHealth `json:"keeper_health,omitempty"`
}

var (
	selAdmin        = crypto.Keccak256([]byte("admin()"))[:4]
	selPendingAdmin = crypto.Keccak256([]byte("pendingAdmin()"))[:4]
	selKeeper       = crypto.Keccak256([]byte("keeper()"))[:4]
	selUpgradeDelay = crypto.Keccak256([]byte("upgradeDelay()"))[:4]
)

// NewGovernanceService wires the service. manager may be empty (read-only
// network) — the handler then answers {deployed:false} instead of 404, so the
// /status page renders the same on every network.
func NewGovernanceService(q *db.Q, eth chain.Caller, chainID uint64, manager, marketplace, auction, offerbook string) *GovernanceService {
	cores := map[string]string{}
	for name, a := range map[string]string{"marketplace": marketplace, "auction_house": auction, "offer_book": offerbook} {
		if a != "" {
			cores[name] = strings.ToLower(a)
		}
	}
	return &GovernanceService{q: q, eth: eth, chainID: int64(chainID), manager: strings.ToLower(manager), cores: cores}
}

// RegisterRoutes mounts GET /governance under /api/v1.
func (s *GovernanceService) RegisterRoutes(api fiber.Router) {
	api.Get("/governance", s.handleGet)
}

func (s *GovernanceService) handleGet(c *fiber.Ctx) error {
	ctx := c.Context()
	resp := governanceResp{Deployed: s.manager != "", ChainID: s.chainID, Events: []db.GovernanceEventRow{}}
	if !resp.Deployed {
		return c.JSON(resp)
	}
	resp.Manager = s.manager

	events, err := s.q.ListGovernanceEvents(ctx, s.chainID, 50)
	if err != nil {
		return writeErr(c, fiber.StatusInternalServerError, "internal error")
	}
	resp.Events = events
	if impls, err := s.q.LatestImplementations(ctx, s.chainID); err == nil && len(impls) > 0 {
		named := map[string]string{}
		for name, proxy := range s.cores {
			if impl, ok := impls[proxy]; ok {
				named[name] = impl
			}
		}
		resp.Implementations = named
	}
	resp.Live = s.live(ctx)
	if kh := ops.Keeper(); kh.Address != "" {
		resp.KeeperHealth = &kh
	}
	return c.JSON(resp)
}

// live reads admin/pendingAdmin/keeper from the manager and upgradeDelay from
// the marketplace core, through the shared 5s cache. RPC failure is reported
// in-band (Error) with the last good snapshot when there is one.
func (s *GovernanceService) live(ctx context.Context) *governanceLive {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil && time.Since(s.cacheAt) < liveTTL {
		return s.cached
	}
	snap := &governanceLive{ReadAt: time.Now().Unix()}
	if s.eth == nil {
		snap.Error = "no rpc"
		return snap
	}
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	mgr := common.HexToAddress(s.manager)
	read := func(to common.Address, sel []byte) (string, error) {
		out, err := s.eth.CallContract(cctx, ethereum.CallMsg{To: &to, Data: sel}, nil)
		if err != nil {
			return "", err
		}
		if len(out) < 32 {
			return "", errShortReturn
		}
		return strings.ToLower(common.BytesToAddress(out[12:32]).Hex()), nil
	}
	var err error
	if snap.Admin, err = read(mgr, selAdmin); err == nil {
		snap.PendingAdmin, err = read(mgr, selPendingAdmin)
	}
	if err == nil {
		snap.Keeper, err = read(mgr, selKeeper)
	}
	if err == nil {
		if mp, ok := s.cores["marketplace"]; ok {
			to := common.HexToAddress(mp)
			if out, e := s.eth.CallContract(cctx, ethereum.CallMsg{To: &to, Data: selUpgradeDelay}, nil); e == nil && len(out) >= 32 {
				snap.UpgradeDelay = new(big.Int).SetBytes(out[24:32]).Uint64()
			} else if e != nil {
				err = e
			}
		}
	}
	if err != nil {
		if s.cached != nil {
			// Keep serving the last good values, flagged; refresh the TTL so a
			// flapping RPC is probed at most once per window, not per poll.
			stale := *s.cached
			stale.Error = "rpc: " + err.Error()
			s.cached, s.cacheAt = &stale, time.Now()
			return &stale
		}
		snap.Error = "rpc: " + err.Error()
		s.cached, s.cacheAt = snap, time.Now()
		return snap
	}
	snap.Renounced = snap.Admin == zeroAddr
	if !snap.Renounced {
		// A Safe (or any contract) admin has code; an EOA does not. Best-effort:
		// a failed code probe leaves the flag false rather than failing the read.
		if cp, ok := s.eth.(interface {
			CodeAt(context.Context, common.Address, *big.Int) ([]byte, error)
		}); ok {
			if code, e := cp.CodeAt(cctx, common.HexToAddress(snap.Admin), nil); e == nil && len(code) > 0 {
				snap.AdminIsSafe = true
			}
		}
	}
	s.cached, s.cacheAt = snap, time.Now()
	return snap
}

const zeroAddr = "0x0000000000000000000000000000000000000000"

var errShortReturn = fiber.NewError(fiber.StatusBadGateway, "short return")
