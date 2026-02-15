// Ferret runtime: IO functions
// Native implementations for std/io module

#define _GNU_SOURCE  // For getline on POSIX systems
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include <limits.h>
#include <inttypes.h>
#include <string.h>
#include <ctype.h>
#include <errno.h>
#include <math.h>
#include "../core/alloc.h"
#include "../core/array.h"
#include "../core/type_system.h"
#include "../core/runtime_naming.h"

// Define the module prefix for this file (implements ferret_libs/std/io.fer)
#define MODULE_PREFIX ferret_std_io

// For ssize_t on non-POSIX systems
#ifdef _WIN32
typedef long long ssize_t;

// Windows doesn't have getline, implement using standard C functions
static ssize_t getline(char** lineptr, size_t* n, FILE* stream) {
    if (lineptr == NULL || n == NULL || stream == NULL) {
        return -1;
    }

    if (*lineptr == NULL || *n == 0) {
        *n = 128;
        *lineptr = (char*)ferret_alloc(*n);
        if (*lineptr == NULL) {
            return -1;
        }
    }

    size_t pos = 0;
    int c;

    while ((c = getc(stream)) != EOF) {
        // Need space for character + null terminator
        if (pos + 2 > *n) {
            size_t new_size = *n * 2;
            char* new_ptr = (char*)ferret_realloc(*lineptr, new_size);
            if (new_ptr == NULL) {
                return -1;
            }
            *lineptr = new_ptr;
            *n = new_size;
        }

        (*lineptr)[pos++] = (char)c;
        if (c == '\n') {
            break;
        }
    }

    if (pos == 0 && c == EOF) {
        return -1;
    }

    (*lineptr)[pos] = '\0';
    return (ssize_t)pos;
}
#endif
// Printable union layout: [4-byte tag][64 bytes data] = 68 bytes total (to accommodate complex512).
// Tags follow ferret_type_kind_t for primitives (Printable union order).
// Printable adds complex-family types after primitives in this order:
// complex, complex64, complex256, complex512.
#define FERRET_PRINTABLE_TAG_COMPLEX FERRET_TYPE__PRIMITIVE_END
#define FERRET_PRINTABLE_TAG_COMPLEX64 (FERRET_TYPE__PRIMITIVE_END + 1)
#define FERRET_PRINTABLE_TAG_COMPLEX256 (FERRET_TYPE__PRIMITIVE_END + 2)
#define FERRET_PRINTABLE_TAG_COMPLEX512 (FERRET_TYPE__PRIMITIVE_END + 3)

typedef struct {
    double re;
    double im;
} ferret_complex_t;

typedef struct {
    float re;
    float im;
} ferret_complex64_t;

typedef struct {
    ferret_f128 re;
    ferret_f128 im;
} ferret_complex256_t;

typedef struct {
    ferret_f256 re;
    ferret_f256 im;
} ferret_complex512_t;

typedef struct {
    int64_t stream;
} ferret_stream_writer_t;

char* ferret_f128_to_string_ptr(const ferret_f128* val);
char* ferret_f256_to_string_ptr(const ferret_f256* val);
void* ferret_string_builder_new(int32_t initial_capacity);
bool ferret_string_builder_append(void* sb, const char* str);
char* ferret_string_builder_string(void* sb);
void ferret_string_builder_destroy(void* sb);
void ferret_global_write(void* out, int64_t stream, const ferret_array_t* data);

// Print a float/double with at least one decimal place (e.g., 8.0 not 8)
static void print_float(double val, int precision) {
    // Use %g to get compact representation, but ensure decimal point
    char buf[64];
    snprintf(buf, sizeof(buf), "%.*g", precision, val);
    
    // Check if there's a decimal point or exponent
    bool has_decimal = false;
    bool has_exponent = false;
    for (char* p = buf; *p; p++) {
        if (*p == '.') has_decimal = true;
        if (*p == 'e' || *p == 'E') has_exponent = true;
    }
    
    // If no decimal point and no exponent, append .0
    if (!has_decimal && !has_exponent) {
        printf("%s.0", buf);
    } else {
        printf("%s", buf);
    }
}

