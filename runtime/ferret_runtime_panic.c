#include "ferret_runtime.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define FERRET_RUNTIME_TRAP_MESSAGE_CAP 512u

static FerretRuntimeTrap ferret__runtime_trap = { 0 };
static ferret_u8 ferret__runtime_trap_storage[FERRET_RUNTIME_TRAP_MESSAGE_CAP];

static void ferret__runtime_set_trap_bytes(ferret_u32 kind, const ferret_u8 *ptr, ferret_usize len) {
    ferret_usize limit;

    ferret__runtime_trap.kind = kind;
    ferret__runtime_trap.message.ptr = ferret__runtime_trap_storage;
    ferret__runtime_trap.message.len = 0;

    if (ptr == NULL || len == 0) {
        return;
    }

    limit = len;
    if (limit > FERRET_RUNTIME_TRAP_MESSAGE_CAP - 1u) {
        limit = FERRET_RUNTIME_TRAP_MESSAGE_CAP - 1u;
    }
    memcpy(ferret__runtime_trap_storage, ptr, (size_t)limit);
    ferret__runtime_trap_storage[limit] = 0;
    ferret__runtime_trap.message.len = limit;
}

static void ferret__runtime_set_trap_cstr(ferret_u32 kind, const char *msg) {
    if (msg == NULL) {
        ferret__runtime_set_trap_bytes(kind, NULL, 0);
        return;
    }
    ferret__runtime_set_trap_bytes(kind, (const ferret_u8 *)msg, (ferret_usize)strlen(msg));
}

static void ferret__runtime_set_trap_bounds(ferret_usize index, ferret_usize len) {
    int written = snprintf((char *)ferret__runtime_trap_storage,
                           FERRET_RUNTIME_TRAP_MESSAGE_CAP,
                           "index out of bounds (%zu >= %zu)",
                           (size_t)index,
                           (size_t)len);
    ferret__runtime_trap.kind = FERRET_RUNTIME_TRAP_BOUNDS;
    ferret__runtime_trap.message.ptr = ferret__runtime_trap_storage;
    if (written <= 0) {
        ferret__runtime_trap.message.len = 0;
        return;
    }
    if ((ferret_usize)written >= FERRET_RUNTIME_TRAP_MESSAGE_CAP) {
        written = (int)(FERRET_RUNTIME_TRAP_MESSAGE_CAP - 1u);
    }
    ferret__runtime_trap.message.len = (ferret_usize)written;
}

static void ferret__runtime_set_trap_interface(const ferret_i8 *expected_type, const ferret_i8 *got_type) {
    const char *expected = expected_type ? (const char *)expected_type : "<unknown>";
    const char *actual = got_type ? (const char *)got_type : "<unknown>";
    int written = snprintf((char *)ferret__runtime_trap_storage,
                           FERRET_RUNTIME_TRAP_MESSAGE_CAP,
                           "expected %s, got %s",
                           expected,
                           actual);
    ferret__runtime_trap.kind = FERRET_RUNTIME_TRAP_INTERFACE;
    ferret__runtime_trap.message.ptr = ferret__runtime_trap_storage;
    if (written <= 0) {
        ferret__runtime_trap.message.len = 0;
        return;
    }
    if ((ferret_usize)written >= FERRET_RUNTIME_TRAP_MESSAGE_CAP) {
        written = (int)(FERRET_RUNTIME_TRAP_MESSAGE_CAP - 1u);
    }
    ferret__runtime_trap.message.len = (ferret_usize)written;
}

const FerretRuntimeTrap *ferret__runtime_last_trap(void) {
    return &ferret__runtime_trap;
}

void ferret__runtime_clear_trap(void) {
    ferret__runtime_trap.kind = FERRET_RUNTIME_TRAP_NONE;
    ferret__runtime_trap.message.ptr = ferret__runtime_trap_storage;
    ferret__runtime_trap.message.len = 0;
    ferret__runtime_trap_storage[0] = 0;
}

__attribute__((noreturn))
void global__panic(const FerretStr *msg) {
    ferret__runtime_set_trap_bytes(FERRET_RUNTIME_TRAP_PANIC, msg ? msg->ptr : NULL, msg ? msg->len : 0);
    fputs("panic: ", stderr);
    if (msg && msg->ptr && msg->len > 0) {
        fwrite(msg->ptr, 1, (size_t)msg->len, stderr);
    }
    fputc('\n', stderr);
    fflush(stderr);
    abort();
}

__attribute__((noreturn))
void ferret__panic(const ferret_i8 *msg) {
    ferret__runtime_set_trap_cstr(FERRET_RUNTIME_TRAP_PANIC, (const char *)msg);
    fputs("panic: ", stderr);
    if (msg) {
        fputs((const char *)msg, stderr);
    }
    fputc('\n', stderr);
    fflush(stderr);
    abort();
}

void ferret__bounds_check(ferret_usize index, ferret_usize len) {
    if (index < len) {
        return;
    }
    ferret__runtime_set_trap_bounds(index, len);
    fprintf(stderr, "panic: index out of bounds (%zu >= %zu)\n", (size_t)index, (size_t)len);
    fflush(stderr);
    abort();
}

__attribute__((noreturn))
void ferret__interface_panic(const ferret_i8 *expected_type, const ferret_i8 *got_type) {
    ferret__runtime_set_trap_interface(expected_type, got_type);
    fputs("interface error: expected ", stderr);
    if (expected_type) {
        fputs((const char *)expected_type, stderr);
    }
    fputs(", got ", stderr);
    if (got_type) {
        fputs((const char *)got_type, stderr);
    }
    fputc('\n', stderr);
    fflush(stderr);
    abort();
}

void *ferret__interface_downcast(const FerretInterface *iface, const FerretTypeInfo *expected) {
    static const ferret_i8 invalid_interface[] = "<invalid interface>";
    static const ferret_i8 unknown_type[] = "<unknown>";
    const void *const *vtable;
    const FerretTypeInfo *actual;
    const ferret_i8 *expected_name = expected ? expected->name : unknown_type;
    const ferret_i8 *actual_name = unknown_type;

    if (iface == NULL || iface->vtable == NULL || expected == NULL) {
        ferret__interface_panic(expected_name, invalid_interface);
    }

    vtable = (const void *const *)iface->vtable;
    actual = (const FerretTypeInfo *)vtable[0];
    if (actual != NULL && actual->name != NULL) {
        actual_name = actual->name;
    }
    if (actual == expected) {
        return iface->data;
    }
    ferret__interface_panic(expected_name, actual_name);
}

FerretStr ferret_global_recover(void) {
    FerretStr empty = { (const ferret_u8 *)0, 0 };
    if (ferret__runtime_trap.kind == FERRET_RUNTIME_TRAP_NONE || ferret__runtime_trap.message.len == 0) {
        return empty;
    }
    return ferret__runtime_trap.message;
}
