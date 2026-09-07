package indexer

// Ops hardening (v3.6 wave 6, plan G15): three small health workers plus one
// alert dispatcher.
//
//   keeper-health  — every FeeSweepTick: keeper wallet balance (gauge, level,
//                    webhook + /status warning below KEEPER_MIN_BALANCE_WEI,
//                    critical below 20% of it) and the fee-cap watch (alert
//                    when the keeper's MaxFeeCapGwei sits below the network's
//                    suggested fee for 3 consecutive ticks — the Coston2
//                    starvation precedent).
//   lag-alert      — every 15s: webhook when head lag stays above 30 blocks
//                    for 2 minutes; resolved when it drops back.
//   image-store    — every 5 min: blob-store bytes gauge, alert at 80% of
//                    MaxTotalBlobBytes, and LRU eviction of unreferenced
//                    blobs down to 70% instead of silently refusing new ones.
//
// Every alert goes through opsAlert(): Discord + Prometheus + SMTP when
// configured, one per key per cooldown, with a resolved notice.

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/rs/zerolog/log"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/imagestore"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/ops"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/webhook"
)

const (
	// feeCapBelowTicks: consecutive keeper-health ticks with the suggested fee
	// above the keeper's cap before the alert fires.
	feeCapBelowTicks = 3
	// lagAlertBlocks / lagAlertFor: the head-lag SLO breach that pages.
	lagAlertBlocks = 30
	lagAlertFor    = 2 * time.Minute
	lagSample      = 15 * time.Second
	// imageStoreSample: blob-store gauge cadence.
	imageStoreSample = 5 * time.Minute
	// imageStoreAlertPct / imageStoreEvictToPct: alert threshold and the
	// eviction target (percent of MaxTotalBlobBytes).
	imageStoreAlertPct   = 80
	imageStoreEvictToPct = 70
	// opsAlertCooldown bounds repeat alerts per key.
	opsAlertCooldown = time.Hour
)

// ── alert dispatcher ─────────────────────────────────────────────────────

type opsAlerter struct {
	mu      sync.Mutex
	lastAt  map[string]time.Time
	active  map[string]bool
}

func (r *Runner) ops() *opsAlerter {
	r.opsOnce.Do(func() { r.opsAlerts = &opsAlerter{lastAt: map[string]time.Time{}, active: map[string]bool{}} })
	return r.opsAlerts
}

// opsAlert fires one alert per key per cooldown across every configured
// channel and marks the key active. Returns true when something was sent.
func (r *Runner) opsAlert(ctx context.Context, key, alertName, title, desc, severity string) bool {
	a := r.ops()
	a.mu.Lock()
	if t, ok := a.lastAt[key]; ok && time.Since(t) < opsAlertCooldown {
		a.mu.Unlock()
		return false
	}
	a.lastAt[key] = time.Now()
	a.active[key] = true
	a.mu.Unlock()

	log.Warn().Str("alert", alertName).Str("severity", severity).Msg(title)
	discordURL, promURL := r.cfg.DiscordWebhookURL, r.cfg.PrometheusWebhookURL
	hasSMTP := r.cfg.SMTPHost != "" && r.cfg.SMTPUser != "" && r.cfg.SMTPPass != "" && r.cfg.EmailFrom != "" && r.cfg.EmailTo != ""
	if discordURL == "" && promURL == "" && !hasSMTP {
		return true // logged only
	}
	wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if discordURL != "" {
		if err := webhook.SendDiscordAlert(wctx, discordURL, title, desc); err != nil {
			log.Error().Err(err).Str("alert", alertName).Msg("ops alert: Discord send failed")
		}
	}
	if promURL != "" {
		if err := webhook.SendPrometheusAlert(wctx, promURL, alertName, desc, severity); err != nil {
			log.Error().Err(err).Str("alert", alertName).Msg("ops alert: Prometheus send failed")
		}
	}
	if hasSMTP {
		body := "<h2>" + title + "</h2><pre style=\"white-space:pre-wrap\">" + desc + "</pre>"
		if err := webhook.SendEmail(wctx, r.cfg.SMTPHost, r.cfg.SMTPPort, r.cfg.SMTPUser, r.cfg.SMTPPass, r.cfg.EmailFrom, r.cfg.EmailTo, "🚨 "+title, body); err != nil {
			log.Error().Err(err).Str("alert", alertName).Msg("ops alert: SMTP send failed")
		}
	}
	return true
}