static void print_char_codepoint(uint32_t codepoint) {
    if (codepoint <= 0x7F) {
        // ASCII
        printf("%c", (char)codepoint);
    } else if (codepoint <= 0x7FF) {
        // 2-byte UTF-8
        printf("%c%c",
            (char)(0xC0 | (codepoint >> 6)),
            (char)(0x80 | (codepoint & 0x3F)));
    } else if (codepoint <= 0xFFFF) {
        // 3-byte UTF-8
        printf("%c%c%c",
            (char)(0xE0 | (codepoint >> 12)),
            (char)(0x80 | ((codepoint >> 6) & 0x3F)),
            (char)(0x80 | (codepoint & 0x3F)));
    } else if (codepoint <= 0x10FFFF) {
        // 4-byte UTF-8
        printf("%c%c%c%c",
            (char)(0xF0 | (codepoint >> 18)),
            (char)(0x80 | ((codepoint >> 12) & 0x3F)),
            (char)(0x80 | ((codepoint >> 6) & 0x3F)),
            (char)(0x80 | (codepoint & 0x3F)));
    } else {
        printf("\\u{%X}", codepoint);
    }
}

static void print_complex(const void* data) {
    const ferret_complex_t* value = (const ferret_complex_t*)data;
    if (value == NULL) {
        printf("(nil)");
        return;
    }
    print_float(value->re, 15);
    if (value->im < 0) {
        printf("-");
        print_float(-value->im, 15);
    } else {
        printf("+");
        print_float(value->im, 15);
    }
    printf("i");
}

static void print_complex64(const void* data) {
    const ferret_complex64_t* value = (const ferret_complex64_t*)data;
    if (value == NULL) {
        printf("(nil)");
        return;
    }
    print_float(value->re, 6);
    if (value->im < 0) {
        printf("-");
        print_float(-value->im, 6);
    } else {
        printf("+");
        print_float(value->im, 6);
    }
    printf("i");
}

static void print_complex256(const void* data) {
    const ferret_complex256_t* value = (const ferret_complex256_t*)data;
    if (value == NULL) {
        printf("(nil)");
        return;
    }

    char* re = ferret_f128_to_string_ptr(&value->re);
    char* im = ferret_f128_to_string_ptr(&value->im);
    if (re == NULL || im == NULL) {
        if (re != NULL) ferret_free(re);
        if (im != NULL) ferret_free(im);
        printf("(nil)");
        return;
    }

    printf("%s", re);
    if (im[0] == '-') {
        printf("%si", im);
    } else {
        printf("+%si", im);
    }

    ferret_free(re);
    ferret_free(im);
}

static void print_complex512(const void* data) {
    const ferret_complex512_t* value = (const ferret_complex512_t*)data;
    if (value == NULL) {
        printf("(nil)");
        return;
    }

    char* re = ferret_f256_to_string_ptr(&value->re);
    char* im = ferret_f256_to_string_ptr(&value->im);
    if (re == NULL || im == NULL) {
        if (re != NULL) ferret_free(re);
        if (im != NULL) ferret_free(im);
        printf("(nil)");
        return;
    }

    printf("%s", re);
    if (im[0] == '-') {
        printf("%si", im);
    } else {
        printf("+%si", im);
    }

    ferret_free(re);
    ferret_free(im);
}

#define FERRET_DECLARE_TO_STRING_PTR_INT(name, lower, ctype, bits) \
    char* ferret_##lower##_to_string_ptr(const ctype* val);

#define FERRET_DECLARE_TO_STRING_PTR_UINT(name, lower, ctype, bits) \
    char* ferret_##lower##_to_string_ptr(const ctype* val);

#define FERRET_DECLARE_TO_STRING_PTR_FLOAT(name, lower, ctype, bits) \
    char* ferret_##lower##_to_string_ptr(const ctype* val);

#define FERRET_DECLARE_TO_STRING_PTR_STRING(name, lower, ctype, bits)
#define FERRET_DECLARE_TO_STRING_PTR_BYTE(name, lower, ctype, bits)
#define FERRET_DECLARE_TO_STRING_PTR_CHAR(name, lower, ctype, bits)
#define FERRET_DECLARE_TO_STRING_PTR_BOOL(name, lower, ctype, bits)

