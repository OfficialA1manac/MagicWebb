package api

import (
	"testing"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
)

// v3.6 wave 6: activity cache keys are chain-namespaced through cache.Key.
func TestActivityCacheKeysAreChainNamespaced(t *testing.T) {
	s := NewMetricsService(nil, cache.New(0), nil).WithChainID(19)
	if got, want := s.actKey("g", "20"), cache.Key(19, "act", "g", "20"); got != want {
		t.Fatalf("global key: got %q want %q", got, want)
	}
	if got, want := s.actKey("0xabc", "50"), "mw:19:act:0xabc:50"; got != want {
		t.Fatalf("address key: got %q want %q", got, want)
	}
	other := NewMetricsService(nil, cache.New(0), nil).WithChainID(114)
	if s.actKey("g", "20") == other.actKey("g", "20") {
		t.Fatal("keys for different chains must differ")
	}
}
