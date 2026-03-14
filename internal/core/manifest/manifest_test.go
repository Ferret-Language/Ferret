package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSupportsDependencyTableSyntax(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	src := `
[package]
name = "app"
version = "0.1.0"

[dependencies]
json = { type = "remote", repo = "github.com/acme/json", version = "v1.2.3" }
ui = { path = "./deps/ui" }
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	file, err := Load(path)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if file.Dependencies["json"].Type != DependencyRemote || file.Dependencies["json"].Version != "v1.2.3" {
		t.Fatalf("unexpected remote dependency: %#v", file.Dependencies["json"])
	}
	if file.Dependencies["ui"].Type != DependencyNeighbor {
		t.Fatalf("unexpected neighbor dependency: %#v", file.Dependencies["ui"])
	}
}

func TestLoadRejectsReservedDependencyAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	src := `
[package]
name = "app"

[dependencies]
std = "./deps/std"
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected reserved alias error")
	}
}
