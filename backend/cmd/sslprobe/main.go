// Command sslprobe verifies that the active POSTGRES_URL advertises the
// SSL posture we expect from Neon. It actively connects with three modes
// and reports the result so operators and CI can eyeball that strict-require
// TLS is fail-closed.
//
// Expected posture (matches the active production URL):
//
//   sslmode=require  -> CONNECTED   (production path; must succeed)
//   sslmode=disable  -> REJECTED    (Neon refuses plaintext)
//   sslmode=prefer   -> CONNECTED   (auto-negotiates TLS as a sanity probe)
//
// Exit 0 = gate passed. Non-zero = posture drifted from expectation; do NOT
// keep this binary as a CI step expecting green without verifying the new
// expectation lines up with whatever Neon now does.
//
// Usage:
//
//	cd backend && POSTGRES_URL=... go run ./cmd/sslprobe
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

// expected is the connection result each sslmode MUST produce against the
// active Neon Postgres direct URL. Edit this map if the deploy target's policy
// changes; the probe is intentionally explicit so the assert does not
// silently drift into "always pass".
var expected = map[string]bool{
	"require": true,  // MUST connect (the active production posture)
	"disable": false, // MUST be rejected (Neon refuses plaintext)
	"prefer":  true,  // MUST connect (auto-negotiates TLS)
}

func main() {
	raw := os.Getenv("POSTGRES_URL")
	if raw == "" {
		fmt.Fprintln(os.Stderr, "POSTGRES_URL is unset")
		os.Exit(2)
	}
	ok := true
	for _, mode := range []string{"require", "disable", "prefer"} {
		connStr, err := modeURL(raw, mode)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sslprobe: cannot parse POSTGRES_URL: %v\n", err)
			os.Exit(2)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		conn, err := pgx.Connect(ctx, connStr)
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

// modeURL returns raw with sslmode and connect_timeout overridden, preserving
// every other query parameter the operator set (options, application_name,
// pooler settings, …). Overriding — rather than stripping the whole query —
// keeps the active sslmode from leaking through without breaking a URL that
// needs its other parameters to connect at all.
func modeURL(raw, mode string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	qs := u.Query()
	qs.Set("sslmode", mode)
	qs.Set("connect_timeout", "5")
	u.RawQuery = qs.Encode()
	return u.String(), nil
}
