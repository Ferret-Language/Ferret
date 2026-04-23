package backend

import (
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/frontend/ast"
	"compiler/internal/tokens"
	"strconv"
	"strings"
)

// UnwrapNamed maps named enums/errors to their lowered scalar form so
// backend scalar/ABI helpers can reason on the effective primitive type.
func UnwrapNamed(typ typeinfo.Type) typeinfo.Type {
	if named, ok := typ.(*typeinfo.NamedType); ok && named != nil && named.Decl != nil {
		switch named.Decl.Type.(type) {
		case *ast.EnumType, *ast.ErrorType:
			return &typeinfo.BuiltinType{Name: "i32"}
		}
	}
	return typ
}

func IsNamedUnion(named *typeinfo.NamedType) bool {
	if named == nil || named.Decl == nil {
		return false
	}
	_, ok := named.Decl.Type.(*ast.UnionType)
	return ok
}

func IsNamedInterface(named *typeinfo.NamedType) bool {
	if named == nil || named.Decl == nil {
		return false
	}
	_, ok := named.Decl.Type.(*ast.InterfaceType)
	return ok
}

func IsVoidType(typ typeinfo.Type) bool {
	if typ == nil {
		return true
	}
	if b, ok := UnwrapNamed(typ).(*typeinfo.BuiltinType); ok {
		return b.Name == "void"
	}
	return false
}

// ResolveMapType resolves map types through direct map types, pointer/ref
// wrappers, and named aliases whose declarations are map types.
func ResolveMapType(typ typeinfo.Type) (*typeinfo.MapType, bool) {
	switch t := typ.(type) {
	case *typeinfo.MapType:
		return t, true
	case *typeinfo.PointerType:
		return ResolveMapType(t.Inner)
	case *typeinfo.RefType:
		return ResolveMapType(t.Inner)
	case *typeinfo.AtomicType:
		return ResolveMapType(t.Inner)
	case *typeinfo.NamedType:
		if mt, ok := namedMapAliasType(t); ok {
			return mt, true
		}
	}
	if mt, ok := UnwrapNamed(typ).(*typeinfo.MapType); ok {
		return mt, true
	}
	return nil, false
}

func namedMapAliasType(named *typeinfo.NamedType) (*typeinfo.MapType, bool) {
	if named == nil || named.Decl == nil {
		return nil, false
	}
	declMap, ok := named.Decl.Type.(*ast.MapType)
	if !ok || declMap == nil {
		return nil, false
	}
	bindings := make(map[string]typeinfo.Type, len(named.TypeArgs))
	if len(named.TypeArgs) == len(named.Decl.TypeParams) {
		for i, param := range named.Decl.TypeParams {
			if param.Name == nil {
				continue
			}
			if arg := named.TypeArgs[i]; arg != nil {
				bindings[param.Name.Text()] = arg
			}
		}
	}
	key := aliasSyntaxType(declMap.Key, bindings)
	value := aliasSyntaxType(declMap.Value, bindings)
	if key == nil || value == nil {
		return nil, false
	}
	return &typeinfo.MapType{Key: key, Value: value}, true
}

func aliasSyntaxType(expr ast.TypeExpr, bindings map[string]typeinfo.Type) typeinfo.Type {
	switch t := expr.(type) {
	case nil:
		return nil
	case *ast.NamedType:
		if len(t.Path) == 1 {
			name := t.Path[0]
			if bound := bindings[name]; bound != nil {
				return bound
			}
			if name == "str" {
				return &typeinfo.StringType{}
			}
			if tokens.IsBuiltinType(name) {
				return &typeinfo.BuiltinType{Name: name}
			}
		}
		named := &typeinfo.NamedType{}
		if len(t.Path) != 0 {
			named.Name = t.Path[len(t.Path)-1]
		}
		if len(t.TypeArgs) != 0 {
			named.TypeArgs = make([]typeinfo.Type, 0, len(t.TypeArgs))
			for _, arg := range t.TypeArgs {
				named.TypeArgs = append(named.TypeArgs, aliasSyntaxType(arg, bindings))
			}
		}
		return named
	case *ast.MapType:
		return &typeinfo.MapType{
			Key:   aliasSyntaxType(t.Key, bindings),
			Value: aliasSyntaxType(t.Value, bindings),
		}
	case *ast.ArrayType:
		return &typeinfo.ArrayType{
			Inner: aliasSyntaxType(t.Inner, bindings),
			Len:   int64(aliasArrayLen(t.Size)),
		}
	case *ast.SliceType:
		return &typeinfo.SliceType{Inner: aliasSyntaxType(t.Inner, bindings)}
	case *ast.TupleType:
		elems := make([]typeinfo.Type, 0, len(t.Elems))
		for _, elem := range t.Elems {
			elems = append(elems, aliasSyntaxType(elem, bindings))
		}
		return &typeinfo.TupleType{Elems: elems}
	case *ast.PointerType:
		return &typeinfo.PointerType{Inner: aliasSyntaxType(t.Inner, bindings)}
	case *ast.RefType:
		return &typeinfo.RefType{Mutable: t.Mutable, Inner: aliasSyntaxType(t.Inner, bindings)}
	case *ast.AtomicType:
		return &typeinfo.AtomicType{Inner: aliasSyntaxType(t.Inner, bindings)}
	case *ast.RawPtrType:
		return &typeinfo.RawPtrType{Const: t.Const, Inner: aliasSyntaxType(t.Inner, bindings)}
	case *ast.OptionalType:
		return &typeinfo.OptionalType{Inner: aliasSyntaxType(t.Inner, bindings)}
	case *ast.ErrorUnionType:
		return &typeinfo.ErrorUnionType{
			Error: aliasSyntaxType(t.Error, bindings),
			Value: aliasSyntaxType(t.Value, bindings),
		}
	case *ast.FuncType:
		params := make([]typeinfo.ParamSpec, 0, len(t.Params))
		for _, param := range t.Params {
			spec := typeinfo.ParamSpec{Type: aliasSyntaxType(param.Type, bindings)}
			if param.IsVariadic {
				spec.Flags |= typeinfo.FlagVariadic
			}
			params = append(params, spec)
		}
		result := aliasSyntaxType(t.Result, bindings)
		if result == nil {
			result = &typeinfo.BuiltinType{Name: "void"}
		}
		return &typeinfo.FuncType{Params: params, Result: result}
	default:
		return nil
	}
}

func aliasArrayLen(expr ast.Expr) int {
	if num, ok := expr.(*ast.NumberLit); ok && num != nil {
		raw := strings.ReplaceAll(num.Value, "_", "")
		if n, err := strconv.ParseInt(raw, 0, 64); err == nil {
			return int(n)
		}
	}
	return -1
}
