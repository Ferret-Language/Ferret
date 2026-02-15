// Ferret runtime: global builtins implemented in C.

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "../core/array.h"
#include "../core/map.h"
#include "../core/alloc.h"
#include "../core/bigint.h"
#include "../core/result.h"
#include "../core/runtime_naming.h"

// Define the module prefix for this file (implements ferret_libs/global.fer)
#define MODULE_PREFIX ferret_global
#include "../core/optional.h"
#include "../core/union.h"

typedef struct {
    void* data;
    void* type_info;
} ferret_interface_t;

typedef struct {
    float re;
    float im;
} ferret_complex64_t;

typedef struct {
    double re;
    double im;
} ferret_complex_t;

typedef struct {
    ferret_f128 re;
    ferret_f128 im;
} ferret_complex256_t;

typedef struct {
    ferret_f256 re;
    ferret_f256 im;
} ferret_complex512_t;

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

enum {
    FERRET_STREAM_STDIN = 0,
    FERRET_STREAM_STDOUT = 1,
    FERRET_STREAM_STDERR = 2,
};

static FILE* ferret_stream_file(int64_t stream) {
    switch (stream) {
        case FERRET_STREAM_STDIN:
            return stdin;
        case FERRET_STREAM_STDOUT:
            return stdout;
        case FERRET_STREAM_STDERR:
            return stderr;
        default:
            return NULL;
    }
}

void FERRET_FUNC(write)(void* out, int64_t stream, const ferret_array_t* data) {
    const uint8_t tag_offset = 8;
    if (out == NULL) {
        return;
    }
    FILE* fp = ferret_stream_file(stream);
    if (fp == NULL) {
        FERRET_RESULT_ERR(out, tag_offset, "invalid stream");
        return;
    }
    if (data == NULL) {
        FERRET_RESULT_OK(out, tag_offset, int32_t, 0);
        return;
    }
    if (data->elem_size != sizeof(uint8_t)) {
        FERRET_RESULT_ERR(out, tag_offset, "write expects []byte");
        return;
    }
    if (data->length <= 0 || data->data == NULL) {
        FERRET_RESULT_OK(out, tag_offset, int32_t, 0);
        return;
    }

    size_t expected = (size_t)data->length;
    size_t written = fwrite(data->data, 1, expected, fp);
    if (written != expected) {
        FERRET_RESULT_ERR(out, tag_offset, "failed to write all bytes");
        return;
    }
    FERRET_RESULT_OK(out, tag_offset, int32_t, (int32_t)written);
}

void FERRET_FUNC(read)(void* out, int64_t stream, int32_t max_bytes) {
    const uint8_t tag_offset = 8;
    if (out == NULL) {
        return;
    }
    FILE* fp = ferret_stream_file(stream);
    if (fp == NULL) {
        FERRET_RESULT_ERR(out, tag_offset, "invalid stream");
        return;
    }
    if (max_bytes <= 0) {
        FERRET_RESULT_ERR(out, tag_offset, "maxBytes must be > 0");
        return;
    }

    uint8_t* buf = (uint8_t*)ferret_alloc((size_t)max_bytes);
    if (buf == NULL) {
        FERRET_RESULT_ERR(out, tag_offset, "out of memory");
        return;
    }

    size_t n = fread(buf, 1, (size_t)max_bytes, fp);
    if (ferror(fp)) {
        ferret_free(buf);
        FERRET_RESULT_ERR(out, tag_offset, "failed to read bytes");
        return;
    }

    ferret_array_t* arr = NULL;
    if (n > 0) {
        arr = ferret_array_from_data(buf, (int32_t)n, (int32_t)n, sizeof(uint8_t), (ferret_type_info_t*)&ferret_type_byte);
        if (arr == NULL) {
            ferret_free(buf);
            FERRET_RESULT_ERR(out, tag_offset, "out of memory");
            return;
        }
    } else {
        ferret_free(buf);
        arr = ferret_array_new(sizeof(uint8_t), 0, (ferret_type_info_t*)&ferret_type_byte);
        if (arr == NULL) {
            FERRET_RESULT_ERR(out, tag_offset, "out of memory");
            return;
        }
    }

    FERRET_RESULT_OK(out, tag_offset, ferret_array_t*, arr);
}

void FERRET_FUNC(flush)(void* out, int64_t stream) {
    const uint8_t tag_offset = 8;
    if (out == NULL) {
        return;
    }
    FILE* fp = ferret_stream_file(stream);
    if (fp == NULL) {
        FERRET_RESULT_ERR(out, tag_offset, "invalid stream");
        return;
    }
    if (stream == FERRET_STREAM_STDIN) {
        FERRET_RESULT_ERR(out, tag_offset, "cannot flush stdin");
        return;
    }
    if (fflush(fp) != 0) {
        FERRET_RESULT_ERR(out, tag_offset, "flush failed");
        return;
    }
    FERRET_RESULT_OK(out, tag_offset, bool, true);
}

float FERRET_FUNC(real_complex64)(const void* value, uint64_t heap) {
    (void)heap;
    if (value == NULL) {
        return 0.0f;
    }
    return ((const ferret_complex64_t*)value)->re;
}

float FERRET_FUNC(imag_complex64)(const void* value, uint64_t heap) {
    (void)heap;
    if (value == NULL) {
        return 0.0f;
    }
    return ((const ferret_complex64_t*)value)->im;
}

double FERRET_FUNC(real_complex)(const void* value, uint64_t heap) {
    (void)heap;
    if (value == NULL) {
        return 0.0;
    }
    return ((const ferret_complex_t*)value)->re;
}

double FERRET_FUNC(imag_complex)(const void* value, uint64_t heap) {
    (void)heap;
    if (value == NULL) {
        return 0.0;
    }
    return ((const ferret_complex_t*)value)->im;
}

void FERRET_FUNC(real_complex256)(ferret_f128* out, const void* value, uint64_t heap) {
    (void)heap;
    if (out == NULL) {
        return;
    }
    *out = ferret_f128_from_f64(0.0);
    if (value == NULL) {
        return;
    }
    *out = ((const ferret_complex256_t*)value)->re;
}

void FERRET_FUNC(imag_complex256)(ferret_f128* out, const void* value, uint64_t heap) {
    (void)heap;
    if (out == NULL) {
        return;
    }
    *out = ferret_f128_from_f64(0.0);
    if (value == NULL) {
        return;
    }
    *out = ((const ferret_complex256_t*)value)->im;
}

void FERRET_FUNC(real_complex512)(ferret_f256* out, const void* value, uint64_t heap) {
    (void)heap;
    if (out == NULL) {
        return;
    }
    *out = ferret_f256_from_f64(0.0);
    if (value == NULL) {
        return;
    }
    *out = ((const ferret_complex512_t*)value)->re;
}

void FERRET_FUNC(imag_complex512)(ferret_f256* out, const void* value, uint64_t heap) {
    (void)heap;
    if (out == NULL) {
        return;
    }
    *out = ferret_f256_from_f64(0.0);
    if (value == NULL) {
        return;
    }
    *out = ((const ferret_complex512_t*)value)->im;
}
