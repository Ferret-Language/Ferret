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

	scope := table.New(ctx.Universe)
	methodSets := make(map[string]map[string]*symbols.Symbol)
	typeMembers := make(map[string]map[string]*symbols.Symbol)

	for _, decl := range mod.AST.Decls {
		switch d := decl.(type) {
		case *ast.LetDecl:
			sym := symbols.New(d.Name, symbols.SymbolVar, d)
			sym.Location = d.NameLocation
			declare(ctx, scope, sym)
		case *ast.ConstDecl:
			sym := symbols.New(d.Name, symbols.SymbolConst, d)
			sym.Location = d.NameLocation
			declare(ctx, scope, sym)
		case *ast.TypeDecl:
			sym := symbols.New(d.Name, symbols.SymbolType, d)
			sym.Location = d.NameLocation
			declare(ctx, scope, sym)
			collectTypeMembers(ctx, typeMembers, d)
		case *ast.FuncDecl:
			if d.Receiver == nil {
				sym := symbols.New(d.Name, symbols.SymbolFunc, d)
				sym.Location = d.NameLocation
				declare(ctx, scope, sym)
				continue
			}
			recvName := receiverTypeName(d.Receiver.Type)
			sym := symbols.New(d.Name, symbols.SymbolMethod, d)
			sym.Location = d.NameLocation
			sym.ReceiverType = recvName
			declareMethod(ctx, methodSets, recvName, sym)
		}
	}

	mod.ModuleScope = scope
	mod.MethodSets = methodSets
	mod.TypeMembers = typeMembers
	mod.Phase = phase.PhaseCollected
}

func collectTypeMembers(ctx *context.CompilerContext, typeMembers map[string]map[string]*symbols.Symbol, decl *ast.TypeDecl) {
	if decl == nil {
		return
	}
	members := typeMembers[decl.Name]
	if members == nil {
		members = make(map[string]*symbols.Symbol)
		typeMembers[decl.Name] = members
	}

	switch t := decl.Type.(type) {
	case *ast.StructType:
		for _, field := range t.StaticFields {
			if field == nil {
				continue
			}
			declareTypeMember(ctx, decl.Name, members, symbols.New(field.Name, symbols.SymbolStatic, field))
		}
	case *ast.EnumType:
		for _, variant := range t.Variants {
			if variant == nil {
				continue
			}
			declareTypeMember(ctx, decl.Name, members, symbols.New(variant.Name, symbols.SymbolVariant, variant))
		}
	case *ast.ErrorType:
		for _, member := range t.Members {
			if member == nil {
				continue
			}
			declareTypeMember(ctx, decl.Name, members, symbols.New(member.Name, symbols.SymbolError, member))
		}
	}
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

func declareTypeMember(ctx *context.CompilerContext, typeName string, members map[string]*symbols.Symbol, sym *symbols.Symbol) {
	if members == nil || sym == nil {
		return
	}
	if prev, exists := members[sym.Name]; exists {
		diag := diagnostics.NewError(fmt.Sprintf("duplicate member %q for type %q", sym.Name, typeName)).
			WithCode(diagnostics.ErrRedeclaredSymbol).
			WithPrimaryLabel(&sym.Location, "duplicate member declaration")
		if prev != nil {
			diag.WithSecondaryLabel(&prev.Location, "previous member is here")
		}
		ctx.Diagnostics.Add(diag)
		return
	}
	members[sym.Name] = sym
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
			prefix += "own "
		}
		if t.IsRaw {
			prefix += "raw "
		}
		if t.IsMut {
			prefix += "mut "
		}
		return prefix + receiverTypeName(t.Inner)
	default:
		return fmt.Sprintf("%T", typ)
	}
}
