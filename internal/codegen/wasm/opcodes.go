package wasm

import (
	"encoding/binary"
	"fmt"
	"math"

	"compiler/internal/tokens"
	"compiler/internal/types"
)

const (
	blockTypeVoid = 0x40
)

const (
	opcodeUnreachable    = 0x00
	opcodeBr             = 0x0c
	opcodeReturn         = 0x0f
	opcodeCall           = 0x10
	opcodeCallIndirect   = 0x11
	opcodeDrop           = 0x1a
	opcodeLocalGet       = 0x20
	opcodeLocalSet       = 0x21
	opcodeLocalTee       = 0x22
	opcodeI32Const       = 0x41
	opcodeI64Const       = 0x42
	opcodeF32Const       = 0x43
	opcodeF64Const       = 0x44
	opcodeI32Eqz         = 0x45
	opcodeI32Eq          = 0x46
	opcodeI32Ne          = 0x47
	opcodeI32LtS         = 0x48
	opcodeI32LtU         = 0x49
	opcodeI32GtS         = 0x4a
	opcodeI32GtU         = 0x4b
	opcodeI32LeS         = 0x4c
	opcodeI32LeU         = 0x4d
	opcodeI32GeS         = 0x4e
	opcodeI32GeU         = 0x4f
	opcodeI64Eq          = 0x51
	opcodeI64Ne          = 0x52
	opcodeI64LtS         = 0x53
	opcodeI64LtU         = 0x54
	opcodeI64GtS         = 0x55
	opcodeI64GtU         = 0x56
	opcodeI64LeS         = 0x57
	opcodeI64LeU         = 0x58
	opcodeI64GeS         = 0x59
	opcodeI64GeU         = 0x5a
	opcodeF32Eq          = 0x5b
	opcodeF32Ne          = 0x5c
	opcodeF32Lt          = 0x5d
	opcodeF32Gt          = 0x5e
	opcodeF32Le          = 0x5f
	opcodeF32Ge          = 0x60
	opcodeF64Eq          = 0x61
	opcodeF64Ne          = 0x62
	opcodeF64Lt          = 0x63
	opcodeF64Gt          = 0x64
	opcodeF64Le          = 0x65
	opcodeF64Ge          = 0x66
	opcodeI32Add         = 0x6a
	opcodeI32Sub         = 0x6b
	opcodeI32Mul         = 0x6c
	opcodeI32DivS        = 0x6d
	opcodeI32DivU        = 0x6e
	opcodeI32RemS        = 0x6f
	opcodeI32RemU        = 0x70
	opcodeI32And         = 0x71
	opcodeI32Or          = 0x72
	opcodeI32Xor         = 0x73
	opcodeI32Shl         = 0x74
	opcodeI32ShrS        = 0x75
	opcodeI32ShrU        = 0x76
	opcodeI64Add         = 0x7c
	opcodeI64Sub         = 0x7d
	opcodeI64Mul         = 0x7e
	opcodeI64DivS        = 0x7f
	opcodeI64DivU        = 0x80
	opcodeI64RemS        = 0x81
	opcodeI64RemU        = 0x82
	opcodeI64And         = 0x83
	opcodeI64Or          = 0x84
	opcodeI64Xor         = 0x85
	opcodeI64Shl         = 0x86
	opcodeI64ShrS        = 0x87
	opcodeI64ShrU        = 0x88
	opcodeF32Neg         = 0x8c
	opcodeF32Add         = 0x92
	opcodeF32Sub         = 0x93
	opcodeF32Mul         = 0x94
	opcodeF32Div         = 0x95
	opcodeF64Neg         = 0x9a
	opcodeF64Add         = 0xa0
	opcodeF64Sub         = 0xa1
	opcodeF64Mul         = 0xa2
	opcodeF64Div         = 0xa3
	opcodeI32WrapI64     = 0xa7
	opcodeI32TruncF32S   = 0xa8
	opcodeI32TruncF32U   = 0xa9
	opcodeI32TruncF64S   = 0xaa
	opcodeI32TruncF64U   = 0xab
	opcodeI64ExtendI32S  = 0xac
	opcodeI64ExtendI32U  = 0xad
	opcodeI64TruncF32S   = 0xae
	opcodeI64TruncF32U   = 0xaf
	opcodeI64TruncF64S   = 0xb0
	opcodeI64TruncF64U   = 0xb1
	opcodeF32ConvertI32S = 0xb2
	opcodeF32ConvertI32U = 0xb3
	opcodeF32ConvertI64S = 0xb4
	opcodeF32ConvertI64U = 0xb5
	opcodeF32DemoteF64   = 0xb6
	opcodeF64ConvertI32S = 0xb7
	opcodeF64ConvertI32U = 0xb8
	opcodeF64ConvertI64S = 0xb9
	opcodeF64ConvertI64U = 0xba
	opcodeF64PromoteF32  = 0xbb
	opcodeEnd            = 0x0b
	opcodeIf             = 0x04
	opcodeElse           = 0x05
	opcodeLoop           = 0x03
)

