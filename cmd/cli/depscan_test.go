package cli

import (
	"os"
	"path/filepath"
	"testing"

	"compiler/internal/core/manifest"
)

func TestUpdateConstraint(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "", want: "latest"},
		{in: "latest", want: "latest"},
		{in: "v0.0.1", want: "latest"},
		{in: "^0.2.0", want: "^0.2.0"},
		{in: ">=0.2.0", want: ">=0.2.0"},
	}
	for _, tt := range tests {
		if got := updateConstraint(tt.in); got != tt.want {
			t.Fatalf("updateConstraint(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestListOrphanCandidatesIncludesLockAndStaleCache(t *testing.T) {
	root := t.TempDir()
	cachePath := filepath.Join(root, ".ferret", "modules")
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatal(err)
	}

	lock := manifest.NewLockfile()
	lock.SetDependency("github.com/acme/used@v1.0.0", manifest.LockfileEntry{
		Version:     "v1.0.0",
		ResolvedURL: "github.com/acme/used",
		Direct:      true,
	})
	lock.SetDependency("github.com/acme/unused@v1.0.0", manifest.LockfileEntry{
		Version:     "v1.0.0",
		ResolvedURL: "github.com/acme/unused",
		Direct:      false,
	})
	lock.SetDirectDependency("used", "github.com/acme/used@v1.0.0")

	mustWriteGetTest(t, filepath.Join(cachePath, "github.com", "acme", "unused@v1.0.0", manifest.FileName), `[package]
name = "unused"
`)
	mustWriteGetTest(t, filepath.Join(cachePath, "github.com", "acme", "stale@v9.9.9", manifest.FileName), `[package]
name = "stale"
`)

	candidates, err := listOrphanCandidates(cachePath, lock)
	if err != nil {
		t.Fatalf("listOrphanCandidates: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 orphan candidates, got %d", len(candidates))
	}
	found := map[string]bool{}
	for _, candidate := range candidates {
		found[candidate.PackageID] = true
	}
	if !found["github.com/acme/unused@v1.0.0"] {
		t.Fatalf("expected unused lockfile package candidate, got %#v", candidates)
	}
	if !found["github.com/acme/stale@v9.9.9"] {
		t.Fatalf("expected stale cache candidate, got %#v", candidates)
	}
}

func TestPruneUnusedDependenciesCascadesAndPreservesShared(t *testing.T) {
	root := t.TempDir()
	cachePath := filepath.Join(root, ".ferret", "modules")
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := manifest.NewLockfile()
	lock.SetDependency("A@v1", manifest.LockfileEntry{
		Version:      "v1",
		ResolvedURL:  "A",
		Direct:       false,
		Dependencies: []string{"B@v1"},
	})
	lock.SetDependency("E@v1", manifest.LockfileEntry{
		Version:      "v1",
		ResolvedURL:  "E",
		Direct:       true,
		Dependencies: []string{"B@v1"},
	})
	lock.SetDependency("B@v1", manifest.LockfileEntry{
		Version:      "v1",
		ResolvedURL:  "B",
		Direct:       false,
		Dependencies: []string{"C@v1"},
		UsedBy:       []string{"A@v1", "E@v1"},
	})
	lock.SetDependency("C@v1", manifest.LockfileEntry{
		Version:      "v1",
		ResolvedURL:  "C",
		Direct:       false,
		Dependencies: []string{"F@v1"},
		UsedBy:       []string{"B@v1"},
	})
	lock.SetDependency("F@v1", manifest.LockfileEntry{
		Version:     "v1",
		ResolvedURL: "F",
		Direct:      false,
		UsedBy:      []string{"C@v1"},
	})

	lock.RemoveDependency("A@v1")
	removed := pruneUnusedDependencies(lock, cachePath)
	if len(removed) != 0 {
		t.Fatalf("expected shared branch to remain; removed=%#v", removed)
	}
	if _, ok := lock.GetDependency("B@v1"); !ok {
		t.Fatalf("expected B to remain because E still uses it")
	}

	lock.RemoveDependency("E@v1")
	removed = pruneUnusedDependencies(lock, cachePath)
	if len(removed) != 3 {
		t.Fatalf("expected cascade removal of B/C/F, got %#v", removed)
	}
	if _, ok := lock.GetDependency("B@v1"); ok {
		t.Fatal("expected B to be pruned")
	}
	if _, ok := lock.GetDependency("C@v1"); ok {
		t.Fatal("expected C to be pruned")
	}
	if _, ok := lock.GetDependency("F@v1"); ok {
		t.Fatal("expected F to be pruned")
	}
}
