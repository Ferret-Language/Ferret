// Ferret runtime: global builtins implemented in C.

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "../core/array.h"
#include "../core/map.h"
#include "../core/optional.h"
#include "../core/union.h"

int32_t ferret_global_len(const void* seq) {
    if (seq == NULL) {
        return 0;
    }
    int32_t tag = ferret_union_tag(seq);
    ferret_array_t* arr = NULL;
    if (tag == 0) {
        arr = (ferret_array_t*)ferret_union_load_ptr(seq);
    } else if (tag == 1) {
        arr = (ferret_array_t*)ferret_union_load_ptr_deref(seq);
    }
    if (arr == NULL) {
        return 0;
    }
    return ferret_array_len(arr);
}

bool ferret_global_append(ferret_array_t** seq_ref, uint64_t heap) {
    (void)heap;
    return seq_ref != NULL;
}

void* ferret_global_at(const void* seq) {
    (void)seq;
    return ferret_optional_alloc_none(sizeof(void*) * 2, sizeof(void*));
}

int32_t ferret_global_size(const void* map_view) {
    if (map_view == NULL) {
        return 0;
    }
    int32_t tag = ferret_union_tag(map_view);
    ferret_map_t* map = NULL;
    if (tag == 0) {
        map = (ferret_map_t*)ferret_union_load_ptr_deref(map_view);
    } else if (tag == 1) {
        map = (ferret_map_t*)ferret_union_load_ptr(map_view);
    }
    if (map == NULL) {
        return 0;
    }
    return (int32_t)ferret_map_size(map);
}

void* ferret_global_get(const void* map_view) {
    (void)map_view;
    return ferret_optional_alloc_none(sizeof(void*) * 2, sizeof(void*));
}

bool ferret_global_set(ferret_map_t** map_ref, uint64_t heap) {
    (void)heap;
    return map_ref != NULL;
}

uint64_t ferret_global_addr(const void* value, uint64_t heap) {
    (void)heap;
    return (uint64_t)(uintptr_t)value;
}

uint64_t ferret_global_self_addr(const void* value, uint64_t heap) {
    (void)heap;
    return (uint64_t)(uintptr_t)value;
}

uint64_t ferret_global_heap_addr(const void* value, uint64_t heap) {
    (void)value;
    return heap;
}