const (
	opcodeI32Load    = 0x28
	opcodeI64Load    = 0x29
	opcodeF32Load    = 0x2a
	opcodeF64Load    = 0x2b
	opcodeI32Load8S  = 0x2c
	opcodeI32Load8U  = 0x2d
	opcodeI32Load16S = 0x2e
	opcodeI32Load16U = 0x2f
	opcodeI32Store   = 0x36
	opcodeI64Store   = 0x37
	opcodeF32Store   = 0x38
	opcodeF64Store   = 0x39
	opcodeI32Store8  = 0x3a
	opcodeI32Store16 = 0x3b
)

func binaryOpcode(op tokens.TOKEN, valType ValType, unsigned bool) ([]byte, error) {
	switch op {
	case tokens.PLUS_TOKEN:
		switch valType {
		case valTypeI32:
			return []byte{opcodeI32Add}, nil
		case valTypeI64:
			return []byte{opcodeI64Add}, nil
		case valTypeF32:
			return []byte{opcodeF32Add}, nil
		case valTypeF64:
			return []byte{opcodeF64Add}, nil
		}
	case tokens.MINUS_TOKEN:
		switch valType {
		case valTypeI32:
			return []byte{opcodeI32Sub}, nil
		case valTypeI64:
			return []byte{opcodeI64Sub}, nil
		case valTypeF32:
			return []byte{opcodeF32Sub}, nil
		case valTypeF64:
			return []byte{opcodeF64Sub}, nil
		}
	case tokens.MUL_TOKEN:
		switch valType {
		case valTypeI32:
			return []byte{opcodeI32Mul}, nil
		case valTypeI64:
			return []byte{opcodeI64Mul}, nil
		case valTypeF32:
			return []byte{opcodeF32Mul}, nil
		case valTypeF64:
			return []byte{opcodeF64Mul}, nil
		}
	case tokens.DIV_TOKEN:
		switch valType {
		case valTypeI32:
			if unsigned {
				return []byte{opcodeI32DivU}, nil
			}
			return []byte{opcodeI32DivS}, nil
		case valTypeI64:
			if unsigned {
				return []byte{opcodeI64DivU}, nil
			}
			return []byte{opcodeI64DivS}, nil
		case valTypeF32:
			return []byte{opcodeF32Div}, nil
		case valTypeF64:
			return []byte{opcodeF64Div}, nil
		}
	case tokens.MOD_TOKEN:
		switch valType {
		case valTypeI32:
			if unsigned {
				return []byte{opcodeI32RemU}, nil
			}
			return []byte{opcodeI32RemS}, nil
		case valTypeI64:
			if unsigned {
				return []byte{opcodeI64RemU}, nil
			}
			return []byte{opcodeI64RemS}, nil
		}
	case tokens.BIT_AND_TOKEN, tokens.AND_TOKEN:
		switch valType {
		case valTypeI32:
			return []byte{opcodeI32And}, nil
		case valTypeI64:
			return []byte{opcodeI64And}, nil
		}
	case tokens.BIT_OR_TOKEN, tokens.OR_TOKEN:
		switch valType {
		case valTypeI32:
			return []byte{opcodeI32Or}, nil
		case valTypeI64:
			return []byte{opcodeI64Or}, nil
		}
	case tokens.BIT_XOR_TOKEN:
		switch valType {
		case valTypeI32:
			return []byte{opcodeI32Xor}, nil
		case valTypeI64:
			return []byte{opcodeI64Xor}, nil
		}
	case tokens.LESS_TOKEN:
		return compareOpcode(valType, unsigned, opcodeI32LtS, opcodeI32LtU, opcodeI64LtS, opcodeI64LtU, opcodeF32Lt, opcodeF64Lt)
	case tokens.GREATER_TOKEN:
		return compareOpcode(valType, unsigned, opcodeI32GtS, opcodeI32GtU, opcodeI64GtS, opcodeI64GtU, opcodeF32Gt, opcodeF64Gt)
	case tokens.LESS_EQUAL_TOKEN:
		return compareOpcode(valType, unsigned, opcodeI32LeS, opcodeI32LeU, opcodeI64LeS, opcodeI64LeU, opcodeF32Le, opcodeF64Le)
	case tokens.GREATER_EQUAL_TOKEN:
		return compareOpcode(valType, unsigned, opcodeI32GeS, opcodeI32GeU, opcodeI64GeS, opcodeI64GeU, opcodeF32Ge, opcodeF64Ge)
	case tokens.DOUBLE_EQUAL_TOKEN:
		return compareOpcode(valType, unsigned, opcodeI32Eq, opcodeI32Eq, opcodeI64Eq, opcodeI64Eq, opcodeF32Eq, opcodeF64Eq)
	case tokens.NOT_EQUAL_TOKEN:
		return compareOpcode(valType, unsigned, opcodeI32Ne, opcodeI32Ne, opcodeI64Ne, opcodeI64Ne, opcodeF32Ne, opcodeF64Ne)
	}
	return nil, fmt.Errorf("wasm: unsupported binary op %s", op)
}

