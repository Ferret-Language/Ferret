package types

func GetNumberBitSize(kind TYPE_NAME) uint16 {
	switch kind {
	case TYPE_I8, TYPE_U8, TYPE_BYTE:
		return 8
	case TYPE_I16, TYPE_U16:
		return 16
	case TYPE_I32, TYPE_U32, TYPE_F32, TYPE_CHAR:
		return 32
	case TYPE_I64, TYPE_U64, TYPE_F64:
		return 64
	case TYPE_I128, TYPE_U128, TYPE_F128:
		return 128
	case TYPE_I256, TYPE_U256, TYPE_F256:
		return 256
	default:
		return 0
	}
}

func IsSigned(kind TYPE_NAME) bool {
	switch kind {
	case TYPE_I8, TYPE_I16, TYPE_I32, TYPE_I64, TYPE_I128, TYPE_I256:
		return true
	default:
		return false
	}
}

func IsUnsigned(kind TYPE_NAME) bool {
	switch kind {
	case TYPE_U8, TYPE_U16, TYPE_U32, TYPE_U64, TYPE_U128, TYPE_U256, TYPE_BYTE:
		return true
	default:
		return false
	}
}

// IsNumericTypeName checks if a type name is numeric
func IsNumericTypeName(typeName TYPE_NAME) bool {
	return IsIntegerTypeName(typeName) || IsFloatTypeName(typeName)
}

// IsIntegerTypeName checks if a type name is an integer type
func IsIntegerTypeName(typeName TYPE_NAME) bool {
	switch typeName {
	case TYPE_I8, TYPE_I16, TYPE_I32, TYPE_I64, TYPE_I128, TYPE_I256,
		TYPE_U8, TYPE_U16, TYPE_U32, TYPE_U64, TYPE_U128, TYPE_U256, TYPE_BYTE:
		return true
	default:
		return false
	}
}

func IsFloatTypeName(typeName TYPE_NAME) bool {
	switch typeName {
	case TYPE_F32, TYPE_F64, TYPE_F128, TYPE_F256:
		return true
	default:
		return false
	}
}

func IsComplexTypeName(typeName TYPE_NAME) bool {
	switch typeName {
	case TYPE_COMPLEX64, TYPE_COMPLEX, TYPE_COMPLEX256, TYPE_COMPLEX512:
		return true
	default:
		return false
	}
}

func ComplexComponentTypeName(typeName TYPE_NAME) (TYPE_NAME, bool) {
	switch typeName {
	case TYPE_COMPLEX64:
		return TYPE_F32, true
	case TYPE_COMPLEX:
		return TYPE_F64, true
	case TYPE_COMPLEX256:
		return TYPE_F128, true
	case TYPE_COMPLEX512:
		return TYPE_F256, true
	default:
		return TYPE_UNKNOWN, false
	}
}

func complexRank(typeName TYPE_NAME) int {
	switch typeName {
	case TYPE_COMPLEX64:
		return 0
	case TYPE_COMPLEX:
		return 1
	case TYPE_COMPLEX256:
		return 2
	case TYPE_COMPLEX512:
		return 3
	default:
		return -1
	}
}

func complexTypeByRank(rank int) SemType {
	switch rank {
	case 0:
		return TypeComplex64
	case 1:
		return TypeComplex
	case 2:
		return TypeComplex256
	case 3:
		return TypeComplex512
	default:
		return TypeComplex
	}
}

func ComplexTypeFromName(typeName TYPE_NAME) (SemType, bool) {
	switch typeName {
	case TYPE_COMPLEX64:
		return TypeComplex64, true
	case TYPE_COMPLEX:
		return TypeComplex, true
	case TYPE_COMPLEX256:
		return TypeComplex256, true
	case TYPE_COMPLEX512:
		return TypeComplex512, true
	default:
		return nil, false
	}
}

func ComplexTypeNameOf(t SemType) (TYPE_NAME, bool) {
	if t == nil {
		return TYPE_UNKNOWN, false
	}
	t = UnwrapType(t)
	complexType, ok := t.(*ComplexType)
	if !ok {
		return TYPE_UNKNOWN, false
	}
	return complexType.GetName(), true
}

