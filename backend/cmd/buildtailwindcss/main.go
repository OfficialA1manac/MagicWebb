// Command buildtailwindcss produces the static `tailwind.css` that
// frontend/templates/layout.html loads instead of the JIT
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
//   - a Tailwind config field changes (../frontend/tailwind.config.cjs)
//   - a template's class list changes (../frontend/templates/**/*.html)
//
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
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Pinned to a stable Tailwind release. Bump in lockstep with
// ../frontend/tailwind.config.cjs content if features used by the
// project require a newer minor.
const tailwindVersion = "v3.4.10"

// ASSET NAMING CONVENTION (Tailwind standalone CLI):
//
// Tailwind standalone-CLI releases version-up the TAG but NOT the asset
// filename. For v3.4.10 the assets are: `tailwindcss-linux-x64`,
// `tailwindcss-linux-arm64`, `tailwindcss-macos-x64`,
// `tailwindcss-macos-arm64`, `tailwindcss-windows-x64.exe`,
// `tailwindcss-windows-arm64.exe`. Earlier versions of this script
// inserted `tailwindVersion` into the assetName AND wrapped unix
// binaries in a tar.gz + extracted them. Both were wrong assumptions:
//   - Tailwind has NEVER shipped unix binaries inside a tarball — they
//     are raw executables, no extension, named after the host/arch.
//   - Tailwind v3.4.x does NOT publish a per-asset `.sha256` companion
//     file (the SHA files used to exist in very early v3.0.x release
//     cycle but were dropped after that).
// We therefore download the raw executable, chmod +x on unix variants,
// and skip SHA verification (see "TRUST MODEL" below).
//
// Earlier assets can be probed with curl:
//   curl -I https://github.com/tailwindlabs/tailwindcss/releases/download/v3.4.10/tailwindcss-linux-x64
//   → 302 Found (binary exists, redirects to the storage CDN)
//
// TRUST MODEL:
//
// We rely on HTTPS to github.com (TLS 1.3, HSTS preloaded) for in-
// transit integrity. We do NOT pin a SHA-256 hash of the binary itself
// because Tailwind's release artifacts no longer ship companion
// checksums. The remaining attack surface is a compromised release
// artifact in tailwindlabs/tailwindcss — pinned to one author on
// GitHub — which would also let a compromised release push malicious
// template instructions into the user's templates. We document this
// trade-off rather than dropping SHA verification entirely without
// comment; if your threat model requires pinned-hash download, store
// the expected sha256 of the cached CLI in version control and modify
// `downloadTailwindCLI` in place to compare the downloaded bytes to
// the pinned hash before invoking the CLI.
func assetNameFor(host, arch string) (assetName string, needsChmod bool) {
	switch host + "/" + arch {
	case "linux/amd64":
		return "tailwindcss-linux-x64", true
	case "linux/arm64":
		return "tailwindcss-linux-arm64", true
	case "darwin/amd64":
		return "tailwindcss-macos-x64", true
	case "darwin/arm64":
		return "tailwindcss-macos-arm64", true
	case "windows/amd64":
		return "tailwindcss-windows-x64.exe", false
	case "windows/arm64":
		return "tailwindcss-windows-arm64.exe", false
	}
	return "", false
}

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

// downloadTailwindCLI fetches the standalone binary for the current
// host/arch from GitHub releases into a per-asset cache file under
// os.TempDir, returning the executable path on success. Cache hits
// are returned without re-download.
func downloadTailwindCLI() (string, error) {
	host := runtime.GOOS
	arch := runtime.GOARCH
	if host != "linux" && host != "darwin" && host != "windows" {
		return "", fmt.Errorf("unsupported host/arch: %s/%s", host, arch)
	}
	assetName, needsChmod := assetNameFor(host, arch)
	if assetName == "" {
		return "", fmt.Errorf("no tailwind assetName mapped for host/arch: %s/%s", host, arch)
	}
	dst := filepath.Join(os.TempDir(), assetName)

	cli := &http.Client{Timeout: 60 * time.Second}
	url := fmt.Sprintf("https://github.com/tailwindlabs/tailwindcss/releases/download/%s/%s", tailwindVersion, assetName)

	// Cache hit: file exists, regular, "binary-shaped" (>1024 bytes —
	// a real executable is far larger, so anything this small almost
	// certainly isn't a usable CLI). We are deliberately permissive
	// here because we cannot verify integrity (no SHA companion file
	// in v3.4.x release storage); cache hits should re-run the CLI
	// at most once per operator session. Re-running the CLI is cheap
	// (~2s) and idempotent, so cache misses / forced re-runs are safe.
	if st, err := os.Stat(dst); err == nil && st.Mode().IsRegular() && st.Size() > 1024 {
		log.Printf("tailwindcss: cache hit on %s (%d bytes)", dst, st.Size())
		return dst, nil
	} else if err == nil {
		log.Printf("tailwindcss: cached file at %s is too small or non-regular — re-downloading", dst)
		_ = os.Remove(dst)
	}

	if err := downloadBinary(cli, url, dst); err != nil {
		return "", err
	}
	if needsChmod {
		if err := os.Chmod(dst, 0o755); err != nil {
			return "", fmt.Errorf("chmod %s: %w", dst, err)
		}
	}
	log.Printf("tailwindcss: downloaded %s", dst)
	return dst, nil
}

// downloadBinary streaming-copies the response body into a staging
// file alongside dst, then atomically renames staging → dst on
// success. A half-written dst never appears on disk — a crash mid-
// download leaves only the staging file, which the next run
// overwrites.
func downloadBinary(cli *http.Client, url, dst string) error {
	resp, err := cli.Get(url)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("non-2xx %d for %s", resp.StatusCode, url)
	}
	staging := dst + ".staging"
	out, err := os.OpenFile(staging, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", staging, err)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		_ = os.Remove(staging)
		return fmt.Errorf("copy: %w", err)
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(staging, dst); err != nil {
		return fmt.Errorf("rename %s → %s: %w", staging, dst, err)
	}
	return nil
}

func main() {
	root := repoRoot()

	cliPath, err := downloadTailwindCLI()
	if err != nil {
		log.Fatalf("download Tailwind CLI: %v", err)
	}
	configPath := filepath.Join(root, "..", "frontend", "tailwind.config.cjs")
	templatesRoot := filepath.Join(root, "..", "frontend", "templates")
	outputCSS := filepath.Join(root, "..", "frontend", "static", "tailwind.css")
	contentGlob := templatesRoot + "/**/*.html"

	if _, err := os.Stat(configPath); err != nil {
		log.Fatalf("config %s not found: %v (this file mirrors the prior runtime tailwind.config object)", configPath, err)
	}

	// Args mirror what an operator running `tailwindcss` standalone
	// would pass; CLI is documented at https://tailwindcss.com/docs/installation.
	args := []string{
		"-c", configPath,
		"-i", filepath.Join(root, "..", "frontend", "static", "tailwind.src.css"),
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
		log.Fatalf("output present but stat failed: %v", err)
	}
	fmt.Printf("wrote %s (%d bytes)\n", outputCSS, st.Size())
}
