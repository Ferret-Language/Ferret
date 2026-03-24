package resolver_test

import (
	"os"
	"path/filepath"
	"testing"

	"compiler/internal/analysis/semantics/binding"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/phase"
	compiler "compiler/internal/driver"
	"compiler/internal/frontend/ast"
)

func TestResolverBindsQualifiedPaths(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.fer"), `
import "util/build" as build

type Point struct {
    X: i32 = 0
}

fn main() -> i32 {
    let p: Point = .{}
    return build::Point::Origin().X
}
`)
	mustWriteResolver(t, filepath.Join(root, "util", "build.fer"), `
type Point struct {
    X: i32 = 0
}

fn Point::Origin() -> Point {
    return .{}
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.Phase < phase.PhaseOwnershipAnalyzed {
		t.Fatalf("expected CFG-analyzed entry phase, got %#v", result.Entry)
	}

	mainFn := findFunc(t, result.Entry.AST, "main")
	ret, ok := mainFn.Body.Stmts[1].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", mainFn.Body.Stmts[1])
	}
	selector, ok := ret.Value.(*ast.SelectorExpr)
	if !ok {
		t.Fatalf("expected selector expr, got %T", ret.Value)
	}
	call, ok := selector.Left.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call expr, got %T", selector.Left)
	}
	callee, ok := call.Callee.(*ast.Ident)
	if !ok {
		t.Fatalf("expected qualified ident callee, got %T", call.Callee)
	}
	resolution, ok := result.Entry.Bindings.Nodes[callee]
	if !ok {
		t.Fatal("expected callee binding")
	}
	if resolution.Kind != binding.ResolutionSymbol || resolution.Symbol == nil || resolution.Symbol.Name != "Origin" {
		t.Fatalf("expected function symbol resolution for Origin, got %#v", resolution)
	}
	if len(resolution.Remaining) != 0 {
		t.Fatalf("expected fully resolved symbol, got %#v", resolution.Remaining)
	}

}

