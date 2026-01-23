// Ferret runtime: Universal Hashing Implementation
// Content-based hashing and equality for all types

#include "hash.h"
#include "map.h"
#include <stdlib.h>
#include <string.h>
#include <stdio.h>

#define FNV_OFFSET_BASIS 2166136261U
#define FNV_PRIME 16777619U

// FNV-1a hash for combining hashes
static uint32_t fnv1a_hash(const uint8_t* data, size_t len, uint32_t hash) {
    for (size_t i = 0; i < len; i++) {
        hash ^= data[i];
        hash *= FNV_PRIME;
    }
    return hash;
}

// Combine two hashes
static uint32_t hash_combine(uint32_t h1, uint32_t h2) {
    return h1 ^ (h2 + 0x9e3779b9 + (h1 << 6) + (h1 >> 2));
}

// Universal hash function
uint32_t ferret_hash_universal(const void* data, ferret_type_info_t* type_info) {
    if (data == NULL || type_info == NULL) {
        return 0;
    }

    if (ferret_type_kind_is_primitive(type_info->kind)) {
        if (type_info->kind == FERRET_TYPE_STRING) {
            // String is stored as char* pointer
            const char* str = *(const char**)data;
            if (str == NULL) {
                return 0;
            }
            return fnv1a_hash((const uint8_t*)str, strlen(str), FNV_OFFSET_BASIS);
        }
        // Hash primitive types by their bytes
        return fnv1a_hash((const uint8_t*)data, type_info->size, FNV_OFFSET_BASIS);
    }

    switch (type_info->kind) {
        case FERRET_TYPE_POINTER: {
            // Hash pointer by value (identity)
            void* ptr = *(void**)data;
            return fnv1a_hash((const uint8_t*)&ptr, sizeof(void*), FNV_OFFSET_BASIS);
        }

        case FERRET_TYPE_STRUCT: {
            // Hash all fields recursively
            uint32_t hash = FNV_OFFSET_BASIS;
            for (size_t i = 0; i < type_info->struct_info.field_count; i++) {
                ferret_field_info_t* field = &type_info->struct_info.fields[i];
                const uint8_t* field_data = (const uint8_t*)data + field->offset;
                uint32_t field_hash = ferret_hash_universal(field_data, field->type);
                hash = hash_combine(hash, field_hash);
            }
            return hash;
        }
        
        case FERRET_TYPE_ARRAY: {
            // Hash all elements recursively
            uint32_t hash = FNV_OFFSET_BASIS;
            size_t elem_size = type_info->array_info.element_type->size;
            const uint8_t* elem_ptr = (const uint8_t*)data;
            for (size_t i = 0; i < type_info->array_info.length; i++) {
                uint32_t elem_hash = ferret_hash_universal(elem_ptr + i * elem_size, 
                                                           type_info->array_info.element_type);
                hash = hash_combine(hash, elem_hash);
            }
            return hash;
        }
        
        case FERRET_TYPE_SLICE: {
            // Slice is stored as { void* data; size_t len; size_t cap; }
            typedef struct { void* data; size_t len; size_t cap; } slice_t;
            const slice_t* slice = (const slice_t*)data;
            
            // NULL check type info
            if (type_info == NULL) {
                return 0;
            }
            if (type_info->slice_info.element_type == NULL) {
                return 0;
            }
            
            if (slice->data == NULL || slice->len == 0) {
                return 0;
            }
            
            // Hash all elements
            uint32_t hash = FNV_OFFSET_BASIS;
            size_t elem_size = type_info->slice_info.element_type->size;
            const uint8_t* elem_ptr = (const uint8_t*)slice->data;
            for (size_t i = 0; i < slice->len; i++) {
                uint32_t elem_hash = ferret_hash_universal(elem_ptr + i * elem_size,
                                                           type_info->slice_info.element_type);
                hash = hash_combine(hash, elem_hash);
            }
            return hash;
        }
        
        case FERRET_TYPE_MAP: {
            // Map is stored as ferret_map_t* pointer
            // Hash all key-value pairs (order-independent using XOR)
            ferret_map_t* map = *(ferret_map_t**)data;
            if (map == NULL || map->size == 0) {
                return 0;
            }
            
            uint32_t hash = 0; // Use XOR for order independence
            
            // Iterate all buckets
            for (size_t i = 0; i < map->bucket_count; i++) {
                ferret_map_entry_t* entry = map->buckets[i];
                while (entry != NULL) {
                    // Hash key and value
                    uint32_t key_hash = ferret_hash_universal(entry->key, type_info->map_info.key_type);
                    uint32_t val_hash = ferret_hash_universal(entry->value, type_info->map_info.value_type);
                    uint32_t pair_hash = hash_combine(key_hash, val_hash);
                    
                    // XOR for order independence
                    hash ^= pair_hash;
                    
                    entry = entry->next;
                }
            }
            
            return hash;
        }
        
        case FERRET_TYPE_FUNCTION: {
            // Hash function pointer by value (identity)
            void* fn_ptr = *(void**)data;
            return fnv1a_hash((const uint8_t*)&fn_ptr, sizeof(void*), FNV_OFFSET_BASIS);
        }
        
        case FERRET_TYPE_INTERFACE: {
            // Interface layout: { void* data; void* extra }
            // For empty interface, extra holds the dynamic type descriptor.
            // For non-empty interface, extra holds the vtable pointer.
            typedef struct { void* data; void* extra; } interface_t;
            const interface_t* iface = (const interface_t*)data;

            if (type_info->interface_info.method_count == 0) {
                ferret_type_info_t* dyn_type = (ferret_type_info_t*)iface->extra;
                if (dyn_type == NULL) {
                    uint32_t h1 = fnv1a_hash((const uint8_t*)&iface->extra, sizeof(void*), FNV_OFFSET_BASIS);
                    uint32_t h2 = fnv1a_hash((const uint8_t*)&iface->data, sizeof(void*), FNV_OFFSET_BASIS);
                    return hash_combine(h1, h2);
                }

                const void* payload = iface->data;
                if (dyn_type->kind == FERRET_TYPE_POINTER) {
                    payload = &iface->data;
                }

                uint32_t type_hash = fnv1a_hash((const uint8_t*)&dyn_type, sizeof(void*), FNV_OFFSET_BASIS);
                uint32_t value_hash = ferret_hash_universal(payload, dyn_type);
                return hash_combine(type_hash, value_hash);
            }

            // Non-empty interface: identity-based hashing (vtable + data pointers)
            uint32_t h1 = fnv1a_hash((const uint8_t*)&iface->extra, sizeof(void*), FNV_OFFSET_BASIS);
            uint32_t h2 = fnv1a_hash((const uint8_t*)&iface->data, sizeof(void*), FNV_OFFSET_BASIS);
            return hash_combine(h1, h2);
        }
        
        default:
            // Fallback: hash raw bytes
            return fnv1a_hash((const uint8_t*)data, type_info->size, FNV_OFFSET_BASIS);
    }
}

