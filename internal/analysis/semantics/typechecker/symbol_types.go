package typechecker

import (
	"compiler/internal/analysis/semantics/symbols"
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/core/context"
	"compiler/internal/frontend/ast"
)

func (c *checker) typeOfSymbol(sym *symbols.Symbol) typeinfo.Type {
	if sym == nil {
		return typeinfo.InvalidType{}
	}
	if typ, ok := c.info.Symbols[sym.ID]; ok {
		return typ
	}
	if owner := c.findModuleForSymbol(sym); owner != nil && owner != c.mod && owner.Types != nil {
		if typ, ok := owner.Types.Symbols[sym.ID]; ok {
			return typ
		}
	}

	owner := c.findModuleForSymbol(sym)
	if owner == nil {
		owner = c.mod
	}
	typ := c.synthesizeSymbolType(owner, sym)
	if owner == c.mod {
		c.info.BindSymbol(sym, typ)
	} else {
		if owner.Types == nil {
			owner.Types = typeinfo.NewModuleInfo()
		}
		owner.Types.BindSymbol(sym, typ)
	}
	return typ
}

func (c *checker) synthesizeSymbolType(mod *context.Module, sym *symbols.Symbol) typeinfo.Type {
	switch sym.Kind {
	case symbols.SymbolType:
		decl, _ := sym.Node.(*ast.TypeDecl)
		return &typeinfo.NamedType{ModuleKey: mod.Key, Name: sym.Name, Decl: decl}
	case symbols.SymbolFunc, symbols.SymbolMethod:
		decl, _ := sym.Node.(*ast.FuncDecl)
		return c.funcType(mod, decl)
	case symbols.SymbolVar, symbols.SymbolConst:
		switch n := sym.Node.(type) {
		case *ast.LetDecl:
			if n.Type != nil {
				return c.typeFromSyntax(mod, n.Type)
			}
			if n.Value != nil {
				return c.typeOfExpr(nil, n.Value, nil)
			}
		case *ast.ConstDecl:
			if n.Type != nil {
				return c.typeFromSyntax(mod, n.Type)
			}
			if n.Value != nil {
				return c.typeOfExpr(nil, n.Value, nil)
			}
		case *ast.ConstStmt:
			if n.Type != nil {
				return c.typeFromSyntax(mod, n.Type)
			}
			if n.Value != nil {
				return c.typeOfExpr(nil, n.Value, nil)
			}
		case *ast.LetStmt:
			if n.Type != nil {
				return c.typeFromSyntax(mod, n.Type)
			}
			if n.Value != nil {
				return c.typeOfExpr(nil, n.Value, nil)
			}
		}
		if sym.Node == nil {
			switch sym.Name {
			case "true", "false":
				return &typeinfo.BuiltinType{Name: "bool"}
			case "none":
				return typeinfo.UnknownType{}
			case "undefined":
				return typeinfo.UndefinedType{}
			}
		}
	case symbols.SymbolVariant, symbols.SymbolError:
		if ownerName, ok := c.findTypeMemberOwner(mod, sym); ok {
			return &typeinfo.NamedType{
				ModuleKey: mod.Key,
				Name:      ownerName,
				Decl:      c.findTypeDecl(mod, ownerName),
			}
		}
	}
	return typeinfo.UnknownType{}
}

func (c *checker) funcType(mod *context.Module, fn *ast.FuncDecl) *typeinfo.FuncType {
	if fn == nil {
		return &typeinfo.FuncType{Result: &typeinfo.BuiltinType{Name: "void"}}
	}
	ownerMod, ownerDecl := c.ownerTypeDeclForFunc(mod, fn)
	if ownerDecl != nil && len(ownerDecl.TypeParams) > 0 {
		c.pushTypeParams(ownerMod, ownerDecl, ownerDecl.TypeParams)
		defer c.popTypeParams()
	}
	typeParams := c.pushTypeParams(mod, fn, fn.TypeParams)
	defer c.popTypeParams()
	var selfType typeinfo.Type
	if fn.Receiver != nil {
		recvType := c.syntaxType(mod, fn.Receiver.Type)
		if base, ok := typeinfo.ReceiverBaseNamedType(recvType); ok {
			selfType = base
		}
	} else if fn.OwnerType != nil {
		selfType = c.syntaxType(mod, fn.OwnerType)
	}
	params := make([]typeinfo.ParamSpec, 0, len(fn.Params))
	for _, param := range fn.Params {
		params = append(params, c.paramSpecFromSyntax(mod, param, selfType))
	}
	return &typeinfo.FuncType{
		IsUnsafe:   fn.IsUnsafe,
		TypeParams: typeParams,
		Params:     params,
		Result:     c.instantiateSelfType(c.funcResultType(mod, fn), selfType),
	}
}

func (c *checker) funcResultType(mod *context.Module, fn *ast.FuncDecl) typeinfo.Type {
	if fn == nil || fn.Result == nil {
		return &typeinfo.BuiltinType{Name: "void"}
	}
	return c.syntaxType(mod, fn.Result)
}
