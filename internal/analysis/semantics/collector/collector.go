package collector

import (
	"fmt"

	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/table"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/context"
	"compiler/internal/core/diagnostics"
	"compiler/internal/core/phase"
	"compiler/internal/frontend/ast"
)

func CollectModule(ctx *context.CompilerContext, mod *context.Module) {
	if ctx == nil || mod == nil || mod.AST == nil {
		return
	}

	scope := table.New(ctx.Universe)
	methodSets := make(map[typeinfo.ReceiverKey]map[string]*symbols.Symbol)
	typeMembers := make(map[string]map[string]*symbols.Symbol)

	for _, decl := range mod.AST.Decls {
		switch d := decl.(type) {
		case *ast.LetDecl:
			sym := symbols.New(d.Name.Text(), symbols.SymbolVar, d)
			sym.Location = d.Name.Loc()
			declare(ctx, scope, sym)
		case *ast.ConstDecl:
			sym := symbols.New(d.Name.Text(), symbols.SymbolConst, d)
			sym.Location = d.Name.Loc()
			declare(ctx, scope, sym)
		case *ast.TypeDecl:
			sym := symbols.New(d.Name.Text(), symbols.SymbolType, d)
			sym.Location = d.Name.Loc()
			declare(ctx, scope, sym)
			collectTypeMembers(ctx, typeMembers, d)
		case *ast.FuncDecl:
			if d.IsStatic && d.OwnerType != nil && len(d.OwnerType.Path) > 0 {
				typeName := d.OwnerType.Path[len(d.OwnerType.Path)-1]
				members := typeMembers[typeName]
				if members == nil {
					members = make(map[string]*symbols.Symbol)
					typeMembers[typeName] = members
				}
				sym := symbols.New(d.Name.Text(), symbols.SymbolFunc, d)
				sym.Location = d.Name.Loc()
				sym.OwnerType = typeName
				declareTypeMember(ctx, typeName, members, sym)
				continue
			}
			if d.Receiver == nil {
				name := d.Name.Text()
				if d.IsDestructor {
					name = "~" + name
				}
				sym := symbols.New(name, symbols.SymbolFunc, d)
				sym.Location = d.Name.Loc()
				declare(ctx, scope, sym)
				continue
			}
			recvKey, _ := receiverKey(d.Receiver.Type)
			name := d.Name.Text()
			if d.IsDestructor {
				name = "~" + name
			}
			sym := symbols.New(name, symbols.SymbolMethod, d)
			sym.Location = d.Name.Loc()
			sym.Receiver = recvKey
			declareMethod(ctx, methodSets, recvKey, sym)
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
	typeName := decl.Name.Text()
	members := typeMembers[typeName]
	if members == nil {
		members = make(map[string]*symbols.Symbol)
		typeMembers[typeName] = members
	}

	switch t := decl.Type.(type) {
	case *ast.EnumType:
		for _, variant := range t.Variants {
			if variant == nil {
				continue
			}
			sym := symbols.New(variant.Name.Text(), symbols.SymbolVariant, variant)
			sym.Location = variant.Name.Loc()
			sym.OwnerType = typeName
			declareTypeMember(ctx, typeName, members, sym)
		}
	case *ast.ErrorType:
		for _, member := range t.Members {
			if member == nil {
				continue
			}
			sym := symbols.New(member.Name.Text(), symbols.SymbolError, member)
			sym.Location = member.Name.Loc()
			sym.OwnerType = typeName
			declareTypeMember(ctx, typeName, members, sym)
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

func declareMethod(ctx *context.CompilerContext, methodSets map[typeinfo.ReceiverKey]map[string]*symbols.Symbol, recv typeinfo.ReceiverKey, sym *symbols.Symbol) {
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
	diag := diagnostics.NewError(fmt.Sprintf("duplicate method %q for receiver %q", sym.Name, sym.Receiver.String())).
		WithCode(diagnostics.ErrRedeclaredSymbol).
		WithPrimaryLabel(&sym.Location, "duplicate method declaration")
	if prev != nil {
		diag.WithSecondaryLabel(&prev.Location, "previous method is here")
	}
	ctx.Diagnostics.Add(diag)
}

func receiverKey(typ ast.TypeExpr) (typeinfo.ReceiverKey, string) {
	switch t := typ.(type) {
	case *ast.NamedType:
		if len(t.Path) == 0 {
			return typeinfo.ReceiverKey{TypeName: "<anon>"}, "<anon>"
		}
		name := t.Path[len(t.Path)-1]
		return typeinfo.ReceiverKey{TypeName: name}, name
	case *ast.PointerType:
		key, text := receiverKey(t.Inner)
		key.Kind = typeinfo.ReceiverPtr
		return key, "*" + text
	case *ast.RefType:
		key, text := receiverKey(t.Inner)
		prefix := "&"
		if t.Mutable {
			key.Kind = typeinfo.ReceiverRefMut
			prefix = "&mut "
		} else {
			key.Kind = typeinfo.ReceiverRef
		}
		return key, prefix + text
	case *ast.RawPtrType:
		key, text := receiverKey(t.Inner)
		key.Kind = typeinfo.ReceiverRawPtr
		return key, "^" + text
	default:
		text := fmt.Sprintf("%T", typ)
		return typeinfo.ReceiverKey{TypeName: text}, text
	}
}
