package binding

import (
	"testing"

	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/core/source"
	"compiler/internal/frontend/ast"
)

func TestImportBindingKey(t *testing.T) {
	var nilBinding *ImportBinding
	if got := nilBinding.Key(); got != "" {
		t.Fatalf("nil key = %q, want empty", got)
	}

	b := &ImportBinding{Segments: []string{"std", "io"}}
	if got := b.Key(); got != "std::io" {
		t.Fatalf("key = %q, want std::io", got)
	}
}

func TestModuleInfoBinders(t *testing.T) {
	info := NewModuleInfo()
	if info == nil || info.Nodes == nil || info.Labels == nil || info.FunctionSymbols == nil || info.FunctionLocals == nil {
		t.Fatalf("module info maps must be initialized")
	}

	node := &ast.Ident{Path: []string{"x"}, Location: source.NewLocation("main.fer", source.NewPosition(), source.NewPosition())}
	res := &Resolution{Kind: ResolutionModule, ModuleKey: "m"}
	info.BindNode(node, res)
	if got := info.Nodes[node]; got != res {
		t.Fatalf("node binding mismatch")
	}

	label := &LabelBinding{Name: "loop"}
	info.BindLabel(node, label)
	if got := info.Labels[node]; got != label {
		t.Fatalf("label binding mismatch")
	}

	fn := &ast.FuncDecl{Name: &ast.Ident{Path: []string{"run"}}}
	sym := symbols.New("run", symbols.SymbolFunc, fn)
	info.BindFunctionSymbol(fn, sym)
	if got := info.FunctionSymbols[fn]; got != sym {
		t.Fatalf("function symbol binding mismatch")
	}

	local := symbols.New("x", symbols.SymbolVar, nil)
	info.AddFunctionLocal(fn, local)
	if len(info.FunctionLocals[fn]) != 1 || info.FunctionLocals[fn][0] != local {
		t.Fatalf("function local binding mismatch")
	}
}
