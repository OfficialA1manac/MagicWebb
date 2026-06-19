// Command pinsha is the deploy-time helper that fills in the
// integrity="sha384-..." attributes on the four pinned CDN scripts in
// backend/internal/ui/templates/layout.html. The structural patch
// (see the corresponding commit) replaces the prior "manual hash paste"
// failure mode by pinning the SHA-384 of the exact bytes each CDN
// currently serves; a CDN-side change between patches will reject the
// script load in production (browsers enforce a strict integrity check),
// so the operator must re-run pinsha before any CDN update.
//
// Usage:
//
//	go run ./cmd/pinsha
//
// Output (per line):
//
//	<name>:         sha384-<base64>
//
// Operator action: copy each line into the matching <script
// integrity="..." crossorigin="anonymous"> tag in layout.html.
// `crossorigin="anonymous"` is required for browsers to verify the
// cross-origin response against the integrity hash (anonymous CORS
// instead of credentials so the CDN response doesn't leak cookies).
//
// Network egress required at run time; the tool exits non-zero on any
// fetch failure so a CI step / ops runbook can halt the deploy.
package main

import (
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// pinned lists every external <script src=...> layout.html loads. Keep
// in lockstep with the <script src="..."> tags themselves — a missing
// entry here means a phantom SRI was added to layout.html without an
// associated fetch test (regression covers via the layout_sri test).
var pinned = []struct {
	Name string // logical name surfaced in operator output
	URL  string // canonical CDN URL
}{
	{"htmx", "https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js"},
	{"htmx-ext-sse", "https://unpkg.com/htmx-ext-sse@2.2.2/sse.js"},
	{"ethers-umd", "https://cdnjs.cloudflare.com/ajax/libs/ethers/6.13.4/ethers.umd.min.js"},
	{"alpinejs", "https://cdn.jsdelivr.net/npm/alpinejs@3.14.1/dist/cdn.min.js"},
}

// repoRoot returns the backend/ module root, anchored on this file's
// SOURCE location via runtime.Caller(0). Mirrors cmd/genicons and
// cmd/buildtailwindcss — using the source path (not os.Executable)
// means the helper resolves correctly under `go test` (where the
// test binary lives in a temp build cache dir without go.mod in any
// parent), under `go run` (same story, different temp path), and
// under direct invocation.
//
// Tests in this package use repoRoot to find
// internal/ui/templates/layout.html regardless of cwd.
func repoRoot() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		log.Fatal("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			log.Fatalf("could not locate go.mod above %s", thisFile)
		}
		dir = parent
	}
}

func main() {
	cli := &http.Client{Timeout: 30 * time.Second}
	anyFailed := false
	for _, p := range pinned {
		integrity, err := fetchIntegrity(cli, p.URL)
		if err != nil {
			log.Printf("%-12s: failed (%v)", p.Name+":", err)
			anyFailed = true
			continue
		}
		fmt.Fprintf(os.Stdout, "%-12s %s\n", p.Name+":", integrity)
	}
	if anyFailed {
		os.Exit(1)
	}
}

// fetchIntegrity downloads url and returns the SRI-formatted integrity
// digest ("sha384-<base64>"). Errors are bubbled up so the main loop can
// tally failures; integrity values shown elsewhere are computed via
// sha384Integrity (mirror function) so tests don't need the network.
func fetchIntegrity(cli *http.Client, url string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	// Friendly UA so CDN operators can see what traffic is pinsha vs.
	// browser load; sometimes they throttle identify-by-User-Agent flows.
	req.Header.Set("User-Agent", "MagicWebb-cmd/pinsha")
	resp, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("non-2xx %d for %s", resp.StatusCode, url)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return sha384Integrity(body), nil
}

// sha384Integrity is the canonical sha384→base64 transform used both
// at operator-run-time and at test-time. Split out so tests can pin the
// format without making a network call.
//
// SHA-384 is implemented as Sum384 on the crypto/sha512 package in
// Go's stdlib (there is no crypto/sha384 package — it's a SHA-2
// truncation). Using Sum384 keeps the function signature stable if
// stdlib ever offers a sha384 alias.
func sha384Integrity(body []byte) string {
	sum := sha512.Sum384(body)
	return "sha384-" + base64.StdEncoding.EncodeToString(sum[:])
}
