#include <limits.h>
#include <string.h>
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include "string_runtime.h"
#include "array.h"

typedef struct ferret_string_node {
    char* ptr;
    struct ferret_string_node* next;
} ferret_string_node_t;

static ferret_string_node_t* ferret_string_head = NULL;

static bool ferret_string_tracked(const char* ptr) {
    const ferret_string_node_t* node = ferret_string_head;
    while (node != NULL) {
        if (node->ptr == ptr) {
            return true;
        }
        node = node->next;
    }
    return false;
}

static void ferret_string_track(char* ptr) {
    if (ptr == NULL || ferret_string_tracked(ptr)) {
        return;
    }
    ferret_string_node_t* node = (ferret_string_node_t*)malloc(sizeof(ferret_string_node_t));
    if (node == NULL) {
        return;
    }
    node->ptr = ptr;
    node->next = ferret_string_head;
    ferret_string_head = node;
}

static void ferret_string_update(char* old_ptr, char* new_ptr) {
    if (old_ptr == new_ptr) {
        return;
    }
    ferret_string_node_t* node = ferret_string_head;
    while (node != NULL) {
        if (node->ptr == old_ptr) {
            node->ptr = new_ptr;
            return;
        }
        node = node->next;
    }
    ferret_string_track(new_ptr);
}

int32_t ferret_string_len(const char* str) {
    if (!str) {
        return 0;
    }
    size_t len = strlen(str);
    if (len > INT32_MAX) {
        return INT32_MAX;
    }
    return (int32_t)len;
}

int32_t ferret_strcmp(const char* s1, const char* s2) {
    if (!s1 && !s2) {
        return 0;
    }
    if (!s1) {
        return -1;
    }
    if (!s2) {
        return 1;
    }
    return strcmp(s1, s2);
}

void ferret_string_assign(char** dst, const char* src) {
    if (dst == NULL) {
        return;
    }
    if (src == NULL) {
        if (*dst != NULL && ferret_string_tracked(*dst)) {
            (*dst)[0] = '\0';
        } else {
            *dst = NULL;
        }
        return;
    }

    size_t src_len = strlen(src);
    char* cur = *dst;

    if (cur != NULL && ferret_string_tracked(cur)) {
        size_t cur_len = strlen(cur);
        if (src_len <= cur_len) {
            memcpy(cur, src, src_len);
            cur[src_len] = '\0';
            return;
        }
        char* next = (char*)realloc(cur, src_len + 1);
        if (next == NULL) {
            return;
        }
        memcpy(next, src, src_len);
        next[src_len] = '\0';
        *dst = next;
        ferret_string_update(cur, next);
        return;
    }

    char* next = (char*)malloc(src_len + 1);
    if (next == NULL) {
        return;
    }
    memcpy(next, src, src_len);
    next[src_len] = '\0';
    *dst = next;
    ferret_string_track(next);
}

// String concatenation: str + str
char* ferret_io_ConcatStrings(const char* s1, const char* s2) {
    size_t len1 = s1 ? strlen(s1) : 0;
    size_t len2 = s2 ? strlen(s2) : 0;
    char* result = (char*)malloc(len1 + len2 + 1);
    if (!result) return NULL;
    if (s1) memcpy(result, s1, len1);
    if (s2) memcpy(result + len1, s2, len2);
    result[len1 + len2] = '\0';
    return result;
}

// String + integer concatenation
#define FERRET_STRING_CONCAT_INT(name_suffix, arg_type, fmt, cast_type) \
    char* ferret_string_concat_##name_suffix(const char* s, arg_type n) { \
        char buf[32]; \
        snprintf(buf, sizeof(buf), fmt, (cast_type)n); \
        return ferret_io_ConcatStrings(s, buf); \
    }

FERRET_STRING_CONCAT_INT(i64, int64_t, "%ld", long)
FERRET_STRING_CONCAT_INT(u64, uint64_t, "%lu", unsigned long)

