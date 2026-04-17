package typeinfo

// DerefForSelector returns the base type for selector/field access.
// It dereferences pointer/ref wrappers, but intentionally does not unwrap
// named types, error unions, or other semantic wrappers.
func DerefForSelector(typ Type) Type {
	switch t := typ.(type) {
	case *PointerType:
		return t.Inner
	case *RefType:
		return t.Inner
	default:
		return typ
	}
}
