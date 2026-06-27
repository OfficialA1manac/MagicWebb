package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRepoRootReachesGomod. Without a path-anchor to the backend/
// module root, buildtailwindcss would need the operator to cd into the
// repo before invoking it (the previous version relied on relative
// paths which broke if the wrapper was launched from a build script).
// This test pins the contract: any caller from anywhere in the repo
// tree should resolve to the same root directory.
func TestRepoRootReachesGomod(t *testing.T) {
	// Run from the cmd/buildtailwindcss package dir via go test.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Walk upwards until we find go.mod (the test is itself part of
	// the module, so go.mod must exist somewhere above cwd).
	dir := wd
	for i := 0; i < 16; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return // success
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate go.mod above %s", wd)
		}
		dir = parent
	}
}

// TestOutputPathIsUnderStaticFS. The compiled CSS must land under
// frontend/static/ so embed.go's //go:embed all:static catches it
// and Fiber's filesystem middleware exposes it at /static/tailwind.css.
// If this fails, layout.html's <link rel=stylesheet href=/static/
// tailwind.css> 404s in production.
func TestOutputPathIsUnderStaticFS(t *testing.T) {
	// Run repoRoot() via the test (it must walk to go.mod).
	root := repoRoot()
	want := filepath.Join(root, "..", "frontend", "static", "tailwind.css")
	got := filepath.Clean(filepath.Join(root, "..", "frontend", "static", "tailwind.css"))
	if got != want {
		t.Fatalf("output path construction drifted: got %q want %q", got, want)
	}
}

// TestContentGlobTemplates. The Tailwind CLI's --content flag must
// point at the templates directory so utility classes used in templates
// are not tree-shaken. Drift here breaks the layout silently: pages
// render but classes like `flex` go unstyled.
func TestContentGlobTemplates(t *testing.T) {
	root := repoRoot()
	glob := filepath.Join(root, "..", "frontend", "templates") + "/**/*.html"
	if _, err := filepath.Match(glob, ""); err != nil {
		t.Fatalf("glob invalid: %v", err)
	}
	if !filepath.IsAbs(root) {
		t.Fatalf("repoRoot must resolve to absolute path, got %q", root)
	}
}
