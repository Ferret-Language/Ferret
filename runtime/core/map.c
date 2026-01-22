// Ferret runtime: Map Library Implementation
// Hash table with chaining for collision resolution

#include "map.h"
#include "hash.h"
#include <string.h>

#define FERRET_MAP_INITIAL_BUCKETS 16
#define FERRET_MAP_LOAD_FACTOR 0.75
#define FERRET_MAP_HASH_SEED 0x9747b28cU

static uint32_t rotl32(uint32_t value, int shift) {
    return (value << shift) | (value >> (32 - shift));
}

static uint32_t murmur3_32(const uint8_t* data, size_t len, uint32_t seed) {
    if (data == NULL) {
        return 0;
    }

    const uint32_t c1 = 0xcc9e2d51U;
    const uint32_t c2 = 0x1b873593U;
    uint32_t h1 = seed;

    size_t i = 0;
    size_t nblocks = len / 4;
    for (size_t block = 0; block < nblocks; block++) {
        size_t offset = i + block * 4;
        uint32_t k1 = (uint32_t)data[offset] |
            ((uint32_t)data[offset + 1] << 8) |
            ((uint32_t)data[offset + 2] << 16) |
            ((uint32_t)data[offset + 3] << 24);

        k1 *= c1;
        k1 = rotl32(k1, 15);
        k1 *= c2;

        h1 ^= k1;
        h1 = rotl32(h1, 13);
        h1 = h1 * 5U + 0xe6546b64U;
    }

    const uint8_t* tail = data + nblocks * 4;
    uint32_t k1 = 0;
    switch (len & 3) {
        case 3:
            k1 ^= ((uint32_t)tail[2] << 16);
        case 2:
            k1 ^= ((uint32_t)tail[1] << 8);
        case 1:
            k1 ^= (uint32_t)tail[0];
            k1 *= c1;
            k1 = rotl32(k1, 15);
            k1 *= c2;
            h1 ^= k1;
    }

    h1 ^= (uint32_t)len;
    h1 ^= h1 >> 16;
    h1 *= 0x85ebca6bU;
    h1 ^= h1 >> 13;
    h1 *= 0xc2b2ae35U;
    h1 ^= h1 >> 16;
    return h1;
}

ferret_map_t* ferret_map_new(
    size_t key_size,
    size_t value_size,
    uint32_t (*hash_fn)(const void* key, size_t key_size),
    bool (*equals_fn)(const void* key1, const void* key2, size_t key_size),
    const char* key_type_id,
    const char* value_type_id
) {
    ferret_map_t* map = (ferret_map_t*)malloc(sizeof(ferret_map_t));
    if (map == NULL) {
        return NULL;
    }

    map->bucket_count = FERRET_MAP_INITIAL_BUCKETS;
    map->buckets = (ferret_map_entry_t**)calloc(map->bucket_count, sizeof(ferret_map_entry_t*));
    if (map->buckets == NULL) {
        free(map);
        return NULL;
    }

    map->size = 0;
    map->key_size = key_size;
    map->value_size = value_size;
    map->hash_fn = hash_fn;
    map->equals_fn = equals_fn;
    map->key_type_info = NULL;  // Initialize to NULL, set by universal constructors
    map->key_type_id = key_type_id;
    map->value_type_id = value_type_id;

    return map;
}

// Global variable to hold type info for universal hashing
static __thread ferret_type_info_t* g_universal_key_type = NULL;

// Helper to set type info before universal map operations
static inline void ferret_map_prepare_universal(const ferret_map_t* map) {
    if (map != NULL && map->key_type_info != NULL) {
        g_universal_key_type = (ferret_type_info_t*)map->key_type_info;
    }
}

// Resize map to new bucket count
static bool ferret_map_resize(ferret_map_t* map, size_t new_bucket_count) {
    ferret_map_prepare_universal(map);  // Set type info for universal maps before rehashing
    
    ferret_map_entry_t** new_buckets = (ferret_map_entry_t**)calloc(new_bucket_count, sizeof(ferret_map_entry_t*));
    if (new_buckets == NULL) {
        return false;
    }

    // Rehash all entries
    for (size_t i = 0; i < map->bucket_count; i++) {
        ferret_map_entry_t* entry = map->buckets[i];
        while (entry != NULL) {
            ferret_map_entry_t* next = entry->next;
            
            // Rehash and insert into new bucket
            uint32_t hash = map->hash_fn(entry->key, map->key_size);
            size_t new_bucket = hash % new_bucket_count;
            entry->hash = hash;
            entry->next = new_buckets[new_bucket];
            new_buckets[new_bucket] = entry;
            
            entry = next;
        }
    }

    free(map->buckets);
    map->buckets = new_buckets;
    map->bucket_count = new_bucket_count;

    return true;
}

