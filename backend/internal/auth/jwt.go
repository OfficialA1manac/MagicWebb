// Package auth provides HMAC-SHA256 JWT issuance and verification.
package auth

import (
	"context"
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

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

type jwtClaims struct {
	Sub string `json:"sub"` // wallet address
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

// Issue signs and returns a JWT for the given wallet address.
func Issue(address, secret string, ttl time.Duration) (string, error) {
	now := time.Now()
	hdr, err := json.Marshal(jwtHeader{Alg: "HS256", Typ: "JWT"})
	if err != nil {
		return "", err
	}
	pay, err := json.Marshal(jwtClaims{
		Sub: address,
		Iat: now.Unix(),
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
func Verify(token, secret string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("malformed token")
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
	if time.Now().Unix() > c.Exp {
		return "", fmt.Errorf("token expired")
	}
	if c.Sub == "" {
		return "", fmt.Errorf("missing sub")
	}
	return c.Sub, nil
}

// CallerFromCtx returns the authenticated wallet address from context.
func CallerFromCtx(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(CallerKey).(string)
	return v, ok && v != ""
}

func b64(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