// Universal equality function
bool ferret_equals_universal(const void* data1, const void* data2, ferret_type_info_t* type_info) {
    if (data1 == data2) {
        return true; // Same pointer
    }
    if (data1 == NULL || data2 == NULL || type_info == NULL) {
        return false;
    }
    
    if (ferret_type_kind_is_primitive(type_info->kind)) {
        return ferret_primitive_equals(type_info->kind, data1, data2);
    }

    switch (type_info->kind) {
        case FERRET_TYPE_POINTER: {
            void* ptr1 = *(void**)data1;
            void* ptr2 = *(void**)data2;
            return ptr1 == ptr2;
        }

        case FERRET_TYPE_STRUCT: {
            // Compare all fields recursively
            for (size_t i = 0; i < type_info->struct_info.field_count; i++) {
                ferret_field_info_t* field = &type_info->struct_info.fields[i];
                const uint8_t* field1 = (const uint8_t*)data1 + field->offset;
                const uint8_t* field2 = (const uint8_t*)data2 + field->offset;
                if (!ferret_equals_universal(field1, field2, field->type)) {
                    return false;
                }
            }
            return true;
        }
        
        case FERRET_TYPE_ARRAY: {
            // Compare all elements recursively
            size_t elem_size = type_info->array_info.element_type->size;
            const uint8_t* elem1 = (const uint8_t*)data1;
            const uint8_t* elem2 = (const uint8_t*)data2;
            for (size_t i = 0; i < type_info->array_info.length; i++) {
                if (!ferret_equals_universal(elem1 + i * elem_size, 
                                            elem2 + i * elem_size,
                                            type_info->array_info.element_type)) {
                    return false;
                }
            }
            return true;
        }
        
        case FERRET_TYPE_SLICE: {
            typedef struct { void* data; size_t len; size_t cap; } slice_t;
            const slice_t* slice1 = (const slice_t*)data1;
            const slice_t* slice2 = (const slice_t*)data2;
            
            if (slice1->len != slice2->len) {
                return false;
            }
            if (slice1->data == slice2->data) {
                return true;
            }
            if (slice1->data == NULL || slice2->data == NULL) {
                return false;
            }
            
            // Compare all elements
            size_t elem_size = type_info->slice_info.element_type->size;
            const uint8_t* elem1 = (const uint8_t*)slice1->data;
            const uint8_t* elem2 = (const uint8_t*)slice2->data;
            for (size_t i = 0; i < slice1->len; i++) {
                if (!ferret_equals_universal(elem1 + i * elem_size,
                                            elem2 + i * elem_size,
                                            type_info->slice_info.element_type)) {
                    return false;
                }
            }
            return true;
        }
        
        case FERRET_TYPE_MAP: {
            ferret_map_t* map1 = *(ferret_map_t**)data1;
            ferret_map_t* map2 = *(ferret_map_t**)data2;
            
            if (map1 == map2) return true;
            if (map1 == NULL || map2 == NULL) return false;
            if (map1->size != map2->size) return false;
            
            // Check all entries in map1 exist in map2 with same values
            for (size_t i = 0; i < map1->bucket_count; i++) {
                ferret_map_entry_t* entry = map1->buckets[i];
                while (entry != NULL) {
                    // Look up this key in map2
                    void* value2 = ferret_map_get(map2, entry->key);
                    if (value2 == NULL) {
                        return false; // Key not found in map2
                    }
                    
                    // Compare values
                    if (!ferret_equals_universal(entry->value, value2, type_info->map_info.value_type)) {
                        return false;
                    }
                    
                    entry = entry->next;
                }
            }
            
            return true;
        }
        
        case FERRET_TYPE_FUNCTION: {
            void* fn1 = *(void**)data1;
            void* fn2 = *(void**)data2;
            return fn1 == fn2;
        }
        
        case FERRET_TYPE_INTERFACE: {
            typedef struct { void* data; void* extra; } interface_t;
            const interface_t* iface1 = (const interface_t*)data1;
            const interface_t* iface2 = (const interface_t*)data2;

            if (type_info->interface_info.method_count == 0) {
                ferret_type_info_t* dyn1 = (ferret_type_info_t*)iface1->extra;
                ferret_type_info_t* dyn2 = (ferret_type_info_t*)iface2->extra;
                if (dyn1 != dyn2) {
                    return false;
                }
                if (dyn1 == NULL) {
                    return iface1->data == iface2->data;
                }

                const void* payload1 = iface1->data;
                const void* payload2 = iface2->data;
                if (dyn1->kind == FERRET_TYPE_POINTER) {
                    payload1 = &iface1->data;
                    payload2 = &iface2->data;
                }

                return ferret_equals_universal(payload1, payload2, dyn1);
            }

            return iface1->extra == iface2->extra && iface1->data == iface2->data;
        }
        
        default:
            // Fallback: byte-wise comparison
            return memcmp(data1, data2, type_info->size) == 0;
    }
}