#define FERRET_DECLARE_TO_STRING_PTR(name, lower, ctype, category, bits) \
    FERRET_DECLARE_TO_STRING_PTR_##category(name, lower, ctype, bits)

FERRET_PRIMITIVE_TYPES(FERRET_DECLARE_TO_STRING_PTR)
#undef FERRET_DECLARE_TO_STRING_PTR
#undef FERRET_DECLARE_TO_STRING_PTR_INT
#undef FERRET_DECLARE_TO_STRING_PTR_UINT
#undef FERRET_DECLARE_TO_STRING_PTR_FLOAT
#undef FERRET_DECLARE_TO_STRING_PTR_STRING
#undef FERRET_DECLARE_TO_STRING_PTR_BYTE
#undef FERRET_DECLARE_TO_STRING_PTR_CHAR
#undef FERRET_DECLARE_TO_STRING_PTR_BOOL

#define FERRET_PRINT_BODY_INT(data, ctype, bits, lower) \
    do { \
        if ((bits) <= 64) { \
            uint64_t raw = 0; \
            int64_t value = 0; \
            size_t bytes = (bits) / 8; \
            memcpy(&raw, (data), bytes); \
            if ((bits) < 64) { \
                int shift = 64 - (bits); \
                value = (int64_t)((raw << shift)) >> shift; \
            } else { \
                value = (int64_t)raw; \
            } \
            printf("%" PRId64, value); \
            break; \
        } \
        char* s = ferret_##lower##_to_string_ptr((const ctype*)(data)); \
        if (s != NULL) { \
            printf("%s", s); \
            ferret_free(s); \
        } \
    } while (0)

#define FERRET_PRINT_BODY_UINT(data, ctype, bits, lower) \
    do { \
        if ((bits) <= 64) { \
            uint64_t value = 0; \
            size_t bytes = (bits) / 8; \
            memcpy(&value, (data), bytes); \
            printf("%" PRIu64, value); \
            break; \
        } \
        char* s = ferret_##lower##_to_string_ptr((const ctype*)(data)); \
        if (s != NULL) { \
            printf("%s", s); \
            ferret_free(s); \
        } \
    } while (0)

#define FERRET_PRINT_BODY_FLOAT(data, ctype, bits, lower) \
    do { \
        if ((bits) <= 32) { \
            print_float(*(const float*)(data), 6); \
            break; \
        } \
        if ((bits) <= 64) { \
            print_float(*(const double*)(data), 15); \
            break; \
        } \
        char* s = ferret_##lower##_to_string_ptr((const ctype*)(data)); \
        if (s != NULL) { \
            printf("%s", s); \
            ferret_free(s); \
        } \
    } while (0)

#define FERRET_PRINT_BODY_STRING(data, ctype, bits, lower) \
    do { \
        const char* str = *(const char**)(data); \
        printf("%s", str ? str : "(null)"); \
    } while (0)

#define FERRET_PRINT_BODY_BYTE(data, ctype, bits, lower) \
    do { \
        printf("%c", *(const uint8_t*)(data)); \
    } while (0)

#define FERRET_PRINT_BODY_CHAR(data, ctype, bits, lower) \
    do { \
        print_char_codepoint(*(const uint32_t*)(data)); \
    } while (0)

#define FERRET_PRINT_BODY_BOOL(data, ctype, bits, lower) \
    do { \
        printf("%s", *(const bool*)(data) ? "true" : "false"); \
    } while (0)

#define FERRET_DEFINE_PRINT(name, lower, ctype, category, bits) \
    static void ferret_print_##lower(const void* data) { \
        FERRET_PRINT_BODY_##category(data, ctype, bits, lower); \
    }