func ComplexComponentType(t SemType) (SemType, bool) {
	complexName, ok := ComplexTypeNameOf(t)
	if !ok {
		return nil, false
	}
	componentName, ok := ComplexComponentTypeName(complexName)
	if !ok {
		return nil, false
	}
	return FromTypeName(componentName), true
}

func ComplexTypeForNumeric(t SemType) SemType {
	if t == nil {
		return TypeComplex
	}
	t = UnwrapType(t)
	prim, ok := t.(*PrimitiveType)
	if !ok {
		return TypeComplex
	}
	if prim.IsUntyped() {
		return TypeComplex
	}
	bits := GetNumberBitSize(prim.GetName())
	switch {
	case bits <= 32:
		return TypeComplex64
	case bits <= 64:
		return TypeComplex
	case bits <= 128:
		return TypeComplex256
	default:
		return TypeComplex512
	}
}

func ComplexBinaryResultType(left, right SemType) SemType {
	if !IsComplex(left) && !IsComplex(right) {
		return TypeUnknown
	}

	maxRank := -1
	for _, t := range []SemType{left, right} {
		if t == nil {
			continue
		}
		if IsComplex(t) {
			if name, ok := ComplexTypeNameOf(t); ok {
				rank := complexRank(name)
				if rank > maxRank {
					maxRank = rank
				}
			}
			continue
		}
		if IsNumericType(UnwrapType(t)) || IsUntyped(UnwrapType(t)) {
			numericComplex := ComplexTypeForNumeric(t)
			if name, ok := ComplexTypeNameOf(numericComplex); ok {
				rank := complexRank(name)
				if rank > maxRank {
					maxRank = rank
				}
			}
		}
	}
	if maxRank < 0 {
		return TypeComplex
	}
	return complexTypeByRank(maxRank)
}

// IsLargePrimitiveTypeName checks if a type name is a 128-bit or 256-bit type
func IsLargePrimitiveTypeName(typeName TYPE_NAME) bool {
	switch typeName {
	case TYPE_I128, TYPE_U128, TYPE_I256, TYPE_U256, TYPE_F128, TYPE_F256:
		return true
	default:
		return false
	}
}

// IsLargePrimitive checks if a SemType is a 128-bit or 256-bit primitive type
func IsLargePrimitive(typ SemType) bool {
	if typ == nil {
		return false
	}
	typ = UnwrapType(typ)
	prim, ok := typ.(*PrimitiveType)
	if !ok {
		return false
	}
	return IsLargePrimitiveTypeName(prim.GetName())
}

// GetPowerResultType determines the result type for a power operation.
// For large primitives: returns the larger of the two operand types (by bit size)
// For floats: returns f64
// For integers: returns the larger type, or f64 if exponent is float
func GetPowerResultType(left, right SemType) SemType {
	if left == nil || right == nil {
		return TypeF64
	}
	left = UnwrapType(left)
	right = UnwrapType(right)

	leftPrim, leftOk := left.(*PrimitiveType)
	rightPrim, rightOk := right.(*PrimitiveType)

	if !leftOk || !rightOk {
		return TypeF64
	}

	leftName := leftPrim.GetName()
	rightName := rightPrim.GetName()
	leftSize := GetNumberBitSize(leftName)
	rightSize := GetNumberBitSize(rightName)

	// For large primitives, return the larger type
	if IsLargePrimitiveTypeName(leftName) || IsLargePrimitiveTypeName(rightName) {
		if leftSize >= rightSize {
			return left
		}
		return right
	}

	// For floats, always return f64
	if IsFloatTypeName(leftName) || IsFloatTypeName(rightName) {
		return TypeF64
	}

	// For regular integers, return f64 (since power can produce non-integer results)
	return TypeF64
}

