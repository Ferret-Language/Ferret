/*
 * ferret_runtime.c — Ferret runtime support.
 *
 * Compiled and linked with every Ferret program.  Provides the small set of
 * primitives that cannot be written in pure Ferret:
 *
 *   global__panic          — `#[builtin] fn panic(msg str)` implementation
 *   ferret__panic          — abort with a message (`panic <msg>` keyword)
 *   global__recover        — return current panic message as str, or empty str
 *   ferret__interface_panic — abort on a bad interface downcast (stub)
 *
 * Everything else (malloc/free, I/O, string building) is accessed through
 * ordinary #[extern] declarations in global.ferr and std/io.ferr, which bind
 * directly to libc at link time.  The runtime never does type dispatch; all
 * interface/union/enum dispatch is compiler-emitted static IR.
 */

#include "ferret_runtime.h"

#include <string.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>

#if defined(_WIN32)
#  include <windows.h>
#else
#  include <unistd.h>
#  if defined(__unix__) || defined(__APPLE__) || defined(__linux__) || defined(__FreeBSD__) || defined(__NetBSD__) || defined(__OpenBSD__)
#    include <sys/utsname.h>
#  endif
#endif

int f_random() {
    // time seed
    srand((unsigned)time(NULL));
    return rand();
}

static FerretStr ferret__static_str(const char *s) {
    FerretStr out;
    out.ptr = (const ferret_u8 *)s;
    out.len = s ? (ferret_usize)strlen(s) : 0;
    return out;
}

static FerretStr ferret__owned_str_from_cstr(const char *s) {
    FerretStr out = { (const ferret_u8 *)0, 0 };
    if (s == NULL) {
        return out;
    }
    size_t len = strlen(s);
    if (len == 0) {
        return out;
    }
    ferret_u8 *buf = (ferret_u8 *)malloc(len);
    if (buf == NULL) {
        return out;
    }
    memcpy(buf, s, len);
    out.ptr = buf;
    out.len = (ferret_usize)len;
    return out;
}

/* -------------------------------------------------------------------------
 * global__panic
 *
 * Called by Ferret source via `panic(msg)` where msg is a str (FerretStr).
 * -------------------------------------------------------------------------*/
__attribute__((noreturn))
void global__panic(const FerretStr *msg) {
    fputs("panic: ", stderr);
    if (msg && msg->ptr && msg->len > 0)
        fwrite(msg->ptr, 1, (size_t)msg->len, stderr);
    fputc('\n', stderr);
    fflush(stderr);
    abort();
}

/* -------------------------------------------------------------------------
 * ferret__panic
 * -------------------------------------------------------------------------*/
__attribute__((noreturn))
void ferret__panic(const ferret_i8 *msg) {
    fputs("panic: ", stderr);
    if (msg) fputs((const char *)msg, stderr);
    fputc('\n', stderr);
    fflush(stderr);
    abort();
}

/* -------------------------------------------------------------------------
 * ferret__interface_panic
 *
 * Stub — not yet called by compiler-emitted code.  Present so the ABI is
 * frozen before interface lowering lands.
 * -------------------------------------------------------------------------*/
__attribute__((noreturn))
void ferret__interface_panic(const ferret_i8 *expected_iface, const ferret_i8 *got_type) {
    fputs("interface error: expected ", stderr);
    if (expected_iface) fputs((const char *)expected_iface, stderr);
    fputs(", got ", stderr);
    if (got_type) fputs((const char *)got_type, stderr);
    fputc('\n', stderr);
    fflush(stderr);
    abort();
}

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

/* -------------------------------------------------------------------------
 * global__recover
 *
 * Stub — real implementation requires per-thread panic state storage, which
 * lands alongside the defer/recover pass.  For now returns an empty FerretStr.
 * -------------------------------------------------------------------------*/
FerretStr global__recover(void) {
    FerretStr empty = { (const ferret_u8 *)0, 0 };
    return empty;
}

void global__print_str(const FerretStr *s) {
    if (s && s->ptr && s->len > 0) {
        fwrite(s->ptr, 1, (size_t)s->len, stdout);
    }
    fputc('\n', stdout);
    fflush(stdout);
}

void global__print_bool(ferret_bool value) {
    fputs(value ? "true" : "false", stdout);
    fputc('\n', stdout);
    fflush(stdout);
}

void global__print_i64(ferret_i64 value) {
    fprintf(stdout, "%lld\n", (long long)value);
    fflush(stdout);
}

void global__print_u64(ferret_u64 value) {
    fprintf(stdout, "%llu\n", (unsigned long long)value);
    fflush(stdout);
}

void global__print_f64(ferret_f64 value) {
    fprintf(stdout, "%.17g\n", (double)value);
    fflush(stdout);
}

void global__print_char(ferret_char value) {
    ferret_u8 out[4];
    ferret_usize len = ferret__utf8_encode(value, out);
    fwrite(out, 1, (size_t)len, stdout);
    fputc('\n', stdout);
    fflush(stdout);
}

void global__print_ptr(ferret_raw value) {
    fprintf(stdout, "%p\n", value);
    fflush(stdout);
}

void global__print_type(const ferret_i8 *type_name) {
    fputc('<', stdout);
    if (type_name != NULL) {
        fputs((const char *)type_name, stdout);
    }
    fputs(">\n", stdout);
    fflush(stdout);
}

/* -------------------------------------------------------------------------
 * global__str_data / global__str_len
 *
 * Field accessors for the str fat-pointer.
 * The caller retains ownership of the str; these functions never free it.
 * -------------------------------------------------------------------------*/
ferret_raw global__str_data(const FerretStr *s) {
    return s ? (ferret_raw)s->ptr : (ferret_raw)0;
}

