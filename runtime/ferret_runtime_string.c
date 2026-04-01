#include "ferret_runtime_internal.h"

#include <stdio.h>

static ferret_char ferret__utf8_decode_next(const ferret_u8 **cursor, const ferret_u8 *end) {
    const ferret_u8 *p = *cursor;
    if (p == NULL || p >= end) {
        return 0;
    }

    if ((*p & 0x80u) == 0) {
        *cursor = p + 1;
        return (ferret_char)(*p);
    }

    if ((*p & 0xE0u) == 0xC0u) {
        if ((p + 1) < end && (p[1] & 0xC0u) == 0x80u) {
            *cursor = p + 2;
            return (ferret_char)(((ferret_char)(p[0] & 0x1Fu) << 6) |
                                 (ferret_char)(p[1] & 0x3Fu));
        }
    } else if ((*p & 0xF0u) == 0xE0u) {
        if ((p + 2) < end && (p[1] & 0xC0u) == 0x80u && (p[2] & 0xC0u) == 0x80u) {
            *cursor = p + 3;
            return (ferret_char)(((ferret_char)(p[0] & 0x0Fu) << 12) |
                                 ((ferret_char)(p[1] & 0x3Fu) << 6) |
                                 (ferret_char)(p[2] & 0x3Fu));
        }
    } else if ((*p & 0xF8u) == 0xF0u) {
        if ((p + 3) < end &&
            (p[1] & 0xC0u) == 0x80u &&
            (p[2] & 0xC0u) == 0x80u &&
            (p[3] & 0xC0u) == 0x80u) {
            *cursor = p + 4;
            return (ferret_char)(((ferret_char)(p[0] & 0x07u) << 18) |
                                 ((ferret_char)(p[1] & 0x3Fu) << 12) |
                                 ((ferret_char)(p[2] & 0x3Fu) << 6) |
                                 (ferret_char)(p[3] & 0x3Fu));
        }
    }

    *cursor = p + 1;
    return 0xFFFDU;
}

static ferret_usize ferret__utf8_encode(ferret_char codepoint, ferret_u8 out[4]) {
    if (codepoint <= 0x7Fu) {
        out[0] = (ferret_u8)codepoint;
        return 1;
    }
    if (codepoint <= 0x7FFu) {
        out[0] = (ferret_u8)(0xC0u | (codepoint >> 6));
        out[1] = (ferret_u8)(0x80u | (codepoint & 0x3Fu));
        return 2;
    }
    if (codepoint <= 0xFFFFu) {
        out[0] = (ferret_u8)(0xE0u | (codepoint >> 12));
        out[1] = (ferret_u8)(0x80u | ((codepoint >> 6) & 0x3Fu));
        out[2] = (ferret_u8)(0x80u | (codepoint & 0x3Fu));
        return 3;
    }
    if (codepoint <= 0x10FFFFu) {
        out[0] = (ferret_u8)(0xF0u | (codepoint >> 18));
        out[1] = (ferret_u8)(0x80u | ((codepoint >> 12) & 0x3Fu));
        out[2] = (ferret_u8)(0x80u | ((codepoint >> 6) & 0x3Fu));
        out[3] = (ferret_u8)(0x80u | (codepoint & 0x3Fu));
        return 4;
    }

    out[0] = 0xEFu;
    out[1] = 0xBFu;
    out[2] = 0xBDu;
    return 3;
}

ferret_raw ferret_global_str_data(const FerretStr *s) {
    return s ? (ferret_raw)s->ptr : (ferret_raw)0;
}

ferret_usize ferret_global_str_len(const FerretStr *s) {
    return s ? s->len : 0;
}

ferret_usize ferret_global_slice_len(const FerretSlicePtr *s) {
    return s ? s->len : 0;
}

ferret_usize ferret_global_len(const FerretSlicePtr *s) {
    return s ? s->len : 0;
}

FerretSliceU8 ferret_global_str_bytes(const FerretStr *s) {
    FerretSliceU8 out = { (ferret_u8 *)0, 0 };
    ferret_u8 *buf;

    if (s == NULL || s->ptr == NULL || s->len == 0) {
        return out;
    }

    buf = (ferret_u8 *)malloc((size_t)s->len);
    if (buf == NULL) {
        return out;
    }
    memcpy(buf, s->ptr, (size_t)s->len);
    out.ptr = buf;
    out.len = s->len;
    return out;
}

