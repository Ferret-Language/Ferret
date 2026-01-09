#ifndef FERRET_BIGINT_H
#define FERRET_BIGINT_H

#include <stdint.h>
#include <stdbool.h>
#include <math.h>

// Limb size detection for big integers (default to 32-bit if unknown)
#if defined(__SIZEOF_INT128__)
    #ifndef FERRET_HAS_WIDE128
        #define FERRET_HAS_WIDE128 1
    #endif
#endif

#if defined(UINTPTR_MAX) && UINTPTR_MAX >= 0xffffffffffffffffULL && defined(FERRET_HAS_WIDE128)
    #define FERRET_LIMB_BITS 64
#else
    #define FERRET_LIMB_BITS 32
#endif

#if FERRET_LIMB_BITS == 64
    typedef uint64_t ferret_limb_t;
#else
    typedef uint32_t ferret_limb_t;
#endif

#define FERRET_INT_WIDTHS(X) \
    X(128) \
    X(256)

#define FERRET_LIMBS_FOR_BITS(BITS) (((BITS) + FERRET_LIMB_BITS - 1) / FERRET_LIMB_BITS)
#define FERRET_LIMB_MAX ((ferret_limb_t)~(ferret_limb_t)0)

// Feature detection for 128-bit floating point support
// Note: __float128 is NOT supported on macOS (Apple Clang), only on Linux with GCC/Clang
#if defined(__GNUC__) && !defined(__APPLE__) && (defined(__x86_64__) || defined(__i386__) || defined(__powerpc__) || defined(__powerpc64__))
    #ifndef FERRET_HAS_FLOAT128
        #define FERRET_HAS_FLOAT128 1
    #endif
#endif