FERRET_PRIMITIVE_TYPES(FERRET_DEFINE_PRINT)
#undef FERRET_DEFINE_PRINT
#undef FERRET_PRINT_BODY_INT
#undef FERRET_PRINT_BODY_UINT
#undef FERRET_PRINT_BODY_FLOAT
#undef FERRET_PRINT_BODY_STRING
#undef FERRET_PRINT_BODY_BYTE
#undef FERRET_PRINT_BODY_CHAR
#undef FERRET_PRINT_BODY_BOOL
#undef FERRET_DEFINE_PRINT

static void print_union(const void* union_ptr) {
    int32_t tag = *(int32_t*)union_ptr;
    const uint8_t* data = (const uint8_t*)union_ptr + 4;
    
    switch (tag) {
#define FERRET_PRINT_CASE(name, lower, ctype, category, bits) \
        case FERRET_TYPE_##name: \
            ferret_print_##lower(data); \
            break;
        FERRET_PRIMITIVE_TYPES(FERRET_PRINT_CASE)
#undef FERRET_PRINT_CASE
        case FERRET_PRINTABLE_TAG_COMPLEX:
            print_complex(data);
            break;
        case FERRET_PRINTABLE_TAG_COMPLEX64:
            print_complex64(data);
            break;
        case FERRET_PRINTABLE_TAG_COMPLEX256:
            print_complex256(data);
            break;
        case FERRET_PRINTABLE_TAG_COMPLEX512:
            print_complex512(data);
            break;
        default: printf("<invalid union tag %d>", tag); break;
    }
}

static char* ferret_dup_cstr(const char* s) {
    if (s == NULL) {
        s = "";
    }
    size_t len = strlen(s);
    char* out = (char*)ferret_alloc(len + 1);
    if (out == NULL) {
        return NULL;
    }
    if (len > 0) {
        memcpy(out, s, len);
    }
    out[len] = '\0';
    return out;
}

static char* format_float_string(double val, int precision) {
    char buf[96];
    snprintf(buf, sizeof(buf), "%.*g", precision, val);

    bool has_decimal = false;
    bool has_exponent = false;
    for (char* p = buf; *p; p++) {
        if (*p == '.') has_decimal = true;
        if (*p == 'e' || *p == 'E') has_exponent = true;
    }
    if (!has_decimal && !has_exponent) {
        size_t len = strlen(buf);
        if (len + 2 < sizeof(buf)) {
            buf[len] = '.';
            buf[len + 1] = '0';
            buf[len + 2] = '\0';
        }
    }
    return ferret_dup_cstr(buf);
}

static int utf8_encode_codepoint(uint32_t codepoint, char out[5]) {
    if (codepoint <= 0x7F) {
        out[0] = (char)codepoint;
        out[1] = '\0';
        return 1;
    }
    if (codepoint <= 0x7FF) {
        out[0] = (char)(0xC0 | (codepoint >> 6));
        out[1] = (char)(0x80 | (codepoint & 0x3F));
        out[2] = '\0';
        return 2;
    }
    if (codepoint <= 0xFFFF) {
        out[0] = (char)(0xE0 | (codepoint >> 12));
        out[1] = (char)(0x80 | ((codepoint >> 6) & 0x3F));
        out[2] = (char)(0x80 | (codepoint & 0x3F));
        out[3] = '\0';
        return 3;
    }
    if (codepoint <= 0x10FFFF) {
        out[0] = (char)(0xF0 | (codepoint >> 18));
        out[1] = (char)(0x80 | ((codepoint >> 12) & 0x3F));
        out[2] = (char)(0x80 | ((codepoint >> 6) & 0x3F));
        out[3] = (char)(0x80 | (codepoint & 0x3F));
        out[4] = '\0';
        return 4;
    }
    snprintf(out, 5, "?");
    return 1;
}

static char* join_complex_signed(const char* re, const char* im) {
    if (re == NULL || im == NULL) {
        return ferret_dup_cstr("(nil)");
    }

    size_t re_len = strlen(re);
    size_t im_len = strlen(im);
    bool has_sign = (im_len > 0 && im[0] == '-');
    size_t total = re_len + (has_sign ? 0 : 1) + im_len + 1 + 1;
    char* out = (char*)ferret_alloc(total);
    if (out == NULL) {
        return NULL;
    }

    size_t off = 0;
    memcpy(out + off, re, re_len);
    off += re_len;
    if (!has_sign) {
        out[off++] = '+';
    }
    if (im_len > 0) {
        memcpy(out + off, im, im_len);
        off += im_len;
    }
    out[off++] = 'i';
    out[off] = '\0';
    return out;
}

