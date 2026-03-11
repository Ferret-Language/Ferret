/*
 * ferret_runtime.c — Ferret runtime support.
 *
 * Compiled and linked with every Ferret program.  Provides the small set of
 * primitives that cannot be written in pure Ferret:
 *
 *   ferret__panic           — abort with a message
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

/* -------------------------------------------------------------------------
 * ferret__panic
 * -------------------------------------------------------------------------*/
__attribute__((noreturn))
void ferret__panic(const char *ptr, ferret_usize len) {
    fwrite("panic: ", 1, 7, stderr);
    if (ptr != 0 && len > 0) {
        fwrite(ptr, 1, (size_t)len, stderr);
    }
    fwrite("\n", 1, 1, stderr);
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
void ferret__interface_panic(const char *iface, ferret_usize iface_len,
                              const char *got,   ferret_usize got_len) {
    fwrite("interface error: expected ", 1, 25, stderr);
    if (iface != 0 && iface_len > 0) {
        fwrite(iface, 1, (size_t)iface_len, stderr);
    }
    fwrite(", got ", 1, 6, stderr);
    if (got != 0 && got_len > 0) {
        fwrite(got, 1, (size_t)got_len, stderr);
    }
    fwrite("\n", 1, 1, stderr);
    fflush(stderr);
    abort();
}
