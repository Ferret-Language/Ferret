// Ferret runtime: Map Library
// Hash table implementation for map[K]V types

#ifndef FERRET_MAP_H
#define FERRET_MAP_H

#include <stdint.h>
#include <stdbool.h>
#include <stdlib.h>
#include "type_system.h"

// Hash table entry
typedef struct ferret_map_entry {
    void* key;              // Key value
    void* value;            // Value
    struct ferret_map_entry* next; // For chaining collisions
    uint32_t hash;          // Cached hash value
} ferret_map_entry_t;

// Map structure
typedef struct {
    ferret_map_entry_t** buckets; // Array of bucket pointers
    size_t bucket_count;          // Number of buckets
    size_t size;                   // Number of entries
    size_t key_size;               // Size of key type in bytes
    size_t value_size;             // Size of value type in bytes
    uint32_t (*hash_fn)(const void* key, size_t key_size); // Hash function
    bool (*equals_fn)(const void* key1, const void* key2, size_t key_size); // Equality function
    ferret_type_info_t* key_type_info;   // Type descriptor for universal hashing
    ferret_type_info_t* key_type_id;     // Type descriptor for key type
    ferret_type_info_t* value_type_id;   // Type descriptor for value type
} ferret_map_t;

// Create a new map
// Returns NULL on allocation failure
ferret_map_t* ferret_map_new(
    size_t key_size,
    size_t value_size,
    uint32_t (*hash_fn)(const void* key, size_t key_size),
    bool (*equals_fn)(const void* key1, const void* key2, size_t key_size),
    ferret_type_info_t* key_type_id,
    ferret_type_info_t* value_type_id
);

// Create a map from initial key-value pairs
// keys and values are arrays of key_size and value_size elements respectively
// count is the number of pairs
ferret_map_t* ferret_map_from_pairs(
    size_t key_size,
    size_t value_size,
    const void* keys,
    const void* values,
    size_t count,
    uint32_t (*hash_fn)(const void* key, size_t key_size),
    bool (*equals_fn)(const void* key1, const void* key2, size_t key_size),
    ferret_type_info_t* key_type_id,
    ferret_type_info_t* value_type_id
);

// Clone map (deep copy of entries)
ferret_map_t* ferret_map_clone(const ferret_map_t* map);

// Assign map contents into a destination slot, reusing buckets when possible.
void ferret_map_assign(ferret_map_t** dst, const ferret_map_t* src);

// Typed map constructors (avoid function pointer arguments in IR)
#define FERRET_MAP_TYPED_DECL(suffix) \
    ferret_map_t* ferret_map_new_##suffix(size_t key_size, size_t value_size, ferret_type_info_t* key_type_id, ferret_type_info_t* value_type_id); \
    ferret_map_t* ferret_map_from_pairs_##suffix( \
        size_t key_size, \
        size_t value_size, \
        const void* keys, \
        const void* values, \
        size_t count, \
        ferret_type_info_t* key_type_id, \
        ferret_type_info_t* value_type_id \
    );

FERRET_MAP_TYPED_DECL(numeric)  // Generic numeric maps for all integer and float types
FERRET_MAP_TYPED_DECL(str)
FERRET_MAP_TYPED_DECL(bytes)

// Get value for a key (returns pointer to value, or NULL if not found)
// The returned pointer is valid until the map is modified.
void* ferret_map_get(const ferret_map_t* map, const void* key);

// Write optional map get result into a Ferret optional layout (value bytes + 1-byte flag).
// out_optional must point to a buffer of size (value_size + 1 + padding).
void ferret_map_get_optional_out(const ferret_map_t* map, const void* key, void* out_optional);

// Set value for a key (inserts or updates)
// Returns false on allocation failure
bool ferret_map_set(ferret_map_t* map, const void* key, const void* value);

// Get map size (number of entries)
size_t ferret_map_size(const ferret_map_t* map);

// Free map memory (doesn't free the map struct itself if stack-allocated)
void ferret_map_free(ferret_map_t* map);

// Free map and the struct itself
void ferret_map_destroy(ferret_map_t* map);

#define FERRET_MAP_HASH_DECL(suffix) uint32_t ferret_map_hash_##suffix(const void* key, size_t key_size);
#define FERRET_MAP_EQUALS_DECL(suffix) bool ferret_map_equals_##suffix(const void* key1, const void* key2, size_t key_size);

FERRET_MAP_HASH_DECL(numeric)  // Generic numeric hash for all integer and float types
FERRET_MAP_HASH_DECL(str)
FERRET_MAP_HASH_DECL(bytes)

FERRET_MAP_EQUALS_DECL(numeric)  // Generic numeric equality for all integer and float types
FERRET_MAP_EQUALS_DECL(str)
FERRET_MAP_EQUALS_DECL(bytes)

// Iterator for map traversal
typedef struct {
    size_t bucket_index;
    ferret_map_entry_t* entry;
} ferret_map_iter_t;

// Initialize iterator; returns true if map has at least one element
bool ferret_map_iter_begin(const ferret_map_t* map, ferret_map_iter_t* iter);

// Advance iterator; returns true while elements remain and sets key/value pointers
bool ferret_map_iter_next(const ferret_map_t* map, ferret_map_iter_t* iter, void** key_out, void** value_out);

// Universal hash/equals functions for all types
#include "hash.h"

// Set the current thread's key type for universal hashing
void ferret_map_set_universal_key_type(ferret_type_info_t* type_info);

// Universal map constructors that use content-based hashing
ferret_map_t* ferret_map_new_universal(
    size_t key_size,
    size_t value_size,
    ferret_type_info_t* key_type_info,
    ferret_type_info_t* key_type_id,
    ferret_type_info_t* value_type_id
);
ferret_map_t* ferret_map_from_pairs_universal(
    size_t key_size, 
    size_t value_size, 
    const void* keys, 
    const void* values, 
    size_t count,
    ferret_type_info_t* key_type_info,
    ferret_type_info_t* key_type_id,
    ferret_type_info_t* value_type_id
);

#endif // FERRET_MAP_H