// Create type info for primitives
ferret_type_info_t* ferret_type_info_primitive(ferret_type_kind_t kind, size_t size) {
    ferret_type_info_t* info = (ferret_type_info_t*)malloc(sizeof(ferret_type_info_t));
    if (info == NULL) return NULL;
    
    info->kind = kind;
    if (size == 0 && ferret_type_kind_is_primitive(kind)) {
        info->size = ferret_primitive_size(kind);
    } else {
        info->size = size;
    }
    return info;
}

// Create type info for structs
ferret_type_info_t* ferret_type_info_struct(size_t field_count, ferret_field_info_t* fields, size_t size) {
    ferret_type_info_t* info = (ferret_type_info_t*)malloc(sizeof(ferret_type_info_t));
    if (info == NULL) return NULL;
    
    info->kind = FERRET_TYPE_STRUCT;
    info->size = size;
    info->struct_info.field_count = field_count;
    info->struct_info.fields = fields;
    return info;
}

// Create type info for arrays
ferret_type_info_t* ferret_type_info_array(ferret_type_info_t* element_type, size_t length) {
    ferret_type_info_t* info = (ferret_type_info_t*)malloc(sizeof(ferret_type_info_t));
    if (info == NULL) return NULL;
    
    info->kind = FERRET_TYPE_ARRAY;
    info->size = element_type->size * length;
    info->array_info.length = length;
    info->array_info.element_type = element_type;
    return info;
}

// Create type info for slices
ferret_type_info_t* ferret_type_info_slice(ferret_type_info_t* element_type) {
    ferret_type_info_t* info = (ferret_type_info_t*)malloc(sizeof(ferret_type_info_t));
    if (info == NULL) return NULL;
    
    info->kind = FERRET_TYPE_SLICE;
    info->size = sizeof(void*) + sizeof(size_t) * 2; // { data, len, cap }
    info->slice_info.element_type = element_type;
    return info;
}

// Create type info for maps
ferret_type_info_t* ferret_type_info_map(ferret_type_info_t* key_type, ferret_type_info_t* value_type) {
    ferret_type_info_t* info = (ferret_type_info_t*)malloc(sizeof(ferret_type_info_t));
    if (info == NULL) return NULL;
    
    info->kind = FERRET_TYPE_MAP;
    info->size = sizeof(void*); // Pointer to ferret_map_t
    info->map_info.key_type = key_type;
    info->map_info.value_type = value_type;
    return info;
}

// Create type info for pointers
ferret_type_info_t* ferret_type_info_pointer(ferret_type_info_t* pointee_type) {
    ferret_type_info_t* info = (ferret_type_info_t*)malloc(sizeof(ferret_type_info_t));
    if (info == NULL) return NULL;
    
    info->kind = FERRET_TYPE_POINTER;
    info->size = sizeof(void*);
    info->pointer_info.pointee_type = pointee_type;
    return info;
}

// Free type info (non-recursive, assumes shared type info)
void ferret_type_info_free(ferret_type_info_t* type_info) {
    if (type_info != NULL) {
        free(type_info);
    }
}