func compareOpcode(valType ValType, unsigned bool, i32s, i32u, i64s, i64u, f32, f64 byte) ([]byte, error) {
	switch valType {
	case valTypeI32:
		if unsigned {
			return []byte{i32u}, nil
		}
		return []byte{i32s}, nil
	case valTypeI64:
		if unsigned {
			return []byte{i64u}, nil
		}
		return []byte{i64s}, nil
	case valTypeF32:
		return []byte{f32}, nil
	case valTypeF64:
		return []byte{f64}, nil
	}
	return nil, fmt.Errorf("wasm: invalid compare type")
}

func castOpcode(from, to ValType, unsigned bool) ([]byte, bool) {
	if from == to {
		return nil, true
	}
	switch from {
	case valTypeI32:
		switch to {
		case valTypeI64:
			if unsigned {
				return []byte{opcodeI64ExtendI32U}, true
			}
			return []byte{opcodeI64ExtendI32S}, true
		case valTypeF32:
			if unsigned {
				return []byte{opcodeF32ConvertI32U}, true
			}
			return []byte{opcodeF32ConvertI32S}, true
		case valTypeF64:
			if unsigned {
				return []byte{opcodeF64ConvertI32U}, true
			}
			return []byte{opcodeF64ConvertI32S}, true
		}
	case valTypeI64:
		switch to {
		case valTypeI32:
			return []byte{opcodeI32WrapI64}, true
		case valTypeF32:
			if unsigned {
				return []byte{opcodeF32ConvertI64U}, true
			}
			return []byte{opcodeF32ConvertI64S}, true
		case valTypeF64:
			if unsigned {
				return []byte{opcodeF64ConvertI64U}, true
			}
			return []byte{opcodeF64ConvertI64S}, true
		}
	case valTypeF32:
		switch to {
		case valTypeI32:
			if unsigned {
				return []byte{opcodeI32TruncF32U}, true
			}
			return []byte{opcodeI32TruncF32S}, true
		case valTypeI64:
			if unsigned {
				return []byte{opcodeI64TruncF32U}, true
			}
			return []byte{opcodeI64TruncF32S}, true
		case valTypeF64:
			return []byte{opcodeF64PromoteF32}, true
		}
	case valTypeF64:
		switch to {
		case valTypeI32:
			if unsigned {
				return []byte{opcodeI32TruncF64U}, true
			}
			return []byte{opcodeI32TruncF64S}, true
		case valTypeI64:
			if unsigned {
				return []byte{opcodeI64TruncF64U}, true
			}
			return []byte{opcodeI64TruncF64S}, true
		case valTypeF32:
			return []byte{opcodeF32DemoteF64}, true
		}
	}
	return nil, false
}

func loadOpcode(typ types.SemType) ([]byte, error) {
	if prim, ok := types.UnwrapType(typ).(*types.PrimitiveType); ok {
		switch prim.GetName() {
		case types.TYPE_I8:
			return memOpcode(opcodeI32Load8S), nil
		case types.TYPE_U8, types.TYPE_BOOL, types.TYPE_BYTE:
			return memOpcode(opcodeI32Load8U), nil
		case types.TYPE_I16:
			return memOpcode(opcodeI32Load16S), nil
		case types.TYPE_U16:
			return memOpcode(opcodeI32Load16U), nil
		case types.TYPE_I32, types.TYPE_U32, types.TYPE_STRING, types.TYPE_CHAR:
			return memOpcode(opcodeI32Load), nil
		case types.TYPE_I64, types.TYPE_U64:
			return memOpcode(opcodeI64Load), nil
		case types.TYPE_F32:
			return memOpcode(opcodeF32Load), nil
		case types.TYPE_F64:
			return memOpcode(opcodeF64Load), nil
		}
	}
	return memOpcode(opcodeI32Load), nil
}

func storeOpcode(typ types.SemType) ([]byte, error) {
	if prim, ok := types.UnwrapType(typ).(*types.PrimitiveType); ok {
		switch prim.GetName() {
		case types.TYPE_I8, types.TYPE_U8, types.TYPE_BOOL, types.TYPE_BYTE:
			return memOpcode(opcodeI32Store8), nil
		case types.TYPE_I16, types.TYPE_U16:
			return memOpcode(opcodeI32Store16), nil
		case types.TYPE_I32, types.TYPE_U32, types.TYPE_STRING, types.TYPE_CHAR:
			return memOpcode(opcodeI32Store), nil
		case types.TYPE_I64, types.TYPE_U64:
			return memOpcode(opcodeI64Store), nil
		case types.TYPE_F32:
			return memOpcode(opcodeF32Store), nil
		case types.TYPE_F64:
			return memOpcode(opcodeF64Store), nil
		}
	}
	return memOpcode(opcodeI32Store), nil
}

func memOpcode(op byte) []byte {
	out := []byte{op}
	out = append(out, encodeU32(0)...)
	out = append(out, encodeU32(0)...)
	return out
}

func encodeF32(v float32) []byte {
	bits := math.Float32bits(v)
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, bits)
	return buf
}

func encodeF64(v float64) []byte {
	bits := math.Float64bits(v)
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, bits)
	return buf
}
