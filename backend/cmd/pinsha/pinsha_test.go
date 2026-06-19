package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestSha384IntegrityFormat pins the SRI base64 string format. Failing
// this test means the encoding or prefix drifted in a way that browsers
// would reject (RawURLEncoding would produce unpadded sha384 values that
// browsers flag as malformed; dropping the "sha384-" prefix changes why
// a hash is computed and silently disables the integrity check).
func TestSha384IntegrityFormat(t *testing.T) {
	body := []byte("hello world")
	got := sha384Integrity(body)
	if !strings.HasPrefix(got, "sha384-") {
		t.Fatalf("integrity must start with 'sha384-', got %q", got)
	}
	// 48-byte digest (sha384) means a 64-char base64 string with NO
	// '=' padding — because 48 % 3 == 0 exactly, the digest encodes
	// to an integer number of 4-char base64 groups. SHA-256 (32 bytes,
	// 32 % 3 == 2) uses '=' padding; SHA-512 (64 bytes, 64 % 3 == 1)
	// uses '==' padding; SHA-384 sits between and uses neither.
	hashPart := strings.TrimPrefix(got, "sha384-")
	if len(hashPart) != 64 {
		t.Fatalf("sha384-in-base64 must be 64 chars (48-byte digest), got %d (%q)", len(hashPart), got)
	}
	if strings.HasSuffix(hashPart, "=") {
		t.Fatalf("sha384 base64 must NOT have '=' padding (48 bytes mod 3 == 0); got %q", got)
	}
	for _, c := range hashPart {
		if !isBase64Char(byte(c)) {
			t.Fatalf("non-base64 char %q in integrity %q", c, got)
		}
	}
}

// TestSha384IntegrityDeterministic. Same bytes must produce the same
// digest on every invocation — protects against accidentally turning
// sha384Integrity into a stateful function (e.g., adding a nonce).
func TestSha384IntegrityDeterministic(t *testing.T) {
	body := []byte("magicwebb-test-fixture")
	a := sha384Integrity(body)
	b := sha384Integrity(body)
	if a != b {
		t.Fatalf("sha384Integrity non-deterministic: %q vs %q", a, b)
	}
}

// TestSha384IntegrityChanges. Different bytes must produce different
// digests — protects against an accidental no-op (e.g., returning a
// constant). sha384 collisions in random inputs are vanishingly rare;
// a constant return would surface here immediately.
func TestSha384IntegrityChanges(t *testing.T) {
	a := sha384Integrity([]byte("alpha"))
	b := sha384Integrity([]byte("beta"))
	if a == b {
		t.Fatalf("sha384Integrity collision on different bodies: both %q", a)
	}
}

// TestPinnedMirrorsCoverLayoutScripts. The four URLs in pinned[] must
// mirror the four <script src=...> tags in layout.html. Drift between
// the two silently leaves a CDN script without an integrity check.
func TestPinnedMirrorsCoverLayoutScripts(t *testing.T) {
	want := map[string]string{
		"htmx":         "https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js",
		"htmx-ext-sse": "https://unpkg.com/htmx-ext-sse@2.2.2/sse.js",
		"ethers-umd":   "https://cdnjs.cloudflare.com/ajax/libs/ethers/6.13.4/ethers.umd.min.js",
		"alpinejs":     "https://cdn.jsdelivr.net/npm/alpinejs@3.14.1/dist/cdn.min.js",
	}
	if len(pinned) != len(want) {
		t.Fatalf("pinned has %d entries, want %d (drift between cmd/pinsha and layout.html)",
			len(pinned), len(want))
	}
	for _, p := range pinned {
		expected, ok := want[p.Name]
		if !ok {
			t.Fatalf("pinned has unexpected entry %q", p.Name)
		}
		if p.URL != expected {
			t.Fatalf("pinned[%q].URL = %q, want %q", p.Name, p.URL, expected)
		}
	}
}

func isBase64Char(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') || c == '+' || c == '/'
}

