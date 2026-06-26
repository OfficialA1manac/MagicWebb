// Package main — post-build assertions for internal/ui/static/tailwind.css.
//
// Why: MagicWebb replaced the public `cdn.tailwindcss.com` runtime JIT
// loader (multimegabyte JS evaluating CSS in every browser + XSS
// surface if the CDN were compromised) with a static pre-built bundle
// emitted by `cmd/buildtailwindcss`. The build script downloads the
// Tailwind standalone CLI from GitHub Releases, runs it against the
// `internal/ui/src.css` + `internal/ui/tailwind.config.cjs` configs
// with `--content templates/**/*.html`, and writes the tree-shaken
// artefact to `internal/ui/static/tailwind.css`.
//
// This file guards against a future refactor silently regressing the
// build to a no-op (empty/truncated/stale artefact would restore the
// broken-JIT behaviour at first page load). The assertions are tiny
// and run as part of `go test ./cmd/buildtailwindcss/...` — i.e.
// they fire automatically under our existing test runner, without
// needing a CI hook. Three things are checked:
//
//  1. The artefact exists (not deleted).
//  2. Its size is >= 10 KB (the real build emits ~50 KB on the current
//     template glob; a 4 KB or 0-byte artefact means a CDN-fetch glitch,
//     a half-write, or a deliberate stub).
//  3. Its body carries BOTH distinctive Tailwind v3+ sentinel class
//     names AND core preflight markers — the combination is what
//     distinguishes a real Tailwind v3.x bundle from any other CSS-ish
//     file or a `<script src="cdn.tailwindcss.com">` JIT-loader
//     fallback that someone might have wired back up.
//
// None of these checks re-run the build; they only verify whatever
// `cmd/buildtailwindcss` last wrote. Pair the assertions with the
// build script in the dev workflow:
//
//	cd backend && go run ./cmd/buildtailwindcss && go test ./cmd/buildtailwindcss/...
package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// cssPath resolves the artefact relative to this test file. Because
// `go test ./cmd/buildtailwindcss/...` runs the test binary from the
// package directory, the relative `../../internal/ui/static/...`
// path works — but we make it absolute via the test file's runtime
// caller info so the test still works if the runner changes cwd.
func cssPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed; cannot resolve artefact path")
	}
	p := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "frontend", "static", "tailwind.css")
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatalf("filepath.Abs(%q): %v", p, err)
	}
	return abs
}

// minTailwindBytes is the floor below which we treat the artefact as
// a regression. The real build emits ~50–55 KB on the current
// template glob, so 10 KB gives us plenty of headroom to catch both
// truncations and CDN-fetch glitches while remaining well below the
// real build size.
const minTailwindBytes = 10 * 1024

// mustContainSentinels are Tailwind utility classes whose presence
// in the output is ONLY guaranteed by a successful `--content` scan
// of the template glob. Pick rationale:
//
//   - `.bg-violet-500\/15` — the escaped forward-slash is a
//     Tailwind v3.0+ feature for slash-opacity modifiers; matches are
//     extremely unlikely to appear via hand-rolled CSS or third-party
//     frameworks. Used by layout.html for the Auctions nav-link hover
//     state.
//   - `.border-gold-300\/40` — gold is a custom palette token
//     declared in `tailwind.config.cjs` (`theme.extend.colors.gold`).
//     A literal `gold` gradient name resolves only via the Tailwind
//     config + scanner path; hand-rolled CSS would not emit it under
//     that exact selector.
//
// If either is missing the user almost certainly will see visual
// regressions on a vanilla `go run ./cmd/buildtailwindcss` re-run,
// which is the exact regression we want to catch early.
var mustContainSentinels = []string{
	".bg-violet-500\\/15",
	".border-gold-300\\/40",
}

// mustContainPreflightMarkers are core Tailwind preflight declarations
// present in every v3.x build. They distinguish a real Tailwind v3.x
// bundle from a stale/empty/placebo file (e.g. someone writes `/*
// see https://tailwindcss.com */` to `tailwind.css` to "fix" a build
// error, or a future refactor accidentally rebinds the route to serve
// a JavaScript-shaped payload instead of CSS).
var mustContainPreflightMarkers = []string{
	"box-sizing:border-box", // preflight base reset
	"--tw-border-spacing-x", // Tailwind's CSS-variable engine marker
	"--tw-translate-x",      // transform utility plumbing
}

func TestTailwindBundleExists(t *testing.T) {
	p := cssPath(t)
	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("tailwind.css missing at %s: %v\n"+
			"This file is emitted by `go run ./cmd/buildtailwindcss`. Re-run:\n"+
			"    cd backend && go run ./cmd/buildtailwindcss",
			p, err)
	}
	t.Logf("tailwind.css present at %s (%d bytes)", p, st.Size())
}

func TestTailwindBundleIsNonTrivial(t *testing.T) {
	p := cssPath(t)
	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Size() < minTailwindBytes {
		t.Fatalf("tailwind.css is only %d bytes; >= %d required.\n"+
			"The Tailwind standalone CLI build appears to have produced a truncated or stale artefact.\n"+
			"Re-run:  cd backend && go run ./cmd/buildtailwindcss",
			st.Size(), minTailwindBytes)
	}
	t.Logf("tailwind.css size %d bytes >= %d-byte floor", st.Size(), minTailwindBytes)
}

func TestTailwindBundleHasSentinelClasses(t *testing.T) {
	p := cssPath(t)
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	haystack := string(body)
	var missing []string
	for _, s := range mustContainSentinels {
		if !strings.Contains(haystack, s) {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("tailwind.css is missing sentinel classes: %v\n"+
			"These classes are emitted by Tailwind scanning internal/ui/templates/**/*.html.\n"+
			"Absence means the build did not scan the templates (truncated artefact, stale cache, or no-op build script).\n"+
			"Re-run:  cd backend && go run ./cmd/buildtailwindcss",
			missing)
	}
	t.Logf("tailwind.css contains all %d sentinel classes (%v)",
		len(mustContainSentinels), mustContainSentinels)
}

func TestTailwindBundleHasPreflightMarkers(t *testing.T) {
	p := cssPath(t)
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	haystack := string(body)
	var missing []string
	for _, m := range mustContainPreflightMarkers {
		if !strings.Contains(haystack, m) {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("tailwind.css is missing preflight markers: %v\n"+
			"Absence means the file is not a real Tailwind v3.x bundle.\n"+
			"Re-run:  cd backend && go run ./cmd/buildtailwindcss",
			missing)
	}
	t.Logf("tailwind.css contains all %d preflight markers", len(mustContainPreflightMarkers))
}
