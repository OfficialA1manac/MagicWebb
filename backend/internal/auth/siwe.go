package auth

import (
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	siwe "github.com/spruceid/siwe-go"
)

type Verifier struct {
	Domain    string
	JWTSecret []byte
}

type Claims struct {
	Address string `json:"addr"`
	jwt.RegisteredClaims
}

// VerifyAndIssue takes raw EIP-4361 message + hex signature, validates, returns address + JWT.
func (v *Verifier) VerifyAndIssue(rawMessage, signature string) (address, token string, err error) {
	msg, err := siwe.ParseMessage(rawMessage)
	if err != nil {
		return "", "", err
	}
	if msg.GetDomain() != v.Domain {
		return "", "", errors.New("siwe: domain mismatch")
	}
	if _, err = msg.Verify(signature, &v.Domain, nil, nil); err != nil {
		return "", "", err
	}
	addr := strings.ToLower(msg.GetAddress().Hex())

	claims := Claims{
		Address: addr,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(v.JWTSecret)
	return addr, tok, err
}

// Middleware extracts JWT from Authorization: Bearer ... and stores address in locals.
func (v *Verifier) Middleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		h := c.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			return c.Next() // unauthenticated path is allowed; resolver enforces
		}
		tokStr := strings.TrimPrefix(h, "Bearer ")
		tok, err := jwt.ParseWithClaims(tokStr, &Claims{}, func(t *jwt.Token) (any, error) {
			return v.JWTSecret, nil
		})
		if err != nil || !tok.Valid {
			return fiber.NewError(fiber.StatusUnauthorized, "invalid token")
		}
		c.Locals("address", tok.Claims.(*Claims).Address)
		return c.Next()
	}
}

func AddressFromCtx(c *fiber.Ctx) (string, bool) {
	v := c.Locals("address")
	if v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok && s != ""
}
