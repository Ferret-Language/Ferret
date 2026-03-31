#ifndef FERRET_RUNTIME_INTERNAL_H
#define FERRET_RUNTIME_INTERNAL_H

#include "ferret_runtime.h"

#include <stdlib.h>
#include <string.h>

static inline FerretStr ferret__static_str(const char *s) {
    FerretStr out;
    out.ptr = (const ferret_u8 *)s;
    out.len = s ? (ferret_usize)strlen(s) : 0;
    return out;
}

static inline FerretStr ferret__owned_str_from_cstr(const char *s) {
    FerretStr out = { (const ferret_u8 *)0, 0 };
    size_t len;
    ferret_u8 *buf;

    if (s == NULL) {
        return out;
    }
    len = strlen(s);
    if (len == 0) {
        return out;
    }
    buf = (ferret_u8 *)malloc(len);
    if (buf == NULL) {
        return out;
    }
    memcpy(buf, s, len);
    out.ptr = buf;
    out.len = (ferret_usize)len;
    return out;
}

#endif /* FERRET_RUNTIME_INTERNAL_H */
