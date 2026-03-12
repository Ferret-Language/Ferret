package llvm_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compiler/internal/backend"
	"compiler/internal/backend/registry"
	compilerapi "compiler/internal/compiler"
	"compiler/internal/layout"
	midmir "compiler/internal/middleend/mir"
)

func TestLowerInterfaceDispatchToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
type Stringer interface {
    String() str
}

type Name struct {
    value i32 = 0
}

fn (n Name) String() str {
    return 1 as str
}

fn main() str {
    let n: Name = .{ .value = 1 }
    let s: Stringer = n
    return s.String()
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetLLVM)
	if err != nil {
		t.Fatalf("unexpected llvm error: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower llvm: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"%local__main__Stringer = type { ptr, ptr }",
		"@vtable__local__main__Stringer__main__Name = private unnamed_addr constant [1 x ptr]",
		"define { ptr, i64 } @ifacewrap__local__main__Stringer__main__Name__String(ptr %data)",
		"%_iface_fnslot",
		"%_iface_fn",
		"call { ptr, i64 } %_iface_fn",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerImportedInterfaceDispatchToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "util", "name.ferr"), `
type Name struct {
    value i32 = 0
}

fn Origin() Name {
    return .{ .value = 7 }
}

fn (n Name) String() str {
    return 1 as str
}
`)
	mustWrite(t, filepath.Join(root, "main.ferr"), `
import "util/name"

type Stringer interface {
    String() str
}

fn main() str {
    let n = name::Origin()
    let s: Stringer = n
    return s.String()
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetLLVM)
	if err != nil {
		t.Fatalf("unexpected llvm error: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower llvm: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"@vtable__local__main__Stringer__util__name__Name",
		"@ifacewrap__local__main__Stringer__util__name__Name__String",
		"@util__name__Name__String",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerGlobalInterfaceValueToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
type Stringer interface {
    String() str
}

type Name struct {
    value i32 = 0
}

fn (n Name) String() str {
    return 1 as str
}

let GlobalName: Name = .{ .value = 1 }
let GlobalStringer: Stringer = GlobalName

fn main() str {
    return GlobalStringer.String()
}
`)
	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetLLVM)
	if err != nil {
		t.Fatalf("unexpected llvm error: %v", err)
	}
	artifact, err := lowerer.LowerModule(testUnit(result))
	if err != nil {
		t.Fatalf("lower llvm: %v", err)
	}
	text := artifact.Text
	for _, want := range []string{
		"@main__GlobalName = global %local__main__Name",
		"@main__GlobalStringer = global %local__main__Stringer",
		"@vtable__local__main__Stringer__main__Name",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testUnit(result compilerapi.Result) *backend.Unit {
	layouts := make(map[string]*layout.Module)
	modules := make(map[string]*midmir.Module)
	for _, mod := range result.Modules {
		if mod == nil {
			continue
		}
		if mod.Layout != nil {
			layouts[mod.Key] = mod.Layout
		}
		if mod.MIR != nil {
			modules[mod.Key] = mod.MIR
		}
	}
	if result.Entry != nil {
		if result.Entry.Layout != nil {
			layouts[result.Entry.Key] = result.Entry.Layout
		}
		if result.Entry.MIR != nil {
			modules[result.Entry.Key] = result.Entry.MIR
		}
	}
	return &backend.Unit{
		Module:  result.Entry.MIR,
		Layout:  result.Entry.Layout,
		Layouts: layouts,
		Modules: modules,
	}
}