// SignedIntPromotionSequence returns signed integer types ordered from the default upward.
func SignedIntPromotionSequence(defaultType TYPE_NAME) []TYPE_NAME {
	order := []TYPE_NAME{TYPE_I8, TYPE_I16, TYPE_I32, TYPE_I64, TYPE_I128, TYPE_I256}
	return promotionSequenceFrom(defaultType, order)
}

// UnsignedIntPromotionSequence returns unsigned integer types ordered from the default upward.
func UnsignedIntPromotionSequence(defaultType TYPE_NAME) []TYPE_NAME {
	order := []TYPE_NAME{TYPE_U8, TYPE_U16, TYPE_U32, TYPE_U64, TYPE_U128, TYPE_U256}
	return promotionSequenceFrom(defaultType, order)
}

// FloatPromotionSequence returns float types ordered from the default upward.
func FloatPromotionSequence(defaultType TYPE_NAME) []TYPE_NAME {
	order := []TYPE_NAME{TYPE_F32, TYPE_F64, TYPE_F128, TYPE_F256}
	return promotionSequenceFrom(defaultType, order)
}

func promotionSequenceFrom(defaultType TYPE_NAME, order []TYPE_NAME) []TYPE_NAME {
	start := 0
	for i, t := range order {
		if t == defaultType {
			start = i
			break
		}
	}
	return order[start:]
}

// UnwrapOptionalType unwraps optional types: ?T -> T
// Returns the inner type if it's optional, otherwise returns the original type.
func UnwrapOptionalType(typ SemType) SemType {
	if optType, ok := typ.(*OptionalType); ok {
		return optType.Inner
	}
	return typ
}

// DereferenceType unwraps reference types: &T -> T
// Returns the inner type if it's a reference, otherwise returns the original type.
func DereferenceType(typ SemType) SemType {
	if refType, ok := typ.(*ReferenceType); ok {
		return refType.Inner
	}
	return typ
}

// IsHeapType reports whether t is a heap ownership type (#T).
func IsHeapType(t SemType) bool {
	if t == nil {
		return false
	}
	_, ok := UnwrapType(t).(*HeapType)
	return ok
}

// UnwrapHeapType returns the payload type for #T, otherwise returns t unchanged.
func UnwrapHeapType(t SemType) SemType {
	if heapType, ok := UnwrapType(t).(*HeapType); ok {
		return heapType.Inner
	}
	return t
}

// IsResourceType reports whether t is a direct resource handle type.
func IsResourceType(t SemType) bool {
	if t == nil {
		return false
	}
	if named, ok := t.(*NamedType); ok {
		if named.Resource {
			return true
		}
		return IsResourceType(named.Underlying)
	}
	return false
}

// ContainsResourceType reports whether values of type t contain resource ownership.
// References are non-owning views and therefore return false.
func ContainsResourceType(t SemType) bool {
	seen := make(map[SemType]struct{})
	return containsResourceType(t, seen)
}

func containsResourceType(t SemType, seen map[SemType]struct{}) bool {
	if t == nil {
		return false
	}
	if _, ok := seen[t]; ok {
		return false
	}
	seen[t] = struct{}{}

	switch tt := t.(type) {
	case *NamedType:
		if tt.Resource {
			return true
		}
		return containsResourceType(tt.Underlying, seen)
	case *ReferenceType:
		return false
	case *HeapType:
		return containsResourceType(tt.Inner, seen)
	case *StructType:
		for _, f := range tt.Fields {
			if containsResourceType(f.Type, seen) {
				return true
			}
		}
		return false
	case *ArrayType:
		return containsResourceType(tt.Element, seen)
	case *MapType:
		return containsResourceType(tt.Key, seen) || containsResourceType(tt.Value, seen)
	case *OptionalType:
		return containsResourceType(tt.Inner, seen)
	case *ResultType:
		return containsResourceType(tt.Ok, seen) || containsResourceType(tt.Err, seen)
	case *UnionType:
		for _, v := range tt.Variants {
			if containsResourceType(v, seen) {
				return true
			}
		}
		return false
	case *EnumType:
		for _, v := range tt.Variants {
			if containsResourceType(v.Type, seen) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
