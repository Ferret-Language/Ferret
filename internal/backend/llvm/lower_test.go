package llvm_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	layout "compiler/internal/analysis/layout/model"
	"compiler/internal/backend"
	"compiler/internal/backend/registry"
	compiler "compiler/internal/driver"
	"compiler/internal/ir/mir"
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
	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
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
	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
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
	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
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

func TestLowerLocalArrayLiteralAndIndexToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let arr: [3]i32 = [1, 2, 3]
    let n = arr[1]
    return n
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
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
		"%arr = alloca [12 x i8], align 4",
		"store i32 1, ptr %arr",
		"getelementptr inbounds i8, ptr %arr, i64 4",
		"getelementptr inbounds i32, ptr %arr, i64 1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerSliceIndexToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
fn main(items []i32) i32 {
    let n = items[1]
    return n
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
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
		"load ptr, ptr %items",
		"getelementptr inbounds i32, ptr %_slice_data",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerUnionLocalAssignmentToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    i64,
}

fn main() i32 {
    let value: Token = 1
    print(0)
    return 0
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
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
		"%local__main__Token = type [16 x i8]",
		"%value = alloca %local__main__Token",
		"store i32 0, ptr %value",
		"getelementptr i8, ptr %value, i64 8",
		"store i32 1, ptr",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerUnionExtractCastToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    i64,
}

fn main(flag bool) i32 {
    let mut value: Token = 1
    if flag {
        value = 2 as i64
    }
    let out = value as i32
    return out
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
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
		"%value = alloca %local__main__Token",
		"store i32 1, ptr %value",
		"getelementptr i8, ptr %value, i64 8",
		"%_cast",
		"store i64 %_cast",
		"load i32, ptr",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerOptionalMatchNoneToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let value: ?i32 = none
    let out: i32 = match value {
        is i32 => value
        _ => -1
    }
    return out
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
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
		"%value = alloca [8 x i8]",
		"store i32 0, ptr %value",
		"%_br3 = icmp ne i8",
		"br i1 %_br3, label %bb1, label %bb2",
		"store i32 %_asgn9, ptr %__match1_alloca",
		"ret i32 %_ld12",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerUnionGlobalToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.ferr"), `
type Token union {
    i32,
    i64,
}

let Global: Token = 1

fn main() i32 {
    let out = Global as i32
    return out
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.ferr"))
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
		"%local__main__Token = type [16 x i8]",
		"@main__Global = global [16 x i8]",
		"i8 0",
		"i8 1",
		"getelementptr i8, ptr @main__Global, i64 8",
		"load i32, ptr",
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

func testUnit(result compiler.Result) *backend.Unit {
	layouts := make(map[string]*layout.Module)
	modules := make(map[string]*mir.Module)
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