ferret_map_t* ferret_map_from_pairs(
    size_t key_size,
    size_t value_size,
    const void* keys,
    const void* values,
    size_t count,
    uint32_t (*hash_fn)(const void* key, size_t key_size),
    bool (*equals_fn)(const void* key1, const void* key2, size_t key_size),
    const char* key_type_id,
    const char* value_type_id
) {
    ferret_map_t* map = ferret_map_new(key_size, value_size, hash_fn, equals_fn, key_type_id, value_type_id);
    if (map == NULL) {
        return NULL;
    }

    // Pre-allocate enough buckets if needed
    size_t needed_buckets = (size_t)(count / FERRET_MAP_LOAD_FACTOR) + 1;
    if (needed_buckets > map->bucket_count) {
        // Round up to next power of 2
        size_t new_buckets = FERRET_MAP_INITIAL_BUCKETS;
        while (new_buckets < needed_buckets) {
            new_buckets *= 2;
        }
        if (!ferret_map_resize(map, new_buckets)) {
            ferret_map_destroy(map);
            return NULL;
        }
    }

    // Insert all pairs
    const uint8_t* key_ptr = (const uint8_t*)keys;
    const uint8_t* value_ptr = (const uint8_t*)values;
    for (size_t i = 0; i < count; i++) {
        if (!ferret_map_set(map, key_ptr + i * key_size, value_ptr + i * value_size)) {
            ferret_map_destroy(map);
            return NULL;
        }
    }

    return map;
}

ferret_map_t* ferret_map_clone(const ferret_map_t* src) {
    if (src == NULL) {
        return NULL;
    }

    ferret_map_t* map = ferret_map_new(
        src->key_size,
        src->value_size,
        src->hash_fn,
        src->equals_fn,
        src->key_type_id,
        src->value_type_id
    );
    if (map == NULL) {
        return NULL;
    }
    
    // Copy type info for universal maps
    map->key_type_info = src->key_type_info;

    if (src->bucket_count > map->bucket_count) {
        if (!ferret_map_resize(map, src->bucket_count)) {
            ferret_map_destroy(map);
            return NULL;
        }
    }

    ferret_map_prepare_universal(map);  // Set type info before cloning entries
    for (size_t i = 0; i < src->bucket_count; i++) {
        ferret_map_entry_t* entry = src->buckets[i];
        while (entry != NULL) {
            if (!ferret_map_set(map, entry->key, entry->value)) {
                ferret_map_destroy(map);
                return NULL;
            }
            entry = entry->next;
        }
    }

    return map;
}

static void ferret_map_clear_entries(ferret_map_t* map) {
    if (map == NULL || map->buckets == NULL) {
        return;
    }
    for (size_t i = 0; i < map->bucket_count; i++) {
        ferret_map_entry_t* entry = map->buckets[i];
        while (entry != NULL) {
            ferret_map_entry_t* next = entry->next;
            free(entry->key);
            free(entry->value);
            free(entry);
            entry = next;
        }
        map->buckets[i] = NULL;
    }
    map->size = 0;
}

void ferret_map_assign(ferret_map_t** dst, const ferret_map_t* src) {
    if (dst == NULL) {
        return;
    }
    if (src == NULL) {
        if (*dst != NULL) {
            ferret_map_clear_entries(*dst);
        }
        return;
    }
    if (*dst == NULL) {
        *dst = ferret_map_clone(src);
        return;
    }
    if (*dst == src) {
        return;
    }

    ferret_map_t* out = *dst;
    ferret_map_clear_entries(out);
    out->key_type_info = src->key_type_info;
    out->key_type_id = src->key_type_id;
    out->value_type_id = src->value_type_id;

    size_t needed_buckets = (size_t)(src->size / FERRET_MAP_LOAD_FACTOR) + 1;
    if (needed_buckets < FERRET_MAP_INITIAL_BUCKETS) {
        needed_buckets = FERRET_MAP_INITIAL_BUCKETS;
    }
    if (out->bucket_count < needed_buckets) {
        size_t new_buckets = out->bucket_count > 0 ? out->bucket_count : FERRET_MAP_INITIAL_BUCKETS;
        while (new_buckets < needed_buckets) {
            new_buckets *= 2;
        }
        if (!ferret_map_resize(out, new_buckets)) {
            return;
        }
    }

    for (size_t i = 0; i < src->bucket_count; i++) {
        ferret_map_entry_t* entry = src->buckets[i];
        while (entry != NULL) {
            if (!ferret_map_set(out, entry->key, entry->value)) {
                return;
            }
            entry = entry->next;
        }
    }
}


