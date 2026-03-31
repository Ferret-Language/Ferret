package typeinfo

import "math/big"

type ConstValueKind uint8

const (
	ConstInvalid ConstValueKind = iota
	ConstInt
	ConstBool
	ConstString
	ConstNone
	ConstSequence
	ConstObject
)

type ConstValue struct {
	Kind       ConstValueKind
	Int        *big.Int
	Bool       bool
	String     string
	Elems      []ConstValue
	FieldNames []string
	Fields     []ConstValue
}

func (v ConstValue) Valid() bool {
	return v.Kind != ConstInvalid
}

func (v ConstValue) NonNegativeInt64() (int64, bool) {
	if v.Kind != ConstInt || v.Int == nil || v.Int.Sign() < 0 || !v.Int.IsInt64() {
		return 0, false
	}
	return v.Int.Int64(), true
}

func ApplyConstUnary(op string, right ConstValue) (ConstValue, bool) {
	switch op {
	case "comptime", "copy", "take", "unsafe", "?":
		return right, true
	case "!":
		if right.Kind != ConstBool {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstBool, Bool: !right.Bool}, true
	case "-":
		if right.Kind != ConstInt || right.Int == nil {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: new(big.Int).Neg(new(big.Int).Set(right.Int))}, true
	case "+":
		if right.Kind != ConstInt || right.Int == nil {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: new(big.Int).Set(right.Int)}, true
	default:
		return ConstValue{}, false
	}
}

func ApplyConstBinary(op string, left, right ConstValue) (ConstValue, bool) {
	switch {
	case left.Kind == ConstString && right.Kind == ConstString:
		switch op {
		case "+":
			return ConstValue{Kind: ConstString, String: left.String + right.String}, true
		case "==":
			return ConstValue{Kind: ConstBool, Bool: left.String == right.String}, true
		case "!=":
			return ConstValue{Kind: ConstBool, Bool: left.String != right.String}, true
		default:
			return ConstValue{}, false
		}
	case left.Kind == ConstNone && right.Kind == ConstNone:
		switch op {
		case "==":
			return ConstValue{Kind: ConstBool, Bool: true}, true
		case "!=":
			return ConstValue{Kind: ConstBool, Bool: false}, true
		default:
			return ConstValue{}, false
		}
	case left.Kind == ConstBool && right.Kind == ConstBool:
		switch op {
		case "&&":
			return ConstValue{Kind: ConstBool, Bool: left.Bool && right.Bool}, true
		case "||":
			return ConstValue{Kind: ConstBool, Bool: left.Bool || right.Bool}, true
		case "==":
			return ConstValue{Kind: ConstBool, Bool: left.Bool == right.Bool}, true
		case "!=":
			return ConstValue{Kind: ConstBool, Bool: left.Bool != right.Bool}, true
		default:
			return ConstValue{}, false
		}
	case left.Kind == ConstInt && left.Int != nil && right.Kind == ConstInt && right.Int != nil:
		l := new(big.Int).Set(left.Int)
		r := new(big.Int).Set(right.Int)
		switch op {
		case "+":
			return ConstValue{Kind: ConstInt, Int: new(big.Int).Add(l, r)}, true
		case "-":
			return ConstValue{Kind: ConstInt, Int: new(big.Int).Sub(l, r)}, true
		case "*":
			return ConstValue{Kind: ConstInt, Int: new(big.Int).Mul(l, r)}, true
		case "/":
			if r.Sign() == 0 {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstInt, Int: new(big.Int).Quo(l, r)}, true
		case "%":
			if r.Sign() == 0 {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstInt, Int: new(big.Int).Rem(l, r)}, true
		case "==":
			return ConstValue{Kind: ConstBool, Bool: l.Cmp(r) == 0}, true
		case "!=":
			return ConstValue{Kind: ConstBool, Bool: l.Cmp(r) != 0}, true
		case "<":
			return ConstValue{Kind: ConstBool, Bool: l.Cmp(r) < 0}, true
		case "<=":
			return ConstValue{Kind: ConstBool, Bool: l.Cmp(r) <= 0}, true
		case ">":
			return ConstValue{Kind: ConstBool, Bool: l.Cmp(r) > 0}, true
		case ">=":
			return ConstValue{Kind: ConstBool, Bool: l.Cmp(r) >= 0}, true
		default:
			return ConstValue{}, false
		}
	default:
		return ConstValue{}, false
	}
}
