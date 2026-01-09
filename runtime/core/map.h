// Ferret runtime: Map Library
// Hash table implementation for map[K]V types

#ifndef FERRET_MAP_H
#define FERRET_MAP_H

#include <stdint.h>
#include <stdbool.h>
#include <stdlib.h>

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
} ferret_map_t;

// Create a new map
// Returns NULL on allocation failure
ferret_map_t* ferret_map_new(
    size_t key_size,
    size_t value_size,
    uint32_t (*hash_fn)(const void* key, size_t key_size),
    bool (*equals_fn)(const void* key1, const void* key2, size_t key_size)
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
    bool (*equals_fn)(const void* key1, const void* key2, size_t key_size)
);

// Typed map constructors (avoid function pointer arguments in IR)
ferret_map_t* ferret_map_new_i32(size_t key_size, size_t value_size);
ferret_map_t* ferret_map_new_i64(size_t key_size, size_t value_size);
ferret_map_t* ferret_map_new_f32(size_t key_size, size_t value_size);
ferret_map_t* ferret_map_new_f64(size_t key_size, size_t value_size);
ferret_map_t* ferret_map_new_str(size_t key_size, size_t value_size);
ferret_map_t* ferret_map_new_bytes(size_t key_size, size_t value_size);

ferret_map_t* ferret_map_from_pairs_i32(
    size_t key_size,
    size_t value_size,
    const void* keys,
    const void* values,
    size_t count
);
ferret_map_t* ferret_map_from_pairs_i64(
    size_t key_size,
    size_t value_size,
    const void* keys,
    const void* values,
    size_t count
);
ferret_map_t* ferret_map_from_pairs_f32(
    size_t key_size,
    size_t value_size,
    const void* keys,
    const void* values,
    size_t count
);
ferret_map_t* ferret_map_from_pairs_f64(
    size_t key_size,
    size_t value_size,
    const void* keys,
    const void* values,
    size_t count
);
ferret_map_t* ferret_map_from_pairs_str(
    size_t key_size,
    size_t value_size,
    const void* keys,
    const void* values,
    size_t count
);
ferret_map_t* ferret_map_from_pairs_bytes(
    size_t key_size,
    size_t value_size,
    const void* keys,
    const void* values,
    size_t count
);

// Get value for a key (returns pointer to value, or NULL if not found)
// The returned pointer is valid until the map is modified
// DEPRECATED: Use ferret_map_get_optional instead
void* ferret_map_get(const ferret_map_t* map, const void* key);

// Get value for a key as an optional type
// Returns a generic optional structure that should be cast to the appropriate optional type
// The structure has: { void* value_ptr; int is_some; }
// For value type T, cast to ferret_optional_T and access .value (which is T) and .is_some
typedef struct {
    void* value_ptr;  // Pointer to the value (needs to be dereferenced and cast to T)
    int is_some;      // 1 if value exists, 0 if not found
} ferret_map_get_result_t;

ferret_map_get_result_t ferret_map_get_optional(const ferret_map_t* map, const void* key);

// Write optional map get result into a Ferret optional layout (value bytes + 1-byte flag).
// out_optional must point to a buffer of size (value_size + 1 + padding).
void ferret_map_get_optional_out(const ferret_map_t* map, const void* key, void* out_optional);

// Macro to get value from map as optional type
// Usage: FERRET_MAP_GET_OPTIONAL(map, key, optional_type_name, value_type)
// Example: FERRET_MAP_GET_OPTIONAL(capitals, &key, ferret_optional_const_char_ptr, const char*)
// This macro constructs the optional type directly
#define FERRET_MAP_GET_OPTIONAL(map, key, opt_type_name, value_type) \
    ({ \
        ferret_map_get_result_t __result = ferret_map_get_optional((map), (key)); \
        (opt_type_name)({ \
            .value = __result.is_some ? *(value_type*)(__result.value_ptr) : ((value_type)0), \
            .is_some = __result.is_some \
        }); \
    })

// Set value for a key (inserts or updates)
// Returns false on allocation failure
bool ferret_map_set(ferret_map_t* map, const void* key, const void* value);

// Check if key exists in map
bool ferret_map_has(const ferret_map_t* map, const void* key);

// Get map size (number of entries)
size_t ferret_map_size(const ferret_map_t* map);

// Free map memory (doesn't free the map struct itself if stack-allocated)
void ferret_map_free(ferret_map_t* map);

// Free map and the struct itself
void ferret_map_destroy(ferret_map_t* map);

#define FERRET_MAP_HASH_DECL(suffix) uint32_t ferret_map_hash_##suffix(const void* key, size_t key_size);
#define FERRET_MAP_EQUALS_DECL(suffix) bool ferret_map_equals_##suffix(const void* key1, const void* key2, size_t key_size);

FERRET_MAP_HASH_DECL(i32)
FERRET_MAP_HASH_DECL(i64)
FERRET_MAP_HASH_DECL(f32)
FERRET_MAP_HASH_DECL(f64)
FERRET_MAP_HASH_DECL(str)
FERRET_MAP_HASH_DECL(bytes)

FERRET_MAP_EQUALS_DECL(i32)
FERRET_MAP_EQUALS_DECL(i64)
FERRET_MAP_EQUALS_DECL(f32)
FERRET_MAP_EQUALS_DECL(f64)
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

#endif // FERRET_MAP_H
