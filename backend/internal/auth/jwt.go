// Package auth provides JWT issuance and verification via golang-jwt.
package auth

import (
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
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

// Issue signs and returns a JWT for the given wallet address, bound to the
// supplied audience. Callers that need service-to-service tokens should pass
// a different audience (e.g. "magicwebb:reindex") so the marketplace API
// will reject them. TTL is clamped to a sane max (24h) to limit blast radius
// on secret leak.
//
// Uses golang-jwt/jwt/v5 with HS256 signing. The library handles base64
// encoding, signature computation, and token serialization — all of which
// were previously hand-rolled. The migration preserves the exact same
// on-the-wire token format (three-part dot-separated JWT with HS256).
func Issue(address, secret, audience string, ttl time.Duration) (string, error) {
	if audience == "" {
		audience = DefaultAudience
	}
	if ttl <= 0 || ttl > 24*time.Hour {
		ttl = 24 * time.Hour
	}
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   address,
		Issuer:    DefaultIssuer,
		Audience:  jwt.ClaimStrings{audience},
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// Verify parses and validates a JWT; returns the wallet address on success.
// All checks are enforced in this order:
//   1. golang-jwt parses the header and enforces alg (rejects alg=none by default).
//   2. Signature MUST match the body (golang-jwt uses constant-time compare internally).
//   3. issuer MUST equal DefaultIssuer (explicit check — golang-jwt's RegisteredClaims
//      does not enforce iss by default).
//   4. audience MUST equal expectedAudience (Verified via jwt.RegisteredClaims.VerifyAudience).
//   5. nbf / exp bounds respected, sub MUST be non-empty.
//
// A forged or downgrade token — even with a "valid-looking" signature claiming
// a different alg — fails before any handler runs.
func Verify(tokenString, secret, expectedAudience string) (string, error) {
	if expectedAudience == "" {
		expectedAudience = DefaultAudience
	}

	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		// Validate the signing method is HMAC-based. This is defense-in-depth:
		// golang-jwt's default parser already rejects alg=none, but an explicit
		// check here prevents any future parser configuration drift from
		// silently accepting a different algorithm family.
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return "", err
	}
	if !token.Valid {
		return "", fmt.Errorf("invalid token")
	}

	// Enforce issuer. golang-jwt's RegisteredClaims does not auto-validate iss
	// (unlike exp/nbf/iat/aud), so this check is explicit and mandatory.
	if claims.Issuer != DefaultIssuer {
		return "", fmt.Errorf("invalid issuer")
	}

	// Verify audience. claims.Audience is jwt.ClaimStrings ([]string).
	// We require an EXACT match — the audience claim must contain
	// expectedAudience. This preserves the old behavior where a token
	// minted for "magicwebb:reindex" cannot verify as "magicwebb:api".
	audOK := false
	for _, aud := range claims.Audience {
		if aud == expectedAudience {
			audOK = true
			break
		}
	}
	if !audOK {
		return "", fmt.Errorf("invalid audience")
	}

	if claims.Subject == "" {
		return "", fmt.Errorf("missing sub")
	}

	return claims.Subject, nil
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
