package interceptors

import (
	"context"
	"net"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/ratelimit"
)

// RateLimitTier maps Connect-RPC procedures to their rate limits.
// Unlisted procedures use the Default tier.
type RateLimitTier struct {
	Limit  int
	Window time.Duration
}

// DefaultRateLimits returns the standard tiered rate limits for all
// MarketplaceService procedures. List endpoints (120/min) are more generous
// than search (30/min); low-volume endpoints (GetListing, GetAuction, etc.)
// get the standard 60/min. Admin/expensive endpoints get 10/min.
//
// Per the Full Stack Optimization Matrix RL-1:
//   - /api/v1/listings = 120/min (list-heavy pages load 48 cards each)
//   - /api/v1/search    = 30/min  (expensive full-text queries)
//   - /api/v1/admin/*   = 10/min  (privileged operations)
func DefaultRateLimits() map[string]RateLimitTier {
	return map[string]RateLimitTier{
		// List endpoints — generous limits for page loads.
		"/marketplace.v1.MarketplaceService/ListCollections": {120, time.Minute},
		"/marketplace.v1.MarketplaceService/ListListings":    {120, time.Minute},
		"/marketplace.v1.MarketplaceService/ListAuctions":    {120, time.Minute},
		"/marketplace.v1.MarketplaceService/ListOffers":      {120, time.Minute},
		"/marketplace.v1.MarketplaceService/GetActivity":     {120, time.Minute},

		// Single-entity lookups — standard limit.
		"/marketplace.v1.MarketplaceService/GetListing":    {60, time.Minute},
		"/marketplace.v1.MarketplaceService/GetAuction":    {60, time.Minute},
		"/marketplace.v1.MarketplaceService/GetOffer":       {60, time.Minute},
		"/marketplace.v1.MarketplaceService/GetToken":       {60, time.Minute},
		"/marketplace.v1.MarketplaceService/GetCollection":  {60, time.Minute},
		"/marketplace.v1.MarketplaceService/GetProfile":     {60, time.Minute},
		"/marketplace.v1.MarketplaceService/GetWalletNFTs":  {60, time.Minute},
		"/marketplace.v1.MarketplaceService/GetMetrics":     {60, time.Minute},

		// Search — expensive full-text query, tighter limit.
		"/marketplace.v1.MarketplaceService/Search": {30, time.Minute},
	}

	// Unknown procedures default to 60/min (standard tier).
}

// TieredRateLimitInterceptor returns a Connect-RPC unary interceptor that
// applies per-procedure rate limits. This closes the DoS vector where an
// attacker could hammer ListCollections or Search without any limit.
//
// When tiers is nil, uses DefaultRateLimits(). The default tier for unknown
// procedures is 60/min.
func TieredRateLimitInterceptor(rl *ratelimit.Limiter, tiers map[string]RateLimitTier) connect.UnaryInterceptorFunc {
	if tiers == nil {
		tiers = DefaultRateLimits()
	}
	defaultTier := RateLimitTier{Limit: 60, Window: time.Minute}

	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			ip := clientIPFromRequest(req)
			if ip == "" {
				ip = "unknown"
			}

			tier, ok := tiers[req.Spec().Procedure]
			if !ok {
				tier = defaultTier
			}

			if !rl.Allow(ip+"|"+req.Spec().Procedure, tier.Limit, tier.Window) {
				return nil, connect.NewError(connect.CodeResourceExhausted,
					errString("rate limit exceeded"))
			}
			return next(ctx, req)
		}
	}
}

// clientIPFromRequest extracts the client IP from a Connect-RPC request.
// Trust hierarchy mirrors api.ClientIP (audit `clientIpSpoof` P1):
//
//  1. Fly-Client-IP — Fly.io's reverse-proxy-stamped header; the edge
//     strips any inbound copy, so clients cannot forge it.
//  2. The transport peer address — derived from the connection itself,
//     not from headers.
//
// Forwarded / X-Forwarded-For / X-Real-IP are attacker-controlled input:
// rotating their values minted a fresh rate-limit bucket per request,
// reversing the DoS protection, so they are deliberately NOT consulted.
func clientIPFromRequest(req connect.AnyRequest) string {
	if v := strings.TrimSpace(req.Header().Get("Fly-Client-IP")); v != "" && net.ParseIP(v) != nil {
		return v
	}
	if addr := req.Peer().Addr; addr != "" {
		if host, _, err := net.SplitHostPort(addr); err == nil {
			return host
		}
		return addr
	}
	return ""
}

func errString(msg string) error {
	return &stringError{msg}
}

type stringError struct{ s string }

func (e *stringError) Error() string { return e.s }
