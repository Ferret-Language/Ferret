// Ferret runtime: len function implementation
// Public API that calls core functions

#include <stdint.h>

#include "../core/array.h"
#include "../core/string_runtime.h"
#include "../core/map.h"

#define FERRET_LEN_WRAPPER(name, arg_type, expr) \
    int32_t name(arg_type value) { \
        if (value == NULL) { \
            return 0; \
        } \
        return (int32_t)(expr); \
    }

// Get length of array (dynamic arrays)
FERRET_LEN_WRAPPER(ferret_len_array, void*, ferret_array_len((ferret_array_t*)value))
FERRET_LEN_WRAPPER(ferret_len_string, const char*, ferret_string_len(value))
FERRET_LEN_WRAPPER(ferret_len_map, void*, ferret_map_size((ferret_map_t*)value))