static char* printable_to_string(const void* union_ptr) {
    if (union_ptr == NULL) {
        return ferret_dup_cstr("");
    }

    int32_t tag = *(const int32_t*)union_ptr;
    const uint8_t* data = (const uint8_t*)union_ptr + 4;

    switch (tag) {
#define FERRET_STRING_CASE_INT(name, lower, ctype, bits) \
        case FERRET_TYPE_##name: { \
            if ((bits) <= 64) { \
                uint64_t raw = 0; \
                int64_t value = 0; \
                size_t bytes = (bits) / 8; \
                memcpy(&raw, data, bytes); \
                if ((bits) < 64) { \
                    int shift = 64 - (bits); \
                    value = (int64_t)((raw << shift)) >> shift; \
                } else { \
                    value = (int64_t)raw; \
                } \
                char tmp[32]; \
                snprintf(tmp, sizeof(tmp), "%" PRId64, value); \
                return ferret_dup_cstr(tmp); \
            } \
            char* s = ferret_##lower##_to_string_ptr((const ctype*)data); \
            return s != NULL ? s : ferret_dup_cstr(""); \
        }
#define FERRET_STRING_CASE_UINT(name, lower, ctype, bits) \
        case FERRET_TYPE_##name: { \
            if ((bits) <= 64) { \
                uint64_t value = 0; \
                size_t bytes = (bits) / 8; \
                memcpy(&value, data, bytes); \
                char tmp[32]; \
                snprintf(tmp, sizeof(tmp), "%" PRIu64, value); \
                return ferret_dup_cstr(tmp); \
            } \
            char* s = ferret_##lower##_to_string_ptr((const ctype*)data); \
            return s != NULL ? s : ferret_dup_cstr(""); \
        }
#define FERRET_STRING_CASE_FLOAT(name, lower, ctype, bits) \
        case FERRET_TYPE_##name: { \
            if ((bits) <= 32) { \
                return format_float_string((double)(*(const float*)data), 6); \
            } \
            if ((bits) <= 64) { \
                return format_float_string(*(const double*)data, 15); \
            } \
            char* s = ferret_##lower##_to_string_ptr((const ctype*)data); \
            return s != NULL ? s : ferret_dup_cstr(""); \
        }
#define FERRET_STRING_CASE_STRING(name, lower, ctype, bits) \
        case FERRET_TYPE_##name: { \
            const char* s = *(const char* const*)data; \
            return ferret_dup_cstr(s != NULL ? s : "(null)"); \
        }
#define FERRET_STRING_CASE_BYTE(name, lower, ctype, bits) \
        case FERRET_TYPE_##name: { \
            char tmp[2]; \
            tmp[0] = (char)(*(const uint8_t*)data); \
            tmp[1] = '\0'; \
            return ferret_dup_cstr(tmp); \
        }
#define FERRET_STRING_CASE_CHAR(name, lower, ctype, bits) \
        case FERRET_TYPE_##name: { \
            char tmp[5]; \
            utf8_encode_codepoint(*(const uint32_t*)data, tmp); \
            return ferret_dup_cstr(tmp); \
        }
#define FERRET_STRING_CASE_BOOL(name, lower, ctype, bits) \
        case FERRET_TYPE_##name: { \
            return ferret_dup_cstr(*(const bool*)data ? "true" : "false"); \
        }
#define FERRET_STRING_CASE(name, lower, ctype, category, bits) \
        FERRET_STRING_CASE_##category(name, lower, ctype, bits)
        FERRET_PRIMITIVE_TYPES(FERRET_STRING_CASE)
