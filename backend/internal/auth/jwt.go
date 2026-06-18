// Package auth provides HMAC-SHA256 JWT issuance and verification.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type contextKey string

// CallerKey is the context key for the authenticated wallet address.
const CallerKey contextKey = "caller"

// DefaultAudience is the JWT audience claim identifying the marketplace API.
// Tokens minted for any other service (e.g. an indexer/reindex tool) MUST
// use a different aud so they cannot be replayed here.
const DefaultAudience = "magicwebb:api"

// DefaultIssuer identifies the marketplace token origin. The verify path
// requires iss == DefaultIssuer so a token signed by any other JWT_SECRET
// (compromised shared secret across deployments) is also rejected.
const DefaultIssuer = "magicwebb"

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

type jwtClaims struct {
	Sub string `json:"sub"` // wallet address
	Iss string `json:"iss"` // issuer (must match DefaultIssuer)
	Aud string `json:"aud"` // audience (must match expected)
	Iat int64  `json:"iat"`
	Nbf int64  `json:"nbf"`
	Exp int64  `json:"exp"`
}

// Issue signs and returns a JWT for the given wallet address, bound to the
// supplied audience. Callers that need service-to-service tokens should pass
// a different audience (e.g. "magicwebb:reindex") so the marketplace API
// will reject them. TTL is clamped to a sane max (24h) to limit blast radius
// on secret leak.
func Issue(address, secret, audience string, ttl time.Duration) (string, error) {
	if audience == "" {
		audience = DefaultAudience
	}
	if ttl <= 0 || ttl > 24*time.Hour {
		ttl = 24 * time.Hour
	}
	now := time.Now()
	hdr, err := json.Marshal(jwtHeader{Alg: "HS256", Typ: "JWT"})
	if err != nil {
		return "", err
	}
	pay, err := json.Marshal(jwtClaims{
		Sub: address,
		Iss: DefaultIssuer,
		Aud: audience,
		Iat: now.Unix(),
		Nbf: now.Unix(),
		Exp: now.Add(ttl).Unix(),
	})
	if err != nil {
		return "", err
	}
	msg := b64(hdr) + "." + b64(pay)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	return msg + "." + b64(mac.Sum(nil)), nil
}

// Verify parses and validates a JWT; returns the wallet address on success.
// All five checks are enforced in this order:
//   1. header.alg MUST be "HS256" (defense-in-depth against alg=none / alg-confusion).
//   2. HMAC signature MUST match the body (constant-time compare).
//   3. issuer MUST equal DefaultIssuer.
//   4. audience MUST equal expectedAudience.
//   5. nbf / exp bounds respected, sub MUST be non-empty.
// A forged or downgrade token — even with a "valid-looking" signature claiming
// a different alg — fails before any handler runs.
func Verify(token, secret, expectedAudience string) (string, error) {
	if expectedAudience == "" {
		expectedAudience = DefaultAudience
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("malformed token")
	}
	// Parse + enforce header BEFORE the HMAC compute.
	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("bad header encoding")
	}
	var hdr jwtHeader
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		return "", fmt.Errorf("bad header")
	}
	if hdr.Alg != "HS256" {
		return "", fmt.Errorf("unsupported alg")
	}
	if hdr.Typ != "JWT" {
		return "", fmt.Errorf("unsupported typ")
	}

	msg := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	expected := mac.Sum(nil)

	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(expected, got) {
		return "", fmt.Errorf("invalid signature")
	}
	payBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("bad payload encoding")
	}
	var c jwtClaims
	if err := json.Unmarshal(payBytes, &c); err != nil {
		return "", fmt.Errorf("bad payload")
	}
	if c.Iss != DefaultIssuer {
		return "", fmt.Errorf("invalid issuer")
	}
	if c.Aud != expectedAudience {
		return "", fmt.Errorf("invalid audience")
	}
	now := time.Now().Unix()
	if c.Nbf > now {
		return "", fmt.Errorf("token not yet valid")
	}
	if now > c.Exp {
		return "", fmt.Errorf("token expired")
	}
	if c.Sub == "" {
		return "", fmt.Errorf("missing sub")
	}
	return c.Sub, nil
}

// CookieName is the credential-bearing cookie set alongside the JWT so the
// browser can authenticate SSE + cross-page loads without exposing the
// token to JS (mitigates XSS exfiltration). Address-bound to prevent reuse
// across wallets in the same browser profile.
func CookieName(address string) string {
	if len(address) < 8 {
		return "mw_session"
	}
	return "mw_s_" + strings.ToLower(address[:8])
}

func b64(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
