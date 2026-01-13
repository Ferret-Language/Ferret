package consteval

import (
	"compiler/internal/types"
	"compiler/internal/utils/numeric"
	"math/big"
)

// inferIntType determines the appropriate integer type based on value size.
func inferIntType(val *big.Int) types.SemType {
	defaultName := types.DEFAULT_INT_TYPE
	promotionSequence := types.SignedIntPromotionSequence(defaultName)
	if types.IsUnsigned(defaultName) {
		promotionSequence = types.UnsignedIntPromotionSequence(defaultName)
	}

	for _, typeName := range promotionSequence {
		bitSize := int(types.GetNumberBitSize(typeName))
		if bitSize == 0 {
			continue
		}
		signed := types.IsSigned(typeName)
		if numeric.FitsInBitSize(val, bitSize, signed) {
			return types.FromTypeName(typeName)
		}
	}

	return types.TypeUnknown
}

// Arithmetic helpers.
func evalAdd(left, right *ConstValue) *ConstValue {
	if leftVal, ok := left.AsInt(); ok {
		if rightVal, ok := right.AsInt(); ok {
			result := new(big.Int).Add(leftVal, rightVal)
			return NewBigIntValue(result, left.Type)
		}
	}
	if leftVal, ok := left.AsFloat(); ok {
		if rightVal, ok := right.AsFloat(); ok {
			result := new(big.Float).Add(leftVal, rightVal)
			return NewBigFloatValue(result, left.Type)
		}
	}
	return nil
}

func evalSubtract(left, right *ConstValue) *ConstValue {
	if leftVal, ok := left.AsInt(); ok {
		if rightVal, ok := right.AsInt(); ok {
			result := new(big.Int).Sub(leftVal, rightVal)
			return NewBigIntValue(result, left.Type)
		}
	}
	if leftVal, ok := left.AsFloat(); ok {
		if rightVal, ok := right.AsFloat(); ok {
			result := new(big.Float).Sub(leftVal, rightVal)
			return NewBigFloatValue(result, left.Type)
		}
	}
	return nil
}

func evalMultiply(left, right *ConstValue) *ConstValue {
	if leftVal, ok := left.AsInt(); ok {
		if rightVal, ok := right.AsInt(); ok {
			result := new(big.Int).Mul(leftVal, rightVal)
			return NewBigIntValue(result, left.Type)
		}
	}
	if leftVal, ok := left.AsFloat(); ok {
		if rightVal, ok := right.AsFloat(); ok {
			result := new(big.Float).Mul(leftVal, rightVal)
			return NewBigFloatValue(result, left.Type)
		}
	}
	return nil
}

func evalDivide(left, right *ConstValue) *ConstValue {
	if leftVal, ok := left.AsInt(); ok {
		if rightVal, ok := right.AsInt(); ok {
			if rightVal.Sign() == 0 {
				return nil // Division by zero.
			}
			result := new(big.Int).Div(leftVal, rightVal)
			return NewBigIntValue(result, left.Type)
		}
	}
	if leftVal, ok := left.AsFloat(); ok {
		if rightVal, ok := right.AsFloat(); ok {
			if rightVal.Sign() == 0 {
				return nil // Division by zero.
			}
			result := new(big.Float).Quo(leftVal, rightVal)
			return NewBigFloatValue(result, left.Type)
		}
	}
	return nil
}

func evalModulo(left, right *ConstValue) *ConstValue {
	if leftVal, ok := left.AsInt(); ok {
		if rightVal, ok := right.AsInt(); ok {
			if rightVal.Sign() == 0 {
				return nil // Modulo by zero.
			}
			result := new(big.Int).Mod(leftVal, rightVal)
			return NewBigIntValue(result, left.Type)
		}
	}
	return nil
}

// Comparison helpers.
func evalLessThan(left, right *ConstValue) *ConstValue {
	cmp := compareValues(left, right)
	if cmp == nil {
		return nil
	}
	return NewBoolValue(*cmp < 0)
}

func evalGreaterThan(left, right *ConstValue) *ConstValue {
	cmp := compareValues(left, right)
	if cmp == nil {
		return nil
	}
	return NewBoolValue(*cmp > 0)
}

func evalLessThanEqual(left, right *ConstValue) *ConstValue {
	cmp := compareValues(left, right)
	if cmp == nil {
		return nil
	}
	return NewBoolValue(*cmp <= 0)
}

func evalGreaterThanEqual(left, right *ConstValue) *ConstValue {
	cmp := compareValues(left, right)
	if cmp == nil {
		return nil
	}
	return NewBoolValue(*cmp >= 0)
}

// compareValues returns -1, 0, 1 for less, equal, greater.
func compareValues(left, right *ConstValue) *int {
	if leftVal, ok := left.AsInt(); ok {
		if rightVal, ok := right.AsInt(); ok {
			cmp := leftVal.Cmp(rightVal)
			return &cmp
		}
	}
	if leftVal, ok := left.AsFloat(); ok {
		if rightVal, ok := right.AsFloat(); ok {
			cmp := leftVal.Cmp(rightVal)
			return &cmp
		}
	}
	if leftVal, ok := left.AsString(); ok {
		if rightVal, ok := right.AsString(); ok {
			var cmp int
			if leftVal < rightVal {
				cmp = -1
			} else if leftVal > rightVal {
				cmp = 1
			} else {
				cmp = 0
			}
			return &cmp
		}
	}
	return nil
}

// Logical helpers.
func evalLogicalAnd(left, right *ConstValue) *ConstValue {
	if leftVal, ok := left.AsBool(); ok {
		if rightVal, ok := right.AsBool(); ok {
			return NewBoolValue(leftVal && rightVal)
		}
	}
	return nil
}

func evalLogicalOr(left, right *ConstValue) *ConstValue {
	if leftVal, ok := left.AsBool(); ok {
		if rightVal, ok := right.AsBool(); ok {
			return NewBoolValue(leftVal || rightVal)
		}
	}
	return nil
}