// opsResolved sends the resolved notice for a key that was active and clears it.
func (r *Runner) opsResolved(ctx context.Context, key, alertName, title, desc string) {
	a := r.ops()
	a.mu.Lock()
	was := a.active[key]
	delete(a.active, key)
	delete(a.lastAt, key)
	a.mu.Unlock()
	if !was {
		return
	}
	log.Info().Str("alert", alertName).Msg(title)
	discordURL, promURL := r.cfg.DiscordWebhookURL, r.cfg.PrometheusWebhookURL
	if discordURL == "" && promURL == "" {
		return
	}
	wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if discordURL != "" {
		if err := webhook.SendDiscordResolvedAlert(wctx, discordURL, title, desc); err != nil {
			log.Error().Err(err).Str("alert", alertName).Msg("ops resolved: Discord send failed")
		}
	}
	if promURL != "" {
		if err := webhook.SendPrometheusResolvedAlert(wctx, promURL, alertName, desc); err != nil {
			log.Error().Err(err).Str("alert", alertName).Msg("ops resolved: Prometheus send failed")
		}
	}
}

// ── keeper health: balance + fee-cap watch ───────────────────────────────

// runKeeperHealthWorker samples the keeper wallet on the profile's
// FeeSweepTick. Read-only (BalanceAt + SuggestGasPrice), so it runs outside
// the single-flight keeper gate — every instance may report.
func (r *Runner) runKeeperHealthWorker(ctx context.Context, keeperAddr common.Address) {
	tick := r.tick(r.cfg.Profile.FeeSweepTick, 5*time.Minute)
	r.sampleKeeperHealth(ctx, keeperAddr)
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sampleKeeperHealth(ctx, keeperAddr)
		}
	}
}

