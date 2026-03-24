package compiler

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
)

func TestParsePathResolvesDependencyAliasFromManifest(t *testing.T) {
	root := t.TempDir()
	depRoot := filepath.Join(root, "deps", "json")

	mustWrite(t, filepath.Join(root, "app", "fer.ret"), `[package]
name = "app"

[dependencies]
json = "../deps/json"
`)
	mustWrite(t, filepath.Join(root, "app", "main.fer"), `import "json/parser"

fn main() -> i32 {
    return parser::Value()
}
`)
	mustWrite(t, filepath.Join(depRoot, "fer.ret"), `[package]
name = "json"
`)
	mustWrite(t, filepath.Join(depRoot, "parser.fer"), `fn Value() -> i32 {
    return 1
}
`)

	result := ParsePath(filepath.Join(root, "app", "main.fer"))
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
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "io.fer"), `#[extern("ferret_io_println")]
fn Println(text: str) -> void;
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/io"

fn main() -> void {
    io::Println("hello")
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
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
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "io.fer"), `#[extern("ferret_io_println")]
fn Println(text: str) -> void;
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/io"

fn main() -> void {
    io::Println(1)
}
`)
	result := ParsePath(filepath.Join(root, "main.fer"))
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
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "os.fer"), `#[extern("ferret_os_cpu_count")]
fn CPUCount() -> usize;

#[extern("ferret_os_platform")]
fn Platform() -> str;

#[extern("ferret_os_debug")]
fn Debug() -> bool;
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/os"

fn main() -> void {
    if os::CPUCount() > 0 {
        print(os::Platform())
    }
    if os::Debug() {
        print("debug")
    }
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
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
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "os.fer"), `#[extern("ferret_os_cpu_count")]
fn CPUCount() -> usize;
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/os"

fn main() -> void {
    os::CPUCount(1)
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
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

func TestParsePathResolvesStdlibMemWithoutManifest(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "mem.fer"), `
type Allocator interface {
    Alloc(&self, size: usize) -> ^void
    Free(&self, ptr: ^void) -> void
}

type CAllocator struct {}

fn CAllocator::Alloc(&self, size: usize) -> ^void {
    return malloc(size)
}

fn CAllocator::Free(&self, ptr: ^void) -> void {
    free(ptr)
}

fn System() -> CAllocator {
    return .{}
}

fn Alloc(a: Allocator, size: usize) -> ^void {
    return a.Alloc(size)
}

fn Free(a: Allocator, ptr: ^void) -> void {
    a.Free(ptr)
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/mem"

fn main() -> void {
    let a = mem::System()
    let p = mem::Alloc(a, 16)
    mem::Free(a, p)
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
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
		case "stdlib:std/mem":
			foundStd = true
		}
	}
	if !foundMain || !foundStd {
		t.Fatalf("expected main and std/mem modules, got %#v", []string{result.Modules[0].Key, result.Modules[1].Key})
	}
}

func TestParsePathTypechecksStdlibMemSignature(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "mem.fer"), `
type Allocator interface {
    Alloc(&self, size: usize) -> ^void
}

type CAllocator struct {}

fn CAllocator::Alloc(&self, size: usize) -> ^void {
    return malloc(size)
}

fn System() -> CAllocator {
    return .{}
}

fn Alloc(a: Allocator, size: usize) -> ^void {
    return a.Alloc(size)
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `import "std/mem"

fn main() -> void {
    let a = mem::System()
    mem::Alloc(a, "bad")
}
`)

	result := ParsePath(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected stdlib mem signature type error")
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

func TestIfAttributeFiltersTopLevelDeclarations(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
#[if(target_os, "linux")]
fn LinuxOnly() -> i32 { return 1 }

#[if(target_os, "windows")]
fn WindowsOnly() -> i32 { return 2 }

fn main() -> i32 {
    return LinuxOnly()
}
`)

	cfg := context.Config{
		RootDir:         root,
		Extension:       ".fer",
		DependencyRoots: map[string]string{},
		TargetOS:        "linux",
		TargetArch:      runtime.GOARCH,
	}
	result := NewWithConfig(cfg, diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if len(result.Entry.AST.Decls) != 2 {
		t.Fatalf("expected 2 active declarations after filtering, got %d", len(result.Entry.AST.Decls))
	}
}

func TestIfAttributeSupportsNegatedTargetSelection(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
#[if(target_os, "linux")]
const PlatformTag = 1

#[ifnot(target_os, "linux")]
const PlatformTag = 2

fn main() -> i32 {
    return PlatformTag
}
`)

	cfg := context.Config{
		RootDir:         root,
		Extension:       ".fer",
		DependencyRoots: map[string]string{},
		TargetOS:        "linux",
		TargetArch:      runtime.GOARCH,
	}
	result := NewWithConfig(cfg, diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if len(result.Entry.AST.Decls) != 2 {
		t.Fatalf("expected 2 active declarations after negated filtering, got %d", len(result.Entry.AST.Decls))
	}
}

func TestIfAttributeSupportsBackendSelection(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
#[if(target_backend, "llvm")]
const BackendTag = 1

#[ifnot(target_backend, "llvm")]
const BackendTag = 2

fn main() -> i32 {
    return BackendTag
}
`)

	cfg := context.Config{
		RootDir:         root,
		Extension:       ".fer",
		DependencyRoots: map[string]string{},
		TargetBackend:   "llvm",
	}
	result := NewWithConfig(cfg, diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if len(result.Entry.AST.Decls) != 2 {
		t.Fatalf("expected 2 active declarations after backend filtering, got %d", len(result.Entry.AST.Decls))
	}
}

func TestIfAttributeInvalidFormReportsDiagnostic(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
#[if(target_os, "linux", extra)]
fn main() -> i32 { return 0 }
`)

	cfg := context.Config{
		RootDir:         root,
		Extension:       ".fer",
		DependencyRoots: map[string]string{},
	}
	result := NewWithConfig(cfg, diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected invalid #[if(...)] diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidOperation && diag.Message == "invalid #[if(...)] attribute" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected invalid #[if(...)] diagnostic, got %#v", result.Diagnostics.Diagnostics())
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