// Macro to generate typed map constructors and from_pairs functions
#define FERRET_MAP_TYPED_CONSTRUCTOR(suffix, hash_fn, equals_fn) \
    ferret_map_t* ferret_map_new_##suffix(size_t key_size, size_t value_size, const char* key_type_id, const char* value_type_id) { \
        return ferret_map_new(key_size, value_size, hash_fn, equals_fn, key_type_id, value_type_id); \
    } \
    ferret_map_t* ferret_map_from_pairs_##suffix(size_t key_size, size_t value_size, const void* keys, const void* values, size_t count, const char* key_type_id, const char* value_type_id) { \
        return ferret_map_from_pairs(key_size, value_size, keys, values, count, hash_fn, equals_fn, key_type_id, value_type_id); \
    }

FERRET_MAP_TYPED_CONSTRUCTOR(numeric, ferret_map_hash_numeric, ferret_map_equals_numeric)
FERRET_MAP_TYPED_CONSTRUCTOR(str, ferret_map_hash_str, ferret_map_equals_str)
FERRET_MAP_TYPED_CONSTRUCTOR(bytes, ferret_map_hash_bytes, ferret_map_equals_bytes)

void* ferret_map_get(const ferret_map_t* map, const void* key) {
    if (map == NULL || key == NULL) {
        return NULL;
    }

    ferret_map_prepare_universal(map);  // Set type info for universal maps
    uint32_t hash = map->hash_fn(key, map->key_size);
    size_t bucket = hash % map->bucket_count;

    ferret_map_entry_t* entry = map->buckets[bucket];
    while (entry != NULL) {
        if (entry->hash == hash && map->equals_fn(entry->key, key, map->key_size)) {
            return entry->value;
        }
        entry = entry->next;
    }

    return NULL;
}

ferret_map_get_result_t ferret_map_get_optional(const ferret_map_t* map, const void* key) {
    ferret_map_get_result_t result = {.value_ptr = NULL, .is_some = 0};
    
    if (map == NULL || key == NULL) {
        return result;
    }

    void* value_ptr = ferret_map_get(map, key);
    if (value_ptr != NULL) {
        result.value_ptr = value_ptr;
        result.is_some = 1;
    }
    
    return result;
}

void ferret_map_get_optional_out(const ferret_map_t* map, const void* key, void* out_optional) {
    if (out_optional == NULL) {
        return;
    }
    uint8_t* out_bytes = (uint8_t*)out_optional;
    size_t value_size = 0;
    if (map != NULL) {
        value_size = map->value_size;
    }
    uint8_t* flag_ptr = out_bytes + value_size;

    ferret_map_get_result_t result = ferret_map_get_optional(map, key);
    if (result.is_some && result.value_ptr != NULL) {
        if (value_size > 0) {
            memcpy(out_bytes, result.value_ptr, value_size);
        }
        *flag_ptr = 1;
    } else {
        *flag_ptr = 0;
    }
}

bool ferret_map_set(ferret_map_t* map, const void* key, const void* value) {
    if (map == NULL || key == NULL) {
        return false;
    }


    ferret_map_prepare_universal(map);  // Set type info for universal maps

    // Resize if needed
    if (map->size >= (size_t)(map->bucket_count * FERRET_MAP_LOAD_FACTOR)) {
        if (!ferret_map_resize(map, map->bucket_count * 2)) {
            return false;
        }
    }

    uint32_t hash = map->hash_fn(key, map->key_size);
    size_t bucket = hash % map->bucket_count;

    // Check if key already exists
    ferret_map_entry_t* entry = map->buckets[bucket];
    while (entry != NULL) {
        if (entry->hash == hash && map->equals_fn(entry->key, key, map->key_size)) {
            // Update existing entry
            memcpy(entry->value, value, map->value_size);
            return true;
        }
        entry = entry->next;
    }

    // Create new entry
    entry = (ferret_map_entry_t*)malloc(sizeof(ferret_map_entry_t));
    if (entry == NULL) {
        return false;
    }

    entry->key = malloc(map->key_size);
    entry->value = malloc(map->value_size);
    if (entry->key == NULL || entry->value == NULL) {
        free(entry->key);
        free(entry->value);
        free(entry);
        return false;
    }

    memcpy(entry->key, key, map->key_size);
    memcpy(entry->value, value, map->value_size);
    entry->hash = hash;
    entry->next = map->buckets[bucket];
    map->buckets[bucket] = entry;
    map->size++;

    return true;
}

