package gen

import "compiler/internal/types"

func largePrimitiveName(typ types.SemType) (string, bool) {
	if typ == nil {
		return "", false
	}
	typ = types.UnwrapType(typ)
	if _, ok := typ.(*types.ReferenceType); ok {
		return "", false
	}
	prim, ok := typ.(*types.PrimitiveType)
	if !ok {
		return "", false
	}
	switch prim.GetName() {
	case types.TYPE_I128, types.TYPE_U128, types.TYPE_I256, types.TYPE_U256,
		types.TYPE_F128, types.TYPE_F256:
		return string(prim.GetName()), true
	default:
		return "", false
	}
}

func isLargePrimitiveType(typ types.SemType) bool {
	_, ok := largePrimitiveName(typ)
	return ok
}

func isSwitchableMatchType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	typ = types.UnwrapType(typ)
	if isLargePrimitiveType(typ) {
		return false
	}
	if prim, ok := typ.(*types.PrimitiveType); ok {
		name := prim.GetName()
		return types.IsIntegerTypeName(name) || name == types.TYPE_BOOL
	}
	if _, ok := typ.(*types.EnumType); ok {
		return true
	}
	return false
}

func isStructType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	typ = types.UnwrapType(typ)
	_, ok := typ.(*types.StructType)
	return ok
}

func isFixedArrayType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	typ = types.UnwrapType(typ)
	if arr, ok := typ.(*types.ArrayType); ok {
		return arr.Length >= 0
	}
	return false
}

func needsByRefType(typ types.SemType) bool {
	if typ == nil {
		return false
	}
	if _, ok := types.UnwrapType(typ).(*types.ReferenceType); ok {
		return false
	}
	if isLargePrimitiveType(typ) || isStructType(typ) || isFixedArrayType(typ) {
		return true
	}
	if iface := interfaceTypeOf(typ); iface != nil {
		return true
	}
	// Unions are aggregate types that must be handled by-ref (memcpy)
	if _, ok := types.UnwrapType(typ).(*types.UnionType); ok {
		return true
	}
	return false
}

func isLargeIntName(name string) bool {
	switch name {
	case string(types.TYPE_I128), string(types.TYPE_U128),
		string(types.TYPE_I256), string(types.TYPE_U256):
		return true
	default:
		return false
	}
}

func isLargeFloatName(name string) bool {
	switch name {
	case string(types.TYPE_F128), string(types.TYPE_F256):
		return true
	default:
		return false
	}
}
