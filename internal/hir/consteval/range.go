package consteval

import "math/big"

// RangeLengthFromInts computes the element count for an integer range.
// It returns false when the range is invalid or length doesn't fit in int.
func RangeLengthFromInts(start, end, step *big.Int, inclusive bool) (int, bool) {
	if start == nil || end == nil || step == nil {
		return 0, false
	}
	if step.Sign() == 0 {
		return 0, false
	}

	startVal := new(big.Int).Set(start)
	endVal := new(big.Int).Set(end)
	stepVal := new(big.Int).Set(step)
	stepSign := stepVal.Sign()
	cmp := startVal.Cmp(endVal)
	if (cmp < 0 && stepSign < 0) || (cmp > 0 && stepSign > 0) {
		return 0, false
	}

	one := big.NewInt(1)
	var length *big.Int
	if stepSign > 0 {
		if inclusive {
			if cmp > 0 {
				return 0, true
			}
			diff := new(big.Int).Sub(endVal, startVal)
			diff.Div(diff, stepVal)
			length = diff.Add(diff, one)
		} else {
			if cmp >= 0 {
				return 0, true
			}
			diff := new(big.Int).Sub(endVal, startVal)
			diff.Add(diff, new(big.Int).Sub(stepVal, one))
			length = diff.Div(diff, stepVal)
		}
	} else {
		stepAbs := new(big.Int).Abs(stepVal)
		if inclusive {
			if cmp < 0 {
				return 0, true
			}
			diff := new(big.Int).Sub(startVal, endVal)
			diff.Div(diff, stepAbs)
			length = diff.Add(diff, one)
		} else {
			if cmp <= 0 {
				return 0, true
			}
			diff := new(big.Int).Sub(startVal, endVal)
			diff.Add(diff, new(big.Int).Sub(stepAbs, one))
			length = diff.Div(diff, stepAbs)
		}
	}

	if length == nil || length.Sign() < 0 {
		return 0, false
	}

	maxInt := big.NewInt(int64(^uint(0) >> 1))
	if length.Cmp(maxInt) > 0 {
		return 0, false
	}

	return int(length.Int64()), true
}
