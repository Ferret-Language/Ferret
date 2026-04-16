package prelude

import (
	"path/filepath"
	"testing"

	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/frontend/ast"
)

func TestLoadRegistersGlobalBuiltinsFromPrelude(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	ctx := context.New(root, ".fer", diagnostics.NewDiagnosticBag(""))
	if err := Load(ctx); err != nil {
		t.Fatalf("unexpected prelude load error: %v", err)
	}
	if ctx.Prelude == nil {
		t.Fatal("expected prelude module to be loaded")
	}
	sym, ok := ctx.Universe.Lookup("recover")
	if !ok {
		t.Fatal("expected recover to be declared in universe scope")
	}
	if sym.Kind != symbols.SymbolFunc {
		t.Fatalf("expected recover to be a function symbol, got %s", sym.Kind)
	}
	fn, ok := sym.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected recover node to be *ast.FuncDecl, got %T", sym.Node)
	}
	// recover is declared as a foreign declaration and has no body.
	if !fn.IsExtern || fn.Body != nil {
		t.Fatalf("expected foreign declaration without body, got %#v", fn)
	}
}

func TestLoadRegistersAnyAndPrintFromPrelude(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	ctx := context.New(root, ".fer", diagnostics.NewDiagnosticBag(""))
	if err := Load(ctx); err != nil {
		t.Fatalf("unexpected prelude load error: %v", err)
	}

	anySym, ok := ctx.Universe.Lookup("Any")
	if !ok {
		t.Fatal("expected Any type to be declared in universe scope")
	}
	if anySym.Kind != symbols.SymbolType {
		t.Fatalf("expected Any to be a type symbol, got %s", anySym.Kind)
	}

	printSym, ok := ctx.Universe.Lookup("print")
	if !ok {
		t.Fatal("expected print to be declared in universe scope")
	}
	if printSym.Kind != symbols.SymbolFunc {
		t.Fatalf("expected print to be a function symbol, got %s", printSym.Kind)
	}
	fn, ok := printSym.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected print node to be *ast.FuncDecl, got %T", printSym.Node)
	}
	if len(fn.Params) != 1 {
		t.Fatalf("expected print to have one parameter, got %d", len(fn.Params))
	}
	paramType, ok := fn.Params[0].Type.(*ast.SliceType)
	if !ok {
		t.Fatalf("expected print parameter type to be a slice type, got %T", fn.Params[0].Type)
	}
	elemType, ok := paramType.Inner.(*ast.NamedType)
	if !ok {
		t.Fatalf("expected variadic print element type to be named, got %T", paramType.Inner)
	}
	if len(elemType.Path) != 1 || elemType.Path[0] != "Any" {
		t.Fatalf("expected print parameter element type Any, got %#v", elemType.Path)
	}
	if !fn.Params[0].IsVariadic {
		t.Fatalf("expected print parameter to be variadic, got %#v", fn.Params[0])
	}
}

func TestLoadAddsPreludeModuleToCompilerContextModules(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	ctx := context.New(root, ".fer", diagnostics.NewDiagnosticBag(""))
	if err := Load(ctx); err != nil {
		t.Fatalf("unexpected prelude load error: %v", err)
	}
	if ctx.Prelude == nil {
		t.Fatal("expected prelude module to be loaded")
	}
	found := false
	for _, mod := range ctx.Modules() {
		if mod != nil && mod.Key == ctx.Prelude.Key {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected prelude module %q to be present in ctx.Modules()", ctx.Prelude.Key)
	}
}
