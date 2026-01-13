package consteval

import "math/big"

// NumericSign returns the sign of a numeric constant (-1, 0, 1).
func NumericSign(val *ConstValue) (int, bool) {
	if val == nil || !val.IsConstant() {
		return 0, false
	}
	if i, ok := val.AsInt(); ok {
		return i.Sign(), true
	}
	if f, ok := val.AsFloat(); ok {
		return f.Sign(), true
	}
	return 0, false
}

// NumericCompare compares two numeric constants.
// Returns -1, 0, 1 for a < b, a == b, a > b.
func NumericCompare(a, b *ConstValue) (int, bool) {
	if a == nil || b == nil || !a.IsConstant() || !b.IsConstant() {
		return 0, false
	}

	ai, aIsInt := a.AsInt()
	bi, bIsInt := b.AsInt()
	if aIsInt && bIsInt {
		return ai.Cmp(bi), true
	}

	af, aIsFloat := a.AsFloat()
	bf, bIsFloat := b.AsFloat()
	if !aIsFloat && aIsInt {
		af = new(big.Float).SetInt(ai)
		aIsFloat = true
	}
	if !bIsFloat && bIsInt {
		bf = new(big.Float).SetInt(bi)
		bIsFloat = true
	}
	if aIsFloat && bIsFloat {
		return af.Cmp(bf), true
	}

	return 0, false
}

// NumericIsFloat reports whether the constant is a float.
func NumericIsFloat(val *ConstValue) bool {
	if val == nil {
		return false
	}
	return val.Kind == ConstFloat
}

// NumericToBigInt returns the integer value if the constant is an int.
func NumericToBigInt(val *ConstValue) (*big.Int, bool) {
	if val == nil {
		return nil, false
	}
	return val.AsInt()
}

// NumericToBigFloat returns the float value if the constant is a float.
func NumericToBigFloat(val *ConstValue) (*big.Float, bool) {
	if val == nil {
		return nil, false
	}
	return val.AsFloat()
}
