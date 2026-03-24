package project

import (
	"compiler/internal/core/manifest"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadResolvesNeighborDependencyRoots(t *testing.T) {
	root := t.TempDir()
	depRoot := filepath.Join(root, "deps", "json")

	mustWrite(t, filepath.Join(root, "fer.ret"), `[package]
name = "app"

[dependencies]
json = "./deps/json"
`)
	mustWrite(t, filepath.Join(depRoot, "fer.ret"), `[package]
name = "json"
`)

	ws, err := Load(root, ".fer")
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}
	if ws.RootDir != root {
		t.Fatalf("expected root %q, got %q", root, ws.RootDir)
	}
	if got := ws.Context.DependencyRoots["json"]; got != depRoot {
		t.Fatalf("expected dependency root %q, got %q", depRoot, got)
	}
}

func TestLoadFindsProjectStdlibRoot(t *testing.T) {
	root := t.TempDir()
	stdRoot := filepath.Join(root, "libs", "std")

	mustWrite(t, filepath.Join(root, "fer.ret"), `[package]
name = "app"
`)
	if err := os.MkdirAll(stdRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	ws, err := Load(root, ".fer")
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}
	if ws.Context.StdlibRoot != stdRoot {
		t.Fatalf("expected stdlib root %q, got %q", stdRoot, ws.Context.StdlibRoot)
	}
}

func TestLoadFindsStdlibRootWithoutManifest(t *testing.T) {
	root := t.TempDir()
	stdRoot := filepath.Join(root, "ferret_libs_dev", "std")
	if err := os.MkdirAll(stdRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	ws, err := Load(root, ".fer")
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}
	if ws.Context.StdlibRoot != stdRoot {
		t.Fatalf("expected stdlib root %q, got %q", stdRoot, ws.Context.StdlibRoot)
	}
}

func TestLoadRejectsUnlockedRemoteDependency(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "fer.ret"), `[package]
name = "app"

[dependencies]
json = "github.com/acme/json@v1.0.0"
`)
	if _, err := Load(root, ".fer"); err == nil {
		t.Fatal("expected unlocked remote dependency error")
	}
}

func TestLoadResolvesLockedRemoteDependency(t *testing.T) {
	root := t.TempDir()
	moduleDir := filepath.Join(root, ".ferret", "modules", "github.com", "acme", "json@v1.0.0")

	mustWrite(t, filepath.Join(root, "fer.ret"), `[package]
name = "app"

[dependencies]
json = "github.com/acme/json@v1.0.0"
`)
	mustWrite(t, filepath.Join(root, manifest.LockfileName), `{
  "version": "1.0",
  "dependencies": {
    "github.com/acme/json@v1.0.0": {
      "version": "v1.0.0"
    }
  }
}`)
	mustWrite(t, filepath.Join(moduleDir, "fer.ret"), `[package]
name = "json"
`)

	ws, err := Load(root, ".fer")
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}
	if got := ws.Context.DependencyRoots["json"]; got != moduleDir {
		t.Fatalf("expected dependency root %q, got %q", moduleDir, got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