bool ferret_map_has(const ferret_map_t* map, const void* key) {
    return ferret_map_get(map, key) != NULL;
}

size_t ferret_map_size(const ferret_map_t* map) {
    return map == NULL ? 0 : map->size;
}

void ferret_map_free(ferret_map_t* map) {
    if (map == NULL) {
        return;
    }

    for (size_t i = 0; i < map->bucket_count; i++) {
        ferret_map_entry_t* entry = map->buckets[i];
        while (entry != NULL) {
            ferret_map_entry_t* next = entry->next;
            free(entry->key);
            free(entry->value);
            free(entry);
            entry = next;
        }
    }

    free(map->buckets);
    map->buckets = NULL;
    map->size = 0;
    map->bucket_count = 0;
}

void ferret_map_destroy(ferret_map_t* map) {
    if (map != NULL) {
        ferret_map_free(map);
        free(map);
    }
}

// Hash functions
#define FERRET_MAP_HASH_INT(suffix, type) \
    uint32_t ferret_map_hash_##suffix(const void* key, size_t key_size) { \
        (void)key_size; \
        return murmur3_32((const uint8_t*)key, sizeof(type), FERRET_MAP_HASH_SEED); \
    }

#define FERRET_MAP_HASH_FLOAT(suffix, type) \
    uint32_t ferret_map_hash_##suffix(const void* key, size_t key_size) { \
        (void)key_size; \
        if (key == NULL) { \
            return 0; \
        } \
        type value = (type)0; \
        memcpy(&value, key, sizeof(type)); \
        if (value == (type)0) { \
            value = (type)0; \
        } \
        return murmur3_32((const uint8_t*)&value, sizeof(type), FERRET_MAP_HASH_SEED); \
    }

FERRET_MAP_HASH_INT(i32, int32_t)
FERRET_MAP_HASH_INT(i64, int64_t)
FERRET_MAP_HASH_FLOAT(f32, float)
FERRET_MAP_HASH_FLOAT(f64, double)

// Generic numeric hash function that works for all integer and float types
uint32_t ferret_map_hash_numeric(const void* key, size_t key_size) {
    if (key == NULL) {
        return 0;
    }
    // Use murmur3 hash on the raw bytes of any numeric type
    return murmur3_32((const uint8_t*)key, key_size, FERRET_MAP_HASH_SEED);
}

uint32_t ferret_map_hash_str(const void* key, size_t key_size) {
    (void)key_size; // Unused
    // key is a pointer to const char*, so we need to dereference it
    const char** str_ptr = (const char**)key;
    if (str_ptr == NULL || *str_ptr == NULL) {
        return 0;
    }
    size_t len = strlen(*str_ptr);
    return murmur3_32((const uint8_t*)(*str_ptr), len, FERRET_MAP_HASH_SEED);
}

uint32_t ferret_map_hash_bytes(const void* key, size_t key_size) {
    if (key == NULL) {
        return 0;
    }
    return murmur3_32((const uint8_t*)key, key_size, FERRET_MAP_HASH_SEED);
}

// Generic numeric equality function that works for all integer and float types
bool ferret_map_equals_numeric(const void* key1, const void* key2, size_t key_size) {
    if (key1 == NULL || key2 == NULL) {
        return key1 == key2;
    }
    // Byte-wise comparison works for all numeric types
    return memcmp(key1, key2, key_size) == 0;
}

bool ferret_map_equals_str(const void* key1, const void* key2, size_t key_size) {
    (void)key_size; // Unused
    // keys are pointers to const char*, so we need to dereference them
    const char** str1_ptr = (const char**)key1;
    const char** str2_ptr = (const char**)key2;
    if (str1_ptr == NULL || str2_ptr == NULL) {
        return str1_ptr == str2_ptr;
    }
    if (*str1_ptr == NULL || *str2_ptr == NULL) {
        return *str1_ptr == *str2_ptr;
    }
    return strcmp(*str1_ptr, *str2_ptr) == 0;
}

bool ferret_map_equals_bytes(const void* key1, const void* key2, size_t key_size) {
    if (key1 == NULL || key2 == NULL) {
        return key1 == key2;
    }
    return memcmp(key1, key2, key_size) == 0;
}

