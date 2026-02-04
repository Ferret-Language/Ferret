// Ferret runtime: String Builder Library Implementation
// Efficient string concatenation and manipulation (avoids repeated malloc)

#define _POSIX_C_SOURCE 200809L
#include <string.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include <stdarg.h>
#include "alloc.h"

// String builder structure (similar to Go's strings.Builder)
typedef struct {
    char* data;        // String data buffer
    int32_t length;    // Current string length
    int32_t capacity;  // Buffer capacity
} ferret_string_builder_t;

#define FERRET_STRING_MIN_CAPACITY 16
#define FERRET_STRING_GROWTH_FACTOR 2

// Create a new string builder with initial capacity
ferret_string_builder_t* ferret_string_builder_new(int32_t initial_capacity) {
    if (initial_capacity < FERRET_STRING_MIN_CAPACITY) {
        initial_capacity = FERRET_STRING_MIN_CAPACITY;
    }
    
    ferret_string_builder_t* sb = (ferret_string_builder_t*)ferret_alloc(sizeof(ferret_string_builder_t));
    if (sb == NULL) {
        return NULL;
    }
    
    sb->data = (char*)ferret_alloc(initial_capacity);
    if (sb->data == NULL) {
        ferret_free(sb);
        return NULL;
    }
    
    sb->data[0] = '\0';
    sb->length = 0;
    sb->capacity = initial_capacity;
    
    return sb;
}

bool ferret_string_builder_append(ferret_string_builder_t* sb, const char* str) {
    if (sb == NULL || str == NULL) {
        return false;
    }
    
    size_t str_len = strlen(str);
    int32_t new_length = sb->length + (int32_t)str_len;
    
    // Grow if needed
    if (new_length + 1 > sb->capacity) {
        int32_t new_capacity = sb->capacity;
        while (new_length + 1 > new_capacity) {
            new_capacity *= FERRET_STRING_GROWTH_FACTOR;
        }
        
        char* new_data = (char*)ferret_realloc(sb->data, new_capacity);
        if (new_data == NULL) {
            return false;
        }
        
        sb->data = new_data;
        sb->capacity = new_capacity;
    }
    
    // Append string
    memcpy(sb->data + sb->length, str, str_len);
    sb->length = new_length;
    sb->data[sb->length] = '\0';
    
    return true;
}

bool ferret_string_builder_append_char(ferret_string_builder_t* sb, char c) {
    if (sb == NULL) {
        return false;
    }
    
    // Grow if needed
    if (sb->length + 1 >= sb->capacity) {
        int32_t new_capacity = sb->capacity * FERRET_STRING_GROWTH_FACTOR;
        char* new_data = (char*)ferret_realloc(sb->data, new_capacity);
        if (new_data == NULL) {
            return false;
        }
        sb->data = new_data;
        sb->capacity = new_capacity;
    }
    
    sb->data[sb->length] = c;
    sb->length++;
    sb->data[sb->length] = '\0';
    
    return true;
}

char* ferret_string_builder_string(ferret_string_builder_t* sb) {
    if (sb == NULL || sb->length == 0) {
        char* empty = (char*)ferret_alloc(1);
        if (empty != NULL) {
            empty[0] = '\0';
        }
        return empty;
    }
    
    char* result = (char*)ferret_alloc(sb->length + 1);
    if (result == NULL) {
        return NULL;
    }
    
    memcpy(result, sb->data, sb->length);
    result[sb->length] = '\0';
    
    return result;
}

const char* ferret_string_builder_cstring(const ferret_string_builder_t* sb) {
    if (sb == NULL || sb->length == 0) {
        return "";
    }
    return sb->data;
}

int32_t ferret_string_builder_len(const ferret_string_builder_t* sb) {
    return sb == NULL ? 0 : sb->length;
}

void ferret_string_builder_reset(ferret_string_builder_t* sb) {
    if (sb != NULL) {
        sb->length = 0;
        if (sb->data != NULL) {
            sb->data[0] = '\0';
        }
    }
}

void ferret_string_builder_free(ferret_string_builder_t* sb) {
    if (sb != NULL && sb->data != NULL) {
        ferret_free(sb->data);
        sb->data = NULL;
        sb->length = 0;
        sb->capacity = 0;
    }
}

void ferret_string_builder_destroy(ferret_string_builder_t* sb) {
    if (sb != NULL) {
        ferret_string_builder_free(sb);
        ferret_free(sb);
    }
}

char* ferret_string_concat_many(const char* str1, ...) {
    if (str1 == NULL) {
        return NULL;
    }
    
    va_list args;
    va_start(args, str1);
    
    // First pass: calculate total length
    size_t total_len = strlen(str1);
    const char* str = va_arg(args, const char*);
    while (str != NULL) {
        total_len += strlen(str);
        str = va_arg(args, const char*);
    }
    va_end(args);
    
    // Allocate result
    char* result = (char*)ferret_alloc(total_len + 1);
    if (result == NULL) {
        return NULL;
    }
    
    // Second pass: copy strings
    strcpy(result, str1);
    va_start(args, str1);
    str = va_arg(args, const char*);
    while (str != NULL) {
        strcat(result, str);
        str = va_arg(args, const char*);
    }
    va_end(args);
    
    return result;
}