func TestResolverBindsImportPathAndAliasAsModuleResolution(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.fer"), `
import "util/build" as build

fn main() -> void {}
`)
	mustWriteResolver(t, filepath.Join(root, "util", "build.fer"), `
fn Pick() -> i32 {
    return 1
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	if result.Entry == nil || result.Entry.AST == nil || len(result.Entry.AST.Imports) != 1 {
		t.Fatalf("expected one import in entry AST")
	}
	imp := result.Entry.AST.Imports[0]
	if imp == nil || imp.Path == nil || imp.Alias == nil {
		t.Fatalf("expected import path and alias nodes")
	}

	pathRes := result.Entry.Bindings.Nodes[imp.Path]
	if pathRes == nil || pathRes.Kind != binding.ResolutionModule || pathRes.ImportPath != "util/build" {
		t.Fatalf("expected module resolution for import path, got %#v", pathRes)
	}
	aliasRes := result.Entry.Bindings.Nodes[imp.Alias]
	if aliasRes == nil || aliasRes.Kind != binding.ResolutionModule || aliasRes.ImportPath != "util/build" {
		t.Fatalf("expected module resolution for import alias, got %#v", aliasRes)
	}
}

func TestResolverBindsImportedTypeMembers(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.fer"), `
import "util/colors"

	fn main() -> colors::Color {
	    return colors::Color::Red
	}
	`)
	mustWriteResolver(t, filepath.Join(root, "util", "colors.fer"), `
type Color enum {
    Red
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	mainFn := findFunc(t, result.Entry.AST, "main")
	ret := mainFn.Body.Stmts[0].(*ast.ReturnStmt)
	path, ok := ret.Value.(*ast.Ident)
	if !ok {
		t.Fatalf("expected qualified ident, got %T", ret.Value)
	}
	resolution, ok := result.Entry.Bindings.Nodes[path]
	if !ok {
		t.Fatal("expected imported type member binding")
	}
	if resolution.Kind != binding.ResolutionSymbol || resolution.Symbol == nil || resolution.Symbol.Name != "Red" {
		t.Fatalf("expected enum variant resolution, got %#v", resolution)
	}
}

func TestResolverBindsLabels(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.fer"), `
fn run(items: [3]i32) -> void {
    outer: for items |v| {
        inner: while 1 < 2 {
            break outer
        }
        continue outer
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
	fn := findFunc(t, result.Entry.AST, "run")
	outer := fn.Body.Stmts[0].(*ast.LabelStmt)
	outerLoop := outer.Stmt.(*ast.ForStmt)
	inner := outerLoop.Body.Stmts[0].(*ast.LabelStmt)
	innerLoop := inner.Stmt.(*ast.WhileStmt)
	breakStmt := innerLoop.Body.Stmts[0].(*ast.BreakStmt)
	continueStmt := outerLoop.Body.Stmts[1].(*ast.ContinueStmt)

	if label := result.Entry.Bindings.Labels[breakStmt]; label == nil || label.Name != "outer" {
		t.Fatalf("expected break label binding to outer, got %#v", label)
	}
	if label := result.Entry.Bindings.Labels[continueStmt]; label == nil || label.Name != "outer" {
		t.Fatalf("expected continue label binding to outer, got %#v", label)
	}
}

func TestResolverBindsDeclarationIdents(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.fer"), `
type Point struct {
    x: i32 = 0
}

fn Point::get(self) -> i32 {
    return self.x
}

fn run(items: [3]i32) -> void {
    let x = 1
    const y = 2
    for items |i, v| {
        let mut z = v
        lock z as g {
            print("hi")
        }
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	getFn := findFunc(t, result.Entry.AST, "get")
	if getFn.Receiver == nil || getFn.Receiver.Name == nil {
		t.Fatalf("expected get() receiver")
	}
	if res := result.Entry.Bindings.Nodes[getFn.Receiver.Name]; res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil || res.Symbol.Name != "self" {
		t.Fatalf("expected receiver decl binding for self, got %#v", res)
	}

	runFn := findFunc(t, result.Entry.AST, "run")
	if len(runFn.Params) != 1 || runFn.Params[0].Name == nil {
		t.Fatalf("expected run(items ...) param")
	}
	if res := result.Entry.Bindings.Nodes[runFn.Params[0].Name]; res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil || res.Symbol.Name != "items" {
		t.Fatalf("expected param decl binding for items, got %#v", res)
	}

	letX := runFn.Body.Stmts[0].(*ast.LetStmt)
	if res := result.Entry.Bindings.Nodes[letX.Name]; res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil || res.Symbol.Name != "x" {
		t.Fatalf("expected let decl binding for x, got %#v", res)
	}

	constY := runFn.Body.Stmts[1].(*ast.ConstStmt)
	if res := result.Entry.Bindings.Nodes[constY.Name]; res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil || res.Symbol.Name != "y" {
		t.Fatalf("expected const decl binding for y, got %#v", res)
	}

	loop := runFn.Body.Stmts[2].(*ast.ForStmt)
	if loop.Index == nil || loop.Value == nil {
		t.Fatalf("expected for bindings i, v")
	}
	if res := result.Entry.Bindings.Nodes[loop.Index]; res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil || res.Symbol.Name != "i" {
		t.Fatalf("expected for-index decl binding for i, got %#v", res)
	}
	if res := result.Entry.Bindings.Nodes[loop.Value]; res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil || res.Symbol.Name != "v" {
		t.Fatalf("expected for-value decl binding for v, got %#v", res)
	}

	letZ := loop.Body.Stmts[0].(*ast.LetStmt)
	if res := result.Entry.Bindings.Nodes[letZ.Name]; res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil || res.Symbol.Name != "z" {
		t.Fatalf("expected let decl binding for z, got %#v", res)
	}

	lock := loop.Body.Stmts[1].(*ast.LockStmt)
	if res := result.Entry.Bindings.Nodes[lock.Name]; res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil || res.Symbol.Name != "g" {
		t.Fatalf("expected lock binder decl binding for g, got %#v", res)
	}
}

func TestResolverBindsSpreadArgumentIdent(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.fer"), `
fn sum(nums: ...i32) -> i32 {
    return nums[0]
}

fn run(items: []i32) -> i32 {
    return sum(items...)
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}

	runFn := findFunc(t, result.Entry.AST, "run")
	if len(runFn.Params) != 1 || runFn.Params[0].Name == nil {
		t.Fatalf("expected run(items ...) param")
	}
	ret, ok := runFn.Body.Stmts[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", runFn.Body.Stmts[0])
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		t.Fatalf("expected call with one spread arg, got %T %#v", ret.Value, ret.Value)
	}
	spread, ok := call.Args[0].(*ast.SpreadExpr)
	if !ok {
		t.Fatalf("expected spread arg, got %T", call.Args[0])
	}
	argIdent, ok := spread.Right.(*ast.Ident)
	if !ok {
		t.Fatalf("expected spread right ident, got %T", spread.Right)
	}
	res := result.Entry.Bindings.Nodes[argIdent]
	if res == nil || res.Kind != binding.ResolutionSymbol || res.Symbol == nil || res.Symbol.Name != "items" {
		t.Fatalf("expected spread right to resolve to param items, got %#v", res)
	}
}

func TestResolverReportsUndefinedSymbol(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.fer"), `
fn run() -> i32 {
    return missing
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected undefined symbol diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUndefinedSymbol {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrUndefinedSymbol, result.Diagnostics.Diagnostics())
	}
}

func TestResolverReportsRedeclaredLocalInSameScope(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.fer"), `
fn run() -> i32 {
    let x = 1
    let x = 2
    return x
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatalf("expected redeclared symbol diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrRedeclaredSymbol {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrRedeclaredSymbol, result.Diagnostics.Diagnostics())
	}
}

func TestResolverAllowsDeclarationTypeParams(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.fer"), `
type Box<T> struct {
    Value: T
}

fn Identity<T>(value: T) -> T {
    return value
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if result.Diagnostics.HasErrors() {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics.Diagnostics())
	}
}

func TestResolverReportsMissingSymbolInImportedModule(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.fer"), `
import "std/math"

fn main() -> i32 {
    return math::ClampToZeros(-34)
}
`)
	mustWriteResolver(t, filepath.Join(root, "ferret_libs_dev", "std", "math.fer"), `
fn ClampToZero(value: i32) -> i32 {
    return value
}
`)

	result := compiler.ParsePath(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected module-member undefined diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrUndefinedSymbol && diag.Message == `symbol "ClampToZeros" not found in module "std/math"` {
			if len(diag.Labels) == 0 || diag.Labels[0].Message != "symbol is not defined in this module" {
				t.Fatalf("expected precise module-missing label, got %#v", diag.Labels)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected module-specific undefined diagnostic, got %#v", result.Diagnostics.Diagnostics())
	}
}

func TestResolverRejectsUnexportedImportedSymbol(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.fer"), `
import "util/build"

fn main() -> i32 {
    return build::hidden()
}
`)
	mustWriteResolver(t, filepath.Join(root, "util", "build.fer"), `
fn hidden() -> i32 {
    return 1
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected visibility diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrSymbolNotExported {
			if len(diag.Labels) == 0 || diag.Labels[0].Message != "symbol is not exported by this module" {
				t.Fatalf("expected module-export label, got %#v", diag.Labels)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrSymbolNotExported, result.Diagnostics.Diagnostics())
	}
}

func TestResolverRejectsUnexportedImportedTypeMember(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.fer"), `
import "util/build"

fn main() -> i32 {
    return build::Point::origin
}
`)
	mustWriteResolver(t, filepath.Join(root, "util", "build.fer"), `
type Point struct {
}

fn Point::origin() -> i32 {
    return 1
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected visibility diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrSymbolNotExported {
			if len(diag.Labels) == 0 || diag.Labels[0].Message != "symbol is not exported by this type" {
				t.Fatalf("expected type-export label, got %#v", diag.Labels)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrSymbolNotExported, result.Diagnostics.Diagnostics())
	}
}

func TestResolverRejectsInvalidLabeledBreak(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.fer"), `
fn run() -> void {
    done: if true {
        break done
    }
}
`)

	result := compiler.New(root, ".fer", diagnostics.NewDiagnosticBag("")).ParseEntry(filepath.Join(root, "main.fer"))
	if !result.Diagnostics.HasErrors() {
		t.Fatal("expected invalid break diagnostic")
	}
	found := false
	for _, diag := range result.Diagnostics.Diagnostics() {
		if diag.Code == diagnostics.ErrInvalidBreak {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s diagnostic, got %#v", diagnostics.ErrInvalidBreak, result.Diagnostics.Diagnostics())
	}
}

func findFunc(t *testing.T, mod *ast.Module, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range mod.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Text() == name {
			return fn
		}
	}
	t.Fatalf("function %s not found", name)
	return nil
}

func mustWriteResolver(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
