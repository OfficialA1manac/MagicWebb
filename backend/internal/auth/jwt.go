// Package auth provides HMAC-SHA256 JWT issuance/verification and gRPC interceptors.
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

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type contextKey string

// CallerKey is the context key for the authenticated wallet address.
const CallerKey contextKey = "caller"

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

type jwtClaims struct {
	Sub string `json:"sub"` // wallet address (EIP-55)
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

// Issue signs and returns a JWT for the given wallet address with the given TTL.
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

// Verify parses and validates a JWT token; returns the wallet address on success.
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

// CallerFromCtx returns the authenticated wallet address injected by UnaryInterceptor.
func CallerFromCtx(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(CallerKey).(string)
	return v, ok && v != ""
}

// openMethods are gRPC methods that do not require authentication.
var openMethods = map[string]bool{
	"/marketplace.v1.MarketplaceService/GetListing":          true,
	"/marketplace.v1.MarketplaceService/GetCollection":       true,
	"/marketplace.v1.MarketplaceService/ListListings":        true,
	"/marketplace.v1.MarketplaceService/ListCollections":     true,
	"/marketplace.v1.MarketplaceService/GetTrending":         true,
	"/marketplace.v1.MarketplaceService/StreamListingEvents": true,
	"/auction.v1.AuctionService/GetAuction":                  true,
	"/auction.v1.AuctionService/ListAuctions":                true,
	"/auction.v1.AuctionService/StreamAuctionEvents":         true,
	"/auction.v1.AuctionService/GetServerTime":               true,
	"/offers.v1.OffersService/GetOffer":                      true,
	"/offers.v1.OffersService/ListOffers":                    true,
	"/offers.v1.OffersService/StreamOfferEvents":             true,
	"/indexer.v1.IndexerService/GetStatus":                   true,
	"/indexer.v1.IndexerService/GetBlockHeight":              true,
}

// UnaryInterceptor validates JWT for protected unary RPCs and injects caller address.
func UnaryInterceptor(secret string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if openMethods[info.FullMethod] {
			return handler(ctx, req)
		}
		addr, err := extractCaller(ctx, secret)
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "auth: %v", err)
		}
		return handler(context.WithValue(ctx, CallerKey, addr), req)
	}
}

// StreamInterceptor validates JWT for protected streaming RPCs.
func StreamInterceptor(secret string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if openMethods[info.FullMethod] {
			return handler(srv, ss)
		}
		if _, err := extractCaller(ss.Context(), secret); err != nil {
			return status.Errorf(codes.Unauthenticated, "auth: %v", err)
		}
		return handler(srv, ss)
	}
}

// RecoveryUnaryInterceptor catches panics and returns an Internal gRPC error.
func RecoveryUnaryInterceptor(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = status.Errorf(codes.Internal, "panic: %v", r)
		}
	}()
	return handler(ctx, req)
}

// RecoveryStreamInterceptor catches panics in streaming handlers.
func RecoveryStreamInterceptor(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = status.Errorf(codes.Internal, "panic: %v", r)
		}
	}()
	return handler(srv, ss)
}

func extractCaller(ctx context.Context, secret string) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", fmt.Errorf("no metadata")
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return "", fmt.Errorf("no authorization header")
	}
	raw := strings.TrimPrefix(vals[0], "Bearer ")
	return Verify(raw, secret)
}

func b64(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
