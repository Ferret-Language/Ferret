package backend

import (
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/frontend/ast"
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
