#include "ferret_runtime.h"

#include <stdio.h>
#include <stdlib.h>

__attribute__((noreturn))
void global__panic(const FerretStr *msg) {
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
    fprintf(stderr, "panic: index out of bounds (%zu >= %zu)\n", (size_t)index, (size_t)len);
    fflush(stderr);
    abort();
}

__attribute__((noreturn))
void ferret__interface_panic(const ferret_i8 *expected_type, const ferret_i8 *got_type) {
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
    return empty;
}
