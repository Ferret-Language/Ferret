/*
 * ferret_runtime.h — Ferret runtime ABI contract.
 *
 * This header defines the stable interface between compiler-emitted code and
 * the tiny C runtime support library (ferret_runtime.c).  Backend lowering
 * must conform to this ABI when synthesising calls to runtime symbols.
 *
 * Rule: the runtime NEVER does type dispatch.  All interface/union/enum
 * dispatch is compiler-emitted static IR.  The runtime only handles
 * unrecoverable failure paths.
 *
 * Companion header
 * ----------------
 *   ferrettypes.h — C mirrors of all Ferret types (primitives, str, optional,
 *                    error-union, union, enum, interface, typed slices).
 *                    Already included below; no need to include both.
 */

#ifndef FERRET_RUNTIME_H
#define FERRET_RUNTIME_H

#include "ferrettypes.h"

#ifdef __cplusplus
extern "C" {
#endif

/* -------------------------------------------------------------------------
 * Type-info record emitted by the backend once per named type into .rodata.
 * Future use: typeof(), reflection, generic specialisation names.
 * -------------------------------------------------------------------------*/
typedef struct {
    ferret_type_id   id;
    const ferret_i8 *name;    /* null-terminated display name (C string)  */
    ferret_usize     size;    /* sizeof the type in bytes                 */
    ferret_usize     align;   /* alignof the type in bytes                */
    ferret_u32       flags;   /* reserved, must be zero for now           */
} FerretTypeInfo;

/* -------------------------------------------------------------------------
 * Panic / recover surface — called by compiler-emitted code and Ferret source.
 *
 *   global__panic           — `panic(msg str)` call from Ferret source
 *   ferret__panic           — compiler-internal panic with a C string
 *   ferret__recover         — `recover()` builtin
 *   ferret__interface_panic — bad interface downcast (stub, ABI frozen)
 * -------------------------------------------------------------------------*/

/* global__panic — the Ferret-callable `panic(msg *str)` builtin.
 *
 * Declared as #[builtin] fn panic(msg *str) void in global.ferr.
 * The compiler mangles this to global__panic.  str is a move type so callers
 * pass a reference (*str); the C side receives a const FerretStr*.
 * Does not return.
 */
__attribute__((noreturn))
void global__panic(const FerretStr *msg);

/*
 * ferret__panic — compiler-internal panic.
 *
 * msg — null-terminated C string (*i8).  String literals are always
 *        null-terminated so no explicit length is needed.  Does not return.
 */
__attribute__((noreturn))
void ferret__panic(const ferret_i8 *msg);

/*
 * ferret__interface_panic — called when a bad interface downcast is attempted.
 *
 * expected_iface — null-terminated name of the expected interface (*i8)
 * got_type       — null-terminated display name of the actual concrete type
 *
 * This function does not return.
 *
 * NOTE: not yet emitted by the compiler; stub is present so the ABI is
 * frozen before interface lowering is implemented.
 */
__attribute__((noreturn))
void ferret__interface_panic(const ferret_i8 *expected_iface, const ferret_i8 *got_type);

/*
 * global__recover — called by `recover()` expressions.
 *
 * Returns the current panic message as a FerretStr, or an empty FerretStr
 * (ptr=NULL, len=0) when called outside of a panic context.
 * Implemented in ferret_runtime.c.
 */
FerretStr global__recover(void);

/* -------------------------------------------------------------------------
 * Temporary generic print helpers.
 *
 * Compiler-emitted `print(x)` calls lower to one of these concrete helpers
 * based on the static type of `x`.
 * -------------------------------------------------------------------------*/

void global__print_str(const FerretStr *s);
void global__print_bool(ferret_bool value);
void global__print_i64(ferret_i64 value);
void global__print_u64(ferret_u64 value);
void global__print_f64(ferret_f64 value);
void global__print_char(ferret_char value);
void global__print_ptr(ferret_raw value);
void global__print_type(const ferret_i8 *type_name);

/* -------------------------------------------------------------------------
 * str_data / str_len  — extract fields from a str fat-pointer.
 *
 * These back the #[builtin] fn str_data(s *str) *raw
 * and #[builtin] fn str_len(s *str) usize declarations in global.ferr.
 *
 * Using them via a function call is safe; the caller keeps the str alive
 * for the duration of any use of the returned pointer.
 * -------------------------------------------------------------------------*/

/* global__str_data — returns the data pointer of a str as *raw.
 * Suitable for passing directly to POSIX write/read/memcpy etc.
 * Takes a reference (*str) so the fat-pointer is not copied. */
ferret_raw global__str_data(const FerretStr *s);

/* global__str_len — returns the byte-length field of a str value.
 * Takes a reference (*str) so the fat-pointer is not copied. */
ferret_usize global__str_len(const FerretStr *s);

/* -------------------------------------------------------------------------
 * Explicit string conversion helpers.
 *
 * These provide the runtime side of dedicated string conversions:
 *   str      -> []u8    (copies raw UTF-8 bytes)
 *   []u8     -> str     (copies raw UTF-8 bytes)
 *   str      -> []char  (UTF-8 decode)
 *   []char   -> str     (UTF-8 encode)
 *   str      -> *raw    (allocates a null-terminated C string)
 *
 * Returned buffers are heap allocated with libc malloc() so callers may free
 * them with the ordinary `free` extern when appropriate.
 * -------------------------------------------------------------------------*/

FerretSliceU8 global__str_bytes(const FerretStr *s);
FerretStr global__bytes_str(const FerretSliceU8 *bytes);
FerretSliceChar global__str_chars(const FerretStr *s);
FerretStr global__chars_str(const FerretSliceChar *chars);
ferret_raw global__str_cstr(const FerretStr *s);

/* -------------------------------------------------------------------------
 * std/os helpers
 *
 * These back the declarations in std/os.ferr.
 * -------------------------------------------------------------------------*/

ferret_usize ferret_os_cpu_count(void);
FerretStr ferret_os_platform(void);
FerretStr ferret_os_arch(void);
FerretStr ferret_os_name(void);
ferret_bool ferret_os_debug(void);

#ifdef __cplusplus
}
#endif

#endif /* FERRET_RUNTIME_H */
