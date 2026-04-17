package context

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveImportRejectsRelativePaths(t *testing.T) {
	ctx := New(t.TempDir(), ".fer", nil)

	_, err := ctx.ResolveImport(nil, "../util/build")
	if err == nil {
		t.Fatal("expected relative import to be rejected")
	}
}

func TestResolveImportClassifiesOrigins(t *testing.T) {
	root := t.TempDir()
	ctx := New(root, ".fer", nil)
	ctx.Config.StdlibRoot = "/stdlib"
	ctx.Config.DependencyRoots["json"] = "/deps/json"
	ctx.Config.DependencyRoots["github.com/acme/json"] = "/deps/json"

	local, err := ctx.ResolveImport(nil, "util/build")
	if err != nil {
		t.Fatalf("resolve local import: %v", err)
	}
	if local.Origin != ModuleOriginLocal || local.Key != "local:util/build" || local.FilePath != root+"/util/build.fer" {
		t.Fatalf("unexpected local import: %#v", local)
	}

	std, err := ctx.ResolveImport(nil, "std/io")
	if err != nil {
		t.Fatalf("resolve std import: %v", err)
	}
	if std.Origin != ModuleOriginStdlib || std.Key != "stdlib:std/io" || std.FilePath != "/stdlib/io.fer" {
		t.Fatalf("unexpected std import: %#v", std)
	}

	dep, err := ctx.ResolveImport(nil, "json/parser")
	if err != nil {
		t.Fatalf("resolve dependency import: %v", err)
	}
	if dep.Origin != ModuleOriginDependency || dep.DependencyAlias != "json" || dep.Key != "dependency:json/parser" || dep.FilePath != "/deps/json/parser.fer" {
		t.Fatalf("unexpected dependency import: %#v", dep)
	}

	remoteDep, err := ctx.ResolveImport(nil, "github.com/acme/json/parser")
	if err != nil {
		t.Fatalf("resolve remote dependency import: %v", err)
	}
	if remoteDep.Origin != ModuleOriginDependency || remoteDep.DependencyAlias != "github.com/acme/json" || remoteDep.Key != "dependency:github.com/acme/json/parser" || remoteDep.FilePath != "/deps/json/parser.fer" {
		t.Fatalf("unexpected remote dependency import: %#v", remoteDep)
	}
}

func TestResolveLocalModuleRejectsReservedPrefix(t *testing.T) {
	root := t.TempDir()
	ctx := New(root, ".fer", nil)

	_, err := ctx.ResolveLocalModule(root + "/std/io.fer")
	if err == nil {
		t.Fatal("expected reserved std prefix to be rejected")
	}
}

func TestUniverseRegistersBuiltInConstants(t *testing.T) {
	ctx := New(t.TempDir(), ".fer", nil)

	for _, name := range []string{"true", "false", "none"} {
		sym, ok := ctx.Universe.Lookup(name)
		if !ok || sym == nil {
			t.Fatalf("expected builtin constant %q to be registered", name)
		}
	}
}

func TestDiscoverModulesSkipsHiddenSourceFiles(t *testing.T) {
	root := t.TempDir()
	visible := filepath.Join(root, "main.fer")
	hidden := filepath.Join(root, ".ferretls-hover-stale.fer")
	if err := os.WriteFile(visible, []byte("fn main() {}"), 0o644); err != nil {
		t.Fatalf("write visible source: %v", err)
	}
	if err := os.WriteFile(hidden, []byte("fn broken("), 0o644); err != nil {
		t.Fatalf("write hidden source: %v", err)
	}

	ctx := New(root, ".fer", nil)
	files, err := ctx.DiscoverModules()
	if err != nil {
		t.Fatalf("discover modules: %v", err)
	}
	if len(files) != 1 || files[0] != filepath.Clean(visible) {
		t.Fatalf("expected only visible source file, got %#v", files)
	}
}
