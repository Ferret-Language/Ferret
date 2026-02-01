// Ferret runtime: Dynamic Array Library Implementation
// Optimized with exponential growth strategy (like Go slices)

#define _POSIX_C_SOURCE 200809L
// Include system string.h before array.h to avoid conflict with runtime/string.h
#include <string.h>
#include "array.h"

#define FERRET_ARRAY_MIN_CAPACITY 4
#define FERRET_ARRAY_GROWTH_FACTOR 2

ferret_array_t* ferret_array_new(size_t elem_size, int32_t initial_capacity, ferret_type_info_t* elem_type_info) {
    if (initial_capacity < FERRET_ARRAY_MIN_CAPACITY) {
        initial_capacity = FERRET_ARRAY_MIN_CAPACITY;
    }
    
    ferret_array_t* arr = (ferret_array_t*)malloc(sizeof(ferret_array_t));
    if (arr == NULL) {
        return NULL;
    }
    
    arr->data = malloc(elem_size * initial_capacity);
    if (arr->data == NULL) {
        free(arr);
        return NULL;
    }
    
    arr->length = 0;
    arr->capacity = initial_capacity;
    arr->elem_size = elem_size;
    arr->elem_type_info = elem_type_info;
    
    return arr;
}

ferret_array_t* ferret_array_from_data(void* data, int32_t length, int32_t capacity, size_t elem_size, ferret_type_info_t* elem_type_info) {
    ferret_array_t* arr = (ferret_array_t*)malloc(sizeof(ferret_array_t));
    if (arr == NULL) {
        return NULL;
    }
    
    arr->data = data;
    arr->length = length;
    arr->capacity = capacity;
    arr->elem_size = elem_size;
    arr->elem_type_info = elem_type_info;
    
    return arr;
}

ferret_array_t* ferret_array_clone(const ferret_array_t* src) {
    if (src == NULL) {
        return NULL;
    }

    ferret_array_t* arr = (ferret_array_t*)malloc(sizeof(ferret_array_t));
    if (arr == NULL) {
        return NULL;
    }

    size_t bytes = (size_t)src->elem_size * (size_t)src->capacity;
    void* data = NULL;
    if (bytes > 0) {
        data = malloc(bytes);
        if (data == NULL) {
            free(arr);
            return NULL;
        }
        if (src->data != NULL && src->length > 0) {
            memcpy(data, src->data, (size_t)src->elem_size * (size_t)src->length);
        }
    }

    arr->data = data;
    arr->length = src->length;
    arr->capacity = src->capacity;
    arr->elem_size = src->elem_size;
    arr->elem_type_info = src->elem_type_info;

    return arr;
}

void ferret_array_assign(ferret_array_t** dst, const ferret_array_t* src) {
    if (dst == NULL) {
        return;
    }
    if (src == NULL) {
        if (*dst != NULL) {
            (*dst)->length = 0;
        }
        return;
    }
    if (*dst == NULL) {
        *dst = ferret_array_clone(src);
        return;
    }
    if (*dst == src) {
        return;
    }

    ferret_array_t* out = *dst;
    if (out->elem_size != src->elem_size) {
        out->elem_size = src->elem_size;
    }
    out->elem_type_info = src->elem_type_info;

    if (src->length > out->capacity) {
        int32_t new_capacity = src->length;
        void* new_data = realloc(out->data, out->elem_size * new_capacity);
        if (new_data == NULL) {
            return;
        }
        out->data = new_data;
        out->capacity = new_capacity;
    }

    if (src->length > 0 && src->data != NULL) {
        memcpy(out->data, src->data, (size_t)out->elem_size * (size_t)src->length);
    }
    out->length = src->length;
}

bool ferret_array_append(ferret_array_t* arr, const void* elem) {
    if (arr == NULL || elem == NULL) {
        return false;
    }
    
    // Grow if needed (exponential growth like Go slices)
    if (arr->length >= arr->capacity) {
        int32_t new_capacity = arr->capacity * FERRET_ARRAY_GROWTH_FACTOR;
        if (new_capacity < FERRET_ARRAY_MIN_CAPACITY) {
            new_capacity = FERRET_ARRAY_MIN_CAPACITY;
        }
        
        void* new_data = realloc(arr->data, arr->elem_size * new_capacity);
        if (new_data == NULL) {
            return false;
        }
        
        arr->data = new_data;
        arr->capacity = new_capacity;
    }
    
    // Append element
    memcpy((char*)arr->data + (arr->length * arr->elem_size), elem, arr->elem_size);
    arr->length++;
    
    return true;
}

void* ferret_array_get(ferret_array_t* arr, int32_t index) {
    if (arr == NULL || index < 0 || index >= arr->length) {
        return NULL;
    }
    
    return (char*)arr->data + (index * arr->elem_size);
}

bool ferret_array_set(ferret_array_t* arr, int32_t index, const void* elem) {
    if (arr == NULL || elem == NULL || index < 0 || index >= arr->length) {
        return false;
    }
    
    memcpy((char*)arr->data + (index * arr->elem_size), elem, arr->elem_size);
    return true;
}

int32_t ferret_array_len(const ferret_array_t* arr) {
    return arr == NULL ? 0 : arr->length;
}

int32_t ferret_array_cap(const ferret_array_t* arr) {
    return arr == NULL ? 0 : arr->capacity;
}
