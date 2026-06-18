// Command buildtailwindcss produces the static `tailwind.css` that
// backend/internal/ui/templates/layout.html loads instead of the JIT
// compiler (https://cdn.tailwindcss.com). The JIT script was a multi-MB
// runtime tailwind-loader evaluating CSS rules in every browser, AND
// introduced an XSS surface if cdn.tailwindcss.com were ever compromised.
// This command compiles the same Tailwind theme + utility classes into
// a static CSS bundle at build time, dropping both costs.
//
// Usage:
//
//	go run ./cmd/buildtailwindcss
//
// Run before first deploy and any time:
//   - a Tailwind config field changes (internal/ui/tailwind.config.cjs)
//   - a template's class list changes (internal/ui/templates/**/*.html)
// The output CSS is committed to git so prod never needs to invoke the
// JIT compiler at runtime.
//
// Network egress required: we download the Tailwind standalone CLI
// binary from the official GitHub release so go run is the only command
// the operator needs. Cross-compiled as a single static Go binary, the
// standalone CLI runs without Node/npm — important for the project's
// "Go-only" constraint.
package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Pinned to a stable Tailwind release. Bump in lockstep with
// internal/ui/tailwind.config.cjs content if features used by the
// project require a newer minor.
const tailwindVersion = "v3.4.10"

// repoRoot returns the backend/ module root. Mirrors cmd/genicons —
// uses runtime.Caller(0) on the *source* file rather than
// os.Executable() because the latter returns the path of the
// compiled binary (a temp test binary under `go test`, a temp
// build cache path under `go run`), neither of which has `go.mod`
// in any parent. Caller-based anchoring makes the wrapper callable
// from any cwd and from any build/test invocation.
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

// downloadTailwindCLI fetches the standalone binary the pinned Tailwind
// release ships to a temp directory and returns the executable path.
// Returns error if the host OS/arch is not supported (we ship only the
// Linux & macOS CLI binaries; Windows builds are typically done via
// `npm install -D tailwindcss` since the project doesn't run on
// Windows in production).
//
// Every binary that lands on disk (cache hit or fresh download) goes
// through verifyTailwindCLI, which fetches the
//   <release>/<assetName>.sha256
// companion file from the same release directory and refuses the
// binary on any mismatch. Supply-chain defense: a MITM between the
// operator's host and github.com, a compromised release artifact, or
// a poisoned /tmp cache entry all fail closed.
func downloadTailwindCLI() (string, error) {
	host := runtime.GOOS
	arch := runtime.GOARCH
	if host != "linux" && host != "darwin" {
		return "", fmt.Errorf("buildtailwindcss does not ship a Windows CLI binary; install Tailwind via npm on Windows hosts")
	}
	var assetName string
	switch host + "/" + arch {
	case "linux/amd64":
		assetName = fmt.Sprintf("tailwindcss-%s-linux-x64", tailwindVersion)
	case "linux/arm64":
		assetName = fmt.Sprintf("tailwindcss-%s-linux-arm64", tailwindVersion)
	case "darwin/amd64":
		assetName = fmt.Sprintf("tailwindcss-%s-macos-x64", tailwindVersion)
	case "darwin/arm64":
		assetName = fmt.Sprintf("tailwindcss-%s-macos-arm64", tailwindVersion)
	default:
		return "", fmt.Errorf("unsupported host/arch: %s/%s", host, arch)
	}
	url := fmt.Sprintf("https://github.com/tailwindlabs/tailwindcss/releases/download/%s/%s.tar.gz", tailwindVersion, assetName)
	dst := filepath.Join(os.TempDir(), assetName)

	cli := &http.Client{Timeout: 60 * time.Second}

	// Cache hit — still verify (cached binaries can be poisoned or
	// bit-rot). On verification failure, drop the cache and fall
	// through to a fresh download so a single failed run doesn't
	// permanently brick the build.
	if st, err := os.Stat(dst); err == nil && st.Mode().IsRegular() {
		if vErr := verifyTailwindCLI(cli, dst, assetName); vErr == nil {
			return dst, nil
		} else {
			log.Printf("cached tailwind binary failed verification (%v); removing and re-downloading", vErr)
			_ = os.Remove(dst)
		}
	}

	resp, err := cli.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("non-2xx %d for %s", resp.StatusCode, url)
	}
	// Stream into a tar.gz reader; the archive has a single top-level
	// directory (named after the asset) with the executable inside.
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var found bool
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// The executable lives at <assetName>/tailwindcss — copy only
		// that one file out, ignore anything else.
		if filepath.Base(hdr.Name) != "tailwindcss" {
			continue
		}
		// Write to a staging path so a half-written file never gets
		// confused for the real cache entry — verifyTailwindCLI gates
		// the rename to dst, and rename is atomic on the same FS.
		staging := dst + ".staging"
		out, err := os.OpenFile(staging, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", fmt.Errorf("open %s: %w", staging, err)
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			_ = os.Remove(staging)
			return "", fmt.Errorf("copy: %w", err)
		}
		if err := out.Close(); err != nil {
			return "", err
		}
		if vErr := verifyTailwindCLI(cli, staging, assetName); vErr != nil {
			_ = os.Remove(staging)
			return "", fmt.Errorf("verify staged binary: %w", vErr)
		}
		if err := os.Rename(staging, dst); err != nil {
			return "", fmt.Errorf("rename %s → %s: %w", staging, dst, err)
		}
		found = true
		break
	}
	if !found {
		return "", errors.New("tailwindcss executable not found in tarball")
	}
	return dst, nil
}

