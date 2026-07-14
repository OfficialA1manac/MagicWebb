// Package auth provides JWT issuance and verification via golang-jwt.
// Supports short-lived access tokens (15min) and long-lived refresh tokens (7d)
// with family-based rotation to limit the blast radius of token theft.
//
// Token types:
//   - access:  Short-lived (15min), used for API authorization. Stored in an
//             HttpOnly cookie (mw_a_<addr>) or Authorization: Bearer header.
//   - refresh: Long-lived (7d), used ONLY at /auth/refresh to obtain new
//             access+refresh pairs. Stored in an HttpOnly cookie (mw_r_<addr>).
//             Rotation: each use invalidates the previous refresh token,
//             making stolen refresh tokens detectable (reuse → whole family revoked).
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
const DefaultAudience = "magicwebb:api"

// RefreshAudience is used for refresh tokens so they cannot be used as
// access tokens on API endpoints.
const RefreshAudience = "magicwebb:refresh"

// DefaultIssuer identifies the marketplace token origin.
const DefaultIssuer = "magicwebb"

// Token lifetimes (as per optimization matrix AUTH-1):
//   Access:  15 minutes — limits blast radius of token theft
//   Refresh:  7 days   — balances UX (no daily re-auth) with security
const AccessTokenTTL  = 15 * time.Minute
const RefreshTokenTTL = 7 * 24 * time.Hour

// TokenType is embedded in custom JWT claims so middleware can distinguish
// access tokens from refresh tokens without a DB lookup.
type TokenType string

const (
	TokenAccess  TokenType = "access"
	TokenRefresh TokenType = "refresh"
)

// Claims extends the standard JWT claims with token_type and family_id fields.
// Refresh tokens carry a family_id (UUID) for family-based rotation and
// use the standard jti claim as the per-token identifier for rotation tracking.
type Claims struct {
	jwt.RegisteredClaims
	TokenType string `json:"token_type,omitempty"` // "access" | "refresh"
	FamilyID  string `json:"family_id,omitempty"`  // UUID identifying the refresh family
}

// IssueAccessToken signs and returns a short-lived access JWT.
func IssueAccessToken(address, secret string) (string, error) {
	return issueWithFamily(address, secret, DefaultAudience, AccessTokenTTL, TokenAccess, "", "")
}

// IssueRefreshTokenWithFamily signs a refresh JWT with family_id and jti
// embedded. The family_id links all tokens in a rotation chain; the jti
// (token_id) identifies this specific token for rotation tracking.
func IssueRefreshTokenWithFamily(address, secret, familyID, tokenID string) (string, error) {
	return issueWithFamily(address, secret, RefreshAudience, RefreshTokenTTL, TokenRefresh, familyID, tokenID)
}

func issueWithFamily(address, secret, audience string, ttl time.Duration, tokType TokenType, familyID, tokenID string) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   address,
			Issuer:    DefaultIssuer,
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        tokenID,
		},
		TokenType: string(tokType),
		FamilyID:  familyID,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// Verify parses and validates a JWT; returns the wallet address and token type.
// Access tokens (token_type=access or empty/legacy) verify against DefaultAudience.
// Refresh tokens (token_type=refresh) verify against RefreshAudience.
//
// All standard JWT checks are enforced:
//   1. golang-jwt parses the header and enforces alg (rejects alg=none).
//   2. Signature MUST match the body (constant-time compare).
//   3. Issuer MUST equal DefaultIssuer.
//   4. Audience MUST equal expectedAudience.
//   5. nbf / exp bounds respected, sub MUST be non-empty.
func Verify(tokenString, secret, expectedAudience string) (address string, tokType TokenType, err error) {
	if expectedAudience == "" {
		expectedAudience = DefaultAudience
	}

	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return "", "", err
	}
	if !token.Valid {
		return "", "", fmt.Errorf("invalid token")
	}

	// Enforce issuer.
	if claims.Issuer != DefaultIssuer {
		return "", "", fmt.Errorf("invalid issuer")
	}

	// Verify audience.
	audOK := false
	for _, aud := range claims.Audience {
		if aud == expectedAudience {
			audOK = true
			break
		}
	}
	if !audOK {
		return "", "", fmt.Errorf("invalid audience")
	}

	if claims.Subject == "" {
		return "", "", fmt.Errorf("missing sub")
	}

	return claims.Subject, TokenType(claims.TokenType), nil
}

// VerifyAccessToken verifies that a token is a valid access token (not a refresh
// token). Used by API middleware to reject refresh tokens on data endpoints.
func VerifyAccessToken(tokenString, secret string) (string, error) {
	addr, tokType, err := Verify(tokenString, secret, DefaultAudience)
	if err != nil {
		return "", err
	}
	// Reject refresh tokens on API endpoints — they should only be used
	// at /auth/refresh. Tokens with an empty token_type (issued before the
	// token-type migration) are treated as access tokens for backward
	// compatibility with existing sessions.
	if tokType == TokenRefresh {
		return "", fmt.Errorf("refresh token cannot be used as access token")
	}
	return addr, nil
}

// VerifyRefreshToken verifies that a token is a valid refresh token.
func VerifyRefreshToken(tokenString, secret string) (string, error) {
	addr, _, err := Verify(tokenString, secret, RefreshAudience)
	return addr, err
}

// ParseRefreshClaims extracts the family_id and jti (token ID) from a
// refresh JWT without full validation. Use after VerifyRefreshToken has
// already confirmed the token is valid.
func ParseRefreshClaims(tokenString, secret string) (familyID, tokenID string) {
	claims := &Claims{}
	parser := jwt.NewParser()
	_, err := parser.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})
	if err != nil {
		return "", ""
	}
	return claims.FamilyID, claims.ID
}

// CookieNameAccess returns the access-token cookie name for a wallet.
// Pattern: mw_a_<addr-prefix>
func CookieNameAccess(address string) string {
	if len(address) < 8 {
		return "mw_access"
	}
	return "mw_a_" + strings.ToLower(address[:8])
}

// CookieNameRefresh returns the refresh-token cookie name for a wallet.
// Pattern: mw_r_<addr-prefix>
func CookieNameRefresh(address string) string {
	if len(address) < 8 {
		return "mw_refresh"
	}
	return "mw_r_" + strings.ToLower(address[:8])
}

// CookieName is the legacy session cookie name (mw_s_<prefix>).
// Superseded by CookieNameAccess (mw_a_<prefix>) as of AUTH-1.
// Retained for backward compatibility in cookie scanners and
// for clearing old sessions created before the migration.
func CookieName(address string) string {
	if len(address) < 8 {
		return "mw_session"
	}
	return "mw_s_" + strings.ToLower(address[:8])
}
