// Ferret runtime: Central type system definitions
// Shared primitive type list and runtime type metadata.

#ifndef FERRET_TYPE_SYSTEM_H
#define FERRET_TYPE_SYSTEM_H

#include <stddef.h>
#include <stdint.h>
#include <stdbool.h>
#include "bigint.h"

// Primitive types in Printable union order (ferret_libs/std/io.fer).
#define FERRET_PRIMITIVE_TYPES(X) \
    X(I8, i8, int8_t, INT, 8) \
    X(I16, i16, int16_t, INT, 16) \
    X(I32, i32, int32_t, INT, 32) \
    X(I64, i64, int64_t, INT, 64) \
    X(I128, i128, ferret_i128, INT, 128) \
    X(I256, i256, ferret_i256, INT, 256) \
    X(U8, u8, uint8_t, UINT, 8) \
    X(U16, u16, uint16_t, UINT, 16) \
    X(U32, u32, uint32_t, UINT, 32) \
    X(U64, u64, uint64_t, UINT, 64) \
    X(U128, u128, ferret_u128, UINT, 128) \
    X(U256, u256, ferret_u256, UINT, 256) \
    X(F32, f32, float, FLOAT, 32) \
    X(F64, f64, double, FLOAT, 64) \
    X(F128, f128, ferret_f128, FLOAT, 128) \
    X(F256, f256, ferret_f256, FLOAT, 256) \
    X(STRING, str, char*, STRING, 0) \
    X(BYTE, byte, uint8_t, BYTE, 8) \
    X(CHAR, char, uint32_t, CHAR, 32) \
    X(BOOL, bool, bool, BOOL, 1)

typedef enum {
#define FERRET_TYPE_ENUM(name, lower, ctype, category, bits) FERRET_TYPE_##name,
    FERRET_PRIMITIVE_TYPES(FERRET_TYPE_ENUM)
#undef FERRET_TYPE_ENUM
    FERRET_TYPE__PRIMITIVE_END,
    FERRET_TYPE_POINTER = FERRET_TYPE__PRIMITIVE_END,
    FERRET_TYPE_STRUCT,
    FERRET_TYPE_ARRAY,
    FERRET_TYPE_SLICE,
    FERRET_TYPE_MAP,
    FERRET_TYPE_FUNCTION,
    FERRET_TYPE_INTERFACE,
} ferret_type_kind_t;

typedef enum {
    FERRET_PRIMITIVE_INT,
    FERRET_PRIMITIVE_UINT,
    FERRET_PRIMITIVE_FLOAT,
    FERRET_PRIMITIVE_BOOL,
    FERRET_PRIMITIVE_BYTE,
    FERRET_PRIMITIVE_CHAR,
    FERRET_PRIMITIVE_STRING,
} ferret_primitive_category_t;

typedef struct {
    size_t size;
    ferret_primitive_category_t category;
} ferret_primitive_info_t;

extern const ferret_primitive_info_t ferret_primitive_info[FERRET_TYPE__PRIMITIVE_END];

// Forward declarations
struct ferret_type_info;

// Field information for struct types
typedef struct {
    size_t offset;                      // Offset in bytes from struct start
    struct ferret_type_info* type;      // Type of this field
} ferret_field_info_t;

// Runtime type information
typedef struct ferret_type_info {
    ferret_type_kind_t kind;
    size_t size;                        // Size in bytes

    union {
        // For FERRET_TYPE_STRUCT
        struct {
            size_t field_count;
            ferret_field_info_t* fields;
        } struct_info;

        // For FERRET_TYPE_ARRAY
        struct {
            size_t length;
            struct ferret_type_info* element_type;
        } array_info;

        // For FERRET_TYPE_SLICE
        struct {
            struct ferret_type_info* element_type;
        } slice_info;

        // For FERRET_TYPE_MAP
        struct {
            struct ferret_type_info* key_type;
            struct ferret_type_info* value_type;
        } map_info;

        // For FERRET_TYPE_POINTER
        struct {
            struct ferret_type_info* pointee_type;
        } pointer_info;
    };
} ferret_type_info_t;

// Built-in primitive type descriptors.
#define FERRET_DECLARE_PRIMITIVE_DESC(name, lower, ctype, category, bits) \
    extern const ferret_type_info_t ferret_type_##lower;
FERRET_PRIMITIVE_TYPES(FERRET_DECLARE_PRIMITIVE_DESC)
#undef FERRET_DECLARE_PRIMITIVE_DESC

static inline bool ferret_type_kind_is_primitive(ferret_type_kind_t kind) {
    return kind < FERRET_TYPE__PRIMITIVE_END;
}

static inline ferret_primitive_category_t ferret_primitive_category(ferret_type_kind_t kind) {
    if (!ferret_type_kind_is_primitive(kind)) {
        return FERRET_PRIMITIVE_INT;
    }
    return ferret_primitive_info[kind].category;
}

static inline size_t ferret_primitive_size(ferret_type_kind_t kind) {
    if (!ferret_type_kind_is_primitive(kind)) {
        return 0;
    }
    return ferret_primitive_info[kind].size;
}

static inline bool ferret_type_kind_is_float(ferret_type_kind_t kind) {
    return ferret_type_kind_is_primitive(kind) &&
        ferret_primitive_info[kind].category == FERRET_PRIMITIVE_FLOAT;
}

static inline bool ferret_type_kind_is_string(ferret_type_kind_t kind) {
    return kind == FERRET_TYPE_STRING;
}

static inline bool ferret_f32_eq(float a, float b) {
    return a == b;
}

static inline bool ferret_f64_eq(double a, double b) {
    return a == b;
}

bool ferret_primitive_equals(ferret_type_kind_t kind, const void* data1, const void* data2);

#endif // FERRET_TYPE_SYSTEM_H