// verifyTailwindCLI compares the SHA-256 of binaryPath against the
// expected hash published in the same release directory as a
// companion .sha256 file. Returns nil on match; non-nil error on any
// mismatch, malformed companion file, or fetch failure. Used on
// both cache hits and freshly-downloaded binaries — see
// downloadTailwindCLI.
func verifyTailwindCLI(cli *http.Client, binaryPath, assetName string) error {
	shaURL := fmt.Sprintf("https://github.com/tailwindlabs/tailwindcss/releases/download/%s/%s.sha256",
		tailwindVersion, assetName)
	resp, err := cli.Get(shaURL)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", shaURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("non-2xx %d for %s", resp.StatusCode, shaURL)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read .sha256: %w", err)
	}
	// Companion file format is the standard `sha256sum` output:
	//   "<hex_hash>    0a   0a   0a   <assetName>"
	// i.e. one line of `<hash>  <whitespace>  <path>` where the path
	// can include multiple separators (Tailwind uses 0x00 padding).
	// We only need the first whitespace-delimited field and require
	// it to be a 64-char hex string (SHA-256 digest). Filename is
	// not compared — the assetName argument is the operator's own
	// canonical form, and we already trust it enough to fetch the
	// matching .sha256.
	fields := strings.Fields(string(body))
	if len(fields) == 0 || len(fields[0]) != 64 {
		return fmt.Errorf("malformed .sha256 file (got %q)", body)
	}
	expected := strings.ToLower(fields[0])

	f, err := os.Open(binaryPath)
	if err != nil {
		return fmt.Errorf("open binary: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash binary: %w", err)
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expected {
		return fmt.Errorf("sha256 mismatch for %s: expected %s, got %s — refusing to use this binary",
			assetName, expected, actual)
	}
	return nil
}

func main() {
	root := repoRoot()

	cliPath, err := downloadTailwindCLI()
	if err != nil {
		log.Fatalf("download Tailwind CLI: %v", err)
	}
	configPath := filepath.Join(root, "internal", "ui", "tailwind.config.cjs")
	templatesRoot := filepath.Join(root, "internal", "ui", "templates")
	outputCSS := filepath.Join(root, "internal", "ui", "static", "tailwind.css")
	contentGlob := templatesRoot + "/**/*.html"

	if _, err := os.Stat(configPath); err != nil {
		log.Fatalf("config %s not found: %v (this file mirrors the prior runtime tailwind.config object)", configPath, err)
	}

	// Args mirror what an operator running `tailwindcss` standalone
	// would pass; CLI is documented at https://tailwindcss.com/docs/installation.
	args := []string{
		"-c", configPath,
		"-i", filepath.Join(root, "internal", "ui", "static", "tailwind.src.css"),
		"-o", outputCSS,
		"--content", contentGlob,
		"--minify",
	}
	cmd := exec.Command(cliPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		log.Fatalf("tailwindcss run: %v", err)
	}

	st, err := os.Stat(outputCSS)
	if err != nil {
		log.Fatalf("output missing: %v", err)
	}
	fmt.Printf("wrote %s (%d bytes)\n", outputCSS, st.Size())
}
