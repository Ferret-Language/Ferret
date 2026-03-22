/*
 * ferret_runtime.h — Ferret runtime ABI contract.
 *
 * This header defines the stable interface between compiler-emitted code and
 * the tiny C runtime support library (ferret_runtime.c).  Backend lowering
 * must conform to this ABI when synthesising calls to runtime symbols.
 *
 * Rule: the runtime keeps dynamic behavior minimal. Interface/union/enum
 * method dispatch remains compiler-emitted static IR; the runtime only
 * performs narrow Any-based helper dispatch such as `print`.
 *
 * Companion header
 * ----------------
 *   ferrettypes.h — C mirrors of all Ferret types (primitives, str, optional,
 *                    error-union, union, enum, interface, typed slices).
 *                    Already included below; no need to include both.
 *                    Includes `FerretAny` alias for empty-interface values.
 */

#ifndef FERRET_RUNTIME_H
#define FERRET_RUNTIME_H

#include "ferrettypes.h"

#ifdef __cplusplus
extern "C" {
#endif

/* -------------------------------------------------------------------------
 * Type-info record emitted by the backend into .rodata and referenced by
 * interface/Any vtables.
 * -------------------------------------------------------------------------*/
typedef struct {
    ferret_type_id   id;
    const ferret_i8 *name;    /* null-terminated display name (C string)  */
    ferret_usize     size;    /* sizeof the type in bytes                 */
    ferret_usize     align;   /* alignof the type in bytes                */
    ferret_u32       flags;   /* runtime classification bits              */
} FerretTypeInfo;

#define FERRET_TYPE_BOOL   1u
#define FERRET_TYPE_I8     2u
#define FERRET_TYPE_I16    3u
#define FERRET_TYPE_I32    4u
#define FERRET_TYPE_I64    5u
#define FERRET_TYPE_ISIZE  6u
#define FERRET_TYPE_U8     7u
#define FERRET_TYPE_U16    8u
#define FERRET_TYPE_U32    9u
#define FERRET_TYPE_U64    10u
#define FERRET_TYPE_USIZE  11u
#define FERRET_TYPE_F32    12u
#define FERRET_TYPE_F64    13u
#define FERRET_TYPE_CHAR   14u
#define FERRET_TYPE_STR    15u

#define FERRET_TYPE_FLAG_POINTER   (1u << 0)
#define FERRET_TYPE_FLAG_NAMED     (1u << 1)
#define FERRET_TYPE_FLAG_INTERFACE (1u << 2)
#define FERRET_TYPE_FLAG_SLICE     (1u << 3)

/* `FerretAny` (declared in ferrettypes.h) is the stable ABI type for empty
 * interface values across C helpers and runtime-adjacent extern functions. */

/* -------------------------------------------------------------------------
 * Panic / recover surface — called by compiler-emitted code and Ferret source.
 *
 *   global__panic           — `panic(msg str)` keyword lowering hook
 *   ferret__panic           — compiler-internal panic with a C string
 *   ferret_global_recover   — `recover()` extern from global.ferr
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
 * ferret_global_recover — called by `recover()` expressions.
 *
 * Returns the current panic message as a FerretStr, or an empty FerretStr
 * (ptr=NULL, len=0) when called outside of a panic context.
 * Implemented in ferret_runtime.c.
 */
FerretStr ferret_global_recover(void);

/* -------------------------------------------------------------------------
 * Generic Any-based print entrypoint.
 * -------------------------------------------------------------------------*/

void ferret_global_print(const FerretAny *value);

/* -------------------------------------------------------------------------
 * str_data / str_len  — extract fields from a str fat-pointer.
 *
 * These back the #[extern] fn str_data(s *str) *raw
 * and #[extern] fn str_len(s *str) usize declarations in global.ferr.
 *
 * Using them via a function call is safe; the caller keeps the str alive
 * for the duration of any use of the returned pointer.
 * -------------------------------------------------------------------------*/

/* ferret_global_str_data — returns the data pointer of a str as *raw.
 * Suitable for passing directly to POSIX write/read/memcpy etc.
 * Takes a reference (*str) so the fat-pointer is not copied. */
ferret_raw ferret_global_str_data(const FerretStr *s);

/* ferret_global_str_len — returns the byte-length field of a str value.
 * Takes a reference (*str) so the fat-pointer is not copied. */
ferret_usize ferret_global_str_len(const FerretStr *s);

/* ferret_global_slice_len — returns the element count of a []T value.
 * The element type is erased at the C boundary. */
ferret_usize ferret_global_slice_len(const FerretSlicePtr *s);

/* ferret_global_len — default extern target for global::len.
 * Currently this accepts the slice ABI and returns the element count.
 * Array len is folded to constants during MIR lowering. */
ferret_usize ferret_global_len(const FerretSlicePtr *s);

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

FerretSliceU8 ferret_global_str_bytes(const FerretStr *s);
FerretStr ferret_global_bytes_str(const FerretSliceU8 *bytes);
FerretSliceChar ferret_global_str_chars(const FerretStr *s);
FerretStr ferret_global_chars_str(const FerretSliceChar *chars);
ferret_raw ferret_global_str_cstr(const FerretStr *s);
FerretStr ferret_global_i64_str(ferret_i64 value);
FerretStr ferret_global_u64_str(ferret_u64 value);
FerretStr ferret_global_f64_str(ferret_f64 value);

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

/* -------------------------------------------------------------------------
 * std/mem ownership-boundary helpers
 *
 * These are explicit, unsafe boundary functions used by std/mem::Adopt/Expose.
 * They are bit-preserving pointer reinterpretations; callers are responsible
 * for allocator/lifetime correctness when crossing between raw and owner APIs.
 * -------------------------------------------------------------------------*/

ferret_raw ferret_std_mem_Expose(ferret_raw owner);
ferret_raw ferret_std_mem_Adopt(ferret_raw raw);

#ifdef __cplusplus
}
#endif

#endif /* FERRET_RUNTIME_H */
