package typeinfo

import "compiler/internal/frontend/ast"

func LookupEnumOrdinal(typ Type, name string) (int, bool) {
	if named, ok := typ.(*NamedType); ok && named != nil && named.Decl != nil {
		if decl, ok := named.Decl.Type.(*ast.EnumType); ok {
			for i, variant := range decl.Variants {
				if variant != nil && variant.Name != nil && variant.Name.Text() == name {
					return i, true
				}
			}
		}
	}
	if enumTyp, ok := typ.(*EnumType); ok && enumTyp != nil && enumTyp.VariantOrdinals != nil {
		ordinal, ok := enumTyp.VariantOrdinals[name]
		return ordinal, ok
	}
	return 0, false
}

func LookupErrorOrdinal(typ Type, name string) (int, bool) {
	if named, ok := typ.(*NamedType); ok && named != nil && named.Decl != nil {
		if decl, ok := named.Decl.Type.(*ast.ErrorType); ok {
			for i, member := range decl.Members {
				if member != nil && member.Name != nil && member.Name.Text() == name {
					return i, true
				}
			}
		}
	}
	if errTyp, ok := typ.(*ErrorSetType); ok && errTyp != nil && errTyp.MemberOrdinals != nil {
		ordinal, ok := errTyp.MemberOrdinals[name]
		return ordinal, ok
	}
	return 0, false
}
