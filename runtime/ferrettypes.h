/*
 * ferrettypes.h  —  C mirror of Ferret's type system.
 *
 * Purpose
 * -------
 * When writing #[extern] C functions that accept or return Ferret types,
 * include this header so your C types exactly match the compiler-generated
 * ABI.  This is the single source of truth for all Ferret ↔ C type mappings.
 *
 * Companion header
 * ----------------
 *   ferret_runtime.h   — runtime functions (panic, recover, str_data/str_len …)
 *                        Already includes this file; no need to include both.
 *
 * Table of contents
 * -----------------
 *   1. Primitive type aliases  (ferret_u8 … ferret_usize, ferret_bool …)
 *   2. str  and  []T  (slice fat-pointer)
 *   3. Enum   (Ferret `enum`)
 *   4. Error  (Ferret `error { … }`)
 *   5. Optional  (?T)
 *   6. Error-union  (E!T)
 *   7. Union
 *   8. Interface  (dynamic dispatch fat-pointer)
 *   9. Generic slice macro (FERRET_SLICE_OF)
 *
 * Layout rules (derived from layoutanalysis/analyze.go)
 * -------------------------------------------------------
 * • Tagged compound types (optional, error-union) share:
 *
 *     offset 0                      : uint32_t tag
 *     offset alignUp(4, payloadAlign): payload union
 *     total size                    : alignUp(payloadOffset + payloadSize,
 *                                             max(4, payloadAlign))
 *
 *   payloadAlign is the maximum alignment of any payload member.
 *
 *   Common cases:
 *     payload align ≤ 4  →  no gap:   { u32 tag; T value; }
 *     payload align = 8  →  4-byte gap: { u32 tag; u32 _pad; T value; }
 *
 * • Plain unions use:
 *
 *     size  = alignUp(max(member sizes), max(member aligns))
 *     align = max(member aligns)
 *
 * • Enums and error-sets are a bare 4-byte tag with no payload.
 * • Slices are fat pointers { T *ptr; uintptr_t len }.
 * • Interfaces are fat pointers { void *vtable; void *data }.
 */

#ifndef FERRET_TYPES_H
#define FERRET_TYPES_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* =========================================================================
 * 1. Primitive type aliases
 *
 * One-to-one mirrors of Ferret's built-in scalar types.
 * Use these in all #[extern] function signatures instead of raw C types.
 *
 *   Ferret type │ C alias        │ underlying C type
 *   ────────────┼────────────────┼──────────────────
 *   u8          │ ferret_u8      │ uint8_t
 *   u16         │ ferret_u16     │ uint16_t
 *   u32         │ ferret_u32     │ uint32_t
 *   u64         │ ferret_u64     │ uint64_t
 *   i8          │ ferret_i8      │ int8_t
 *   i16         │ ferret_i16     │ int16_t
 *   i32         │ ferret_i32     │ int32_t
 *   i64         │ ferret_i64     │ int64_t
 *   f32         │ ferret_f32     │ float
 *   f64         │ ferret_f64     │ double
 *   usize       │ ferret_usize   │ unsigned long  (pointer-sized)
 *   isize       │ ferret_isize   │ signed long    (pointer-sized)
 *   char        │ ferret_char    │ uint32_t       (Unicode scalar U+0000..U+10FFFF)
 *   bool        │ ferret_bool    │ uint8_t        (0 = false, 1 = true)
 *   *raw        │ ferret_raw     │ void *         (C void*, erased pointer)
 * ========================================================================= */

