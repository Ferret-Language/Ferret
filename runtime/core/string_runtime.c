#include <limits.h>
#include <string.h>
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include "string_runtime.h"

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
