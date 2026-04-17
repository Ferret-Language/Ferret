package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadLockfileMigratesV1Shape(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, LockfileName)
	content := `{
  "version": "1.0",
  "direct_deps": ["github.com/acme/json"],
  "dependencies": {
    "github.com/acme/json": {
      "version": "v1.2.3",
      "direct": true
    }
  }
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	lock, err := LoadLockfile(root)
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if got := lock.DirectDeps["github.com/acme/json"]; got != "github.com/acme/json@v1.2.3" {
		t.Fatalf("expected migrated direct dep mapping, got %q", got)
	}
	if _, ok := lock.Packages["github.com/acme/json@v1.2.3"]; !ok {
		t.Fatalf("expected migrated package entry")
	}
}

func TestLoadLockfileReadsV2Shape(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, LockfileName)
	content := `{
  "version": "2.0",
  "direct_deps": {
    "json": "github.com/acme/json@v1.2.3"
  },
  "packages": {
    "github.com/acme/json@v1.2.3": {
      "version": "v1.2.3",
      "direct": true
    }
  }
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	lock, err := LoadLockfile(root)
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	if got := lock.DirectDeps["json"]; got != "github.com/acme/json@v1.2.3" {
		t.Fatalf("unexpected direct dep mapping %q", got)
	}
	if _, ok := lock.Packages["github.com/acme/json@v1.2.3"]; !ok {
		t.Fatalf("expected v2 package entry")
	}
}

func TestSaveLockfileOmitsLegacyDependenciesField(t *testing.T) {
	root := t.TempDir()
	lock := NewLockfile()
	lock.SetDirectDependency("json", "github.com/acme/json@v1.2.3")
	lock.SetDependency("github.com/acme/json@v1.2.3", LockfileEntry{
		Version:     "v1.2.3",
		ResolvedURL: "github.com/acme/json",
		Direct:      true,
	})

	if err := SaveLockfile(root, lock); err != nil {
		t.Fatalf("save lockfile: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, LockfileName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, `"dependencies"`) {
		t.Fatalf("expected saved lockfile to omit legacy dependencies field:\n%s", text)
	}
	if !strings.Contains(text, `"packages"`) {
		t.Fatalf("expected saved lockfile to include packages field:\n%s", text)
	}
}

func TestSetDirectDependencyDemotesPreviousVersion(t *testing.T) {
	lock := NewLockfile()
	lock.SetDependency("github.com/acme/json@v1.0.0", LockfileEntry{
		Version:     "v1.0.0",
		ResolvedURL: "github.com/acme/json",
		Direct:      true,
	})
	lock.SetDependency("github.com/acme/json@v1.1.0", LockfileEntry{
		Version:     "v1.1.0",
		ResolvedURL: "github.com/acme/json",
		Direct:      true,
	})

	lock.SetDirectDependency("json", "github.com/acme/json@v1.0.0")
	lock.SetDirectDependency("json", "github.com/acme/json@v1.1.0")

	oldEntry, ok := lock.GetDependency("github.com/acme/json@v1.0.0")
	if !ok {
		t.Fatal("expected old package entry")
	}
	if oldEntry.Direct {
		t.Fatalf("expected old direct package to be demoted")
	}
	newEntry, ok := lock.GetDependency("github.com/acme/json@v1.1.0")
	if !ok {
		t.Fatal("expected new package entry")
	}
	if !newEntry.Direct {
		t.Fatalf("expected new package entry to stay direct")
	}
}

func TestLoadLockfileReconcilesDirectFlagsFromDirectDeps(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, LockfileName)
	content := `{
  "version": "2.0",
  "direct_deps": {
    "json": "github.com/acme/json@v1.1.0"
  },
  "packages": {
    "github.com/acme/json@v1.0.0": {
      "version": "v1.0.0",
      "resolved_url": "github.com/acme/json",
      "direct": true
    },
    "github.com/acme/json@v1.1.0": {
      "version": "v1.1.0",
      "resolved_url": "github.com/acme/json",
      "direct": true
    }
  }
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	lock, err := LoadLockfile(root)
	if err != nil {
		t.Fatalf("load lockfile: %v", err)
	}
	oldEntry, ok := lock.GetDependency("github.com/acme/json@v1.0.0")
	if !ok {
		t.Fatal("expected old package")
	}
	if oldEntry.Direct {
		t.Fatalf("expected old package to be non-direct after reconcile")
	}
	newEntry, ok := lock.GetDependency("github.com/acme/json@v1.1.0")
	if !ok {
		t.Fatal("expected new package")
	}
	if !newEntry.Direct {
		t.Fatalf("expected mapped direct package to be direct")
	}
}

func TestUpdateDependencyEdgesRewiresUsedBy(t *testing.T) {
	lock := NewLockfile()
	lock.SetDependency("A@v1", LockfileEntry{Version: "v1", ResolvedURL: "A", Dependencies: []string{"B@v1"}})
	lock.SetDependency("B@v1", LockfileEntry{Version: "v1", ResolvedURL: "B", UsedBy: []string{"A@v1"}})
	lock.SetDependency("C@v1", LockfileEntry{Version: "v1", ResolvedURL: "C"})

	lock.UpdateDependencyEdges("A@v1", []string{"C@v1"})

	b, ok := lock.GetDependency("B@v1")
	if !ok {
		t.Fatal("expected B entry")
	}
	if len(b.UsedBy) != 0 {
		t.Fatalf("expected B to drop used_by, got %#v", b.UsedBy)
	}
	c, ok := lock.GetDependency("C@v1")
	if !ok {
		t.Fatal("expected C entry")
	}
	if len(c.UsedBy) != 1 || c.UsedBy[0] != "A@v1" {
		t.Fatalf("expected C used_by to include A@v1, got %#v", c.UsedBy)
	}
	a, ok := lock.GetDependency("A@v1")
	if !ok {
		t.Fatal("expected A entry")
	}
	if len(a.Dependencies) != 1 || a.Dependencies[0] != "C@v1" {
		t.Fatalf("expected A dependencies rewired to C@v1, got %#v", a.Dependencies)
	}
}

func TestRemoveDependencyDetachesGraphReferences(t *testing.T) {
	lock := NewLockfile()
	lock.SetDependency("A@v1", LockfileEntry{
		Version:      "v1",
		ResolvedURL:  "A",
		Dependencies: []string{"B@v1"},
	})
	lock.SetDependency("B@v1", LockfileEntry{
		Version:      "v1",
		ResolvedURL:  "B",
		Dependencies: []string{"C@v1"},
		UsedBy:       []string{"A@v1"},
	})
	lock.SetDependency("C@v1", LockfileEntry{
		Version:     "v1",
		ResolvedURL: "C",
		UsedBy:      []string{"B@v1"},
	})

	lock.RemoveDependency("B@v1")

	if _, ok := lock.GetDependency("B@v1"); ok {
		t.Fatal("expected B to be removed")
	}
	a, ok := lock.GetDependency("A@v1")
	if !ok {
		t.Fatal("expected A entry")
	}
	if len(a.Dependencies) != 0 {
		t.Fatalf("expected A dependencies to drop B, got %#v", a.Dependencies)
	}
	c, ok := lock.GetDependency("C@v1")
	if !ok {
		t.Fatal("expected C entry")
	}
	if len(c.UsedBy) != 0 {
		t.Fatalf("expected C used_by to drop B, got %#v", c.UsedBy)
	}
}
