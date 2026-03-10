package typechecker_test

import (
	"os"
	"path/filepath"
	"testing"

	compilerapi "compiler/internal/compiler"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/phase"
	"compiler/internal/semantics/typeinfo"
)

func TestTypecheckerChecksWorkspaceExampleSubset(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
import "math/vec2"
import "util/build"

fn main() i32 {
    let p = build::Origin()
    if p == .{ .X = 0, .Y = 0 } {
        return vec2::Len2(copy p)
    }
    return 1
}
`)
	mustWriteType(t, filepath.Join(root, "math", "vec2.ferr"), `
type Vec2 struct {
    X i32 = 0
    Y i32 = 0
}

fn Len2(v Vec2) i32 {
    return v.X * v.X + v.Y * v.Y
}
`)
	mustWriteType(t, filepath.Join(root, "util", "build.ferr"), `
import "math/vec2"

fn Origin() vec2::Vec2 {
    return .{}
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.Phase != phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected CFG-analyzed entry, got %#v", result.Entry)
	}
	if result.Entry.Types == nil {
		t.Fatal("expected module type info")
	}

	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	ifStmt := mainFn.Body.Stmts[1].(*ast.IfStmt)
	condType := result.Entry.Types.Nodes[ifStmt.Cond]
	if !typeinfo.IsBuiltinNamed(condType, "bool") {
		t.Fatalf("expected bool condition type, got %v", condType)
	}
}

