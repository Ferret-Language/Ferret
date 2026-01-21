// Ferret runtime: Universal Hashing
// Content-based hashing for all types (structs, maps, slices, etc.)

#ifndef FERRET_HASH_H
#define FERRET_HASH_H

#include <stdint.h>
#include <stdbool.h>
#include <stdlib.h>

// Type kinds for runtime type information
typedef enum {
    FERRET_TYPE_I32,
    FERRET_TYPE_I64,
    FERRET_TYPE_F32,
    FERRET_TYPE_F64,
    FERRET_TYPE_BOOL,
    FERRET_TYPE_BYTE,
    FERRET_TYPE_STRING,
    FERRET_TYPE_POINTER,
    FERRET_TYPE_STRUCT,
    FERRET_TYPE_ARRAY,
    FERRET_TYPE_SLICE,
    FERRET_TYPE_MAP,
    FERRET_TYPE_FUNCTION,
    FERRET_TYPE_INTERFACE,
} ferret_type_kind_t;

// Forward declarations
struct ferret_type_info;
struct ferret_map;

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

// Universal hash function that handles all types using type info
uint32_t ferret_hash_universal(const void* data, ferret_type_info_t* type_info);

// Universal equality function that handles all types using type info
bool ferret_equals_universal(const void* data1, const void* data2, ferret_type_info_t* type_info);

// Convenience functions for creating type info
ferret_type_info_t* ferret_type_info_primitive(ferret_type_kind_t kind, size_t size);
ferret_type_info_t* ferret_type_info_struct(size_t field_count, ferret_field_info_t* fields, size_t size);
ferret_type_info_t* ferret_type_info_array(ferret_type_info_t* element_type, size_t length);
ferret_type_info_t* ferret_type_info_slice(ferret_type_info_t* element_type);
ferret_type_info_t* ferret_type_info_map(ferret_type_info_t* key_type, ferret_type_info_t* value_type);
ferret_type_info_t* ferret_type_info_pointer(ferret_type_info_t* pointee_type);
void ferret_type_info_free(ferret_type_info_t* type_info);

#endif // FERRET_HASH_H