typedef uint8_t       ferret_u8;
typedef uint16_t      ferret_u16;
typedef uint32_t      ferret_u32;
typedef uint64_t      ferret_u64;
typedef int8_t        ferret_i8;
typedef int16_t       ferret_i16;
typedef int32_t       ferret_i32;
typedef int64_t       ferret_i64;
typedef float         ferret_f32;
typedef double        ferret_f64;
typedef unsigned long ferret_usize;   /* Ferret `usize` — pointer-sized unsigned integer */
typedef signed   long ferret_isize;   /* Ferret `isize` — pointer-sized signed integer   */
typedef ferret_u32    ferret_char;    /* Ferret `char`  — Unicode scalar value           */
typedef ferret_u8     ferret_bool;    /* Ferret `bool`  — 0 = false, 1 = true            */
typedef void         *ferret_raw;     /* Ferret `*raw`  — erased / untyped pointer       */

/* ferret_type_id — stable numeric identity assigned to every named Ferret type.
 * ID 0 is reserved (FERRET_TYPE_UNKNOWN).                                    */
typedef ferret_u32 ferret_type_id;
#define FERRET_TYPE_UNKNOWN 0u

/* =========================================================================
 * 2. str  and  []T  (slice fat-pointer)
 *
 * Ferret slices are fat pointers: { T *ptr; ferret_usize len }.
 *
 *   Ferret type │ C type
 *   ────────────┼──────────────────────────────────────────────
 *   str         │ FerretStr          { ferret_u8 *ptr; ferret_usize len; }
 *   []T         │ FERRET_SLICE_OF(T) { T *ptr;         ferret_usize len; }
 *
 * `str` is a dedicated UTF-8 text type. It shares ABI layout with `[]u8`,
 * but not semantics:
 *   • `str` is immutable text
 *   • `[]u8` is a mutable byte slice
 *   • explicit conversions between them may copy at runtime
 *
 * The ptr field points to the first byte and does NOT need to be
 * null-terminated. Use the len field for bounds.
 *
 * Pre-defined typed slices for common element types are in section 9.
 * ========================================================================= */

typedef struct {
    const ferret_u8 *ptr;   /* pointer to first byte; need not be null-terminated */
    ferret_usize     len;   /* number of bytes                                     */
} FerretStr;

/* =========================================================================
 * 3. Enum  (Ferret `enum`)
 *
 * A Ferret enum is a single 4-byte unsigned integer.  Variant indices are
 * assigned in declaration order: first declared variant = 0.
 *
 * Example — Ferret:
 *   type Direction enum { North, South, East, West }
 *
 * Equivalent C:
 *   typedef FerretEnumTag Direction;
 *   #define DIRECTION_NORTH 0u
 *   #define DIRECTION_SOUTH 1u
 *   #define DIRECTION_EAST  2u
 *   #define DIRECTION_WEST  3u
 * ========================================================================= */

typedef ferret_u32 FerretEnumTag;

/* FERRET_ENUM_VARIANT(EnumType, VariantName, index)
 * Convenience macro to declare a typed enum constant. */