func TestTypecheckerReportsInvalidReturn(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn bad() i32 {
    return
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected invalid return diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidReturn {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrInvalidReturn, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerReportsArgumentTypeMismatch(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn Add(x i32, y i32) i32 {
    return x + y
}

fn main() i32 {
    return Add("x", 1)
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected type mismatch diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrTypeMismatch, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerContextualizesNumericLiteralsAndDefaults(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i64 {
    let a = 1
    let b = 1.5
    let c: i64 = 1
    return c
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	letA := mainFn.Body.Stmts[0].(*ast.LetStmt)
	letB := mainFn.Body.Stmts[1].(*ast.LetStmt)
	letC := mainFn.Body.Stmts[2].(*ast.LetStmt)
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[letA.Value], typeinfo.DefaultIntTypeName) {
		t.Fatalf("expected default int literal type %s, got %#v", typeinfo.DefaultIntTypeName, result.Entry.Types.Nodes[letA.Value])
	}
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[letB.Value], typeinfo.DefaultFloatTypeName) {
		t.Fatalf("expected default float literal type %s, got %#v", typeinfo.DefaultFloatTypeName, result.Entry.Types.Nodes[letB.Value])
	}
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[letC.Value], "i64") {
		t.Fatalf("expected contextualized literal type i64, got %#v", result.Entry.Types.Nodes[letC.Value])
	}
}

func TestTypecheckerAllowsImplicitNumericWidening(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i64 {
    let a: i32 = 1
    let b: i64 = a
    return b
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerUsesBuiltInBoolConstants(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() bool {
    if true {
        return false
    }
    return true
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	ifStmt := mainFn.Body.Stmts[0].(*ast.IfStmt)
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[ifStmt.Cond], "bool") {
		t.Fatalf("expected bool type for true constant, got %#v", result.Entry.Types.Nodes[ifStmt.Cond])
	}
}

func TestTypecheckerAllowsUndefinedWithContext(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let mut x: i32 = undefined
    x = 1
    return x
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsNumericNarrowingAndLiteralOverflow(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let a: i64 = 1
    let b: i32 = a
    let c: i8 = 1000
    return b
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected numeric narrowing diagnostics")
	}
	mismatchCount := 0
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch {
			mismatchCount++
		}
	}
	if mismatchCount < 2 {
		t.Fatalf("expected at least two %s diagnostics, got %#v", diagnostics.ErrTypeMismatch, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsDefaultNumericLiteralOverflow(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let huge = 10235543634636243636263462346
    return 0
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected numeric default overflow diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && diag.Message == "numeric literal 10235543634636243636263462346 does not fit in default numeric type i32" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected default overflow diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsCatchFallbackValue(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Io error { denied }

fn main(x Io!i32) i32 {
    return x catch -1
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerTreatsRecoverAsBuiltinFunction(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() string {
    return recover()
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	mainFn := findTypeFunc(t, result.Entry.AST, "main")
	ret := mainFn.Body.Stmts[0].(*ast.ReturnStmt)
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected recover call, got %T", ret.Value)
	}
	if !typeinfo.IsBuiltinNamed(result.Entry.Types.Nodes[call], "string") {
		t.Fatalf("expected recover() to typecheck as string, got %#v", result.Entry.Types.Nodes[call])
	}
}

func TestTypecheckerRejectsRecoverArguments(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() string {
    return recover("x")
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected wrong argument count diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrWrongArgumentCount {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrWrongArgumentCount, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerDoesNotCascadeNotCallableAfterMissingImportedSymbol(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "ferret_libs_dev", "std", "math.ferr"), `
fn ClampToZero(value i32) i32 {
    return value
}
`)
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
import "std/math"

fn main() i32 {
    return math::ClampToZeros(-34)
}
`)

	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected missing imported symbol diagnostic")
	}
	notCallable := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrNotCallable {
			notCallable = true
			break
		}
	}
	if notCallable {
		t.Fatalf("did not expect %s cascade, got %#v", diagnostics.ErrNotCallable, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRequiresCatchHandlerToExit(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Io error { denied }

fn log(x Io) void {}

fn main(x Io!i32) i32 {
    let file = x catch |err| {
        log(err)
    }
    return file
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected catch early-exit diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Message == "catch handler block must exit early" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected catch early-exit diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerReportsNumericNarrowingMessage(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let num1: i32 = 1
    let num2: i8 = num1
    return 0
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected numeric narrowing diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && diag.Message == "cannot implicitly narrow i32 to i8" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected narrowing diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsExplicitNumericCast(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let num1: i32 = 1
    let num2: i8 = num1 as i8
    return num2 as i32
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerResolvesMethodCalls(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X i32 = 0
    Y i32 = 0
}

fn (p Point) Len2() i32 {
    return p.X * p.X + p.Y * p.Y
}

fn (p *Point) Len() i32 {
    return p.X * p.X + p.Y * p.Y
}

fn main() i32 {
    let p: Point = .{ .X = 3, .Y = 4 }
    let q = copy p
    return q.Len2() + p.Len()
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerReportsMissingMethod(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X i32 = 0
}

fn main() i32 {
    let p: Point = .{ .X = 1 }
    return p.Missing()
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected missing method diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrMethodNotFound {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrMethodNotFound, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerReportsUseAfterMoveFromLetBinding(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X i32 = 0
}

fn main() i32 {
    let p: Point = .{ .X = 1 }
    let q = p
    return p.X
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected use-after-move diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUseAfterMove {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrUseAfterMove, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerAllowsExplicitCopyForStructValue(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Point struct {
    X i32 = 0
    Y i32 = 0
}

fn main() i32 {
    let p: Point = .{ .X = 1, .Y = 2 }
    let q = copy p
    return p.X + q.Y
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsCopyOfOwnPointer(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
type Conn struct {}

fn bad(c *own Conn) void {
    let d = copy c
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected invalid copy diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidCopy {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrInvalidCopy, result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsNonConstConstInitializer(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let x = 1
    const y = x
    return y
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected const initializer diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && diag.Message == "constant initializer must be compile-time evaluable" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected const-evaluable diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsNonConstComptimeArgument(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn id(comptime T i32, x i32) i32 {
    return x
}

fn main() i32 {
    let x = 1
    return id(x, 2)
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected comptime argument diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrTypeMismatch && diag.Message == "argument to comptime parameter must be compile-time evaluable" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected comptime-argument diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestTypecheckerRejectsInvalidExplicitCast(t *testing.T) {
	root := t.TempDir()
	mustWriteType(t, filepath.Join(root, "main.ferr"), `
fn main() i32 {
    let x = "hi" as i32
    return 0
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected invalid cast diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidCast {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrInvalidCast, result.Diagnostics.Diagnostics())
	}
}

func findTypeFunc(t *testing.T, mod *ast.Module, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range mod.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name == name {
			return fn
		}
	}
	t.Fatalf("function %s not found", name)
	return nil
}

func mustWriteType(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
