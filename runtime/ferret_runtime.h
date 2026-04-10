/*
 * ferret_runtime.h — Ferret runtime ABI contract.
 *
 * This header defines the stable interface between compiler-emitted code and
 * the tiny C runtime support library in the `runtime/` sources. Backend lowering
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
    const void      *meta;    /* optional composite print metadata        */
} FerretTypeInfo;

typedef struct {
    const FerretTypeInfo *elem;
    ferret_usize          stride;
} FerretSliceTypeInfo;

typedef struct {
    const FerretTypeInfo *elem;
    ferret_usize          len;
    ferret_usize          stride;
} FerretArrayTypeInfo;

typedef struct {
    ferret_usize          offset;
    const FerretTypeInfo *type;
} FerretTupleFieldInfo;

typedef struct {
    ferret_usize                len;
    const FerretTupleFieldInfo *fields;
} FerretTupleTypeInfo;

typedef struct {
    ferret_usize           len;
    const ferret_i8 *const *names;
} FerretVariantTypeInfo;

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
#define FERRET_TYPE_FLAG_INTEGER   (1u << 4)
#define FERRET_TYPE_FLAG_SIGNED    (1u << 5)
#define FERRET_TYPE_FLAG_ARRAY     (1u << 6)
#define FERRET_TYPE_FLAG_TUPLE     (1u << 7)
#define FERRET_TYPE_FLAG_VARIANTS  (1u << 8)
#define FERRET_TYPE_FLAG_OPTIONAL  (1u << 9)

typedef struct {
    const FerretTypeInfo *inner;
    ferret_usize          payload_offset;
} FerretOptionalTypeInfo;

/* `FerretAny` (declared in ferrettypes.h) is the stable ABI type for empty
 * interface values across C helpers and runtime-adjacent extern functions. */

/* -------------------------------------------------------------------------
 * Panic / recover surface — called by compiler-emitted code and Ferret source.
 *
 *   global__panic           — `panic(msg str)` keyword lowering hook
 *   ferret__panic           — compiler-internal panic with a C string
 *   ferret_global_recover   — `recover()` extern from global.fer
 *   ferret__interface_panic — bad interface downcast
 *   ferret__interface_downcast — checked interface-to-concrete downcast
 * -------------------------------------------------------------------------*/

/* global__panic — the Ferret-callable `panic(msg *str)` builtin.
 *
 * Declared as #[builtin] fn panic(msg *str) void in global.fer.
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
 * ferret__bounds_check — panic when index >= len for array/slice indexing.
 *
 * The compiler emits this for all non-provable-safe array/slice reads and
 * writes. Returns normally when the access is in range.
 */
void ferret__bounds_check(ferret_usize index, ferret_usize len);

/*
 * ferret__interface_panic — called when a bad interface downcast is attempted.
 *
 * expected_type — null-terminated name of the expected target type (*i8)
 * got_type       — null-terminated display name of the actual concrete type
 *
 * This function does not return.
 */
__attribute__((noreturn))
void ferret__interface_panic(const ferret_i8 *expected_type, const ferret_i8 *got_type);

/*
 * ferret__interface_downcast — checks an interface value against the expected
 * concrete runtime type and returns the concrete data pointer on success.
 *
 * iface     — pointer to the interface fat-pointer slot ({data, vtable})
 * expected  — compiler-emitted runtime type-info record for the target type
 *
 * On mismatch this calls ferret__interface_panic and does not return.
 */
void *ferret__interface_downcast(const FerretInterface *iface, const FerretTypeInfo *expected);

/*
 * ferret_global_recover — called by `recover()` expressions.
 *
 * Returns the current panic message as a FerretStr, or an empty FerretStr
 * (ptr=NULL, len=0) when called outside of a panic context.
 * Implemented in the runtime support library.
 */
FerretStr ferret_global_recover(void);

/* -------------------------------------------------------------------------
 * Generic Any-based print entrypoint.
 * Accepts the variadic print payload as a []Any slice and prints each element.
 * -------------------------------------------------------------------------*/

void ferret_global_print(const FerretSliceAny *values);

/* -------------------------------------------------------------------------
 * std/io surface.
 * -------------------------------------------------------------------------*/

ferret_usize ferret_std_io_write_stream(ferret_i32 kind, const FerretStr *text);
ferret_raw ferret_std_io_buffer_new(void);
ferret_usize ferret_std_io_buffer_write(ferret_raw handle, const FerretStr *text);
FerretSliceU8 ferret_std_io_buffer_read(ferret_raw handle, ferret_usize size);
FerretStr ferret_std_io_buffer_view(ferret_raw handle);
void ferret_std_io_buffer_reset(ferret_raw handle);
void ferret_std_io_buffer_close(ferret_raw handle);

/* -------------------------------------------------------------------------
 * std/fs surface.
 * -------------------------------------------------------------------------*/

ferret_raw ferret_std_fs_open(const FerretStr *path);
ferret_usize ferret_std_fs_write(ferret_raw handle, const FerretStr *text);
void ferret_std_fs_close(ferret_raw handle);

/* -------------------------------------------------------------------------
 * std/net/tcp surface.
 * -------------------------------------------------------------------------*/

ferret_raw ferret_std_net_tcp_dial(const FerretStr *host, ferret_u16 port);
ferret_raw ferret_std_net_tcp_listen(const FerretStr *host, ferret_u16 port);
ferret_raw ferret_std_net_tcp_accept(ferret_raw handle);
ferret_usize ferret_std_net_tcp_write(ferret_raw handle, const FerretStr *text);
FerretSliceU8 ferret_std_net_tcp_read(ferret_raw handle, ferret_usize size);
void ferret_std_net_tcp_close_listener(ferret_raw handle);
void ferret_std_net_tcp_close(ferret_raw handle);

/* -------------------------------------------------------------------------
 * str_data / str_len  — extract fields from a str fat-pointer.
 *
 * These back the #[extern] fn str_data(s *str) *raw
 * and #[extern] fn str_len(s *str) usize declarations in global.fer.
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
ferret_char ferret_global_str_index(const FerretStr *s, ferret_usize index);
FerretSliceChar ferret_global_str_chars(const FerretStr *s);
FerretStr ferret_global_chars_str(const FerretSliceChar *chars);
ferret_raw ferret_global_str_cstr(const FerretStr *s);
FerretStr ferret_global_i64_str(ferret_i64 value);
FerretStr ferret_global_u64_str(ferret_u64 value);
FerretStr ferret_global_f64_str(ferret_f64 value);

/* -------------------------------------------------------------------------
 * std/os helpers
 *
 * These back the declarations in std/os.fer.
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
ferret_raw ferret_std_mem_ExposeRef(const ferret_raw *owner);
ferret_raw ferret_std_mem_Adopt(ferret_raw raw);

#ifdef __cplusplus
}
#endif

#endif /* FERRET_RUNTIME_H */
