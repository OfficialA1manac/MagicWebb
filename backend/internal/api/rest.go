// Package api wires all REST handlers, CORS, rate-limiting and SSE.
package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// NewRouter returns an http.Handler mounting all REST + SSE routes.
// Excludes /healthz, /readyz, /auth/* — those stay in main.go.
func NewRouter(q *db.Q, rdb *cache.Client, cfg *config.Config) http.Handler {
	mux := http.NewServeMux()

	// marketplace
	mux.HandleFunc("GET /api/v1/listings", handleListListings(q))
	mux.HandleFunc("GET /api/v1/listings/{collection}/{id}", handleGetListing(q))
	mux.HandleFunc("GET /api/v1/collections", handleListCollections(q))
	mux.HandleFunc("GET /api/v1/collections/{address}", handleGetCollection(q))
	mux.HandleFunc("GET /api/v1/trending", handleGetTrending(q))

	// auctions
	mux.HandleFunc("GET /api/v1/auctions", handleListAuctions(q))
	mux.HandleFunc("GET /api/v1/auctions/{id}", handleGetAuction(q))
	mux.HandleFunc("GET /api/v1/server-time", handleServerTime())

	// offers
	mux.HandleFunc("GET /api/v1/offers", handleListOffers(q))
	mux.HandleFunc("POST /api/v1/offers", handleNotifyOffer(q))

	// search
	mux.HandleFunc("GET /api/v1/search", handleSearch(q))

	// metrics + activity
	mux.HandleFunc("GET /api/v1/metrics", handleGetMarketMetrics(q))
	mux.HandleFunc("GET /api/v1/activity", handleGetRecentActivity(q))

	// indexer status
	mux.HandleFunc("GET /api/v1/indexer/status", handleIndexerStatus(q))

	// SSE
	mux.HandleFunc("GET /events", handleEvents(rdb))

	return corsMiddleware(cfg.FrontendURL, rateLimitMiddleware(rdb, mux))
}

// ── CORS ──────────────────────────────────────────────────────────────────────

func corsMiddleware(frontendURL string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == frontendURL ||
			strings.HasPrefix(origin, "http://localhost:") ||
			strings.HasPrefix(origin, "http://127.0.0.1:") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── Rate limiter (Redis-backed sliding window) ────────────────────────────────

const (
	rateLimitRequests = 60
	rateLimitWindow   = time.Minute
)

func rateLimitMiddleware(rdb *cache.Client, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		ok, _ := rdb.Allow(r.Context(), "api:"+ip, rateLimitRequests, rateLimitWindow)
		if !ok {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	// strip port
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i != -1 {
		return addr[:i]
	}
	return addr
}

// ── JSON helpers ──────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "not found")
}
