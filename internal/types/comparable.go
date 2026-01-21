package types

// IsMapKeyComparable reports whether a type can be used as a map key.
// Like Go, only comparable types can be map keys.
// This includes: primitives, enums, strings, pointers, fixed-size arrays of comparable types,
// structs with all comparable fields, interfaces, and named types wrapping comparable types.
//
// NOT comparable (cannot be map keys):
// - Functions (no meaningful equality)
// - Maps (mutable, no meaningful equality)
// - Slices (mutable, no meaningful equality)
// - Unions, Optionals, Results (complex equality semantics)
func IsMapKeyComparable(t SemType) bool {
	if t == nil {
		return false
	}
	t = UnwrapType(t)
	switch v := t.(type) {
	case *PrimitiveType:
		// All primitives are comparable (i32, f64, bool, byte, char, string, etc.)
		return true
	case *EnumType:
		// Enums are comparable
		return true
	case *ReferenceType:
		// Pointers are comparable (by address)
		return true
	case *StructType:
		// Structs are comparable if ALL fields are comparable
		for _, field := range v.Fields {
			if !IsMapKeyComparable(field.Type) {
				return false
			}
		}
		return true
	case *ArrayType:
		// Fixed-size arrays [N]T are comparable if T is comparable
		// Slices []T are NOT comparable (like Go)
		if v.Length < 0 {
			return false // slices not comparable
		}
		return IsMapKeyComparable(v.Element)
	case *NamedType:
		// Named types are comparable if their underlying type is comparable
		return IsMapKeyComparable(v.Underlying)
	case *InterfaceType:
		// Interfaces are comparable (16-byte struct: data pointer + type ID string)
		// Two interface values are equal if they have identical dynamic types and equal dynamic values
		return true
	case *FunctionType, *MapType:
		// Functions and maps are not comparable
		return false
	case *OptionalType, *UnionType, *ResultType:
		// Optional, union, and result types are not comparable
		return false
	default:
		return false
	}
}
