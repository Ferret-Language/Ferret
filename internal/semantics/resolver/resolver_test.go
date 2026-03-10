package resolver_test

import (
	"os"
	"path/filepath"
	"testing"

	compilerapi "compiler/internal/compiler"
	"compiler/internal/context"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/phase"
	"compiler/internal/semantics/binding"
)

func TestResolverBindsQualifiedPaths(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.ferr"), `
import "util/build" as build

type Point struct {
    x i32 = 0
}

fn main() i32 {
    let p: Point = .{}
    return build::Origin().x
}
`)
	mustWriteResolver(t, filepath.Join(root, "util", "build.ferr"), `
type Point struct {
    x i32 = 0
    static Origin Point = .{}
}

fn Origin() Point {
    return Point::Origin
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
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

	utilMod := findModuleByImportPath(t, result.Modules, "util/build")
	originFn := findFunc(t, utilMod.AST, "Origin")
	utilRet, ok := originFn.Body.Stmts[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", originFn.Body.Stmts[0])
	}
	typeMember, ok := utilRet.Value.(*ast.Ident)
	if !ok {
		t.Fatalf("expected qualified type ident, got %T", utilRet.Value)
	}
	utilResolution, ok := utilMod.Bindings.Nodes[typeMember]
	if !ok {
		t.Fatal("expected type-qualified binding")
	}
	if utilResolution.Kind != binding.ResolutionSymbol || utilResolution.Symbol == nil || utilResolution.Symbol.Name != "Origin" {
		t.Fatalf("expected static field Origin resolution, got %#v", utilResolution)
	}
	if len(utilResolution.Remaining) != 0 {
		t.Fatalf("expected fully resolved static member, got %#v", utilResolution.Remaining)
	}
}

func TestResolverBindsImportedTypeMembers(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.ferr"), `
import "util/colors"

	fn main() colors::Color {
	    return colors::Color::Red
	}
	`)
	mustWriteResolver(t, filepath.Join(root, "util", "colors.ferr"), `
type Color enum {
    Red
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
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
	mustWriteResolver(t, filepath.Join(root, "main.ferr"), `
fn run(items [3]i32) void {
    outer: for items |v| {
        inner: while 1 < 2 {
            break outer
        }
        continue outer
    }
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
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

func TestResolverReportsUndefinedSymbol(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.ferr"), `
fn run() i32 {
    return missing
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
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

func TestResolverReportsMissingSymbolInImportedModule(t *testing.T) {
	root := t.TempDir()
	mustWriteResolver(t, filepath.Join(root, "main.ferr"), `
import "std/math"

fn main() i32 {
    return math::ClampToZeros(-34)
}
`)
	mustWriteResolver(t, filepath.Join(root, "ferret_libs_dev", "std", "math.ferr"), `
fn ClampToZero(value i32) i32 {
    return value
}
`)

	result := compilerapi.ParsePath(filepath.Join(root, "main.ferr"))
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
	mustWriteResolver(t, filepath.Join(root, "main.ferr"), `
import "util/build"

fn main() i32 {
    return build::hidden()
}
`)
	mustWriteResolver(t, filepath.Join(root, "util", "build.ferr"), `
fn hidden() i32 {
    return 1
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
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
	mustWriteResolver(t, filepath.Join(root, "main.ferr"), `
import "util/build"

fn main() i32 {
    return build::Point::origin
}
`)
	mustWriteResolver(t, filepath.Join(root, "util", "build.ferr"), `
type Point struct {
    static origin i32 = 1
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
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
	mustWriteResolver(t, filepath.Join(root, "main.ferr"), `
fn run() void {
    done: if true {
        break done
    }
}
`)

	result := compilerapi.New(root, ".ferr", diagnostics.NewBag()).ParseEntry(filepath.Join(root, "main.ferr"))
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

func findModuleByImportPath(t *testing.T, mods []*context.Module, importPath string) *context.Module {
	t.Helper()
	for _, mod := range mods {
		if mod != nil && mod.ImportPath == importPath {
			return mod
		}
	}
	t.Fatalf("module %s not found", importPath)
	return nil
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
