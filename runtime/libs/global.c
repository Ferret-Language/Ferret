// Ferret runtime: global builtins implemented in C.

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

#include "../core/array.h"
#include "../core/map.h"
#include "../core/alloc.h"
#include "../core/file_handle.h"
#include "../core/runtime_naming.h"

// Define the module prefix for this file (implements ferret_libs/global.fer)
#define MODULE_PREFIX ferret_global
#include "../core/optional.h"
#include "../core/union.h"

typedef struct {
    void* data;
    void* type_info;
} ferret_interface_t;

int32_t FERRET_FUNC(len)(const void* seq) {
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

bool FERRET_FUNC(append)(ferret_array_t** seq_ref, uint64_t heap, const void* value) {
    (void)heap;
    if (seq_ref == NULL || value == NULL) {
        return false;
    }
    ferret_array_t* arr = *seq_ref;
    if (arr == NULL) {
        return false;
    }
    const size_t iface_size = sizeof(ferret_interface_t);
    const ferret_interface_t* iface = (const ferret_interface_t*)value;
    const void* elem_ptr = value;
    if (arr->elem_size != iface_size) {
        if (iface == NULL || iface->data == NULL) {
            return false;
        }
        elem_ptr = iface->data;
    }
    return ferret_array_append(arr, elem_ptr);
}

void FERRET_FUNC(at)(void* out, const void* seq, int32_t index) {
    if (out == NULL) {
        return;
    }
    void** out_ptr = (void**)out;
    *out_ptr = NULL;

    if (seq == NULL) {
        *out_ptr = ferret_optional_alloc_none(sizeof(void*) * 2, sizeof(void*));
        return;
    }

    int32_t tag = ferret_union_tag(seq);
    ferret_array_t* arr = NULL;
    if (tag == 0) {
        arr = (ferret_array_t*)ferret_union_load_ptr(seq);
    } else if (tag == 1) {
        arr = (ferret_array_t*)ferret_union_load_ptr_deref(seq);
    }
    if (arr == NULL) {
        *out_ptr = ferret_optional_alloc_none(sizeof(ferret_interface_t), sizeof(void*));
        return;
    }

    const size_t iface_size = sizeof(ferret_interface_t);

    int32_t idx = index;
    if (idx < 0) {
        idx = arr->length + idx;
    }
    if (idx < 0 || idx >= arr->length) {
        *out_ptr = ferret_optional_alloc_none(iface_size, sizeof(void*));
        return;
    }

    void* elem = ferret_array_get(arr, idx);
    if (elem == NULL) {
        *out_ptr = ferret_optional_alloc_none(iface_size, sizeof(void*));
        return;
    }

    void* opt = ferret_optional_alloc_none(iface_size, sizeof(void*));
    if (opt == NULL) {
        return;
    }
    if (arr->elem_size == iface_size) {
        memcpy(opt, elem, iface_size);
    } else {
        size_t alloc_size = arr->elem_size > 0 ? arr->elem_size : 1;
        void* boxed = ferret_alloc(alloc_size);
        if (boxed == NULL) {
            return;
        }
        if (arr->elem_size > 0) {
            memcpy(boxed, elem, arr->elem_size);
        }
        ferret_interface_t* iface = (ferret_interface_t*)opt;
        iface->data = boxed;
        iface->type_info = (void*)(arr->elem_type_info);
    }
    uint8_t* flag = (uint8_t*)opt + iface_size;
    *flag = 1;
    *out_ptr = opt;
}

int32_t FERRET_FUNC(size)(const void* map_view) {
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

void FERRET_FUNC(get)(void* out, const void* map_view, const void* key) {
    if (out == NULL) {
        return;
    }
    void** out_ptr = (void**)out;
    *out_ptr = NULL;

    if (map_view == NULL || key == NULL) {
        *out_ptr = ferret_optional_alloc_none(sizeof(void*) * 2, sizeof(void*));
        return;
    }

    int32_t tag = ferret_union_tag(map_view);
    ferret_map_t* map = NULL;
    if (tag == 0) {
        map = (ferret_map_t*)ferret_union_load_ptr_deref(map_view);
    } else if (tag == 1) {
        map = (ferret_map_t*)ferret_union_load_ptr(map_view);
    }
    if (map == NULL) {
        *out_ptr = ferret_optional_alloc_none(sizeof(ferret_interface_t), sizeof(void*));
        return;
    }
    const size_t iface_size = sizeof(ferret_interface_t);
    const ferret_interface_t* key_iface = (const ferret_interface_t*)key;
    const void* key_ptr = key;
    if (map->key_size != iface_size) {
        if (key_iface == NULL || key_iface->data == NULL) {
            *out_ptr = ferret_optional_alloc_none(iface_size, sizeof(void*));
            return;
        }
        key_ptr = key_iface->data;
    }

    void* opt = ferret_optional_alloc_none(iface_size, sizeof(void*));
    if (opt == NULL) {
        return;
    }
    if (map->value_size == iface_size) {
        ferret_map_get_optional_out(map, key_ptr, opt);
        *out_ptr = opt;
        return;
    }

    void* value = ferret_map_get(map, key_ptr);
    if (value == NULL) {
        *out_ptr = opt;
        return;
    }

    size_t alloc_size = map->value_size > 0 ? map->value_size : 1;
    void* boxed = ferret_alloc(alloc_size);
    if (boxed == NULL) {
        return;
    }
    if (map->value_size > 0) {
        memcpy(boxed, value, map->value_size);
    }
    ferret_interface_t* iface = (ferret_interface_t*)opt;
    iface->data = boxed;
    iface->type_info = (void*)(map->value_type_id);
    uint8_t* flag = (uint8_t*)opt + iface_size;
    *flag = 1;
    *out_ptr = opt;
}

bool FERRET_FUNC(set)(ferret_map_t** map_ref, uint64_t heap, const void* key, const void* value) {
    (void)heap;
    if (map_ref == NULL || key == NULL || value == NULL) {
        return false;
    }
    ferret_map_t* map = *map_ref;
    if (map == NULL) {
        return false;
    }
    const size_t iface_size = sizeof(ferret_interface_t);
    const ferret_interface_t* key_iface = (const ferret_interface_t*)key;
    const ferret_interface_t* value_iface = (const ferret_interface_t*)value;
    const void* key_ptr = key;
    const void* value_ptr = value;
    if (map->key_size != iface_size) {
        if (key_iface == NULL || key_iface->data == NULL) {
            return false;
        }
        key_ptr = key_iface->data;
    }
    if (map->value_size != iface_size) {
        if (value_iface == NULL || value_iface->data == NULL) {
            return false;
        }
        value_ptr = value_iface->data;
    }
    return ferret_map_set(map, key_ptr, value_ptr);
}

uint64_t FERRET_FUNC(addr)(const void* value, uint64_t heap) {
    (void)heap;
    return (uint64_t)(uintptr_t)value;
}

uint64_t FERRET_FUNC(self_addr)(const void* value, uint64_t heap) {
    (void)heap;
    return (uint64_t)(uintptr_t)value;
}

uint64_t FERRET_FUNC(heap_addr)(const void* value, uint64_t heap) {
    (void)value;
    return heap;
}

int64_t FERRET_FUNC(__file_clone)(int64_t handle) {
    ferret_file_handle_t* h = ferret_file_handle_from_raw(handle);
    if (h == NULL) {
        return 0;
    }
    FILE* src = ferret_file_handle_file(h);
    const char* path = ferret_file_handle_path(h);
    const char* mode = ferret_file_handle_mode(h);
    if (src == NULL || path == NULL || mode == NULL) {
        return 0;
    }

    // Flush writer state before cloning to ensure the reopened stream sees current data.
    fflush(src);

    long pos = ftell(src);
    if (pos < 0) {
        pos = 0;
    }

    // Copy should not truncate existing file contents.
    const char* clone_mode = mode;
    if (strcmp(mode, "w") == 0 || strcmp(mode, "w+") == 0) {
        clone_mode = "r+";
    }

    FILE* fp = fopen(path, clone_mode);
    if (fp == NULL) {
        return 0;
    }
    if (fseek(fp, pos, SEEK_SET) != 0) {
        fclose(fp);
        return 0;
    }

    ferret_file_handle_t* cloned = ferret_file_handle_new_with_meta(fp, path, mode);
    if (cloned == NULL) {
        fclose(fp);
        return 0;
    }
    return ferret_file_handle_to_raw(cloned);
}

void FERRET_FUNC(__file_drop)(int64_t handle) {
    ferret_file_handle_t* h = ferret_file_handle_from_raw(handle);
    ferret_file_handle_release(h);
}
