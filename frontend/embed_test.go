package frontend

import (
	"io/fs"
	"testing"
)

// Init calls template.Must, so a path listed here that no longer exists on disk
// panics the server at startup rather than failing a build. That is exactly how
// pages/admin_stalled.html survived its own deletion in 3d2010d: the file was
// removed, the list was not, and nothing in CI parsed the templates.
//
// Failing here names the offending path instead of dumping a panic trace.
func TestTemplatePathsAllExist(t *testing.T) {
	sub, err := fs.Sub(FS, "templates")
	if err != nil {
		t.Fatalf("templates sub: %v", err)
	}
	for _, p := range append(append([]string{"layout.html"}, pagePaths...), partialPaths...) {
		if _, err := fs.Stat(sub, p); err != nil {
			t.Errorf("%s is listed in embed.go but not present in templates/: %v", p, err)
		}
	}
}

// The inverse: a template added to templates/ but never listed is silently
// unreachable — render() would fail at request time with "no such template".
func TestEveryTemplateFileIsListed(t *testing.T) {
	sub, err := fs.Sub(FS, "templates")
	if err != nil {
		t.Fatalf("templates sub: %v", err)
	}
	listed := map[string]bool{"layout.html": true}
	for _, p := range append(append([]string{}, pagePaths...), partialPaths...) {
		listed[p] = true
	}
	err = fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !listed[path] {
			if err == nil && !d.IsDir() {
				t.Errorf("templates/%s exists but is not listed in embed.go", path)
			}
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