// String + float concatenation
char* ferret_string_concat_f64(const char* s, double n) {
    char buf[64];
    snprintf(buf, sizeof(buf), "%.15g", n);
    
    // Ensure decimal point is present (e.g., "8" becomes "8.0")
    int has_decimal = 0, has_exponent = 0;
    for (char* p = buf; *p; p++) {
        if (*p == '.') has_decimal = 1;
        if (*p == 'e' || *p == 'E') has_exponent = 1;
    }
    if (!has_decimal && !has_exponent) {
        size_t len = strlen(buf);
        buf[len] = '.';
        buf[len + 1] = '0';
        buf[len + 2] = '\0';
    }
    
    return ferret_io_ConcatStrings(s, buf);
}

// String + byte/char concatenation
char* ferret_string_concat_byte(const char* s, uint8_t c) {
    size_t len = s ? strlen(s) : 0;
    char* result = (char*)malloc(len + 2);
    if (!result) return NULL;
    if (s) memcpy(result, s, len);
    result[len] = (char)c;
    result[len + 1] = '\0';
    return result;
}

// String + bool concatenation
char* ferret_string_concat_bool(const char* s, int b) {
    return ferret_io_ConcatStrings(s, b ? "true" : "false");
}

// UTF-8 decoding helper: decode next codepoint from UTF-8 bytes
// Returns the codepoint and advances *ptr to the next character
static uint32_t utf8_decode(const uint8_t** ptr) {
    const uint8_t* p = *ptr;
    uint32_t codepoint;
    
    if ((*p & 0x80) == 0) {
        // 1-byte sequence (0xxxxxxx): ASCII
        codepoint = *p;
        *ptr += 1;
    } else if ((*p & 0xE0) == 0xC0) {
        // 2-byte sequence (110xxxxx 10xxxxxx)
        codepoint = ((*p & 0x1F) << 6) | (*(p+1) & 0x3F);
        *ptr += 2;
    } else if ((*p & 0xF0) == 0xE0) {
        // 3-byte sequence (1110xxxx 10xxxxxx 10xxxxxx)
        codepoint = ((*p & 0x0F) << 12) | ((*(p+1) & 0x3F) << 6) | (*(p+2) & 0x3F);
        *ptr += 3;
    } else if ((*p & 0xF8) == 0xF0) {
        // 4-byte sequence (11110xxx 10xxxxxx 10xxxxxx 10xxxxxx)
        codepoint = ((*p & 0x07) << 18) | ((*(p+1) & 0x3F) << 12) | ((*(p+2) & 0x3F) << 6) | (*(p+3) & 0x3F);
        *ptr += 4;
    } else {
        // Invalid UTF-8, skip byte
        codepoint = 0xFFFD; // Unicode replacement character
        *ptr += 1;
    }
    
    return codepoint;
}

// Convert string to []char (UTF-8 decode to Unicode codepoints)
ferret_array_t* ferret_string_to_char_array(const char* str, const char* elem_type_id) {
    if (str == NULL) {
        return ferret_array_new(4, 0, elem_type_id); // char is 4 bytes
    }
    
    // First pass: count UTF-8 characters
    const uint8_t* p = (const uint8_t*)str;
    int32_t char_count = 0;
    while (*p) {
        if ((*p & 0x80) == 0) {
            p += 1; // 1-byte
        } else if ((*p & 0xE0) == 0xC0) {
            p += 2; // 2-byte
        } else if ((*p & 0xF0) == 0xE0) {
            p += 3; // 3-byte
        } else if ((*p & 0xF8) == 0xF0) {
            p += 4; // 4-byte
        } else {
            p += 1; // Invalid, skip
        }
        char_count++;
    }
    
    // Create array with exact capacity
    ferret_array_t* arr = ferret_array_new(4, char_count, elem_type_id); // char is 4 bytes (uint32_t)
    if (arr == NULL) return NULL;
    
    // Second pass: decode UTF-8 and populate array
    p = (const uint8_t*)str;
    while (*p) {
        uint32_t codepoint = utf8_decode(&p);
        ferret_array_append(arr, &codepoint);
    }
    
    return arr;
}