// Fixed-width integer types (limb-based)
#define FERRET_DECLARE_INT_TYPES(BITS) \
    enum { FERRET_U##BITS##_LIMBS = FERRET_LIMBS_FOR_BITS(BITS) }; \
    typedef struct { \
        ferret_limb_t words[FERRET_U##BITS##_LIMBS];  \
    } ferret_u##BITS; \
    typedef struct { \
        ferret_limb_t words[FERRET_U##BITS##_LIMBS];  \
    } ferret_i##BITS;

FERRET_INT_WIDTHS(FERRET_DECLARE_INT_TYPES)
#undef FERRET_DECLARE_INT_TYPES

// Fixed-width integer operations
#define FERRET_DECL_SIGNED_INT_OPS(BITS) \
    ferret_i##BITS ferret_i##BITS##_add(ferret_i##BITS a, ferret_i##BITS b); \
    ferret_i##BITS ferret_i##BITS##_sub(ferret_i##BITS a, ferret_i##BITS b); \
    ferret_i##BITS ferret_i##BITS##_mul(ferret_i##BITS a, ferret_i##BITS b); \
    ferret_i##BITS ferret_i##BITS##_div(ferret_i##BITS a, ferret_i##BITS b); \
    ferret_i##BITS ferret_i##BITS##_mod(ferret_i##BITS a, ferret_i##BITS b); \
    bool ferret_i##BITS##_eq(ferret_i##BITS a, ferret_i##BITS b); \
    bool ferret_i##BITS##_lt(ferret_i##BITS a, ferret_i##BITS b); \
    bool ferret_i##BITS##_gt(ferret_i##BITS a, ferret_i##BITS b); \
    ferret_i##BITS ferret_i##BITS##_pow(ferret_i##BITS base, ferret_i##BITS exp);

#define FERRET_DECL_UNSIGNED_INT_OPS(BITS) \
    ferret_u##BITS ferret_u##BITS##_add(ferret_u##BITS a, ferret_u##BITS b); \
    ferret_u##BITS ferret_u##BITS##_sub(ferret_u##BITS a, ferret_u##BITS b); \
    ferret_u##BITS ferret_u##BITS##_mul(ferret_u##BITS a, ferret_u##BITS b); \
    ferret_u##BITS ferret_u##BITS##_div(ferret_u##BITS a, ferret_u##BITS b); \
    ferret_u##BITS ferret_u##BITS##_mod(ferret_u##BITS a, ferret_u##BITS b); \
    bool ferret_u##BITS##_eq(ferret_u##BITS a, ferret_u##BITS b); \
    bool ferret_u##BITS##_lt(ferret_u##BITS a, ferret_u##BITS b); \
    bool ferret_u##BITS##_gt(ferret_u##BITS a, ferret_u##BITS b); \
    ferret_u##BITS ferret_u##BITS##_pow(ferret_u##BITS base, ferret_u##BITS exp);

#define FERRET_DECL_INT_CONVERSIONS(BITS) \
    ferret_i##BITS ferret_i##BITS##_from_i64(int64_t val); \
    ferret_u##BITS ferret_u##BITS##_from_u64(uint64_t val); \
    int64_t ferret_i##BITS##_to_i64(ferret_i##BITS val); \
    uint64_t ferret_u##BITS##_to_u64(ferret_u##BITS val);

FERRET_INT_WIDTHS(FERRET_DECL_SIGNED_INT_OPS)
FERRET_INT_WIDTHS(FERRET_DECL_UNSIGNED_INT_OPS)
FERRET_INT_WIDTHS(FERRET_DECL_INT_CONVERSIONS)

#undef FERRET_DECL_SIGNED_INT_OPS
#undef FERRET_DECL_UNSIGNED_INT_OPS
#undef FERRET_DECL_INT_CONVERSIONS

// 128-bit floating point types
#ifdef FERRET_HAS_FLOAT128
    typedef __float128 ferret_f128;
#else
    // Fallback: struct-based 128-bit float (IEEE 754 binary128 format)
    // 1 sign bit + 15 exponent bits + 112 mantissa bits = 128 bits
    typedef struct {
        uint64_t mantissa_lo;  // Lower 64 bits of mantissa
        uint64_t mantissa_hi;  // Upper 48 bits of mantissa + 15 exponent bits + 1 sign bit
    } ferret_f128;
#endif

// 256-bit floating point type (always struct-based)
// IEEE 754 extended format: 1 sign + 19 exponent + 236 mantissa = 256 bits
typedef struct {
    uint64_t mantissa[3];  // 192 bits of mantissa (3 × 64)
    uint64_t exp_sign;      // 19 exponent bits + 1 sign bit + remaining mantissa bits
} ferret_f256;

// 128-bit floating point operations
#ifdef FERRET_HAS_FLOAT128
    // Native operations - compiler handles these
    #define ferret_f128_add(a, b) ((a) + (b))
    #define ferret_f128_sub(a, b) ((a) - (b))
    #define ferret_f128_mul(a, b) ((a) * (b))
    #define ferret_f128_div(a, b) ((a) / (b))
    #define ferret_f128_eq(a, b) ((a) == (b))
    #define ferret_f128_lt(a, b) ((a) < (b))
    #define ferret_f128_gt(a, b) ((a) > (b))
#else
    // Struct-based operations - implemented in bigint.c
    ferret_f128 ferret_f128_add(ferret_f128 a, ferret_f128 b);
    ferret_f128 ferret_f128_sub(ferret_f128 a, ferret_f128 b);
    ferret_f128 ferret_f128_mul(ferret_f128 a, ferret_f128 b);
    ferret_f128 ferret_f128_div(ferret_f128 a, ferret_f128 b);
    bool ferret_f128_eq(ferret_f128 a, ferret_f128 b);
    bool ferret_f128_lt(ferret_f128 a, ferret_f128 b);
    bool ferret_f128_gt(ferret_f128 a, ferret_f128 b);
#endif

// 256-bit floating point operations (always implemented in bigint.c)
ferret_f256 ferret_f256_add(ferret_f256 a, ferret_f256 b);
ferret_f256 ferret_f256_sub(ferret_f256 a, ferret_f256 b);
ferret_f256 ferret_f256_mul(ferret_f256 a, ferret_f256 b);
ferret_f256 ferret_f256_div(ferret_f256 a, ferret_f256 b);
bool ferret_f256_eq(ferret_f256 a, ferret_f256 b);
bool ferret_f256_lt(ferret_f256 a, ferret_f256 b);
bool ferret_f256_gt(ferret_f256 a, ferret_f256 b);

// Conversion functions for floating point
ferret_f128 ferret_f128_from_f64(double val);
ferret_f256 ferret_f256_from_f64(double val);
double ferret_f128_to_f64(ferret_f128 val);
double ferret_f256_to_f64(ferret_f256 val);

// Bitwise operations for integers
#define FERRET_DECL_SIGNED_INT_BITS(BITS) \
    ferret_i##BITS ferret_i##BITS##_and(ferret_i##BITS a, ferret_i##BITS b); \
    ferret_i##BITS ferret_i##BITS##_or(ferret_i##BITS a, ferret_i##BITS b); \
    ferret_i##BITS ferret_i##BITS##_xor(ferret_i##BITS a, ferret_i##BITS b); \
    ferret_i##BITS ferret_i##BITS##_not(ferret_i##BITS a); \
    ferret_i##BITS ferret_i##BITS##_shl(ferret_i##BITS a, int n); \
    ferret_i##BITS ferret_i##BITS##_shr(ferret_i##BITS a, int n);

#define FERRET_DECL_UNSIGNED_INT_BITS(BITS) \
    ferret_u##BITS ferret_u##BITS##_and(ferret_u##BITS a, ferret_u##BITS b); \
    ferret_u##BITS ferret_u##BITS##_or(ferret_u##BITS a, ferret_u##BITS b); \
    ferret_u##BITS ferret_u##BITS##_xor(ferret_u##BITS a, ferret_u##BITS b); \
    ferret_u##BITS ferret_u##BITS##_not(ferret_u##BITS a); \
    ferret_u##BITS ferret_u##BITS##_shl(ferret_u##BITS a, int n); \
    ferret_u##BITS ferret_u##BITS##_shr(ferret_u##BITS a, int n);

FERRET_INT_WIDTHS(FERRET_DECL_SIGNED_INT_BITS)
FERRET_INT_WIDTHS(FERRET_DECL_UNSIGNED_INT_BITS)

#undef FERRET_DECL_SIGNED_INT_BITS
#undef FERRET_DECL_UNSIGNED_INT_BITS

// String conversion functions
#define FERRET_DECL_INT_STRINGS(BITS) \
    char* ferret_i##BITS##_to_string(ferret_i##BITS val); \
    char* ferret_u##BITS##_to_string(ferret_u##BITS val); \
    ferret_i##BITS ferret_i##BITS##_from_string(const char* str); \
    ferret_u##BITS ferret_u##BITS##_from_string(const char* str);

FERRET_INT_WIDTHS(FERRET_DECL_INT_STRINGS)
#undef FERRET_DECL_INT_STRINGS

char* ferret_f128_to_string(ferret_f128 val);
char* ferret_f256_to_string(ferret_f256 val);

ferret_f128 ferret_f128_from_string(const char* str);
ferret_f256 ferret_f256_from_string(const char* str);

// Pointer-based helpers (for MIR/QBE lowering)
void ferret_memcpy(void* dst, const void* src, uint64_t size);

#define FERRET_DECL_INT_PTR_OPS(BITS) \
    void ferret_i##BITS##_add_ptr(const ferret_i##BITS* a, const ferret_i##BITS* b, ferret_i##BITS* out); \
    void ferret_i##BITS##_sub_ptr(const ferret_i##BITS* a, const ferret_i##BITS* b, ferret_i##BITS* out); \
    void ferret_i##BITS##_mul_ptr(const ferret_i##BITS* a, const ferret_i##BITS* b, ferret_i##BITS* out); \
    void ferret_i##BITS##_div_ptr(const ferret_i##BITS* a, const ferret_i##BITS* b, ferret_i##BITS* out); \
    void ferret_i##BITS##_mod_ptr(const ferret_i##BITS* a, const ferret_i##BITS* b, ferret_i##BITS* out); \
    bool ferret_i##BITS##_eq_ptr(const ferret_i##BITS* a, const ferret_i##BITS* b); \
    bool ferret_i##BITS##_lt_ptr(const ferret_i##BITS* a, const ferret_i##BITS* b); \
    bool ferret_i##BITS##_gt_ptr(const ferret_i##BITS* a, const ferret_i##BITS* b); \
    void ferret_i##BITS##_and_ptr(const ferret_i##BITS* a, const ferret_i##BITS* b, ferret_i##BITS* out); \
    void ferret_i##BITS##_or_ptr(const ferret_i##BITS* a, const ferret_i##BITS* b, ferret_i##BITS* out); \
    void ferret_i##BITS##_xor_ptr(const ferret_i##BITS* a, const ferret_i##BITS* b, ferret_i##BITS* out); \
    void ferret_i##BITS##_not_ptr(const ferret_i##BITS* a, ferret_i##BITS* out); \
    void ferret_i##BITS##_pow_ptr(const ferret_i##BITS* base, const ferret_i##BITS* exp, ferret_i##BITS* out); \
    void ferret_u##BITS##_add_ptr(const ferret_u##BITS* a, const ferret_u##BITS* b, ferret_u##BITS* out); \
    void ferret_u##BITS##_sub_ptr(const ferret_u##BITS* a, const ferret_u##BITS* b, ferret_u##BITS* out); \
    void ferret_u##BITS##_mul_ptr(const ferret_u##BITS* a, const ferret_u##BITS* b, ferret_u##BITS* out); \
    void ferret_u##BITS##_div_ptr(const ferret_u##BITS* a, const ferret_u##BITS* b, ferret_u##BITS* out); \
    void ferret_u##BITS##_mod_ptr(const ferret_u##BITS* a, const ferret_u##BITS* b, ferret_u##BITS* out); \
    bool ferret_u##BITS##_eq_ptr(const ferret_u##BITS* a, const ferret_u##BITS* b); \
    bool ferret_u##BITS##_lt_ptr(const ferret_u##BITS* a, const ferret_u##BITS* b); \
    bool ferret_u##BITS##_gt_ptr(const ferret_u##BITS* a, const ferret_u##BITS* b); \
    void ferret_u##BITS##_and_ptr(const ferret_u##BITS* a, const ferret_u##BITS* b, ferret_u##BITS* out); \
    void ferret_u##BITS##_or_ptr(const ferret_u##BITS* a, const ferret_u##BITS* b, ferret_u##BITS* out); \
    void ferret_u##BITS##_xor_ptr(const ferret_u##BITS* a, const ferret_u##BITS* b, ferret_u##BITS* out); \
    void ferret_u##BITS##_not_ptr(const ferret_u##BITS* a, ferret_u##BITS* out); \
    void ferret_u##BITS##_pow_ptr(const ferret_u##BITS* base, const ferret_u##BITS* exp, ferret_u##BITS* out);

FERRET_INT_WIDTHS(FERRET_DECL_INT_PTR_OPS)
#undef FERRET_DECL_INT_PTR_OPS

void ferret_f128_add_ptr(const ferret_f128* a, const ferret_f128* b, ferret_f128* out);
void ferret_f128_sub_ptr(const ferret_f128* a, const ferret_f128* b, ferret_f128* out);
void ferret_f128_mul_ptr(const ferret_f128* a, const ferret_f128* b, ferret_f128* out);
void ferret_f128_div_ptr(const ferret_f128* a, const ferret_f128* b, ferret_f128* out);
bool ferret_f128_eq_ptr(const ferret_f128* a, const ferret_f128* b);
bool ferret_f128_lt_ptr(const ferret_f128* a, const ferret_f128* b);
bool ferret_f128_gt_ptr(const ferret_f128* a, const ferret_f128* b);
void ferret_f128_pow_ptr(const ferret_f128* base, const ferret_f128* exp, ferret_f128* out);

void ferret_f256_add_ptr(const ferret_f256* a, const ferret_f256* b, ferret_f256* out);
void ferret_f256_sub_ptr(const ferret_f256* a, const ferret_f256* b, ferret_f256* out);
void ferret_f256_mul_ptr(const ferret_f256* a, const ferret_f256* b, ferret_f256* out);
void ferret_f256_div_ptr(const ferret_f256* a, const ferret_f256* b, ferret_f256* out);
bool ferret_f256_eq_ptr(const ferret_f256* a, const ferret_f256* b);
bool ferret_f256_lt_ptr(const ferret_f256* a, const ferret_f256* b);
bool ferret_f256_gt_ptr(const ferret_f256* a, const ferret_f256* b);
void ferret_f256_pow_ptr(const ferret_f256* base, const ferret_f256* exp, ferret_f256* out);

#define FERRET_DECL_INT_PTR_CONV(BITS) \
    void ferret_i##BITS##_from_i64_ptr(int64_t val, ferret_i##BITS* out); \
    void ferret_u##BITS##_from_u64_ptr(uint64_t val, ferret_u##BITS* out); \
    int64_t ferret_i##BITS##_to_i64_ptr(const ferret_i##BITS* val); \
    uint64_t ferret_u##BITS##_to_u64_ptr(const ferret_u##BITS* val); \
    char* ferret_i##BITS##_to_string_ptr(const ferret_i##BITS* val); \
    char* ferret_u##BITS##_to_string_ptr(const ferret_u##BITS* val); \
    void ferret_i##BITS##_from_string_ptr(const char* str, ferret_i##BITS* out); \
    void ferret_u##BITS##_from_string_ptr(const char* str, ferret_u##BITS* out);

FERRET_INT_WIDTHS(FERRET_DECL_INT_PTR_CONV)
#undef FERRET_DECL_INT_PTR_CONV

void ferret_f128_from_f64_ptr(double val, ferret_f128* out);
void ferret_f256_from_f64_ptr(double val, ferret_f256* out);

double ferret_f128_to_f64_ptr(const ferret_f128* val);
double ferret_f256_to_f64_ptr(const ferret_f256* val);

char* ferret_f128_to_string_ptr(const ferret_f128* val);
char* ferret_f256_to_string_ptr(const ferret_f256* val);

void ferret_f128_from_string_ptr(const char* str, ferret_f128* out);
void ferret_f256_from_string_ptr(const char* str, ferret_f256* out);

#endif // FERRET_BIGINT_H