FerretStr ferret_global_bytes_str(const FerretSliceU8 *bytes) {
    FerretStr out = { (const ferret_u8 *)0, 0 };
    ferret_u8 *buf;

    if (bytes == NULL || bytes->ptr == NULL || bytes->len == 0) {
        return out;
    }

    buf = (ferret_u8 *)malloc((size_t)bytes->len);
    if (buf == NULL) {
        return out;
    }
    memcpy(buf, bytes->ptr, (size_t)bytes->len);
    out.ptr = buf;
    out.len = bytes->len;
    return out;
}

ferret_char ferret_global_str_index(const FerretStr *s, ferret_usize index) {
    const ferret_u8 *cursor;
    const ferret_u8 *end;
    ferret_usize current = 0;

    if (s == NULL || s->ptr == NULL || s->len == 0) {
        ferret__bounds_check(index, 0);
        return 0;
    }

    cursor = s->ptr;
    end = s->ptr + s->len;
    while (cursor < end) {
        ferret_char ch = ferret__utf8_decode_next(&cursor, end);
        if (current == index) {
            return ch;
        }
        current++;
    }

    ferret__bounds_check(index, current);
    return 0;
}

FerretSliceChar ferret_global_str_chars(const FerretStr *s) {
    FerretSliceChar out = { (ferret_char *)0, 0 };
    const ferret_u8 *cursor;
    const ferret_u8 *end;
    ferret_usize count = 0;
    ferret_char *buf;

    if (s == NULL || s->ptr == NULL || s->len == 0) {
        return out;
    }

    cursor = s->ptr;
    end = s->ptr + s->len;
    while (cursor < end) {
        (void)ferret__utf8_decode_next(&cursor, end);
        count++;
    }

    buf = (ferret_char *)malloc((size_t)(count * sizeof(ferret_char)));
    if (buf == NULL) {
        out.len = 0;
        return out;
    }

    cursor = s->ptr;
    count = 0;
    while (cursor < end) {
        buf[count++] = ferret__utf8_decode_next(&cursor, end);
    }

    out.ptr = buf;
    out.len = count;
    return out;
}

FerretStr ferret_global_chars_str(const FerretSliceChar *chars) {
    FerretStr out = { (const ferret_u8 *)0, 0 };
    ferret_usize total = 0;
    ferret_u8 *buf;
    ferret_usize i;
    ferret_usize offset = 0;

    if (chars == NULL || chars->ptr == NULL || chars->len == 0) {
        return out;
    }

    for (i = 0; i < chars->len; i++) {
        ferret_char cp = chars->ptr[i];
        if (cp <= 0x7Fu) {
            total += 1;
        } else if (cp <= 0x7FFu) {
            total += 2;
        } else if (cp <= 0xFFFFu) {
            total += 3;
        } else {
            total += 4;
        }
    }

    buf = (ferret_u8 *)malloc((size_t)total);
    if (buf == NULL) {
        return out;
    }

    for (i = 0; i < chars->len; i++) {
        ferret_u8 tmp[4];
        ferret_usize written = ferret__utf8_encode(chars->ptr[i], tmp);
        memcpy(buf + offset, tmp, (size_t)written);
        offset += written;
    }

    out.ptr = buf;
    out.len = offset;
    return out;
}

ferret_raw ferret_global_str_cstr(const FerretStr *s) {
    ferret_u8 *buf;

    if (s == NULL || s->ptr == NULL || s->len == 0) {
        buf = (ferret_u8 *)malloc(1u);
        if (buf != NULL) {
            buf[0] = 0;
        }
        return (ferret_raw)buf;
    }

    buf = (ferret_u8 *)malloc((size_t)s->len + 1u);
    if (buf == NULL) {
        return (ferret_raw)0;
    }
    memcpy(buf, s->ptr, (size_t)s->len);
    buf[s->len] = 0;
    return (ferret_raw)buf;
}

FerretStr ferret_global_i64_str(ferret_i64 value) {
    char buf[32];
    snprintf(buf, sizeof(buf), "%lld", (long long)value);
    return ferret__owned_str_from_cstr(buf);
}

FerretStr ferret_global_u64_str(ferret_u64 value) {
    char buf[32];
    snprintf(buf, sizeof(buf), "%llu", (unsigned long long)value);
    return ferret__owned_str_from_cstr(buf);
}

FerretStr ferret_global_f64_str(ferret_f64 value) {
    char buf[64];
    snprintf(buf, sizeof(buf), "%.17g", (double)value);
    return ferret__owned_str_from_cstr(buf);
}
