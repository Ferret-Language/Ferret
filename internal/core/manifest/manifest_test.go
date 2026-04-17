package manifest

import (
	"os"
	"path/filepath"
	"strings"
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

func TestLoadSupportsConstraintSyntaxForRemoteDependency(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	src := `
[package]
name = "app"

[dependencies]
json = "github.com/acme/json@^0.2.0"
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	file, err := Load(path)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	dep, ok := file.Dependencies["json"]
	if !ok {
		t.Fatal("expected dependency json")
	}
	if dep.Path != "github.com/acme/json" || dep.Version != "^0.2.0" {
		t.Fatalf("unexpected dependency: %#v", dep)
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

func TestLoadParsesPackageEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	src := `
[package]
name = "app"
entry = "cmd/main"
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	file, err := Load(path)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if file.Package.Entry != "cmd/main" {
		t.Fatalf("expected package.entry to be parsed, got %q", file.Package.Entry)
	}
}

func TestSaveWritesPackageEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	file := &File{
		Package: PackageInfo{
			Name:  "app",
			Entry: "main.fer",
		},
		Dependencies: map[string]Dependency{},
	}

	if err := Save(path, file); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if loaded.Package.Entry != "main.fer" {
		t.Fatalf("expected package.entry to round-trip, got %q", loaded.Package.Entry)
	}
}

func TestSaveWritesExplicitLatestRemoteDependency(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	file := &File{
		Package: PackageInfo{Name: "app"},
		Dependencies: map[string]Dependency{
			"json": {Type: DependencyRemote, Path: "github.com/acme/json", Version: "latest"},
		},
	}

	if err := Save(path, file); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got == "" || !strings.Contains(got, `json = "github.com/acme/json@latest"`) {
		t.Fatalf("expected explicit @latest dependency, got:\n%s", got)
	}
}
