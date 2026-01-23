// Ferret runtime: Central type system definitions
// Shared primitive type list and runtime type metadata.

#ifndef FERRET_TYPE_SYSTEM_H
#define FERRET_TYPE_SYSTEM_H

#include <stddef.h>
#include <stdint.h>
#include <stdbool.h>
#include "bigint.h"
#include "abi_constants.h"
#include "primitive_types.h"

typedef enum {
#define FERRET_TYPE_ENUM(name, lower, ctype, category, bits) FERRET_TYPE_##name = FERRET_TYPE_KIND_##name,
    FERRET_PRIMITIVE_TYPES(FERRET_TYPE_ENUM)
#undef FERRET_TYPE_ENUM
    FERRET_TYPE__PRIMITIVE_END = FERRET_TYPE_KIND_POINTER,
    FERRET_TYPE_POINTER = FERRET_TYPE_KIND_POINTER,
    FERRET_TYPE_STRUCT = FERRET_TYPE_KIND_STRUCT,
    FERRET_TYPE_ARRAY = FERRET_TYPE_KIND_ARRAY,
    FERRET_TYPE_SLICE = FERRET_TYPE_KIND_SLICE,
    FERRET_TYPE_MAP = FERRET_TYPE_KIND_MAP,
    FERRET_TYPE_FUNCTION = FERRET_TYPE_KIND_FUNCTION,
    FERRET_TYPE_INTERFACE = FERRET_TYPE_KIND_INTERFACE,
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

        // For FERRET_TYPE_INTERFACE
        struct {
            size_t method_count;
        } interface_info;
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
