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
