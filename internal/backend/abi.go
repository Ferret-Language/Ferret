package backend

import "compiler/internal/analysis/semantics/typeinfo"

type ABITypeKind uint8

const (
	ABITypeScalar ABITypeKind = iota
	ABITypeNamedLayout
	ABITypeNamedInterface
	ABITypeOptionalAggregate
	ABITypeErrorUnionAggregate
	ABITypeSliceLike
)

// OptionalUsesNiche reports whether ?T can use niche optimization instead of
// aggregate storage with explicit tag/payload representation.
func OptionalUsesNiche(typ typeinfo.Type) bool {
	switch t := UnwrapNamed(typ).(type) {
	case *typeinfo.PointerType, *typeinfo.RefType, *typeinfo.RawPtrType:
		return true
	case *typeinfo.BuiltinType:
		switch t.Name {
		case "bool", "char":
			return true
		}
	case *typeinfo.EnumType, *typeinfo.ErrorSetType:
		return true
	}
	return false
}

func IsSliceLikeType(typ typeinfo.Type) bool {
	switch typ.(type) {
	case *typeinfo.StringType, *typeinfo.SliceType:
		return true
	}
	return false
}

// ClassifyABIType classifies backend ABI shape for a semantic type.
// Backends provide hasNamedLayout to report when a named type has concrete
// layout information available and should be emitted as a named ABI type.
func ClassifyABIType(typ typeinfo.Type, hasNamedLayout func(*typeinfo.NamedType) bool) ABITypeKind {
	if named, ok := typ.(*typeinfo.NamedType); ok {
		if hasNamedLayout != nil && hasNamedLayout(named) {
			return ABITypeNamedLayout
		}
		if IsNamedInterface(named) {
			return ABITypeNamedInterface
		}
	}
	if opt, ok := typ.(*typeinfo.OptionalType); ok && !OptionalUsesNiche(opt.Inner) {
		return ABITypeOptionalAggregate
	}
	if _, ok := typ.(*typeinfo.ErrorUnionType); ok {
		return ABITypeErrorUnionAggregate
	}
	if IsSliceLikeType(typ) {
		return ABITypeSliceLike
	}
	return ABITypeScalar
}