ferret_usize global__str_len(const FerretStr *s) {
    return s ? s->len : 0;
}

FerretSliceU8 global__str_bytes(const FerretStr *s) {
    FerretSliceU8 out = { (ferret_u8 *)0, 0 };
    if (s == NULL || s->ptr == NULL || s->len == 0) {
        return out;
    }

    ferret_u8 *buf = (ferret_u8 *)malloc((size_t)s->len);
    if (buf == NULL) {
        return out;
    }
    memcpy(buf, s->ptr, (size_t)s->len);
    out.ptr = buf;
    out.len = s->len;
    return out;
}

FerretStr global__bytes_str(const FerretSliceU8 *bytes) {
    FerretStr out = { (const ferret_u8 *)0, 0 };
    if (bytes == NULL || bytes->ptr == NULL || bytes->len == 0) {
        return out;
    }

    ferret_u8 *buf = (ferret_u8 *)malloc((size_t)bytes->len);
    if (buf == NULL) {
        return out;
    }
    memcpy(buf, bytes->ptr, (size_t)bytes->len);
    out.ptr = buf;
    out.len = bytes->len;
    return out;
}

FerretSliceChar global__str_chars(const FerretStr *s) {
    FerretSliceChar out = { (ferret_char *)0, 0 };
    if (s == NULL || s->ptr == NULL || s->len == 0) {
        return out;
    }

    const ferret_u8 *cursor = s->ptr;
    const ferret_u8 *end = s->ptr + s->len;
    ferret_usize count = 0;
    while (cursor < end) {
        (void)ferret__utf8_decode_next(&cursor, end);
        count++;
    }

    ferret_char *buf = (ferret_char *)malloc((size_t)(count * sizeof(ferret_char)));
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

FerretStr global__chars_str(const FerretSliceChar *chars) {
    FerretStr out = { (const ferret_u8 *)0, 0 };
    if (chars == NULL || chars->ptr == NULL || chars->len == 0) {
        return out;
    }

    ferret_usize total = 0;
    for (ferret_usize i = 0; i < chars->len; i++) {
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

    ferret_u8 *buf = (ferret_u8 *)malloc((size_t)total);
    if (buf == NULL) {
        return out;
    }

    ferret_usize offset = 0;
    for (ferret_usize i = 0; i < chars->len; i++) {
        ferret_u8 tmp[4];
        ferret_usize written = ferret__utf8_encode(chars->ptr[i], tmp);
        memcpy(buf + offset, tmp, (size_t)written);
        offset += written;
    }

    out.ptr = buf;
    out.len = offset;
    return out;
}

ferret_raw global__str_cstr(const FerretStr *s) {
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

FerretStr global__i64_str(ferret_i64 value) {
    char buf[32];
    snprintf(buf, sizeof(buf), "%lld", (long long)value);
    return ferret__owned_str_from_cstr(buf);
}

FerretStr global__u64_str(ferret_u64 value) {
    char buf[32];
    snprintf(buf, sizeof(buf), "%llu", (unsigned long long)value);
    return ferret__owned_str_from_cstr(buf);
}

FerretStr global__f64_str(ferret_f64 value) {
    char buf[64];
    snprintf(buf, sizeof(buf), "%.17g", (double)value);
    return ferret__owned_str_from_cstr(buf);
}

ferret_usize ferret_os_cpu_count(void) {
#if defined(_WIN32)
    SYSTEM_INFO info;
    GetSystemInfo(&info);
    if (info.dwNumberOfProcessors == 0) {
        return 1;
    }
    return (ferret_usize)info.dwNumberOfProcessors;
#elif defined(_SC_NPROCESSORS_ONLN)
    long value = sysconf(_SC_NPROCESSORS_ONLN);
    if (value < 1) {
        return 1;
    }
    return (ferret_usize)value;
#else
    return 1;
#endif
}

FerretStr ferret_os_platform(void) {
#if defined(_WIN32)
    return ferret__static_str("windows");
#elif defined(__APPLE__) && defined(__MACH__)
    return ferret__static_str("darwin");
#elif defined(__linux__)
    return ferret__static_str("linux");
#elif defined(__FreeBSD__)
    return ferret__static_str("freebsd");
#elif defined(__NetBSD__)
    return ferret__static_str("netbsd");
#elif defined(__OpenBSD__)
    return ferret__static_str("openbsd");
#else
    return ferret__static_str("unknown");
#endif
}

FerretStr ferret_os_arch(void) {
#if defined(__x86_64__) || defined(_M_X64)
    return ferret__static_str("amd64");
#elif defined(__aarch64__) || defined(_M_ARM64)
    return ferret__static_str("arm64");
#elif defined(__arm__) || defined(_M_ARM)
    return ferret__static_str("arm");
#elif defined(__i386__) || defined(_M_IX86)
    return ferret__static_str("386");
#elif defined(__riscv) && (__riscv_xlen == 64)
    return ferret__static_str("riscv64");
#else
    return ferret__static_str("unknown");
#endif
}

FerretStr ferret_os_name(void) {
#if defined(_WIN32)
    return ferret__static_str("Windows");
#elif defined(__unix__) || defined(__APPLE__) || defined(__linux__) || defined(__FreeBSD__) || defined(__NetBSD__) || defined(__OpenBSD__)
    struct utsname info;
    if (uname(&info) == 0 && info.sysname[0] != '\0') {
        return ferret__static_str(info.sysname);
    }
#endif
    return ferret_os_platform();
}

ferret_bool ferret_os_debug(void) {
#if defined(FERRET_DEBUG)
    return 1;
#else
    return 0;
#endif
}
