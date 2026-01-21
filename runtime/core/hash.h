// Ferret runtime: Universal Hashing
// Content-based hashing for all types (structs, maps, slices, etc.)

#ifndef FERRET_HASH_H
#define FERRET_HASH_H

#include "type_system.h"

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