bool ferret_map_iter_begin(const ferret_map_t* map, ferret_map_iter_t* iter) {
    if (map == NULL || iter == NULL || map->size == 0) {
        return false;
    }

    iter->bucket_index = 0;
    iter->entry = NULL;

    // Find first non-empty bucket
    while (iter->bucket_index < map->bucket_count) {
        if (map->buckets[iter->bucket_index] != NULL) {
            iter->entry = map->buckets[iter->bucket_index];
            return true;
        }
        iter->bucket_index++;
    }

    return false;
}

bool ferret_map_iter_next(const ferret_map_t* map, ferret_map_iter_t* iter, void** key_out, void** value_out) {
    if (map == NULL || iter == NULL || key_out == NULL || value_out == NULL) {
        return false;
    }

    if (iter->entry == NULL) {
        return false;
    }

    // Current entry
    *key_out = iter->entry->key;
    *value_out = iter->entry->value;

    // Move to next entry
    if (iter->entry->next != NULL) {
        iter->entry = iter->entry->next;
        return true;
    }

    // Move to next bucket
    iter->bucket_index++;
    while (iter->bucket_index < map->bucket_count) {
        if (map->buckets[iter->bucket_index] != NULL) {
            iter->entry = map->buckets[iter->bucket_index];
            return true;
        }
        iter->bucket_index++;
    }

    // End of map
    iter->entry = NULL;
    return true;
}

// Universal map support
void ferret_map_set_universal_key_type(ferret_type_info_t* type_info) {
    g_universal_key_type = type_info;
}

// Universal hash wrapper
static uint32_t ferret_map_hash_universal_wrapper(const void* key, size_t key_size) {
    (void)key_size;
    if (g_universal_key_type == NULL) {
        // Fallback to byte hashing if no type info
        return ferret_map_hash_bytes(key, key_size);
    }
    return ferret_hash_universal(key, g_universal_key_type);
}

// Universal equals wrapper
static bool ferret_map_equals_universal_wrapper(const void* key1, const void* key2, size_t key_size) {
    (void)key_size;
    if (g_universal_key_type == NULL) {
        // Fallback to byte comparison if no type info
        return ferret_map_equals_bytes(key1, key2, key_size);
    }
    return ferret_equals_universal(key1, key2, g_universal_key_type);
}

// Universal map constructor - type_info can be NULL for fallback to byte comparison
ferret_map_t* ferret_map_new_universal(
    size_t key_size,
    size_t value_size,
    ferret_type_info_t* key_type_info,
    const char* key_type_id,
    const char* value_type_id
) {
    ferret_map_set_universal_key_type(key_type_info);  // Sets thread-local for initial creation
    ferret_map_t* map = ferret_map_new(
        key_size,
        value_size,
        ferret_map_hash_universal_wrapper,
        ferret_map_equals_universal_wrapper,
        key_type_id,
        value_type_id
    );
    if (map != NULL) {
        map->key_type_info = key_type_info;  // Store in map for future operations
    }
    return map;
}

ferret_map_t* ferret_map_from_pairs_universal(
    size_t key_size, 
    size_t value_size, 
    const void* keys, 
    const void* values, 
    size_t count,
    ferret_type_info_t* key_type_info,
    const char* key_type_id,
    const char* value_type_id
) {
    ferret_map_set_universal_key_type(key_type_info);  // Sets thread-local for initial creation
    
    // Create map with universal hash/equals and store type info
    ferret_map_t* map = ferret_map_new_universal(key_size, value_size, key_type_info, key_type_id, value_type_id);
    if (map == NULL) {
        return NULL;
    }

    // Pre-allocate enough buckets if needed
    size_t needed_buckets = (size_t)(count / FERRET_MAP_LOAD_FACTOR) + 1;
    if (needed_buckets > map->bucket_count) {
        // Round up to next power of 2
        size_t new_buckets = FERRET_MAP_INITIAL_BUCKETS;
        while (new_buckets < needed_buckets) {
            new_buckets *= 2;
        }
        if (!ferret_map_resize(map, new_buckets)) {
            ferret_map_destroy(map);
            return NULL;
        }
    }

    // Insert all pairs
    const uint8_t* key_ptr = (const uint8_t*)keys;
    const uint8_t* value_ptr = (const uint8_t*)values;
    for (size_t i = 0; i < count; i++) {
        if (!ferret_map_set(map, key_ptr + i * key_size, value_ptr + i * value_size)) {
            ferret_map_destroy(map);
            return NULL;
        }
    }

    return map;
}
