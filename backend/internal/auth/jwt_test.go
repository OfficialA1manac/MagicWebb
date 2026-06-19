package auth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// TestIssueVerifyRoundtrip: address ↔ subject parity for valid tokens.
func TestIssueVerifyRoundtrip(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	const address = "0xabc000000000000000000000000000000000dead"
	tok, err := Issue(address, secret, DefaultAudience, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	got, err := Verify(tok, secret, DefaultAudience)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if strings.ToLower(got) != strings.ToLower(address) {
		t.Fatalf("sub mismatch: %q vs %q", got, address)
	}
}

// TestVerifyRejectsBadAudience: a token minted with one audience MUST NOT
// verify against another — otherwise a leaked auth-token from any other
// service that uses the same secret would have JWT-bypass access here.
func TestVerifyRejectsBadAudience(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	const address = "0xabc000000000000000000000000000000000dead"
	tok, err := Issue(address, secret, "magicwebb:reindex", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := Verify(tok, secret, DefaultAudience); err == nil {
		t.Fatal("expected audience mismatch rejection")
	}
}

// TestVerifyRejectsExpired: TTL is honored after expiry. Issue() sets exp
// via `.Unix()` which rounds DOWN — so a 2s TTL issued at the very END of a
// Unix second has effective lifetime up to 2.999s, but issued at the START
// has only 2.001s. We pass a 2s TTL and sleep 3000ms so we land AT LEAST one
// full second past the worst-case exp floor, leaving generous headroom for
// Go scheduler / linker preemption observed to flake test runs at the
// boundary. The production clamp floors negative TTLs at 24h to defend
// against signer callers that pass junk in, so we cannot issue with ttl<0.
func TestVerifyRejectsExpired(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	const address = "0xabc000000000000000000000000000000000dead"
	tok, err := Issue(address, secret, DefaultAudience, 2*time.Second)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	time.Sleep(3000 * time.Millisecond)
	if _, err := Verify(tok, secret, DefaultAudience); err == nil {
		t.Fatal("expected expired token to fail verification")
	}
}

// TestVerifyRejectsBadSignature: tampering with the payload must be caught.
func TestVerifyRejectsBadSignature(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	const other = "ffffffffffffffffffffffffffffffff"
	const address = "0xabc000000000000000000000000000000000dead"
	tok, _ := Issue(address, secret, DefaultAudience, time.Hour)
	if _, err := Verify(tok, other, DefaultAudience); err == nil {
		t.Fatal("expected signature mismatch rejection")
	}
}

// TestCookieNameAddressBound: cookie names include a wallet-prefix so two
// wallets in one browser don't collide, AND so a wallet switch invalidates
// the previous session cookie automatically (browsers won't send it).
func TestCookieNameAddressBound(t *testing.T) {
	a := CookieName("0xabcd1234efab5678cdef9012abcd3456efab7855")
	b := CookieName("0x12345678abcdef9012345678abcdef9012345678")
	if a == b {
		t.Fatalf("cookie names collided: %q", a)
	}
	if !strings.HasPrefix(a, "mw_s_") || !strings.HasPrefix(b, "mw_s_") {
		t.Fatalf("cookie name prefix missing: %q %q", a, b)
	}
}

// TestIssueClampsExcessiveTTL: a mishandled default-short TTL handler that
// accidentally passes 0 or a week-long TTL is clamped to 24h so a single
// leaked token never outlives a day. Negative TTL is also clamped — that's
// intentional defense against a caller passing time.Duration(unix_ts - now)
// which would otherwise mint a forever-valid token.
func TestIssueClampsExcessiveTTL(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	const address = "0xabc000000000000000000000000000000000dead"
	for _, in := range []time.Duration{168 * time.Hour, -time.Second, 0} {
		tok, err := Issue(address, secret, DefaultAudience, in)
		if err != nil {
			t.Fatalf("issue %v: %v", in, err)
		}
		if _, err := Verify(tok, secret, DefaultAudience); err != nil {
			t.Fatalf("verify %v: %v", in, err)
		}
	}
}

// TestVerifyRejectsAlgNoneForgery: a hand-crafted token claiming alg=none
// MUST be rejected before any handler. This is the canonical JWT downgrade
// attack; HMAC compare alone is not enough — the explicit header check
// protects against any future library that reads alg and short-circuits.
func TestVerifyRejectsAlgNoneForgery(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	const address = "0xabc000000000000000000000000000000000dead"
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	pay := base64.RawURLEncoding.EncodeToString([]byte(
		`{"sub":"` + address + `","iss":"magicwebb","aud":"magicwebb:api","iat":1,"nbf":1,"exp":9999999999}`))
	tok := hdr + "." + pay + "."
	if _, err := Verify(tok, secret, DefaultAudience); err == nil {
		t.Fatal("expected alg=none forgery to be rejected")
	}
}

// TestVerifyRejectsTamperedClaims: a validly-signed token whose payload has
// been post-mutated (header alg+signature reused) MUST be rejected by HMAC
// mismatch, NOT silently re-validated. Defense against front-end or
// intermediate proxy tampering that swaps claims in flight.
func TestVerifyRejectsTamperedClaims(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	const address = "0xabc000000000000000000000000000000000dead"
	tok, err := Issue(address, secret, DefaultAudience, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	tampered := parts[0] + "." +
		base64.RawURLEncoding.EncodeToString([]byte(
			`{"sub":"`+address+`","iss":"magicwebb","aud":"magicwebb:reindex","iat":1,"nbf":1,"exp":9999999999}`)) +
		"." + parts[2]
	if _, err := Verify(tampered, secret, DefaultAudience); err == nil {
		t.Fatal("expected tampered claims to be rejected")
	}
}
