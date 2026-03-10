package collector

import (
	"fmt"

	"compiler/internal/context"
	"compiler/internal/diagnostics"
	"compiler/internal/frontend/ast"
	"compiler/internal/phase"
	"compiler/internal/semantics/symbols"
	"compiler/internal/semantics/table"
)

func CollectModule(ctx *context.CompilerContext, mod *context.Module) {
	if ctx == nil || mod == nil || mod.AST == nil {
		return
	}

	scope := table.New(nil)
	methodSets := make(map[string]map[string]*symbols.Symbol)

	for _, decl := range mod.AST.Decls {
		switch d := decl.(type) {
		case *ast.LetDecl:
			declare(ctx, scope, symbols.New(d.Name, symbols.SymbolVar, d))
		case *ast.ConstDecl:
			declare(ctx, scope, symbols.New(d.Name, symbols.SymbolConst, d))
		case *ast.TypeDecl:
			declare(ctx, scope, symbols.New(d.Name, symbols.SymbolType, d))
		case *ast.FuncDecl:
			if d.Receiver == nil {
				declare(ctx, scope, symbols.New(d.Name, symbols.SymbolFunc, d))
				continue
			}
			recvName := receiverTypeName(d.Receiver.Type)
			sym := symbols.New(d.Name, symbols.SymbolMethod, d)
			sym.ReceiverType = recvName
			declareMethod(ctx, methodSets, recvName, sym)
		}
	}

	mod.ModuleScope = scope
	mod.MethodSets = methodSets
	mod.Phase = phase.PhaseCollected
}

func declare(ctx *context.CompilerContext, scope *table.Scope, sym *symbols.Symbol) {
	if scope.Declare(sym) {
		return
	}
	prev, _ := scope.LookupLocal(sym.Name)
	reportDuplicate(ctx, sym, prev)
}

func declareMethod(ctx *context.CompilerContext, methodSets map[string]map[string]*symbols.Symbol, recv string, sym *symbols.Symbol) {
	if methodSets[recv] == nil {
		methodSets[recv] = make(map[string]*symbols.Symbol)
	}
	if prev, exists := methodSets[recv][sym.Name]; exists {
		reportDuplicateMethod(ctx, sym, prev)
		return
	}
	methodSets[recv][sym.Name] = sym
}

func reportDuplicate(ctx *context.CompilerContext, sym, prev *symbols.Symbol) {
	diag := diagnostics.NewError(fmt.Sprintf("duplicate symbol %q", sym.Name)).
		WithCode(diagnostics.ErrRedeclaredSymbol).
		WithPrimaryLabel(&sym.Location, "duplicate declaration")
	if prev != nil {
		diag.WithSecondaryLabel(&prev.Location, "previous declaration is here")
	}
	ctx.Diagnostics.Add(diag)
}

func reportDuplicateMethod(ctx *context.CompilerContext, sym, prev *symbols.Symbol) {
	diag := diagnostics.NewError(fmt.Sprintf("duplicate method %q for receiver %q", sym.Name, sym.ReceiverType)).
		WithCode(diagnostics.ErrRedeclaredSymbol).
		WithPrimaryLabel(&sym.Location, "duplicate method declaration")
	if prev != nil {
		diag.WithSecondaryLabel(&prev.Location, "previous method is here")
	}
	ctx.Diagnostics.Add(diag)
}

func receiverTypeName(typ ast.TypeExpr) string {
	switch t := typ.(type) {
	case *ast.NamedType:
		if len(t.Path) == 0 {
			return "<anon>"
		}
		return t.Path[len(t.Path)-1]
	case *ast.PointerType:
		prefix := "*"
		if t.IsOwn {
			prefix = "own *"
		} else if t.IsRaw {
			prefix = "raw *"
		} else if t.IsMut {
			prefix = "*mut "
		}
		return prefix + receiverTypeName(t.Inner)
	default:
		return fmt.Sprintf("%T", typ)
	}
}
