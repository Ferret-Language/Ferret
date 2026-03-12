package compiler

import (
	"os"
	"path/filepath"
	"testing"

	"compiler/internal/diagnostics"
)

func TestParsePathResolvesDependencyAliasFromManifest(t *testing.T) {
	root := t.TempDir()
	depRoot := filepath.Join(root, "deps", "json")

	mustWrite(t, filepath.Join(root, "app", "fer.ret"), `[package]
name = "app"

[dependencies]
json = "../deps/json"
`)
	mustWrite(t, filepath.Join(root, "app", "main.ferr"), `import "json/parser"

fn main() i32 {
    return parser::Value()
}
`)
	mustWrite(t, filepath.Join(depRoot, "fer.ret"), `[package]
name = "json"
`)
	mustWrite(t, filepath.Join(depRoot, "parser.ferr"), `fn Value() i32 {
    return 1
}
`)

	result := ParsePath(filepath.Join(root, "app", "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", result.Diagnostics.Diagnostics())
	}
	if len(result.Modules) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(result.Modules))
	}
	if result.Modules[0].Key != "dependency:json/parser" || result.Modules[1].Key != "local:main" {
		t.Fatalf("unexpected modules: %#v", []string{result.Modules[0].Key, result.Modules[1].Key})
	}
}

func TestParsePathResolvesStdlibWithoutManifest(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "io.ferr"), `#[extern("ferret_io_println")]
fn Println(text str) void;
`)
	mustWrite(t, filepath.Join(root, "main.ferr"), `import "std/io"

fn main() void {
    io::Println("hello")
}
`)

	result := ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", result.Diagnostics.Diagnostics())
	}
	if len(result.Modules) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(result.Modules))
	}
	foundMain := false
	foundStd := false
	for _, mod := range result.Modules {
		switch mod.Key {
		case "local:main":
			foundMain = true
		case "stdlib:std/io":
			foundStd = true
		}
	}
	if !foundMain || !foundStd {
		t.Fatalf("expected main and std/io modules, got %#v", []string{result.Modules[0].Key, result.Modules[1].Key})
	}
}

func TestParsePathTypechecksExternStdlibSignature(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "io.ferr"), `#[extern("ferret_io_println")]
fn Println(text str) void;
`)
	mustWrite(t, filepath.Join(root, "main.ferr"), `import "std/io"

fn main() void {
    io::Println(1)
}
`)
	result := ParsePath(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected stdlib signature type error")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected type mismatch diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestParsePathResolvesStdlibOSWithoutManifest(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "os.ferr"), `#[extern("ferret_os_cpu_count")]
fn CPUCount() usize;

#[extern("ferret_os_platform")]
fn Platform() str;

#[extern("ferret_os_debug")]
fn Debug() bool;
`)
	mustWrite(t, filepath.Join(root, "main.ferr"), `import "std/os"

fn main() void {
    if os::CPUCount() > 0 {
        print(os::Platform())
    }
    if os::Debug() {
        print("debug")
    }
}
`)

	result := ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", result.Diagnostics.Diagnostics())
	}
	if len(result.Modules) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(result.Modules))
	}
	foundMain := false
	foundStd := false
	for _, mod := range result.Modules {
		switch mod.Key {
		case "local:main":
			foundMain = true
		case "stdlib:std/os":
			foundStd = true
		}
	}
	if !foundMain || !foundStd {
		t.Fatalf("expected main and std/os modules, got %#v", []string{result.Modules[0].Key, result.Modules[1].Key})
	}
}

func TestParsePathTypechecksStdlibOSSignature(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "os.ferr"), `#[extern("ferret_os_cpu_count")]
fn CPUCount() usize;
`)
	mustWrite(t, filepath.Join(root, "main.ferr"), `import "std/os"

fn main() void {
    os::CPUCount(1)
}
`)

	result := ParsePath(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected stdlib signature type error")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrWrongArgumentCount {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected wrong arg count diagnostic, got %#v", result.Diagnostics.Diagnostics())
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
