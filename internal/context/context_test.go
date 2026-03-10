package context

import "testing"

func TestResolveImportRejectsRelativePaths(t *testing.T) {
	ctx := New(t.TempDir(), ".ferr", nil)

	_, err := ctx.ResolveImport(nil, "../util/build")
	if err == nil {
		t.Fatal("expected relative import to be rejected")
	}
}

func TestResolveImportClassifiesOrigins(t *testing.T) {
	root := t.TempDir()
	ctx := New(root, ".ferr", nil)
	ctx.Config.StdlibRoot = "/stdlib"
	ctx.Config.DependencyRoots["json"] = "/deps/json"

	local, err := ctx.ResolveImport(nil, "util/build")
	if err != nil {
		t.Fatalf("resolve local import: %v", err)
	}
	if local.Origin != ModuleOriginLocal || local.Key != "local:util/build" || local.FilePath != root+"/util/build.ferr" {
		t.Fatalf("unexpected local import: %#v", local)
	}

	std, err := ctx.ResolveImport(nil, "std/io")
	if err != nil {
		t.Fatalf("resolve std import: %v", err)
	}
	if std.Origin != ModuleOriginStdlib || std.Key != "stdlib:std/io" || std.FilePath != "/stdlib/io.ferr" {
		t.Fatalf("unexpected std import: %#v", std)
	}

	dep, err := ctx.ResolveImport(nil, "json/parser")
	if err != nil {
		t.Fatalf("resolve dependency import: %v", err)
	}
	if dep.Origin != ModuleOriginDependency || dep.DependencyAlias != "json" || dep.Key != "dependency:json/parser" || dep.FilePath != "/deps/json/parser.ferr" {
		t.Fatalf("unexpected dependency import: %#v", dep)
	}
}

func TestResolveLocalModuleRejectsReservedPrefix(t *testing.T) {
	root := t.TempDir()
	ctx := New(root, ".ferr", nil)

	_, err := ctx.ResolveLocalModule(root + "/std/io.ferr")
	if err == nil {
		t.Fatal("expected reserved std prefix to be rejected")
	}
}
