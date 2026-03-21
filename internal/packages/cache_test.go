package packages

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModuleCacheHelpers(t *testing.T) {
	cache := t.TempDir()
	repo := "github.com/acme/math"
	ver := "1.2.3"
	path := GetModulePath(cache, repo, ver)
	if path == "" {
		t.Fatalf("empty module path")
	}

	if IsModuleCached(cache, repo, ver) {
		t.Fatalf("module should not be cached yet")
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsModuleCached(cache, repo, ver) {
		t.Fatalf("module should be cached")
	}
	if err := DeleteModule(cache, repo, ver); err != nil {
		t.Fatalf("DeleteModule failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("module dir should be deleted, stat err=%v", err)
	}
}

func TestDownloadHelpers(t *testing.T) {
	url, err := packageArchiveURL("github.com/a/b", "v1.0.0")
	if err != nil || url == "" {
		t.Fatalf("github archive URL failed: %q err=%v", url, err)
	}
	if _, err := packageArchiveURL("example.com/a/b", "v1.0.0"); err == nil {
		t.Fatalf("expected unsupported host error")
	}
	if got := stripProviderPrefix("gitlab.com/group/repo"); got != filepath.ToSlash("group/repo") {
		t.Fatalf("strip provider mismatch: %q", got)
	}
}
