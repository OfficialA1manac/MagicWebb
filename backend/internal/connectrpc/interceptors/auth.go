package interceptors

import (
	"context"
	"strings"

	"connectrpc.com/connect"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
)

// AuthInterceptor returns a Connect-RPC unary interceptor that verifies JWT
// tokens AND API keys (AUTH-3) from request headers. This provides a central
// auth check for all gRPC handlers, eliminating per-handler JWT verification
// duplication.
//
// When secret is empty, the interceptor is a no-op pass-through (useful for
// tests and development where auth is handled at the HTTP layer).
//
// Currently gRPC handlers are all public-read, so the interceptor extracts
// the caller address for audit/rate-limiting context but does NOT reject
// unauthenticated requests. When write-path RPCs are added (create listing,
// place bid, etc.), set requireAuth=true to enforce authentication.
//
// When apiKeyStore is non-nil, the interceptor also checks for API keys
// (X-API-Key header or Authorization: Bearer mw_...). API keys authenticate
// as machine identities with scoped permissions.
func AuthInterceptor(secret string, requireAuth bool) connect.UnaryInterceptorFunc {
	return AuthInterceptorWithAPIKeys(secret, requireAuth, nil, nil)
}

// AuthInterceptorWithAPIKeys extends AuthInterceptor with API key support
// (AUTH-3). When apiKeyStore and auditLog are non-nil, the interceptor
// checks for API keys before falling through to JWT verification.
func AuthInterceptorWithAPIKeys(secret string, requireAuth bool, apiKeyStore auth.APIKeyStore, auditLog auth.AuditLogger) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if secret == "" {
				return next(ctx, req)
			}

			// ── API key check (AUTH-3) ──────────────────────────────────
			if apiKeyStore != nil {
				apiKey := extractAPIKey(req)
				if apiKey != "" && auth.ValidateAPIKeyFormat(apiKey) {
					info, err := auth.VerifyAndHash(ctx, apiKeyStore, apiKey)
					if err == nil {
						// Auth via API key — attach key info to context.
						ctx = context.WithValue(ctx, auth.CallerKey, "apikey:"+info.Label)
						if auditLog != nil {
							auth.AuditAPIKeyVerified(auditLog, info.ID,
								clientIPFromRequest(req),
								req.Header().Get("User-Agent"),
								info.Label)
						}
						return next(ctx, req)
					}
					if auditLog != nil {
						auth.AuditAPIKeyFailed(auditLog,
							clientIPFromRequest(req),
							req.Header().Get("User-Agent"),
							"invalid_key")
					}
				}
			}

			// ── JWT check (existing) ───────────────────────────────────
			raw := req.Header().Get("Authorization")
			var token string
			if strings.HasPrefix(raw, "Bearer ") {
				bearer := strings.TrimPrefix(raw, "Bearer ")
				// Skip API keys in Bearer header — already handled above.
				if !strings.HasPrefix(bearer, auth.APIKeyPrefix) {
					token = bearer
				}
			}
			// Also check cookies (for browser-initiated gRPC-Web calls).
			if token == "" {
				if cookie := req.Header().Get("Cookie"); cookie != "" {
					token = extractSessionCookie(cookie)
				}
			}

			if token != "" {
				addr, err := auth.VerifyAccessToken(token, secret)
				if err == nil {
					ctx = context.WithValue(ctx, auth.CallerKey, addr)
				}
				if err != nil && requireAuth {
					return nil, connect.NewError(connect.CodeUnauthenticated, err)
				}
			} else if requireAuth {
				return nil, connect.NewError(connect.CodeUnauthenticated,
					errString("authentication required"))
			}

			return next(ctx, req)
		}
	}
}

// extractAPIKey pulls the API key from X-API-Key header or Authorization: Bearer mw_...
func extractAPIKey(req connect.AnyRequest) string {
	// 1. X-API-Key header (standard machine-to-machine header).
	if v := strings.TrimSpace(req.Header().Get("X-API-Key")); v != "" {
		return v
	}
	// 2. Authorization: Bearer mw_... (API key in Bearer format).
	if raw := req.Header().Get("Authorization"); strings.HasPrefix(raw, "Bearer ") {
		bearer := strings.TrimPrefix(raw, "Bearer ")
		if strings.HasPrefix(bearer, auth.APIKeyPrefix) {
			return bearer
		}
	}
	return ""
}

// extractSessionCookie pulls the JWT from wallet-bound session cookies.
// Mirrors the cookie-scanning logic in api/rest.go and ws/handler.go.
func extractSessionCookie(cookieHeader string) string {
	for _, part := range strings.Split(cookieHeader, ";") {
		p := strings.TrimSpace(part)
		if !strings.HasPrefix(p, "mw_s_") {
			continue
		}
		eq := strings.IndexByte(p, '=')
		if eq <= 0 {
			continue
		}
		return p[eq+1:]
	}
	return ""
}
