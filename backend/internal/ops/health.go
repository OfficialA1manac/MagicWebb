// Package ops is the process-wide operational health snapshot (v3.6 wave 6).
// The indexer's health workers write it; /internal/metrics, /api/v1/governance
// and /status read it. No dependencies, so any package can import it without
// a cycle.
package ops

import (
	"math/big"
	"sync"
	"sync/atomic"
)

// Keeper balance levels. "disabled" = no keeper key / check switched off.
const (
	LevelOK       = "ok"
	LevelWarning  = "warning"  // below KEEPER_MIN_BALANCE_WEI
	LevelCritical = "critical" // below 20% of KEEPER_MIN_BALANCE_WEI
	LevelUnknown  = "unknown"  // balance RPC failed
	LevelDisabled = "disabled"
)

// CriticalFractionPct: below this percentage of the minimum the level is critical.
const CriticalFractionPct = 20

// KeeperHealth is the last keeper wallet + gas-cap sample.
type KeeperHealth struct {
	Address    string  `json:"address"`
	BalanceWei string  `json:"balance_wei"`
	MinWei     string  `json:"min_wei"`
	Level      string  `json:"level"`
	CheckedAt  int64   `json:"checked_at"`
	Currency   string  `json:"currency"`
	// Fee-cap watch: the keeper's MaxFeeCapGwei vs the network's suggested fee.
	FeeCapGwei      float64 `json:"fee_cap_gwei"`
	SuggestedGwei   float64 `json:"suggested_gwei"`
	FeeCapBelowFor  int     `json:"fee_cap_below_for"` // consecutive ticks the cap sat below the suggestion
	UnderpricedTotal int64  `json:"underpriced_total"`
}

// ImageStoreHealth is the last blob-store sample.
type ImageStoreHealth struct {
	Bytes     int64 `json:"bytes"`
	CapBytes  int64 `json:"cap_bytes"`
	Evicted   int64 `json:"evicted_total"` // blobs evicted since boot
	CheckedAt int64 `json:"checked_at"`
}

// LagHealth is the indexer head-lag alert state.
type LagHealth struct {
	Blocks   uint64 `json:"blocks"`
	Alerting bool   `json:"alerting"`
	Since    int64  `json:"since,omitempty"` // unix seconds the breach started
}

var (
	mu          sync.RWMutex
	keeper      KeeperHealth = KeeperHealth{Level: LevelDisabled}
	img         ImageStoreHealth
	lag         LagHealth
	underpriced atomic.Int64
	evicted     atomic.Int64
)

func SetKeeper(k KeeperHealth) { mu.Lock(); keeper = k; mu.Unlock() }
func Keeper() KeeperHealth   { mu.RLock(); defer mu.RUnlock(); k := keeper; k.UnderpricedTotal = underpriced.Load(); return k }

func SetImageStore(h ImageStoreHealth) { mu.Lock(); img = h; mu.Unlock() }
func ImageStore() ImageStoreHealth     { mu.RLock(); defer mu.RUnlock(); h := img; h.Evicted = evicted.Load(); return h }

func SetLag(l LagHealth) { mu.Lock(); lag = l; mu.Unlock() }
func Lag() LagHealth     { mu.RLock(); defer mu.RUnlock(); return lag }

// IncUnderpriced counts a keeper broadcast the RPC rejected as underpriced.
func IncUnderpriced()      { underpriced.Add(1) }
func Underpriced() int64   { return underpriced.Load() }
func AddEvicted(n int64)   { evicted.Add(n) }
func EvictedTotal() int64  { return evicted.Load() }

// KeeperLevel classifies a balance against the configured minimum.
// min == nil or <= 0 → disabled; balance == nil → unknown.
func KeeperLevel(balance, min *big.Int) string {
	if min == nil || min.Sign() <= 0 {
		return LevelDisabled
	}
	if balance == nil {
		return LevelUnknown
	}
	if balance.Cmp(min) >= 0 {
		return LevelOK
	}
	crit := new(big.Int).Mul(min, big.NewInt(CriticalFractionPct))
	crit.Div(crit, big.NewInt(100))
	if balance.Cmp(crit) < 0 {
		return LevelCritical
	}
	return LevelWarning
}

// ResetForTest zeroes every snapshot (tests only).
func ResetForTest() {
	mu.Lock()
	keeper = KeeperHealth{Level: LevelDisabled}
	img = ImageStoreHealth{}
	lag = LagHealth{}
	mu.Unlock()
	underpriced.Store(0)
	evicted.Store(0)
}
