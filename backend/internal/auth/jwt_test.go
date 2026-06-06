package auth

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

const testSecret = "test-secret-at-least-32-chars-long-xx"

func TestIssueVerifyRoundTrip(t *testing.T) {
	addr := "0xabc0000000000000000000000000000000000001"
	tok, err := Issue(addr, testSecret, time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	got, err := Verify(tok, testSecret)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got != addr {
		t.Fatalf("sub = %q, want %q", got, addr)
	}
}

func TestVerifyRejectsTamperedSignature(t *testing.T) {
	tok, _ := Issue("0xabc", testSecret, time.Hour)
	parts := strings.Split(tok, ".")
	// Flip a full byte of the decoded signature so the change is deterministic
	// (flipping the last base64 char can be a no-op — its low bits are ignored).
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	sigBytes[0] ^= 0xFF
	tampered := parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(sigBytes)
	if _, err := Verify(tampered, testSecret); err == nil {
		t.Fatal("expected tampered signature to be rejected")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	tok, _ := Issue("0xabc", testSecret, time.Hour)
	if _, err := Verify(tok, "a-different-secret-32-chars-long-xxx"); err == nil {
		t.Fatal("expected wrong secret to be rejected")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	tok, _ := Issue("0xabc", testSecret, -time.Second) // already expired
	if _, err := Verify(tok, testSecret); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	for _, tok := range []string{"", "abc", "a.b", "a.b.c.d"} {
		if _, err := Verify(tok, testSecret); err == nil {
			t.Fatalf("expected malformed token %q to be rejected", tok)
		}
	}
}

func TestVerifyRejectsEmptySub(t *testing.T) {
	tok, _ := Issue("", testSecret, time.Hour)
	if _, err := Verify(tok, testSecret); err == nil {
		t.Fatal("expected empty sub to be rejected")
	}
}

func TestCallerFromCtx(t *testing.T) {
	ctx := context.WithValue(context.Background(), CallerKey, "0xdead")
	if v, ok := CallerFromCtx(ctx); !ok || v != "0xdead" {
		t.Fatalf("CallerFromCtx = %q,%v", v, ok)
	}
	if _, ok := CallerFromCtx(context.Background()); ok {
		t.Fatal("expected no caller in empty context")
	}
	// empty-string caller must read as not-present
	ctxEmpty := context.WithValue(context.Background(), CallerKey, "")
	if _, ok := CallerFromCtx(ctxEmpty); ok {
		t.Fatal("expected empty caller to read as absent")
	}
}
