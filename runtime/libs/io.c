// Ferret runtime: IO functions
// Native implementations for std/io module

#define _GNU_SOURCE  // For getline on POSIX systems
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include <limits.h>
#include "../core/alloc.h"
#include "../core/array.h"
#include "../core/bigint.h"
#include "../core/type_system.h"

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
        *lineptr = (char*)malloc(*n);
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
            char* new_ptr = (char*)realloc(*lineptr, new_size);
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
// Printable union layout: [4-byte tag][32 bytes data] = 36 bytes total (to accommodate 256-bit types)
// Tags follow ferret_type_kind_t for primitives (Printable union order).

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

static void print_union(const void* union_ptr) {
    int32_t tag = *(int32_t*)union_ptr;
    const uint8_t* data = (const uint8_t*)union_ptr + 4;
    
    switch (tag) {
        case FERRET_TYPE_I8: printf("%d", *(int8_t*)data); break;      // i8
        case FERRET_TYPE_I16: printf("%d", *(int16_t*)data); break;    // i16
        case FERRET_TYPE_I32: printf("%d", *(int32_t*)data); break;    // i32
        case FERRET_TYPE_I64: printf("%ld", *(int64_t*)data); break;   // i64
        case FERRET_TYPE_I128: {  // i128
            char* s = ferret_i128_to_string_ptr((const ferret_i128*)data);
            printf("%s", s);
            free(s);
            break;
        }
        case FERRET_TYPE_I256: {  // i256
            char* s = ferret_i256_to_string_ptr((const ferret_i256*)data);
            printf("%s", s);
            free(s);
            break;
        }
        case FERRET_TYPE_U8: printf("%u", *(uint8_t*)data); break;     // u8
        case FERRET_TYPE_U16: printf("%u", *(uint16_t*)data); break;   // u16
        case FERRET_TYPE_U32: printf("%u", *(uint32_t*)data); break;   // u32
        case FERRET_TYPE_U64: printf("%lu", *(uint64_t*)data); break;  // u64
        case FERRET_TYPE_U128: {  // u128
            char* s = ferret_u128_to_string_ptr((const ferret_u128*)data);
            printf("%s", s);
            free(s);
            break;
        }
        case FERRET_TYPE_U256: {  // u256
            char* s = ferret_u256_to_string_ptr((const ferret_u256*)data);
            printf("%s", s);
            free(s);
            break;
        }
        case FERRET_TYPE_F32: print_float(*(float*)data, 6); break;     // f32
        case FERRET_TYPE_F64: print_float(*(double*)data, 15); break;   // f64
        case FERRET_TYPE_F128: {  // f128
            char* s = ferret_f128_to_string_ptr((const ferret_f128*)data);
            printf("%s", s);
            free(s);
            break;
        }
        case FERRET_TYPE_F256: {  // f256
            char* s = ferret_f256_to_string_ptr((const ferret_f256*)data);
            printf("%s", s);
            free(s);
            break;
        }
        case FERRET_TYPE_STRING: {  // str
            const char* str = *(const char**)data;
            printf("%s", str ? str : "(null)");
            break;
        }
        case FERRET_TYPE_BYTE: printf("%c", *(uint8_t*)data); break;    // byte
        case FERRET_TYPE_CHAR: {  // char (32-bit Unicode scalar)
            uint32_t codepoint = *(uint32_t*)data;
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
            break;
        }
        case FERRET_TYPE_BOOL: printf("%s", *(bool*)data ? "true" : "false"); break; // bool
        default: printf("<invalid union tag %d>", tag); break;
    }
}

// Naming convention: std_io_Print -> ferret_std_io_Print
// Slice layout: { void* ptr; int32_t len; int32_t cap } = 16 bytes (with padding)
void ferret_std_io_Print(void* slice_ptr) {
    if (!slice_ptr) {
        return;
    }
    
    ferret_array_t* arr = (ferret_array_t*)slice_ptr;
    uint8_t* current = (uint8_t*)arr->data;
    
    for (int32_t i = 0; i < arr->length; i++) {
        if (i > 0) printf(" ");
        print_union(current);
        current += arr->elem_size;
    }
}

