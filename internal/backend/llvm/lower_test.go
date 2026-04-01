package llvm_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	layout "compiler/internal/analysis/layout/model"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/backend"
	llvmbackend "compiler/internal/backend/llvm"
	"compiler/internal/backend/registry"
	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	compiler "compiler/internal/driver"
	"compiler/internal/ir/mir"
)

func TestLowerInterfaceDispatchToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Stringer interface {
    String(self) -> str
}

type Name struct {
    value: i32 = 0
}

fn Name::String(self) -> str {
    return 1 as str
}

fn main() -> str {
    let n: Name = .{ .value = 1 }
    let s: Stringer = n
    return s.String()
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"@vtable__local__main__Stringer__Name = private unnamed_addr constant [2 x ptr]",
		"@typeinfo__main__Name = private unnamed_addr constant { i32, ptr, i64, i64, i32 }",
		"define { ptr, i64 } @ifacewrap__local__main__Stringer__Name__String(ptr %data)",
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
	mustWrite(t, filepath.Join(root, "util", "name.fer"), `
type Name struct {
    value: i32 = 0
}

fn Origin() -> Name {
    return .{ .value = 7 }
}

fn Name::String(self) -> str {
    return 1 as str
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "util/name"

type Stringer interface {
    String(self) -> str
}

fn main() -> str {
    let n = name::Origin()
    let s: Stringer = n
    return s.String()
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"@vtable__local__main__Stringer__Name",
		"@ifacewrap__local__main__Stringer__Name__String",
		"@util__name__Name__String",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerProgramDedupesImportedInterfaceHelpers(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "global.fer"), `
type Any interface {}
fn print(value: Any) -> void;
`)
	mustWrite(t, filepath.Join(root, "util", "testing.fer"), `
fn Expect(cond: bool, message: str) -> void {
    if !cond {
        print(message)
    }
}
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "util/testing"

fn main() -> void {
    print("hello")
    testing::Expect(true, "ok")
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	ir, err := llvmbackend.LowerProgram(testUnits(result), false)
	if err != nil {
		t.Fatalf("lower llvm program: %v", err)
	}
	const sym = "@typeinfo__str ="
	if got := strings.Count(ir, sym); got != 1 {
		t.Fatalf("expected %q once, got %d\n%s", sym, got, ir)
	}
}

func TestLowerGlobalInterfaceValueToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Stringer interface {
    String(self) -> str
}

type Name struct {
    value: i32 = 0
}

fn Name::String(self) -> str {
    return 1 as str
}

let GlobalName: Name = .{ .value = 1 }
let GlobalStringer: Stringer = GlobalName

fn main() -> str {
    return GlobalStringer.String()
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"@main__GlobalStringer = global %local__main__Stringer { ptr @main__GlobalName, ptr getelementptr inbounds ([2 x ptr], ptr @vtable__local__main__Stringer__Name",
		"@vtable__local__main__Stringer__Name",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerRuntimeInterfaceTypeTestToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Stringer interface {
    String() -> str
}

type Name struct {}

fn Name::String() -> str {
    return "name"
}

fn main(s: Stringer) -> bool {
    return s is Name
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	if !strings.Contains(text, "@typeinfo__main__Name = private unnamed_addr constant") {
		t.Fatalf("expected runtime type info in llvm output:\n%s", text)
	}
	for _, pattern := range []*regexp.Regexp{
		regexp.MustCompile(`%_iface_vt_addr[0-9]+ = getelementptr inbounds i8, ptr %[A-Za-z0-9_]+, i64 8`),
		regexp.MustCompile(`%_iface_vt[0-9]+ = load ptr, ptr %_iface_vt_addr[0-9]+`),
		regexp.MustCompile(`%_iface_typeinfo[0-9]+ = load ptr, ptr %_iface_vt[0-9]+`),
		regexp.MustCompile(`%_istype[0-9]+ = icmp eq ptr %_iface_typeinfo[0-9]+, @typeinfo__main__Name`),
	} {
		if !pattern.MatchString(text) {
			t.Fatalf("expected %q in llvm output:\n%s", pattern.String(), text)
		}
	}
}

func TestLowerNarrowedInterfaceValueToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Stringer interface {
    String(self) -> str
}

type Name struct {
    value: i32 = 0
}

fn Name::String(self) -> str {
    return "name"
}

fn main(s: Stringer) -> i32 {
    if s is Name {
        let narrowed: Name = s
        return narrowed.value
    }
    return 0
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	for _, pattern := range []*regexp.Regexp{
		regexp.MustCompile(`%_iface_data[0-9]+ = load ptr, ptr %s`),
		regexp.MustCompile(`call void @llvm\.memcpy\.p0\.p0\.i64\(ptr align 4 %narrowed, ptr align 4 %_iface_data[0-9]+, i64 4, i1 false\)`),
	} {
		if !pattern.MatchString(text) {
			t.Fatalf("expected %q in llvm output:\n%s", pattern.String(), text)
		}
	}
}

func TestLowerExplicitInterfaceDowncastToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Stringer interface {
    String(self) -> str
}

type Name struct {
    value: i32 = 0
}

fn Name::String(self) -> str {
    return "name"
}

fn main(s: Stringer) -> i32 {
    let narrowed: Name = s as Name
    return narrowed.value
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	for _, pattern := range []*regexp.Regexp{
		regexp.MustCompile(`%_iface_cast[0-9]+ = call ptr @ferret__interface_downcast\(ptr %[A-Za-z0-9_]+, ptr @typeinfo__main__Name\)`),
		regexp.MustCompile(`call void @llvm\.memcpy\.p0\.p0\.i64\(ptr align 4 %narrowed, ptr align 4 %_iface_cast[0-9]+, i64 4, i1 false\)`),
	} {
		if !pattern.MatchString(text) {
			t.Fatalf("expected %q in llvm output:\n%s", pattern.String(), text)
		}
	}
}

func TestLowerDeclaresPreludeExternCallSymbolsToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> void {
    print("ok")
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	if !strings.Contains(text, "declare void @ferret_global_print(ptr)") {
		t.Fatalf("expected declaration for prelude extern print call:\n%s", text)
	}
	if !strings.Contains(text, "call void @ferret_global_print(") {
		t.Fatalf("expected lowered call to ferret_global_print:\n%s", text)
	}
}

func TestLowerLocalArrayLiteralAndIndexToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let arr: [3]i32 = [3]i32{1, 2, 3}
    let n = arr[1]
    return n
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"call void @ferret__bounds_check(i64 1, i64 3)",
		"getelementptr inbounds i8, ptr %arr, i64 4",
		"getelementptr inbounds i32, ptr %arr, i64 1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerLocalArrayElementWriteToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let mut arr: [3]i32 = [3]i32{1, 2, 3}
    arr[1] = 9
    return arr[1]
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"call void @ferret__bounds_check(i64 1, i64 3)",
		"getelementptr inbounds i8, ptr %arr, i64 4",
		"store i32 9, ptr",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerLocalTupleLiteralAndIndexToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let pair: (i32, bool, i32) = (1, true, 7)
    if !pair[1] {
        return 0
    }
    return pair[2]
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"%pair = alloca [12 x i8], align 4",
		"store i32 1, ptr %pair",
		"store i8 1",
		"getelementptr inbounds i8, ptr %pair, i64 8",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerArbitraryWidthIntegersToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn addWide(a: i128, b: i128) -> i128 {
    return a + b
}

fn main() -> i128 {
    return addWide(1, 2)
}
`)
	cfg := context.Config{
		RootDir:         root,
		Extension:       ".fer",
		DependencyRoots: map[string]string{},
		TargetBackend:   "llvm",
	}
	result := compiler.NewWithConfig(cfg, diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		msgs := make([]string, 0, len(result.Diagnostics.Diagnostics()))
		for _, diag := range result.Diagnostics.Diagnostics() {
			if diag == nil {
				continue
			}
			msgs = append(msgs, diag.Message)
		}
		t.Fatalf("unexpected diagnostics: %v", msgs)
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
		"define i128 @main__addWide(i128 %a, i128 %b)",
		"add i128 ",
		"define i128 @main()",
		"call i128 @main__addWide(i128 1, i128 2)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerSliceIndexToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main(items: []i32) -> i32 {
    let n = items[1]
    return n
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"call void @ferret__bounds_check(i64 1, i64 %_slice_len",
		"getelementptr inbounds i32, ptr %_slice_data",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerBuiltinLenArrayToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> usize {
    let items: [_]i32 = [_]i32{1, 2, 3}
    return len(items)
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	if !strings.Contains(text, "ret i64 3") {
		t.Fatalf("expected array len constant lowering in llvm output:\n%s", text)
	}
}

func TestLowerBuiltinLenSliceToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main(items: []i32) -> usize {
    return len(items)
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"declare i64 @ferret_global_slice_len(ptr)",
		"call i64 @ferret_global_slice_len(ptr %items)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerBuiltinLenStringToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main(s: str) -> usize {
    return len(s)
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	if !strings.Contains(text, "call i64 @ferret_global_slice_len(ptr %s)") {
		t.Fatalf("expected string len runtime call in llvm output:\n%s", text)
	}
}

func TestLowerSliceLiteralToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let items: []i32 = []i32{1, 2, 3}
    return items[1]
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"slice_lit_buf",
		"store i64 3",
		"store i32 1",
		"store i32 2",
		"store i32 3",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerArrayToSliceCallToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn head(items: []i32) -> i32 {
    return items[0]
}

fn main() -> i32 {
    let items: [3]i32 = [3]i32{1, 2, 3}
    return head(items)
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"define i32 @main__head(ptr byval({ ptr, i64 }) align 8 %items)",
		"call i32 @main__head(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerStringSliceCastsToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main(s: str) -> str {
    let bytes = s as []u8
    let text = bytes as str
    return text
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"declare { ptr, i64 } @ferret_global_str_bytes(ptr)",
		"declare { ptr, i64 } @ferret_global_bytes_str(ptr)",
		"call { ptr, i64 } @ferret_global_str_bytes(",
		"call { ptr, i64 } @ferret_global_bytes_str(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerAggregateLoadAssignmentToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Conn struct {}

fn run(mut c: *Conn) -> void {
    let r = &*c
    r
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetLLVM)
	if err != nil {
		t.Fatalf("unexpected llvm error: %v", err)
	}
	if _, err := lowerer.LowerModule(testUnit(result)); err != nil {
		t.Fatalf("lower llvm aggregate load: %v", err)
	}
}

func TestLowerReceiverFieldReadToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Point struct {
    Value: i32 = 0
}

fn Point::Incr(&mut self) -> void {
    self.Value = self.Value + 1
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	lowerer, err := registry.New(backend.TargetLLVM)
	if err != nil {
		t.Fatalf("unexpected llvm error: %v", err)
	}
	if _, err := lowerer.LowerModule(testUnit(result)); err != nil {
		t.Fatalf("lower llvm receiver field read: %v", err)
	}
}

func TestLowerUnionLocalAssignmentToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

fn main() -> i32 {
    let value: Token = 1
    print(0)
    return 0
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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

func TestLowerRawAddressLocalToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let a = 10
    unsafe {
        let p: ^const i32 = &a
        print(p)
    }
    return 0
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"%a_alloca = alloca i32",
		"store i32 %_asgn1, ptr %a_alloca",
		"call void @ferret_global_print(",
		"ret i32 0",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerDefaultExternFunctionCallToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "ferret_libs_dev", "std", "io.fer"), `
#[extern]
fn Println(text: str) -> void;
`)
	mustWrite(t, filepath.Join(root, "main.fer"), `
import "std/io"

fn main() -> void {
    io::Println("hello")
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"declare void @ferret_std_io_Println",
		"call void @ferret_std_io_Println(",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerUnionExtractCastToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

fn main(flag: bool) -> i32 {
    let mut value: Token = 1
    if flag {
        value = 2 as i64
    }
    let out = value as i32
    return out
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"%_unioncast",
		"load i32, ptr",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
	if !regexp.MustCompile(`store i64 %[A-Za-z0-9_]+, ptr %_unionpayload[0-9]+`).MatchString(text) {
		t.Fatalf("expected normalized payload store in llvm output:\n%s", text)
	}
}

func TestLowerIntegerToRawPointerCastToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> ^void {
    unsafe {
        return 0 as ^void
    }
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
	if !strings.Contains(text, "getelementptr i8, ptr null, i64 0") {
		t.Fatalf("expected raw pointer zero cast to lower to a null pointer expression:\n%s", text)
	}
}

func TestLowerOptionalMatchNoneToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    let value: ?i32 = none
    let out: i32 = match value {
        is i32 => value
        _ => -1
    }
    return out
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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

func TestLowerMatchToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main(x: i32) -> i32 {
    match x {
        0 => { return 1 }
        _ => { return 2 }
    }
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"%_ld1 = load i32, ptr %x_alloca",
		"switch i32 %_ld1, label %bb2 [",
		"i32 0, label %bb1",
		"ret i32 1",
		"ret i32 2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerEnumMatchToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Color enum {
    Red,
    Green,
    Blue,
}

fn main(value: Color) -> i32 {
    match value {
        Color::Green => { return 1 }
        _ => { return 2 }
    }
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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
		"%_ld1 = load i32, ptr %value_alloca",
		"switch i32 %_ld1, label %bb2 [",
		"i32 1, label %bb1",
		"ret i32 1",
		"ret i32 2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in llvm output:\n%s", want, text)
		}
	}
}

func TestLowerUnionGlobalToLLVM(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
type Token union {
    i32,
    i64,
}

let Global: Token = 1

fn main() -> i32 {
    let out = Global as i32
    return out
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
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

func TestLowerUnsupportedFunctionResultTypeReturnsError(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.fer"), `
fn main() -> i32 {
    return 1
}
`)
	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.MIR == nil {
		t.Fatal("expected MIR entry module")
	}
	var mainFn *mir.Function
	for _, fn := range result.Entry.MIR.Functions {
		if fn != nil && fn.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatal("expected main function in MIR")
	}
	mainFn.Result = &typeinfo.TypeParam{Name: "T"}

	lowerer, err := registry.New(backend.TargetLLVM)
	if err != nil {
		t.Fatalf("unexpected llvm error: %v", err)
	}
	_, err = lowerer.LowerModule(testUnit(result))
	if err == nil {
		t.Fatal("expected unsupported type lowering error")
	}
	if !strings.Contains(err.Error(), "unsupported llvm base type T") {
		t.Fatalf("expected llvm unsupported type error, got %v", err)
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

func testUnits(result compiler.Result) []*backend.Unit {
	layouts := make(map[string]*layout.Module)
	modules := make(map[string]*mir.Module)
	seen := make(map[string]struct{})
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
	units := make([]*backend.Unit, 0, len(modules))
	for _, mod := range result.Modules {
		if mod == nil || mod.MIR == nil || mod.Layout == nil {
			continue
		}
		if _, ok := seen[mod.Key]; ok {
			continue
		}
		seen[mod.Key] = struct{}{}
		units = append(units, &backend.Unit{
			Module:  mod.MIR,
			Layout:  mod.Layout,
			Layouts: layouts,
			Modules: modules,
		})
	}
	if result.Entry != nil && result.Entry.MIR != nil && result.Entry.Layout != nil {
		if _, ok := seen[result.Entry.Key]; ok {
			return units
		}
		units = append(units, &backend.Unit{
			Module:  result.Entry.MIR,
			Layout:  result.Entry.Layout,
			Layouts: layouts,
			Modules: modules,
		})
	}
	return units
}
