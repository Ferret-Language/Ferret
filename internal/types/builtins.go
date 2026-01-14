package types

type TYPE_NAME string

const (
	TYPE_I8        TYPE_NAME = "i8"
	TYPE_I16       TYPE_NAME = "i16"
	TYPE_I32       TYPE_NAME = "i32"
	TYPE_I64       TYPE_NAME = "i64"
	TYPE_I128      TYPE_NAME = "i128"
	TYPE_I256      TYPE_NAME = "i256"
	TYPE_U8        TYPE_NAME = "u8"
	TYPE_U16       TYPE_NAME = "u16"
	TYPE_U32       TYPE_NAME = "u32"
	TYPE_U64       TYPE_NAME = "u64"
	TYPE_U128      TYPE_NAME = "u128"
	TYPE_U256      TYPE_NAME = "u256"
	TYPE_F32       TYPE_NAME = "f32"
	TYPE_F64       TYPE_NAME = "f64"
	TYPE_F128      TYPE_NAME = "f128"
	TYPE_F256      TYPE_NAME = "f256"
	TYPE_STRING    TYPE_NAME = "str"
	TYPE_BOOL      TYPE_NAME = "bool"
	TYPE_NONE      TYPE_NAME = "none"
	TYPE_VOID      TYPE_NAME = "void"
	TYPE_BYTE      TYPE_NAME = "byte"
	TYPE_FUNC      TYPE_NAME = "fn"
	TYPE_ARRAY     TYPE_NAME = "array"
	TYPE_INTERFACE TYPE_NAME = "interface"
	TYPE_STRUCT    TYPE_NAME = "struct"
	TYPE_ENUM      TYPE_NAME = "enum"
	TYPE_MAP       TYPE_NAME = "map"

	TYPE_UNTYPED TYPE_NAME = "untyped" // For untyped numeric literals before contextual instantiation

	TYPE_UNKNOWN TYPE_NAME = "unknown"
)

// Default type sizes for untyped literals
// Centralized here so users can configure default sizes in one place
const (
	// DEFAULT_INT_TYPE is the default type for untyped integer literals
	DEFAULT_INT_TYPE TYPE_NAME = TYPE_I32

	// DEFAULT_FLOAT_TYPE is the default type for untyped float literals
	DEFAULT_FLOAT_TYPE TYPE_NAME = TYPE_F32
)