#define FERRET_ENUM_VARIANT(EnumType, VariantName, index) \
    enum { EnumType##_##VariantName = (int)(index) }

/* =========================================================================
 * 4. Error  (Ferret `error { … }`)
 *
 * A Ferret error-set is also a bare 4-byte unsigned integer.
 * Error code 0 is NEVER a valid error (used as the "ok" sentinel in
 * error-union values — see section 6).  Actual error codes start at 1,
 * assigned in declaration order.
 *
 * Example — Ferret:
 *   type IoError error { NotFound, PermDenied, BrokenPipe }
 *
 * Equivalent C:
 *   typedef FerretErrorCode IoError;
 *   #define IO_ERROR_NOT_FOUND    1u   // first variant → 1 (0 is reserved for ok)
 *   #define IO_ERROR_PERM_DENIED  2u
 *   #define IO_ERROR_BROKEN_PIPE  3u
 *
 * NOTE: For bare error-set values (not inside an error-union), the values
 * start at 1 by convention; verify against the compiler output for your type.
 * ========================================================================= */

typedef ferret_u32 FerretErrorCode;

#define FERRET_ERROR_OK  0u   /* sentinel: "no error" (used in error-union tag field) */

/* =========================================================================
 * 5. Optional  (?T)
 *
 * Layout rule (from layoutanalysis):
 *
 *   If T has a stable invalid bit-pattern niche, ?T uses the same size and
 *   alignment as T and encodes `none` in that niche.
 *
 *   Current niche-optimized cases:
 *     ?*T / ?*raw T      → null pointer means none
 *     ?bool              → value 2 means none
 *     ?char              → value 0xFFFFFFFF means none
 *     ?enum / ?error     → value 0xFFFFFFFF means none
 *
 *   Otherwise ?T falls back to tagged storage:
 *     payloadAlign ≤ 4  →  { uint32_t tag; T value; }
 *     payloadAlign = 8  →  { uint32_t tag; uint32_t _pad; T value; }
 *
 * Tagged values use:
 *   FERRET_NONE (0) — no value
 *   FERRET_SOME (1) — value is valid
 *
 * Macros FERRET_OPTIONAL4 / FERRET_OPTIONAL8 produce anonymous struct
 * types suitable for use in parameters, return values, and local variables.
 *
 * Examples:
 *
 *   // Ferret:  fn find_index(arr []i32, val i32) ?i32
 *   FERRET_OPTIONAL4(ferret_i32) find_index(FerretSliceI32 arr, ferret_i32 val);
 *
 *   // Ferret:  fn lookup(key str) ?f64
 *   FERRET_OPTIONAL8(ferret_f64) lookup(FerretStr key);
 *
 *   // Constructing in C:
 *   FERRET_OPTIONAL4(ferret_i32) result;
 *   result.tag   = FERRET_SOME;
 *   result.value = 42;
 * ========================================================================= */

#define FERRET_NONE  0u
#define FERRET_SOME  1u

/*
 * FERRET_OPTIONAL4(T) — tagged ?T where T has alignment ≤ 4 and no niche.
 * Total size: 8 bytes.
 */
#define FERRET_OPTIONAL4(T) \
    struct { ferret_u32 tag; T value; }

/*
 * FERRET_OPTIONAL8(T) — tagged ?T where T has alignment 8 and no niche.
 * Applies to: u64, i64, f64, usize, isize, str, []T, interface.
 * Total size: 8 + sizeof(T), rounded up to 8-byte alignment.
 */
#define FERRET_OPTIONAL8(T) \
    struct { ferret_u32 tag; ferret_u32 _pad; T value; }

/* Pre-defined concrete optional types for common Ferret types: */
typedef void        *FerretOptPtr;   /* null => none */
typedef ferret_u8    FerretOptBool;  /* 2 => none */
typedef ferret_char  FerretOptChar;  /* 0xFFFFFFFF => none */
typedef struct { ferret_u32 tag; ferret_i32   value; } FerretOptI32;
typedef struct { ferret_u32 tag; ferret_u32   value; } FerretOptU32;
typedef struct { ferret_u32 tag; ferret_f32   value; } FerretOptF32;
typedef struct { ferret_u32 tag; ferret_u32 _pad; ferret_i64   value; } FerretOptI64;
typedef struct { ferret_u32 tag; ferret_u32 _pad; ferret_u64   value; } FerretOptU64;
typedef struct { ferret_u32 tag; ferret_u32 _pad; ferret_f64   value; } FerretOptF64;
typedef struct { ferret_u32 tag; ferret_u32 _pad; ferret_usize value; } FerretOptUsize;
typedef struct { ferret_u32 tag; ferret_u32 _pad; FerretStr    value; } FerretOptStr;

/* Helpers */
#define FERRET_OPT_IS_SOME(opt)  ((opt).tag == FERRET_SOME)
#define FERRET_OPT_IS_NONE(opt)  ((opt).tag == FERRET_NONE)
#define FERRET_OPT_VALUE(opt)    ((opt).value)
#define FERRET_OPT_PTR_IS_NONE(opt)   ((opt) == NULL)
#define FERRET_OPT_BOOL_NONE          2u
#define FERRET_OPT_CHAR_NONE          0xFFFFFFFFu

/* =========================================================================
 * 6. Error-union  (E!T  —  "either error E or value T")
 *
 * Layout rule: identical to optional, but with the error-code sitting
 * in the tag field instead of an explicit variant index.
 *
 *   tag == FERRET_ERROR_OK (0) — value half is valid
 *   tag != FERRET_ERROR_OK    — error half is valid; tag IS the FerretErrorCode
 *
 * payloadAlign ≤ 4  →  { uint32_t tag; T value; }
 * payloadAlign = 8  →  { uint32_t tag; uint32_t _pad; T value; }
 *
 * The "error" itself is encoded in the tag, so there is no separate error
 * field — just read the tag when FERRET_IS_ERR(tag).
 *
 * Example — Ferret:
 *   type IoError error { NotFound, PermDenied }
 *   fn read_byte(fd i32) IoError!u8
 *
 * Equivalent C:
 *   typedef struct { uint32_t tag; ferret_u8 value; } IoError_result_t;
 *   // or use the macro:
 *   FERRET_RESULT4(ferret_u8) read_byte(ferret_i32 fd);
 *
 * Checking in C:
 *   IoError_result_t r = read_byte(fd);
 *   if (FERRET_IS_ERR(r.tag)) {
 *       // r.tag is the IoError variant index (1 = NotFound, 2 = PermDenied …)
 *   } else {
 *       ferret_u8 byte = r.value;
 *   }
 * ========================================================================= */

#define FERRET_IS_ERR(tag)  ((tag) != FERRET_ERROR_OK)
#define FERRET_IS_OK(tag)   ((tag) == FERRET_ERROR_OK)

/*
 * FERRET_RESULT4(T) — E!T where T has alignment ≤ 4.
 * Applies to: bool, u8, i8, u16, i16, u32, i32, f32, char, enum, error, void.
 * (For void — i.e. "E!void" — use FERRET_RESULT_VOID below.)
 * Total size: 8 bytes.
 */
#define FERRET_RESULT4(T) \
    struct { ferret_u32 tag; T value; }

/*
 * FERRET_RESULT8(T) — E!T where T has alignment 8.
 * Applies to: u64, i64, f64, usize, isize, any pointer, str, []T, interface.
 */
#define FERRET_RESULT8(T) \
    struct { ferret_u32 tag; ferret_u32 _pad; T value; }

/*
 * FERRET_RESULT_VOID — E!void.
 * When the success case carries no data, only the tag matters.
 * Total size: 4 bytes.
 */
typedef struct { ferret_u32 tag; } FerretResultVoid;

/* Pre-defined concrete error-union types for common value types: */
typedef struct { ferret_u32 tag; ferret_i32   value; } FerretResultI32;
typedef struct { ferret_u32 tag; ferret_u32   value; } FerretResultU32;
typedef struct { ferret_u32 tag; ferret_f32   value; } FerretResultF32;
typedef struct { ferret_u32 tag; ferret_u32 _pad; ferret_i64   value; } FerretResultI64;
typedef struct { ferret_u32 tag; ferret_u32 _pad; ferret_u64   value; } FerretResultU64;
typedef struct { ferret_u32 tag; ferret_u32 _pad; ferret_f64   value; } FerretResultF64;
typedef struct { ferret_u32 tag; ferret_u32 _pad; ferret_usize value; } FerretResultUsize;
typedef struct { ferret_u32 tag; ferret_u32 _pad; void        *value; } FerretResultPtr;
typedef struct { ferret_u32 tag; ferret_u32 _pad; FerretStr    value; } FerretResultStr;

/* =========================================================================
 * 7. Union  (Ferret `union { T1, T2, … }`)
 *
 * A plain storage union.  Layout:
 *
 *   size  = alignUp(max(member sizes), max(member aligns))
 *   align = max(member aligns)
 *
 * There is no implicit runtime tag in storage.  The active member is a
 * compile-time / semantic notion tracked by the compiler, not encoded in
 * the in-memory representation.
 *
 * Because the member types vary per union declaration, there is no single
 * pre-defined C type.  Write the C union by hand following the max-size /
 * max-align rule.
 *
 * Example — Ferret:
 *   type Token union { i32, f64, str }
 *
 * C representation (size=16, align=8 because of str):
 *   typedef union {
 *       ferret_i32 as_i32;
 *       ferret_f64 as_f64;
 *       FerretStr  as_str;
 *   } Token;
 * ========================================================================= */
#define FERRET_UNION_VARIANT(ptr, member) ((ptr)->member)

/* =========================================================================
 * 8. Interface  (dynamic dispatch fat-pointer)
 *
 * A Ferret interface value is a pair of pointers:
 *   vtable — pointer to a compiler-generated vtable struct
 *   data   — pointer to the concrete value (heap-allocated or stack-pinned)
 *
 * Total size: 16 bytes, 8-byte aligned.
 *
 * The vtable layout is compiler-internal; from C you can call interface
 * methods only if you know the concrete type and cast accordingly.
 * Treat FerretInterface as opaque unless you have a matching vtable struct
 * generated by the compiler.
 *
 * Example — passing an interface to a Ferret function from C:
 *   // Construct manually only if you implement the vtable in C.
 *   FerretInterface iface = { &my_vtable, &my_concrete_value };
 * ========================================================================= */

typedef struct {
    const void *vtable;   /* pointer to compiler-generated vtable (read-only) */
    void       *data;     /* pointer to the concrete value                   */
} FerretInterface;

/* =========================================================================
 * 9. Generic slice macro  (FERRET_SLICE_OF)
 *
 * A Ferret slice []T is { T *ptr; ferret_usize len }.
 * FERRET_SLICE_OF(T) produces an anonymous struct with the same ABI shape as
 * FerretStr but with the correct element pointer type for type-safe C code.
 *
 * Example:
 *   // Ferret:  fn sum(vals []i32) i64
 *   ferret_i64 sum(FERRET_SLICE_OF(ferret_i32) vals);
 *
 *   // Constructing in C:
 *   ferret_i32 data[4] = { 1, 2, 3, 4 };
 *   FERRET_SLICE_OF(ferret_i32) slice = { data, 4 };
 * ========================================================================= */

#define FERRET_SLICE_OF(T) \
    struct { T *ptr; ferret_usize len; }

/* Pre-defined typed slice types for common element types: */
typedef struct { ferret_i8  *ptr; ferret_usize len; } FerretSliceI8;
typedef struct { ferret_u8  *ptr; ferret_usize len; } FerretSliceU8;   /* same layout as FerretStr */
typedef struct { ferret_i16 *ptr; ferret_usize len; } FerretSliceI16;
typedef struct { ferret_u16 *ptr; ferret_usize len; } FerretSliceU16;
typedef struct { ferret_i32 *ptr; ferret_usize len; } FerretSliceI32;
typedef struct { ferret_u32 *ptr; ferret_usize len; } FerretSliceU32;
typedef struct { ferret_char *ptr; ferret_usize len; } FerretSliceChar;
typedef struct { ferret_i64 *ptr; ferret_usize len; } FerretSliceI64;
typedef struct { ferret_u64 *ptr; ferret_usize len; } FerretSliceU64;
typedef struct { ferret_f32 *ptr; ferret_usize len; } FerretSliceF32;
typedef struct { ferret_f64 *ptr; ferret_usize len; } FerretSliceF64;
typedef struct { ferret_raw  ptr; ferret_usize len; } FerretSlicePtr;  /* [](*T) — erased element type */

#ifdef __cplusplus
}
#endif

#endif /* FERRET_TYPES_H */
