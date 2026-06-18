// Command sslprobe verifies that the active POSTGRES_URL advertises the
// SSL posture we expect from Supabase. It actively connects with three modes
// and reports the result so operators and CI can eyeball that strict-require
// TLS is fail-closed.
//
// Expected posture (matches the active production URL):
//
//   sslmode=require  -> CONNECTED   (production path; must succeed)
//   sslmode=disable  -> REJECTED    (Supabase refuses plaintext)
//   sslmode=prefer   -> CONNECTED   (auto-negotiates TLS as a sanity probe)
//
// Exit 0 = gate passed. Non-zero = posture drifted from expectation; do NOT
// keep this binary as a CI step expecting green without verifying the new
// expectation lines up with whatever Supabase now does.
//
// Usage:
//
//	cd backend && POSTGRES_URL=... go run ./cmd/sslprobe
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// expected is the connection result each sslmode MUST produce against the
// active Supabase direct URL. Edit this map if the deploy target's policy
// changes; the probe is intentionally explicit so the assert does not
// silently drift into "always pass".
var expected = map[string]bool{
	"require": true,  // MUST connect (the active production posture)
	"disable": false, // MUST be rejected (Supabase refuses plaintext)
	"prefer":  true,  // MUST connect (auto-negotiates TLS)
}

func main() {
	raw := os.Getenv("POSTGRES_URL")
	if raw == "" {
		fmt.Fprintln(os.Stderr, "POSTGRES_URL is unset")
		os.Exit(2)
	}
	base := stripQuery(raw)

	ok := true
	for _, mode := range []string{"require", "disable", "prefer"} {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		conn, err := pgx.Connect(ctx, fmt.Sprintf("%s?sslmode=%s&connect_timeout=5", base, mode))
		if err != nil {
			fmt.Printf("sslmode=%-8s REJECTED  %v\n", mode, err)
			if expected[mode] {
				ok = false
			}
		} else {
			fmt.Printf("sslmode=%-8s CONNECTED\n", mode)
			if !expected[mode] {
				ok = false
			}
			conn.Close(ctx)
		}
		cancel()
	}

	if !ok {
		fmt.Fprintln(os.Stderr, "sslprobe: posture drifted from expectation — refusing to bless this URL")
		os.Exit(1)
	}
}

// stripQuery removes the query string from a URL (everything after '?').
// We re-derive the query per-mode so the active sslmode never leaks through.
func stripQuery(s string) string {
	if i := strings.Index(s, "?"); i >= 0 {
		return s[:i]
	}
	return s
}
