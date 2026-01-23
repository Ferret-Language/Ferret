package types

// IsBuiltinTypeName reports whether the name is a builtin type keyword.
func IsBuiltinTypeName(name string) bool {
	_, ok := BuiltinTypeNameSet[name]
	return ok
}

// BuiltinTypesList returns the builtin types registered in the universe scope.
func BuiltinTypesList() []SemType {
	out := make([]SemType, len(BuiltinTypes))
	copy(out, BuiltinTypes)
	return out
}