#undef FERRET_STRING_CASE
#undef FERRET_STRING_CASE_INT
#undef FERRET_STRING_CASE_UINT
#undef FERRET_STRING_CASE_FLOAT
#undef FERRET_STRING_CASE_STRING
#undef FERRET_STRING_CASE_BYTE
#undef FERRET_STRING_CASE_CHAR
#undef FERRET_STRING_CASE_BOOL
        case FERRET_PRINTABLE_TAG_COMPLEX: {
            const ferret_complex_t* value = (const ferret_complex_t*)data;
            if (value == NULL) {
                return ferret_dup_cstr("(nil)");
            }
            char* re = format_float_string(value->re, 15);
            char* im = format_float_string(value->im, 15);
            if (re == NULL || im == NULL) {
                if (re != NULL) ferret_free(re);
                if (im != NULL) ferret_free(im);
                return ferret_dup_cstr("(nil)");
            }
            char* out = join_complex_signed(re, im);
            ferret_free(re);
            ferret_free(im);
            return out != NULL ? out : ferret_dup_cstr("(nil)");
        }
        case FERRET_PRINTABLE_TAG_COMPLEX64: {
            const ferret_complex64_t* value = (const ferret_complex64_t*)data;
            if (value == NULL) {
                return ferret_dup_cstr("(nil)");
            }
            char* re = format_float_string((double)value->re, 6);
            char* im = format_float_string((double)value->im, 6);
            if (re == NULL || im == NULL) {
                if (re != NULL) ferret_free(re);
                if (im != NULL) ferret_free(im);
                return ferret_dup_cstr("(nil)");
            }
            char* out = join_complex_signed(re, im);
            ferret_free(re);
            ferret_free(im);
            return out != NULL ? out : ferret_dup_cstr("(nil)");
        }
        case FERRET_PRINTABLE_TAG_COMPLEX256: {
            const ferret_complex256_t* value = (const ferret_complex256_t*)data;
            if (value == NULL) {
                return ferret_dup_cstr("(nil)");
            }
            char* re = ferret_f128_to_string_ptr(&value->re);
            char* im = ferret_f128_to_string_ptr(&value->im);
            if (re == NULL || im == NULL) {
                if (re != NULL) ferret_free(re);
                if (im != NULL) ferret_free(im);
                return ferret_dup_cstr("(nil)");
            }
            char* out = join_complex_signed(re, im);
            ferret_free(re);
            ferret_free(im);
            return out != NULL ? out : ferret_dup_cstr("(nil)");
        }
        case FERRET_PRINTABLE_TAG_COMPLEX512: {
            const ferret_complex512_t* value = (const ferret_complex512_t*)data;
            if (value == NULL) {
                return ferret_dup_cstr("(nil)");
            }
            char* re = ferret_f256_to_string_ptr(&value->re);
            char* im = ferret_f256_to_string_ptr(&value->im);
            if (re == NULL || im == NULL) {
                if (re != NULL) ferret_free(re);
                if (im != NULL) ferret_free(im);
                return ferret_dup_cstr("(nil)");
            }
            char* out = join_complex_signed(re, im);
            ferret_free(re);
            ferret_free(im);
            return out != NULL ? out : ferret_dup_cstr("(nil)");
        }
        default:
            return ferret_dup_cstr("<invalid union>");
    }
}

char* FERRET_FUNC(_formatPrintable)(void* slice_ptr) {
    if (slice_ptr == NULL) {
        return ferret_dup_cstr("");
    }

    ferret_array_t* arr = (ferret_array_t*)slice_ptr;
    if (arr->length <= 0 || arr->data == NULL) {
        return ferret_dup_cstr("");
    }

    void* sb = ferret_string_builder_new(64);
    if (sb == NULL) {
        return ferret_dup_cstr("");
    }

    uint8_t* current = (uint8_t*)arr->data;
    for (int32_t i = 0; i < arr->length; i++) {
        if (i > 0 && !ferret_string_builder_append(sb, " ")) {
            ferret_string_builder_destroy(sb);
            return ferret_dup_cstr("");
        }

        char* part = printable_to_string(current);
        if (part == NULL) {
            ferret_string_builder_destroy(sb);
            return ferret_dup_cstr("");
        }

        bool ok = ferret_string_builder_append(sb, part);
        ferret_free(part);
        if (!ok) {
            ferret_string_builder_destroy(sb);
            return ferret_dup_cstr("");
        }

        current += arr->elem_size;
    }

    char* out = ferret_string_builder_string(sb);
    ferret_string_builder_destroy(sb);
    return out != NULL ? out : ferret_dup_cstr("");
}

