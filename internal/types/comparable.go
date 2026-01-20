package types

// IsMapKeyComparable reports whether a type can be used as a map key.
// Like Go, any comparable type can be used as a map key.
// This includes primitives, structs, arrays, named types, references, and interfaces.
// Note: Interface comparison can panic at runtime if the dynamic type is not comparable (like Go).
// Only non-comparable types are: functions, maps, slices, optionals, unions, and results.
func IsMapKeyComparable(t SemType) bool {
	if t == nil {
		return false
	}
	t = UnwrapType(t)
	switch v := t.(type) {
	case *PrimitiveType:
		// All primitives are comparable
		return true
	case *EnumType:
		// Enums are comparable
		return true
	case *ReferenceType:
		// References (pointers) are comparable
		return true
	case *InterfaceType:
		// Interfaces are comparable (like Go)
		// Note: Comparison may panic at runtime if dynamic type is not comparable
		return true
	case *StructType:
		// Structs are comparable if all fields are comparable
		for _, field := range v.Fields {
			if !IsMapKeyComparable(field.Type) {
				return false
			}
		}
		return true
	case *ArrayType:
		// Fixed-size arrays are comparable if their elements are comparable
		// Dynamic slices are NOT comparable (like Go)
		if v.Length < 0 {
			return false // slices not comparable
		}
		return IsMapKeyComparable(v.Element)
	case *NamedType:
		// Named types are comparable if their underlying type is comparable
		return IsMapKeyComparable(v.Underlying)
	case *FunctionType, *MapType:
		// Functions and maps are not comparable
		return false
	case *OptionalType, *UnionType, *ResultType:
		// Optional, union, and result types are not comparable
		// (would require complex equality semantics)
		return false
	default:
		return false
	}
}
