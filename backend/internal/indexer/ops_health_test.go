package indexer

import (
	"errors"
	"math/big"
	"testing"
	"time"
)

func TestFeeCapStreak(t *testing.T) {
	cap := big.NewInt(200)
	if got := feeCapStreak(0, big.NewInt(250), cap); got != 1 {
		t.Fatalf("above cap: want 1, got %d", got)
	}
	if got := feeCapStreak(2, big.NewInt(201), cap); got != 3 {
		t.Fatalf("still above: want 3, got %d", got)
	}
	if got := feeCapStreak(5, big.NewInt(200), cap); got != 0 {
		t.Fatalf("equal to cap resets: want 0, got %d", got)
	}
	if got := feeCapStreak(5, big.NewInt(10), cap); got != 0 {
		t.Fatalf("below cap resets: want 0, got %d", got)
	}
	if got := feeCapStreak(5, nil, cap); got != 0 {
		t.Fatalf("nil suggestion resets: want 0, got %d", got)
	}
}

func TestIsUnderpricedErr(t *testing.T) {
	yes := []string{
		"transaction underpriced",
		"replacement transaction underpriced",
		"have gas fee cap (100000000000) < pool minimum fee cap (500000000000)",
		"max fee per gas less than block base fee: address 0x..., maxFeePerGas: 1 baseFee: 2",
		"gas price below configured minimum",
	}
	for _, s := range yes {
		if !isUnderpricedErr(errors.New(s)) {
			t.Errorf("%q should count as underpriced", s)
		}
	}
	no := []string{"nonce too low", "insufficient funds for gas * price + value", "execution reverted", ""}
	for _, s := range no {
		if isUnderpricedErr(errors.New(s)) {
			t.Errorf("%q should NOT count as underpriced", s)
		}
	}
	if isUnderpricedErr(nil) {
		t.Error("nil is not underpriced")
	}
}

func TestLagAlertStep(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	var st lagState
	var ev string

	// Under the SLO: nothing.
	st, ev = lagAlertStep(st, 5, t0)
	if ev != "" || st.alerting || !st.breachSince.IsZero() {
		t.Fatalf("under SLO: %+v %q", st, ev)
	}
	// Breach starts: no alert yet.
	st, ev = lagAlertStep(st, 40, t0)
	if ev != "" || st.alerting || !st.breachSince.Equal(t0) {
		t.Fatalf("breach start: %+v %q", st, ev)
	}
	// 1m59s later still breaching: not yet.
	st, ev = lagAlertStep(st, 45, t0.Add(lagAlertFor-time.Second))
	if ev != "" || st.alerting {
		t.Fatalf("before window: %+v %q", st, ev)
	}
	// 2 min: fire once.
	st, ev = lagAlertStep(st, 45, t0.Add(lagAlertFor))
	if ev != "fire" || !st.alerting {
		t.Fatalf("at window: %+v %q", st, ev)
	}
	st, ev = lagAlertStep(st, 60, t0.Add(lagAlertFor+time.Minute))
	if ev != "" || !st.alerting {
		t.Fatalf("still breaching: must not re-fire: %+v %q", st, ev)
	}
	// Recovery: resolve once, then quiet.
	st, ev = lagAlertStep(st, 3, t0.Add(10*time.Minute))
	if ev != "resolve" || st.alerting || !st.breachSince.IsZero() {
		t.Fatalf("recovery: %+v %q", st, ev)
	}
	st, ev = lagAlertStep(st, 3, t0.Add(11*time.Minute))
	if ev != "" {
		t.Fatalf("after recovery: %q", ev)
	}
	// A short blip (breach shorter than the window) never fires.
	st, _ = lagAlertStep(st, 50, t0.Add(20*time.Minute))
	st, ev = lagAlertStep(st, 1, t0.Add(20*time.Minute+30*time.Second))
	if ev != "" || st.alerting {
		t.Fatalf("blip: %+v %q", st, ev)
	}
	// Exactly 30 blocks is NOT a breach (> 30 is).
	st, _ = lagAlertStep(lagState{}, lagAlertBlocks, t0)
	if !st.breachSince.IsZero() {
		t.Fatal("lag == 30 must not start a breach")
	}
}