func (r *Runner) sampleKeeperHealth(ctx context.Context, keeperAddr common.Address) {
	// KEEPER_MIN_BALANCE_WEI is validated by config.Load(); a malformed value
	// here still must not silently become "0 = disabled", so it is logged and
	// the level reads as unknown until fixed.
	minWei := new(big.Int)
	if s := r.cfg.KeeperMinBalanceWei; s != "" {
		if _, ok := minWei.SetString(s, 10); !ok {
			log.Error().Str("value", s).Msg("keeper health: KEEPER_MIN_BALANCE_WEI is not a decimal integer — balance level unknown")
			minWei = nil
		}
	}
	prev := ops.Keeper()
	minStr := "0"
	if minWei != nil {
		minStr = minWei.String()
	}
	h := ops.KeeperHealth{
		Address: strings.ToLower(keeperAddr.Hex()), MinWei: minStr, Currency: r.cfg.NativeCurrency,
		CheckedAt: time.Now().Unix(), FeeCapGwei: r.cfg.MaxFeeCapGwei, FeeCapBelowFor: prev.FeeCapBelowFor,
	}

	// Balance.
	bctx, bcancel := context.WithTimeout(ctx, 8*time.Second)
	bal, err := r.eth.BalanceAt(bctx, keeperAddr, nil)
	bcancel()
	if err != nil {
		log.Warn().Err(err).Msg("keeper health: BalanceAt failed")
		h.BalanceWei = prev.BalanceWei
		h.Level = ops.LevelUnknown
	} else {
		h.BalanceWei = bal.String()
		h.Level = ops.KeeperLevel(bal, minWei)
		if minWei == nil {
			h.Level = ops.LevelUnknown
		}
	}
	switch h.Level {
	case ops.LevelWarning, ops.LevelCritical:
		sev := "warning"
		if h.Level == ops.LevelCritical {
			sev = "critical"
		}
		if prev.Level != h.Level { // transition (ok→warning, warning→critical, …) fires regardless of cooldown
			r.ops().mu.Lock()
			delete(r.ops().lastAt, "keeper-balance")
			r.ops().mu.Unlock()
		}
		r.opsAlert(ctx, "keeper-balance", "KeeperBalanceLow",
			fmt.Sprintf("Keeper wallet %s: %s %s (min %s)", sev, fmtNative(bal), h.Currency, fmtNative(minWei)),
			fmt.Sprintf("Keeper %s on %s holds %s %s; the configured minimum is %s %s (KEEPER_MIN_BALANCE_WEI). Below the minimum, gas spikes fail settlements; below 20%% of it the keeper stops within hours. Top up the keeper wallet.",
				h.Address, r.cfg.NetworkName, fmtNative(bal), h.Currency, fmtNative(minWei), h.Currency), sev)
	case ops.LevelOK:
		r.opsResolved(ctx, "keeper-balance", "KeeperBalanceLow",
			fmt.Sprintf("Keeper wallet back above minimum: %s %s", fmtNative(bal), h.Currency),
			fmt.Sprintf("Keeper %s on %s holds %s %s (min %s %s).", h.Address, r.cfg.NetworkName, fmtNative(bal), h.Currency, fmtNative(minWei), h.Currency))
	}

	// Fee-cap watch.
	if cap := r.cfg.MaxFeeCapWei(); cap != nil {
		gctx, gcancel := context.WithTimeout(ctx, 8*time.Second)
		suggested, gerr := r.eth.SuggestGasPrice(gctx)
		gcancel()
		if gerr == nil && suggested != nil {
			h.SuggestedGwei = weiToGwei(suggested)
			h.FeeCapBelowFor = feeCapStreak(prev.FeeCapBelowFor, suggested, cap)
			if h.FeeCapBelowFor >= feeCapBelowTicks {
				r.opsAlert(ctx, "keeper-feecap", "KeeperFeeCapBelowNetwork",
					fmt.Sprintf("Keeper fee cap %.0f gwei is below the network's suggested %.0f gwei", h.FeeCapGwei, h.SuggestedGwei),
					fmt.Sprintf("For %d consecutive checks on %s the suggested gas price (%.1f gwei) exceeded KEEPER_MAX_FEE_CAP_GWEI (%.1f gwei). Every keeper broadcast is clamped to the cap and the RPC rejects it as underpriced, so settlements stall (Coston2 2026-08-31 precedent). Raise KEEPER_MAX_FEE_CAP_GWEI or the profile's MaxFeeCapGwei.",
						h.FeeCapBelowFor, r.cfg.NetworkName, h.SuggestedGwei, h.FeeCapGwei), "critical")
			} else if h.FeeCapBelowFor == 0 {
				r.opsResolved(ctx, "keeper-feecap", "KeeperFeeCapBelowNetwork",
					fmt.Sprintf("Keeper fee cap %.0f gwei is above the suggested %.0f gwei again", h.FeeCapGwei, h.SuggestedGwei), "The network's suggested gas price dropped back under the keeper's cap.")
			}
		}
	}
	ops.SetKeeper(h)
}

// feeCapStreak advances the consecutive-ticks counter: +1 while the suggested
// fee is above the cap, reset to 0 otherwise.
func feeCapStreak(prev int, suggested, cap *big.Int) int {
	if suggested != nil && cap != nil && suggested.Cmp(cap) > 0 {
		return prev + 1
	}
	return 0
}

// isUnderpricedErr matches the RPC rejections a too-low fee cap produces
// (go-ethereum / coreth wording): "transaction underpriced",
// "max fee per gas less than block base fee", "fee cap ... < pool minimum fee cap".
func isUnderpricedErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "underpriced") ||
		strings.Contains(s, "less than block base fee") ||
		strings.Contains(s, "minimum fee cap") ||
		strings.Contains(s, "fee cap less than") ||
		strings.Contains(s, "gas price below") ||
		strings.Contains(s, "max fee per gas less than")
}

func fmtNative(wei *big.Int) string {
	if wei == nil {
		return "?"
	}
	f, _ := new(big.Float).Quo(new(big.Float).SetInt(wei), big.NewFloat(1e18)).Float64()
	return fmt.Sprintf("%.4f", f)
}

func weiToGwei(wei *big.Int) float64 {
	if wei == nil {
		return 0
	}
	f, _ := new(big.Float).Quo(new(big.Float).SetInt(wei), big.NewFloat(1e9)).Float64()
	return f
}

// ── head-lag alert ───────────────────────────────────────────────────────

type lagState struct {
	breachSince time.Time // zero = not breaching
	alerting    bool
}