// Convert string to []byte (raw UTF-8 bytes)
ferret_array_t* ferret_string_to_byte_array(const char* str, const char* elem_type_id) {
    if (str == NULL) {
        return ferret_array_new(1, 0, elem_type_id); // byte is 1 byte
    }
    
    int32_t len = strlen(str);
    ferret_array_t* arr = ferret_array_new(1, len, elem_type_id); // byte is 1 byte (uint8_t)
    if (arr == NULL) return NULL;
    
    // Copy bytes directly
    for (int32_t i = 0; i < len; i++) {
        uint8_t byte = (uint8_t)str[i];
        ferret_array_append(arr, &byte);
    }
    
    return arr;
}

// UTF-8 encoding helper: encode codepoint to UTF-8 bytes
// Returns number of bytes written
static int utf8_encode(uint32_t codepoint, uint8_t* out) {
    if (codepoint <= 0x7F) {
        // 1-byte sequence
        out[0] = (uint8_t)codepoint;
        return 1;
    } else if (codepoint <= 0x7FF) {
        // 2-byte sequence
        out[0] = 0xC0 | (uint8_t)(codepoint >> 6);
        out[1] = 0x80 | (uint8_t)(codepoint & 0x3F);
        return 2;
    } else if (codepoint <= 0xFFFF) {
        // 3-byte sequence
        out[0] = 0xE0 | (uint8_t)(codepoint >> 12);
        out[1] = 0x80 | (uint8_t)((codepoint >> 6) & 0x3F);
        out[2] = 0x80 | (uint8_t)(codepoint & 0x3F);
        return 3;
    } else if (codepoint <= 0x10FFFF) {
        // 4-byte sequence
        out[0] = 0xF0 | (uint8_t)(codepoint >> 18);
        out[1] = 0x80 | (uint8_t)((codepoint >> 12) & 0x3F);
        out[2] = 0x80 | (uint8_t)((codepoint >> 6) & 0x3F);
        out[3] = 0x80 | (uint8_t)(codepoint & 0x3F);
        return 4;
    }
    // Invalid codepoint, use replacement character
    out[0] = 0xEF; out[1] = 0xBF; out[2] = 0xBD; // U+FFFD
    return 3;
}

// Convert []char to string (UTF-8 encode from Unicode codepoints)
char* ferret_char_array_to_string(const ferret_array_t* arr) {
    if (arr == NULL || arr->length == 0) {
        char* empty = (char*)malloc(1);
        if (empty) empty[0] = '\0';
        return empty;
    }
    
    // First pass: calculate total byte length needed
    size_t total_bytes = 0;
    for (int32_t i = 0; i < arr->length; i++) {
        uint32_t* char_ptr = (uint32_t*)ferret_array_get((ferret_array_t*)arr, i);
        if (char_ptr == NULL) continue;
        uint32_t codepoint = *char_ptr;
        
        if (codepoint <= 0x7F) total_bytes += 1;
        else if (codepoint <= 0x7FF) total_bytes += 2;
        else if (codepoint <= 0xFFFF) total_bytes += 3;
        else total_bytes += 4;
    }
    
    // Allocate string buffer
    char* str = (char*)malloc(total_bytes + 1);
    if (str == NULL) return NULL;
    
    // Second pass: encode UTF-8
    size_t pos = 0;
    for (int32_t i = 0; i < arr->length; i++) {
        uint32_t* char_ptr = (uint32_t*)ferret_array_get((ferret_array_t*)arr, i);
        if (char_ptr == NULL) continue;
        uint32_t codepoint = *char_ptr;
        
        pos += utf8_encode(codepoint, (uint8_t*)(str + pos));
    }
    str[pos] = '\0';
    
    return str;
}

// Convert []byte to string (interpret as UTF-8)
char* ferret_byte_array_to_string(const ferret_array_t* arr) {
    if (arr == NULL || arr->length == 0) {
        char* empty = (char*)malloc(1);
        if (empty) empty[0] = '\0';
        return empty;
    }
    
    // Allocate string buffer
    char* str = (char*)malloc(arr->length + 1);
    if (str == NULL) return NULL;
    
    // Copy bytes directly
    for (int32_t i = 0; i < arr->length; i++) {
        uint8_t* byte_ptr = (uint8_t*)ferret_array_get((ferret_array_t*)arr, i);
        if (byte_ptr == NULL) {
            str[i] = 0;
        } else {
            str[i] = (char)(*byte_ptr);
        }
    }
    str[arr->length] = '\0';
    
    return str;
}
