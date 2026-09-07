package indexer

import (
	"errors"
	"testing"
)

// v3.6 wave 6: the Allowance Module boot check must disable the sweeper only
// on a definite "no code here" answer, never on an RPC hiccup.
func TestAllowanceModuleProbe(t *testing.T) {
	if d, tr := allowanceModuleProbe(make([]byte, 32), nil); !d || tr {
		t.Fatalf("32-byte answer: want deployed, got deployed=%v transient=%v", d, tr)
	}
	if d, tr := allowanceModuleProbe(nil, nil); d || tr {
		t.Fatalf("empty answer: want not deployed + not transient, got deployed=%v transient=%v", d, tr)
	}
	if d, tr := allowanceModuleProbe([]byte{1, 2, 3}, nil); d || tr {
		t.Fatalf("short answer: want not deployed, got deployed=%v transient=%v", d, tr)
	}
	if d, tr := allowanceModuleProbe(nil, errors.New("rpc: timeout")); d || !tr {
		t.Fatalf("rpc error: want transient, got deployed=%v transient=%v", d, tr)
	}
}