// lagAlertStep is the pure state machine: returns the next state and an
// event ("fire", "resolve" or "") for the current sample.
func lagAlertStep(st lagState, lag uint64, now time.Time) (lagState, string) {
	if lag > lagAlertBlocks {
		if st.breachSince.IsZero() {
			st.breachSince = now
		}
		if !st.alerting && now.Sub(st.breachSince) >= lagAlertFor {
			st.alerting = true
			return st, "fire"
		}
		return st, ""
	}
	st.breachSince = time.Time{}
	if st.alerting {
		st.alerting = false
		return st, "resolve"
	}
	return st, ""
}

func (r *Runner) runLagAlertWorker(ctx context.Context) {
	ticker := time.NewTicker(lagSample)
	defer ticker.Stop()
	var st lagState
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			lag := r.HeadLagBlocks()
			var ev string
			st, ev = lagAlertStep(st, lag, time.Now())
			since := int64(0)
			if !st.breachSince.IsZero() {
				since = st.breachSince.Unix()
			}
			ops.SetLag(ops.LagHealth{Blocks: lag, Alerting: st.alerting, Since: since})
			switch ev {
			case "fire":
				r.opsAlert(ctx, "head-lag", "IndexerHeadLag",
					fmt.Sprintf("Indexer is %d blocks behind %s for over %s", lag, r.cfg.NetworkName, lagAlertFor),
					fmt.Sprintf("/api/v1/indexer/slo has reported head_lag_blocks > %d for %s on %s. Listings, bids and offers appear late until it catches up. Check RPC health (magicwebb_rpc_healthy_count), the watcher logs, and whether the database is suspended.", lagAlertBlocks, lagAlertFor, r.cfg.NetworkName), "warning")
			case "resolve":
				r.opsResolved(ctx, "head-lag", "IndexerHeadLag",
					fmt.Sprintf("Indexer caught up on %s (%d blocks behind)", r.cfg.NetworkName, lag), "head_lag_blocks is back under the SLO.")
			}
		}
	}
}

// ── image store: gauge, alert, LRU eviction ──────────────────────────────

func (r *Runner) runImageStoreWorker(ctx context.Context) {
	r.sampleImageStore(ctx)
	ticker := time.NewTicker(imageStoreSample)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sampleImageStore(ctx)
		}
	}
}

func (r *Runner) sampleImageStore(ctx context.Context) {
	if r.imgStore == nil {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	total, err := r.imgStore.TotalBlobBytes(sctx)
	if err != nil {
		log.Warn().Err(err).Msg("image store: TotalBlobBytes failed")
		return
	}
	capBytes := int64(imagestore.MaxTotalBlobBytes)
	alertAt := capBytes * imageStoreAlertPct / 100
	if total >= alertAt {
		// Evict LRU unreferenced blobs down to the target before deciding whether to alert.
		target := capBytes * imageStoreEvictToPct / 100
		if ev, ok := r.imgStore.(imagestore.Evicter); ok {
			freed, n, eerr := imagestore.EvictUnreferenced(sctx, ev, total-target)
			if eerr != nil {
				log.Warn().Err(eerr).Msg("image store: eviction failed")
			} else if n > 0 {
				ops.AddEvicted(int64(n))
				log.Info().Int("blobs", n).Int64("freed_bytes", freed).Msg("image store: evicted unreferenced blobs (LRU)")
				total -= freed
			}
		} else {
			log.Warn().Msg("image store: backend cannot evict (no Evicter); new blobs will be skipped at the cap")
		}
	}
	ops.SetImageStore(ops.ImageStoreHealth{Bytes: total, CapBytes: capBytes, CheckedAt: time.Now().Unix()})
	if total >= alertAt {
		r.opsAlert(ctx, "image-store", "ImageStoreNearCap",
			fmt.Sprintf("Image store at %d%% of its %d MB cap", total*100/capBytes, capBytes>>20),
			fmt.Sprintf("Self-hosted image blobs use %d MB of the %d MB MaxTotalBlobBytes cap on %s and nothing unreferenced is left to evict. New images fall back to upstream proxying (slower, gateway-dependent). Raise the cap (imagestore.MaxTotalBlobBytes) or move blobs to S3 (IMG_STORE_BACKEND=s3).",
				total>>20, capBytes>>20, r.cfg.NetworkName), "warning")
	} else {
		r.opsResolved(ctx, "image-store", "ImageStoreNearCap",
			fmt.Sprintf("Image store back at %d%% of its cap", total*100/capBytes), "Eviction or a raised cap brought the blob store under the alert threshold.")
	}
}
