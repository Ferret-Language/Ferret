package compiler

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	projectpkg "compiler/internal/core/project"
	"compiler/internal/frontend/ast"
	"compiler/internal/prelude"
)

func setIDEExecutablePaths(t *testing.T, root string) {
	t.Helper()
	execPath := filepath.Join(root, "bundle", "bin", "ferret")
	mustWrite(t, execPath, "")
	mustWrite(t, filepath.Join(root, "bundle", "libs", "global.fer"), ``)
	mustWrite(t, filepath.Join(root, "bundle", "libs", "std", "io.fer"), ``)
	oldProjectExecutablePath := projectpkg.ExecutablePath
	oldPreludeExecutablePath := prelude.ExecutablePath
	projectpkg.ExecutablePath = func() (string, error) { return execPath, nil }
	prelude.ExecutablePath = func() (string, error) { return execPath, nil }
	t.Cleanup(func() {
		projectpkg.ExecutablePath = oldProjectExecutablePath
		prelude.ExecutablePath = oldPreludeExecutablePath
	})
}

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

func TestParseEntrySynthesizesMainForTests(t *testing.T) {
	root := t.TempDir()
	libsRoot := filepath.Join(root, "bundle", "core", "libs")
	mustWrite(t, filepath.Join(libsRoot, "global.fer"), ``)
	mustWrite(t, filepath.Join(root, "main.fer"), `
test "smoke" {
    let ok = true
    ok
}
`)

	cfg := context.Config{
		RootDir:         root,
		Extension:       ".fer",
		StdlibRoot:      filepath.Join(libsRoot, "std"),
		DependencyRoots: map[string]string{},
	}
	result := NewWithConfig(cfg, diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.AST == nil {
		t.Fatalf("expected parsed entry AST, got %#v", result.Entry)
	}

	foundTest := false
	foundMain := false
	for _, decl := range result.Entry.AST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn == nil {
			continue
		}
		if fn.IsTest && fn.TestName == "smoke" {
			foundTest = true
		}
		if fn.IsSynthetic && fn.Name != nil && fn.Name.Text() == "main" {
			foundMain = true
		}
	}
	if !foundTest || !foundMain {
		t.Fatalf("expected synthesized test harness and test decl, got %#v", result.Entry.AST.Decls)
	}
}

func TestParseEntryTestModeOverridesUserMain(t *testing.T) {
	root := t.TempDir()
	libsRoot := filepath.Join(root, "bundle", "core", "libs")
	mustWrite(t, filepath.Join(libsRoot, "global.fer"), `
#[extern]
fn print(value: Any) -> void;
type Any interface {}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    print("app")
}

test "smoke" {
    let ok = true
    ok
}
`)

	cfg := context.Config{
		RootDir:         root,
		Extension:       ".fer",
		StdlibRoot:      filepath.Join(libsRoot, "std"),
		DependencyRoots: map[string]string{},
		TestMode:        true,
	}
	result := NewWithConfig(cfg, diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	userMain := 0
	synthMain := 0
	for _, decl := range result.Entry.AST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn == nil || fn.Name == nil || fn.Name.Text() != "main" {
			continue
		}
		if fn.IsSynthetic {
			synthMain++
		} else {
			userMain++
		}
	}
	if userMain != 0 || synthMain != 1 {
		t.Fatalf("expected only one synthetic main in test mode, got user=%d synthetic=%d", userMain, synthMain)
	}
}

func TestParseEntryTestModeSelectsSingleTest(t *testing.T) {
	root := t.TempDir()
	libsRoot := filepath.Join(root, "bundle", "core", "libs")
	mustWrite(t, filepath.Join(libsRoot, "global.fer"), ``)
	mustWrite(t, filepath.Join(root, "main.fer"), `
test "smoke" {}

test "other" {}
`)

	cfg := context.Config{
		RootDir:         root,
		Extension:       ".fer",
		StdlibRoot:      filepath.Join(libsRoot, "std"),
		DependencyRoots: map[string]string{},
		TestMode:        true,
		TestName:        "__ferret_test_1",
	}
	result := NewWithConfig(cfg, diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	tests := 0
	for _, decl := range result.Entry.AST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn == nil || !fn.IsTest {
			continue
		}
		tests++
	}
	if tests != 2 {
		t.Fatalf("expected original test decls to remain visible, got %d", tests)
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
		t.Fatalf("unexpected diagnostics: %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
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
		t.Fatalf("expected type mismatch diagnostic, got %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
}

func TestParsePathForIDEReportsUnusedLocalDiagnostics(t *testing.T) {
	root := t.TempDir()
	setIDEExecutablePaths(t, root)
	mustWrite(t, filepath.Join(root, "main.fer"), `fn main() -> i32 {
    let dead = 1
    return 0
}
`)

	result := ParsePathForIDE(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		got := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			got = append(got, diag.Code+": "+diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", got)
	}

	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.WarnUnusedLocal {
			found = true
			break
		}
	}
	if !found {
		got := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			got = append(got, diag.Code+": "+diag.Message)
		}
		t.Fatalf("expected %s diagnostic, got %v", diagnostics.WarnUnusedLocal, got)
	}
}

func TestParsePathForIDERejectsRuntimeConstInitializer(t *testing.T) {
	root := t.TempDir()
	setIDEExecutablePaths(t, root)
	mustWrite(t, filepath.Join(root, "main.fer"), `#[extern("clock")]
fn clock() -> i32;

fn main() -> i32 {
    const y = clock()
    return y
}
`)

	result := ParsePathForIDE(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected const initializer diagnostic in IDE mode")
	}
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag != nil && diag.Code == diagnostics.ErrTypeMismatch && strings.Contains(diag.Message, "constant initializer must be compile-time evaluable") {
			return
		}
	}
	t.Fatalf("expected const initializer diagnostic, got %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
}

func TestParsePathForIDEAllowsPotentialCTFEConstCall(t *testing.T) {
	root := t.TempDir()
	setIDEExecutablePaths(t, root)
	mustWrite(t, filepath.Join(root, "main.fer"), `fn add(a: i32, b: i32) -> i32 {
    return a + b
}

fn main() -> i32 {
    const y = add(1, 2)
    return y
}
`)

	result := ParsePathForIDE(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("expected potential CTFE const call to remain valid in IDE mode, got %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
}

func TestParsePathResolvesStdlibOSWithoutManifest(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "global.fer"), `
type Any interface {}
#[extern("ferret_io_print")]
fn print(value: Any) -> void;
`)
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
		t.Fatalf("unexpected diagnostics: %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
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
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "global.fer"), `
type Any interface {}
#[extern("ferret_io_print")]
fn print(value: Any) -> void;
`)
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
		t.Fatalf("expected wrong arg count diagnostic, got %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
	}
}

func TestParsePathResolvesStdlibMemWithoutManifest(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "global.fer"), `
#[extern("malloc")]
fn malloc(size: usize) -> ^void;
#[extern("free")]
fn free(ptr: ^void) -> void;
`)
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
		t.Fatalf("unexpected diagnostics: %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
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
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "global.fer"), `
#[extern("malloc")]
fn malloc(size: usize) -> ^void;
#[extern("free")]
fn free(ptr: ^void) -> void;
`)
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
		t.Fatalf("expected type mismatch diagnostic, got %v", diagnosticSummaries(result.Diagnostics.Diagnostics()))
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

func diagnosticSummaries(diags []*diagnostics.Diagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, diag := range diags {
		if diag == nil {
			continue
		}
		out = append(out, diag.Code+": "+diag.Message)
	}
	return out
}
