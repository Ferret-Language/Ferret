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
	if typ, ok := c.info.Symbols[sym]; ok {
		return typ
	}
	if owner := c.findModuleForSymbol(sym); owner != nil && owner != c.mod && owner.Types != nil {
		if typ, ok := owner.Types.Symbols[sym]; ok {
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
	typeParams := c.pushTypeParams(mod, fn, fn.TypeParams)
	defer c.popTypeParams()
	var selfType typeinfo.Type
	if fn.OwnerType != nil {
		selfType = c.typeFromSyntax(mod, fn.OwnerType)
	}
	params := make([]typeinfo.Type, 0, len(fn.Params))
	mutParams := make([]bool, 0, len(fn.Params))
	comptimeParams := make([]bool, 0, len(fn.Params))
	for _, param := range fn.Params {
		params = append(params, c.instantiateSelfType(c.typeFromSyntax(mod, param.Type), selfType))
		mutParams = append(mutParams, param.IsMut)
		comptimeParams = append(comptimeParams, param.IsComptime)
	}
	return &typeinfo.FuncType{
		IsUnsafe:       fn.IsUnsafe,
		TypeParams:     typeParams,
		Params:         params,
		MutParams:      mutParams,
		ComptimeParams: comptimeParams,
		Result:         c.instantiateSelfType(c.funcResultType(mod, fn), selfType),
	}
}

func (c *checker) funcResultType(mod *context.Module, fn *ast.FuncDecl) typeinfo.Type {
	if fn == nil || fn.Result == nil {
		return &typeinfo.BuiltinType{Name: "void"}
	}
	return c.typeFromSyntax(mod, fn.Result)
}