// ── Structural SRI tests on layout.html ───────────────────────────────────────
//
// The SRI patch is enforced by the browser at load time on production,
// but we ALSO need CI checks that the static template still wires:
//
//   1. integrity="sha384-<64 base64 chars>" on every pinned CDN <script>
//   2. crossorigin="anonymous" on the same tag (SRI silently skipped
//      without it on cross-origin scripts)
//   3. Distinct integrity values per slot (cross-paste detection)
//   4. No `@N.x.x` / `@latest` float-version pins (SRI is incompatible
//      with CDN-driven version drift)
//
// If any of these fail, the patch is no longer protective and
// production is silently vulnerable again.
//
// Strategy: each test compiles a tight per-slot regex (anchored on the
// pinned src URL via regexp.QuoteMeta) and asserts on the matched
// substring. Avoids the LastIndex/Index "span between two tags" trap
// the previous helper-based implementation hit, where non-first slots
// returned a span covering TWO tags and FindStringSubmatch then
// plucked the wrong slot's integrity.

// layoutPath resolves backend/internal/ui/templates/layout.html from
// any cwd, anchored on go.mod via repoRoot().
func layoutPath(t *testing.T) string {
	t.Helper()
	root := repoRoot()
	p := root + string(os.PathSeparator) + "internal" + string(os.PathSeparator) + "ui" + string(os.PathSeparator) + "templates" + string(os.PathSeparator) + "layout.html"
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("layout.html not found at %s: %v", p, err)
	}
	return p
}

// pinnedSlot is the per-url test surface. Matches cmd/pinsha/main.go's
// `pinned` slice once more, defensively — TestPinnedMirrorsCoverLayoutScripts
// catches drift between this list and main.go's `pinned`, and the
// per-layout tests below use this same list to drive assertions.
type pinnedSlot struct {
	name string // logical name surfaced in failure messages
	src  string // canonical CDN URL
}

// TestLayoutHasSRIOnEveryPinnedScript enforces: for each pinned slot,
// there is exactly one <script src="…"> open tag in layout.html.
// (We split the integrity+crossorigin assertion off so a missing
// SRI attribute is reported by NAME rather than as a single
// monolithic "tag regex didn't match" failure that hides which
// attribute drifted.)
func TestLayoutHasSRIOnEveryPinnedScript(t *testing.T) {
	layoutBytes, err := os.ReadFile(layoutPath(t))
	if err != nil {
		t.Fatal(err)
	}
	layout := string(layoutBytes)

	slots := pinnedSlots()
	tagOnlyRE := regexp.MustCompile(`<script\s[^>]*>`)
	integrityRE := regexp.MustCompile(`integrity="(sha384-[A-Za-z0-9+/]{64})"`)
	crossoriginRE := regexp.MustCompile(`crossorigin="anonymous"`)

	failures := []string{}
	for _, s := range slots {
		// Find the <script …> tag whose src matches this slot's URL.
		var tag string
		for _, candidate := range tagOnlyRE.FindAllString(layout, -1) {
			if strings.Contains(candidate, `src="`+s.src+`"`) {
				if strings.Count(candidate, "src=") > 1 {
					failures = append(failures, fmt.Sprintf(
						"%s: script tag has multiple src attrs:\n  %s", s.name, candidate))
				}
				tag = candidate
				break
			}
		}
		if tag == "" {
			failures = append(failures, fmt.Sprintf(
				"%s: <script src=%q> not found in layout.html", s.name, s.src))
			continue
		}
		if !integrityRE.MatchString(tag) {
			failures = append(failures, fmt.Sprintf(
				"%s: <script src=%q> tag is missing integrity=\"sha384-<64 base64 chars>\" (or the value isn't 64 chars). Actual tag:\n  %s",
				s.name, s.src, tag))
		}
		if !crossoriginRE.MatchString(tag) {
			failures = append(failures, fmt.Sprintf(
				"%s: <script src=%q> tag is missing crossorigin=\"anonymous\" (SRI is silently skipped without it on cross-origin scripts). Actual tag:\n  %s",
				s.name, s.src, tag))
		}
	}
	if len(failures) > 0 {
		t.Fatalf("layout.html SRI wiring broken (failures: %d):\n  %s",
			len(failures), strings.Join(failures, "\n  "))
	}
}

