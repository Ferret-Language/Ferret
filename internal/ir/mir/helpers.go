package mir

import (
	"compiler/internal/frontend/ast"
	"compiler/internal/analysis/semantics/typeinfo"
)

func (fn *Function) LocalByID(id int) *Local {
	if fn == nil || id < 0 {
		return nil
	}
	for _, local := range fn.Locals {
		if local != nil && local.ID == id {
			return local
		}
	}
	return nil
}

func (fn *Function) LocalName(id int) string {
	if local := fn.LocalByID(id); local != nil {
		return local.Name
	}
	return ""
}

func (fn *Function) LocalType(id int) typeinfo.Type {
	if local := fn.LocalByID(id); local != nil {
		return local.Type
	}
	return typeinfo.UnknownType{}
}

func FieldByIndex(typ typeinfo.Type, index int) (*typeinfo.StructField, bool) {
	structType, ok := structView(derefForSelector(typ))
	if !ok || index < 0 || index >= len(structType.OrderedFields) {
		return nil, false
	}
	field := structType.OrderedFields[index]
	if field == nil {
		return nil, false
	}
	return field, true
}

func FieldName(typ typeinfo.Type, index int) string {
	if field, ok := FieldByIndex(typ, index); ok {
		return field.Name
	}
	if named, ok := derefForSelector(typ).(*typeinfo.NamedType); ok && named.Decl != nil {
		if st, ok := named.Decl.Type.(*ast.StructType); ok && index >= 0 && index < len(st.Fields) {
			field := st.Fields[index]
			if field != nil {
				return field.Name.Text()
			}
		}
	}
	return ""
}

func FieldType(typ typeinfo.Type, index int) typeinfo.Type {
	if field, ok := FieldByIndex(typ, index); ok {
		return field.Type
	}
	return typeinfo.UnknownType{}
}

func structView(typ typeinfo.Type) (*typeinfo.StructType, bool) {
	st, ok := typ.(*typeinfo.StructType)
	return st, ok
}

func derefForSelector(typ typeinfo.Type) typeinfo.Type {
	if ptr, ok := typ.(*typeinfo.PointerType); ok {
		return ptr.Inner
	}
	return typ
}
