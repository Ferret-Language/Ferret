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

#include <stdio.h>
#include <stdlib.h>
#include <time.h>

int f_random() {
    // time seed
    srand((unsigned)time(NULL));
    return rand();
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

/* -------------------------------------------------------------------------
 * global__str_data / global__str_len
 *
 * Field accessors for the str fat-pointer.
 * The caller retains ownership of the str; these functions never free it.
 * -------------------------------------------------------------------------*/
const void *global__str_data(const FerretStr *s) {
    return s ? (const void *)s->ptr : (const void *)0;
}

ferret_usize global__str_len(const FerretStr *s) {
    return s ? s->len : 0;
}
