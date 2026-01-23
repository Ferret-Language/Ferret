package abi

import "compiler/internal/types"

// MapKeyKinds defines runtime map key hashing strategies.
var MapKeyKinds = []string{
	"i32",
	"i64",
	"f32",
	"f64",
	"str",
	"bytes",
	"numeric",
	"universal",
}

// PrimitiveSpec describes a primitive type for the native runtime.
type PrimitiveSpec struct {
	Name     types.TYPE_NAME
	CType    string
	Category string
	Bits     int
}

// PrimitiveSpecs define native primitive metadata in ABI order.
var PrimitiveSpecs = []PrimitiveSpec{
	{Name: types.TYPE_I8, CType: "int8_t", Category: "INT", Bits: 8},
	{Name: types.TYPE_I16, CType: "int16_t", Category: "INT", Bits: 16},
	{Name: types.TYPE_I32, CType: "int32_t", Category: "INT", Bits: 32},
	{Name: types.TYPE_I64, CType: "int64_t", Category: "INT", Bits: 64},
	{Name: types.TYPE_I128, CType: "ferret_i128", Category: "INT", Bits: 128},
	{Name: types.TYPE_I256, CType: "ferret_i256", Category: "INT", Bits: 256},
	{Name: types.TYPE_U8, CType: "uint8_t", Category: "UINT", Bits: 8},
	{Name: types.TYPE_U16, CType: "uint16_t", Category: "UINT", Bits: 16},
	{Name: types.TYPE_U32, CType: "uint32_t", Category: "UINT", Bits: 32},
	{Name: types.TYPE_U64, CType: "uint64_t", Category: "UINT", Bits: 64},
	{Name: types.TYPE_U128, CType: "ferret_u128", Category: "UINT", Bits: 128},
	{Name: types.TYPE_U256, CType: "ferret_u256", Category: "UINT", Bits: 256},
	{Name: types.TYPE_F32, CType: "float", Category: "FLOAT", Bits: 32},
	{Name: types.TYPE_F64, CType: "double", Category: "FLOAT", Bits: 64},
	{Name: types.TYPE_F128, CType: "ferret_f128", Category: "FLOAT", Bits: 128},
	{Name: types.TYPE_F256, CType: "ferret_f256", Category: "FLOAT", Bits: 256},
	{Name: types.TYPE_STRING, CType: "char*", Category: "STRING", Bits: 0},
	{Name: types.TYPE_BYTE, CType: "uint8_t", Category: "BYTE", Bits: 8},
	{Name: types.TYPE_CHAR, CType: "uint32_t", Category: "CHAR", Bits: 32},
	{Name: types.TYPE_BOOL, CType: "bool", Category: "BOOL", Bits: 1},
}

// IntWidths defines the supported big integer bit widths.
var IntWidths = []int{128, 256}

// FloatSpec describes a soft-float format.
type FloatSpec struct {
	Bits          int
	Words         int
	FracBits      int
	ExpBits       int
	ExpBias       int
	DecimalDigits int
}

// FloatSpecs defines the supported soft-float formats.
var FloatSpecs = []FloatSpec{
	{Bits: 128, Words: 2, FracBits: 112, ExpBits: 15, ExpBias: 16383, DecimalDigits: 36},
	{Bits: 256, Words: 4, FracBits: 236, ExpBits: 19, ExpBias: 262143, DecimalDigits: 73},
}

// SoftExtraBits is the guard/round/sticky padding used in soft-float operations.
const SoftExtraBits = 3

// Type descriptor layout for the WASM runtime ABI (packed u32 fields).
const (
	TypeDescKindOffset  uint32 = 0
	TypeDescSizeOffset  uint32 = 4
	TypeDescInfo1Offset uint32 = 8
	TypeDescInfo2Offset uint32 = 12
	TypeDescSizeBytes   uint32 = 16

	FieldInfoOffsetOffset uint32 = 0
	FieldInfoTypeOffset   uint32 = 4
	FieldInfoSizeBytes    uint32 = 8
)

// WASM runtime layout constants (pointer size 4).
const (
	ArrayDataOffset     uint32 = 0
	ArrayLenOffset      uint32 = 4
	ArrayCapOffset      uint32 = 8
	ArrayElemSizeOffset uint32 = 12
	ArrayElemTypeOffset uint32 = 16
	ArrayHeaderSize     uint32 = 20

	SliceDataOffset     uint32 = ArrayDataOffset
	SliceLenOffset      uint32 = ArrayLenOffset
	SliceCapOffset      uint32 = ArrayCapOffset
	SliceElemSizeOffset uint32 = ArrayElemSizeOffset
	SliceElemTypeOffset uint32 = ArrayElemTypeOffset
	SliceHeaderSize     uint32 = ArrayHeaderSize

	InterfaceDataOffset  uint32 = 0
	InterfaceExtraOffset uint32 = 4
	InterfaceSize        uint32 = 8

	UnionTagSize   uint32 = 4
	UnionPtrOffset uint32 = 4

	MapHandleSize uint32 = 8
)

// Hash constants shared across runtimes.
const (
	MapHashSeed    uint32 = 0x9747b28c
	FNVOffsetBasis uint32 = 2166136261
	FNVPrime       uint32 = 16777619
)

// Core WASM runtime import names used by codegen.
const (
	WasmImportAlloc             = "ferret_alloc"
	WasmImportMemcpy            = "ferret_memcpy"
	WasmImportArrayGet          = "ferret_array_get"
	WasmImportArrayNew          = "ferret_array_new"
	WasmImportArrayAppend       = "ferret_array_append"
	WasmImportArraySet          = "ferret_array_set"
	WasmImportMapGet            = "ferret_map_get"
	WasmImportMapGetOptionalOut = "ferret_map_get_optional_out"
	WasmImportMapSet            = "ferret_map_set"
	WasmImportGlobalPanic       = "ferret_global_panic"
	WasmImportPow               = "ferret_pow"
	WasmImportOptionalUnwrapOr  = "ferret_optional_unwrap_or"
)

var WasmCoreImportNames = []string{
	WasmImportAlloc,
	WasmImportMemcpy,
	WasmImportArrayGet,
	WasmImportArrayNew,
	WasmImportArrayAppend,
	WasmImportArraySet,
	WasmImportMapGet,
	WasmImportMapGetOptionalOut,
	WasmImportMapSet,
	WasmImportGlobalPanic,
	WasmImportPow,
	WasmImportOptionalUnwrapOr,
}

// QBEWordType returns the QBE type for a pointer-sized value.
func QBEWordType(pointerSize int) string {
	if pointerSize <= 4 {
		return "w"
	}
	return "l"
}

// QBETypeDescNeedsPad reports if a padding word is needed after the kind field.
func QBETypeDescNeedsPad(pointerSize int) bool {
	return pointerSize > 4
}
