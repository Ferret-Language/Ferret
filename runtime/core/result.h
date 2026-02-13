// Ferret runtime: Result helpers for native functions
// Layout: [union data][1-byte tag] (+ padding)
// Tag: 1 = Ok, 0 = Err

#ifndef FERRET_RESULT_H
#define FERRET_RESULT_H

#include <stdint.h>
#include <stdbool.h>
#include <stddef.h>
#include "array.h"

#define FERRET_RESULT_ERR(out, tag_offset, msg) \
    do { \
        if (!(out)) break; \
        *(char**)(out) = (char*)((msg) ? (msg) : "unknown error"); \
        *(uint8_t*)((char*)(out) + (tag_offset)) = 0; \
    } while (0)

#define FERRET_RESULT_OK(out, tag_offset, value_type, value) \
    do { \
        if (!(out)) break; \
        *(value_type*)(out) = (value); \
        *(uint8_t*)((char*)(out) + (tag_offset)) = 1; \
    } while (0)

#endif // FERRET_RESULT_H