char* FERRET_FUNC(_readLineUnsafe)(void) {
    char* line = NULL;
    size_t len = 0;
    ssize_t read = getline(&line, &len, stdin);

    if (read == -1) {
        if (line) ferret_free(line);
        return ferret_dup_cstr("");
    }

    if (read > 0 && line[read - 1] == '\n') {
        line[read - 1] = '\0';
    }
    return line;
}

void FERRET_FUNC(_readLine)(void* out) {
    if (!out) return;

    char* line = NULL;
    size_t len = 0;
    ssize_t read = getline(&line, &len, stdin);

    char** str_ptr = (char**)out;
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 8);
    if (read == -1) {
        *str_ptr = "failed to read input";
        *tag_ptr = 0;
        if (line) ferret_free(line);
        return;
    }

    if (read > 0 && line[read - 1] == '\n') {
        line[read - 1] = '\0';
    }
    *str_ptr = line;
    *tag_ptr = 1;
}

void FERRET_FUNC(StreamWriter_Write)(void* out, const ferret_stream_writer_t* w, uint64_t w_heap, ferret_array_t* buf) {
    (void)w_heap;
    if (out == NULL) {
        return;
    }
    if (w == NULL) {
        char** err_ptr = (char**)out;
        uint8_t* tag_ptr = (uint8_t*)((char*)out + 8);
        *err_ptr = "invalid stream writer";
        *tag_ptr = 0;
        return;
    }
    ferret_global_write(out, w->stream, buf);
}

void FERRET_FUNC(_parseInt)(void* out, const char* value) {
    if (!out) return;

    int32_t* val_ptr = (int32_t*)out;
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 8);

    if (value == NULL) {
        *(char**)out = "invalid integer format";
        *tag_ptr = 0;
        return;
    }

    const char* p = value;
    while (*p && isspace((unsigned char)*p)) p++;
    if (*p == '\0') {
        *(char**)out = "invalid integer format";
        *tag_ptr = 0;
        return;
    }

    errno = 0;
    char* endptr = NULL;
    long parsed = strtol(p, &endptr, 10);
    while (endptr && *endptr && isspace((unsigned char)*endptr)) endptr++;

    if (endptr == p || (endptr && *endptr != '\0')) {
        *(char**)out = "invalid integer format";
        *tag_ptr = 0;
        return;
    }
    if (errno == ERANGE || parsed < INT32_MIN || parsed > INT32_MAX) {
        *(char**)out = "integer out of range";
        *tag_ptr = 0;
        return;
    }

    *val_ptr = (int32_t)parsed;
    *tag_ptr = 1;
}

void FERRET_FUNC(_parseFloat)(void* out, const char* value) {
    if (!out) return;

    double* val_ptr = (double*)out;
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 8);

    if (value == NULL) {
        *(char**)out = "invalid float format";
        *tag_ptr = 0;
        return;
    }

    const char* p = value;
    while (*p && isspace((unsigned char)*p)) p++;
    if (*p == '\0') {
        *(char**)out = "invalid float format";
        *tag_ptr = 0;
        return;
    }

    errno = 0;
    char* endptr = NULL;
    double parsed = strtod(p, &endptr);
    while (endptr && *endptr && isspace((unsigned char)*endptr)) endptr++;

    if (endptr == p || (endptr && *endptr != '\0') || errno == ERANGE || !isfinite(parsed)) {
        *(char**)out = "invalid float format";
        *tag_ptr = 0;
        return;
    }

    *val_ptr = parsed;
    *tag_ptr = 1;
}

// Enum to string conversion helper
// Used by codegen to convert enum tags to variant names
const char* FERRET_FUNC(enum_to_string)(const char* const* table, uint32_t count, int32_t tag) {
    if (tag < 0 || (uint32_t)tag >= count) {
        return "<invalid enum tag>";
    }
    return table[tag];
}
