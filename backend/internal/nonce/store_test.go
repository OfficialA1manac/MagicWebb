package nonce

import (
	"testing"
	"time"
)

func TestMemStoreSetIfFreeRejectsSecondIssue(t *testing.T) {
	s := New()
	if !s.SetIfFree("0xabc", "n1", time.Minute) {
		t.Fatal("first SetIfFree should succeed")
	}
	if s.SetIfFree("0xabc", "n2", time.Minute) {
		t.Fatal("second SetIfFree must FAIL while first is live")
	}
	// After expiry, a new one is allowed.
	s.Set("0xabc", "expired", -time.Second)
	if !s.SetIfFree("0xabc", "n3", time.Minute) {
		t.Fatal("SetIfFree should succeed after prior is expired")
	}
}

func TestMemStoreGetDelIsSingleUse(t *testing.T) {
	s := New()
	s.Set("0xabc", "n", time.Minute)
	v1, ok := s.GetDel("0xabc")
	if !ok || v1 != "n" {
		t.Fatalf("first GetDel: %q %v", v1, ok)
	}
	v2, ok := s.GetDel("0xabc")
	if ok || v2 != "" {
		t.Fatalf("second GetDel must fail: %q %v", v2, ok)
	}
}
