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
	ctx := context.New(root, ".ferr", diagnostics.NewBag())
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
	// recover is declared #[builtin] — IsBuiltin, not IsExtern.
	if !fn.IsBuiltin || fn.Body != nil {
		t.Fatalf("expected builtin declaration without body, got %#v", fn)
	}
}
