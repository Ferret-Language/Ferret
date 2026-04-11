package typechecker

import (
	"compiler/internal/analysis/semantics/typeinfo"
	"compiler/internal/frontend/ast"
)

func (c *checker) isComparableType(typ typeinfo.Type) bool {
	return c.isComparableTypeSeen(typ, make(map[*ast.TypeDecl]struct{}))
}

func (c *checker) isComparableTypeSeen(typ typeinfo.Type, seen map[*ast.TypeDecl]struct{}) bool {
	if typ == nil {
		return false
	}
	switch t := c.underlyingSeen(typ, seen).(type) {
	case *typeinfo.BuiltinType, *typeinfo.StringType, *typeinfo.EnumType, *typeinfo.ErrorSetType:
		return true
	case *typeinfo.PointerType, *typeinfo.RefType, *typeinfo.RawPtrType:
		return true
	case *typeinfo.OptionalType:
		return c.isComparableTypeSeen(t.Inner, seen)
	case *typeinfo.ApproxType:
		return c.isComparableTypeSeen(t.Inner, seen)
	case *typeinfo.ArrayType:
		return c.isComparableTypeSeen(t.Inner, seen)
	case *typeinfo.TupleType:
		for _, elem := range t.Elems {
			if !c.isComparableTypeSeen(elem, seen) {
				return false
			}
		}
		return true
	case *typeinfo.StructType:
		for _, field := range t.OrderedFields {
			if field == nil || !c.isComparableTypeSeen(field.Type, seen) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