// TestLayoutIntegritySlotsAreDistinct enforces that the four
// pinned SRI slots hold DISTINCT integrity strings — i.e. no two
// slots share their integrity="…" value. Cross-paste operator
// errors leave every slot with a structurally-valid 64-char base64
// string, so TestLayoutHasSRIOnEveryPinnedScript passes silently
// while production breaks because the browser computes each real
// CDN SHA-384 and only the slot whose real hash happens to be
// the cross-pasted value actually loads. Distinct placeholders
// (sha384-AAAA…64, sha384-BBBB…64, sha384-CCCC…64, sha384-DDDD…64)
// expose the mistake here regardless of when the real hashes are
// filled in.
func TestLayoutIntegritySlotsAreDistinct(t *testing.T) {
	layoutBytes, err := os.ReadFile(layoutPath(t))
	if err != nil {
		t.Fatal(err)
	}
	layout := string(layoutBytes)

	slots := pinnedSlots()
	tagOnlyRE := regexp.MustCompile(`<script\s[^>]*>`)
	integrityRE := regexp.MustCompile(`integrity="(sha384-[A-Za-z0-9+/]{64})"`)

	seen := map[string]string{} // integrity → slot name
	failures := []string{}
	for _, s := range slots {
		var tag string
		for _, candidate := range tagOnlyRE.FindAllString(layout, -1) {
			if strings.Contains(candidate, `src="`+s.src+`"`) {
				tag = candidate
				break
			}
		}
		if tag == "" {
			continue // already reported by TestLayoutHasSRIOnEveryPinnedScript
		}
		m := integrityRE.FindStringSubmatch(tag)
		if len(m) < 2 {
			continue // already reported
		}
		integrity := m[1]
		if prev, dup := seen[integrity]; dup {
			failures = append(failures, fmt.Sprintf(
				"SRI slot COLLISION: %s and %s both hold integrity %q. Likely cross-paste operator error — paste each `name: sha384-…` line from `go run ./cmd/pinsha` into the integrity=\"…\" attribute of the matching script in turn.",
				prev, s.name, integrity))
		}
		seen[integrity] = s.name
	}
	if len(failures) > 0 {
		t.Fatalf("layout.html SRI slot collisions (failures: %d):\n  %s",
			len(failures), strings.Join(failures, "\n  "))
	}
	if len(seen) != len(slots) {
		t.Fatalf("found %d distinct integrity slots in layout.html, want %d (this means a slot has zero integrity — see TestLayoutHasSRIOnEveryPinnedScript)",
			len(seen), len(slots))
	}
}

// TestLayoutNoFloatVersionPinsOnCDNScripts guards against reverting
// any pinned CDN script back to a float-version pin. Float pins like
// `@3.x.x` resolve to whatever the registry thinks "latest 3.x" is
// at fetch time — a CDN-side minor bump would change the actual
// script bytes and (because of SRI enforcement at the browser)
// silently break production. This test fails if a `@N.x.x` (or
// `@latest`) pattern reappears on a pinned script src.
func TestLayoutNoFloatVersionPinsOnCDNScripts(t *testing.T) {
	layoutBytes, err := os.ReadFile(layoutPath(t))
	if err != nil {
		t.Fatal(err)
	}
	layout := string(layoutBytes)
	floatPin := regexp.MustCompile(`/@(\d+)\.x\.x/`)
	if m := floatPin.FindStringIndex(layout); m != nil {
		t.Fatalf("layout.html has a float-version pin (e.g. @3.x.x) on a CDN script; SRI requires exact-version pins. Conflict at byte %d–%d:\n  %s",
			m[0], m[1], layout[m[0]:m[1]])
	}
	if strings.Contains(layout, "/@latest/") {
		t.Fatalf("layout.html has an @latest pin on a CDN script; SRI requires exact-version pins")
	}
}

// pinnedSlots is the source-of-truth list of CDN scripts the SRI
// patch protects. Drives both the per-slot regex assertions above
// AND the duplicate-detection in TestLayoutIntegritySlotsAreDistinct.
// Mirrors cmd/pinsha/main.go's `pinned` slice.
func pinnedSlots() []pinnedSlot {
	out := make([]pinnedSlot, 0, len(pinned))
	for _, p := range pinned {
		out = append(out, pinnedSlot{name: p.Name, src: p.URL})
	}
	return out
}
