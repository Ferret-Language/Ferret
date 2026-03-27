package typeinfo

import "math/big"

type ConstValueKind uint8

const (
	ConstInvalid ConstValueKind = iota
	ConstInt
	ConstBool
	ConstString
	ConstNone
)

type ConstValue struct {
	Kind   ConstValueKind
	Int    *big.Int
	Bool   bool
	String string
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
