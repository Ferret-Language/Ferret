package types

// IsMapKeyComparable reports whether a type can be used as a map key.
// Map keys must be comparable without relying on padding-sensitive equality.
func IsMapKeyComparable(t SemType) bool {
	if t == nil {
		return false
	}
	t = UnwrapType(t)
	switch v := t.(type) {
	case *PrimitiveType:
		switch v.GetName() {
		case TYPE_STRING, TYPE_BOOL, TYPE_BYTE:
			return true
		case TYPE_F32, TYPE_F64:
			return true
		default:
			return IsIntegerTypeName(v.GetName())
		}
	case *EnumType:
		return true
	case *ReferenceType:
		return true
	default:
		return false
	}
}