void ferret_std_io_Println(void* slice_ptr) {
    ferret_std_io_Print(slice_ptr);
    printf("\n");
}

// Read a line from stdin, returns str (unsafe, no result wrapper)
char* ferret_std_io_ReadUnsafe(void) {
    char* line = NULL;
    size_t len = 0;
    ssize_t read = getline(&line, &len, stdin);

    if (read == -1) {
        if (line) free(line);
        char* empty = (char*)ferret_alloc(1);
        if (empty != NULL) {
            empty[0] = '\0';
        }
        return empty;
    }

    if (read > 0 && line[read - 1] == '\n') {
        line[read - 1] = '\0';
    }
    return line;
}

// Result type layout for str!str: [union: str (8 bytes)][1-byte tag] (+ padding)
// Tag: 1 = Ok (value), 0 = Err (error) (matches QBE codegen)
// The union holds the str pointer (8 bytes on 64-bit)

// Read a line from stdin, returns str!str
void ferret_std_io_Read(void* out) {
    if (!out) return;
    
    char* line = NULL;
    size_t len = 0;
    ssize_t read = getline(&line, &len, stdin);
    
    // Result layout: [8-byte str pointer][1-byte tag]
    char** str_ptr = (char**)out;
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 8);
    
    if (read == -1) {
        // Error case
        *str_ptr = "failed to read input";
        *tag_ptr = 0;  // Err
        if (line) free(line);
    } else {
        // Remove trailing newline if present
        if (read > 0 && line[read - 1] == '\n') {
            line[read - 1] = '\0';
        }
        *str_ptr = line;  // Caller owns this memory now
        *tag_ptr = 1;  // Ok
    }
}

// Result layout for str!T: [8-byte union][1-byte tag] (+ padding)
#define FERRET_STD_IO_READ_NUMERIC(name, out_type, parse_type, parse_expr, invalid_msg, range_check, range_msg) \
    void ferret_std_io_##name(void* out) { \
        if (!out) return; \
        char* line = NULL; \
        size_t len = 0; \
        ssize_t read = getline(&line, &len, stdin); \
        out_type* val_ptr = (out_type*)out; \
        uint8_t* tag_ptr = (uint8_t*)((char*)out + 8); \
        if (read == -1) { \
            *(char**)out = "failed to read input"; \
            *tag_ptr = 0; \
        } else { \
            char* endptr; \
            parse_type parsed = (parse_expr); \
            if (endptr == line || (*endptr != '\0' && *endptr != '\n')) { \
                *(char**)out = (invalid_msg); \
                *tag_ptr = 0; \
            } else if (range_check) { \
                *(char**)out = (range_msg); \
                *tag_ptr = 0; \
            } else { \
                *val_ptr = (out_type)parsed; \
                *tag_ptr = 1; \
            } \
        } \
        if (line) free(line); \
    }

// Read an integer from stdin, returns str!i32
FERRET_STD_IO_READ_NUMERIC(
    ReadInt,
    int32_t,
    long,
    strtol(line, &endptr, 10),
    "invalid integer format",
    (parsed < INT32_MIN || parsed > INT32_MAX),
    "integer out of range"
)

// Read a float from stdin, returns str!f64
FERRET_STD_IO_READ_NUMERIC(
    ReadFloat,
    double,
    double,
    strtod(line, &endptr),
    "invalid float format",
    0,
    ""
)

// Enum to string conversion helper
// Used by codegen to convert enum tags to variant names
const char* ferret_enum_to_string(const char* const* table, uint32_t count, int32_t tag) {
    if (tag < 0 || (uint32_t)tag >= count) {
        return "<invalid enum tag>";
    }
    return table[tag];
}
