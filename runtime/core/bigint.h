#ifndef FERRET_BIGINT_H
#define FERRET_BIGINT_H

#include "abi_constants.h"

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

#define FERRET_LIMBS_FOR_BITS(BITS) (((BITS) + FERRET_LIMB_BITS - 1) / FERRET_LIMB_BITS)
#define FERRET_LIMB_MAX ((ferret_limb_t)~(ferret_limb_t)0)

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

// Fixed-width floating point types (word-based)
#define FERRET_DECLARE_FLOAT_TYPES(BITS, WORDS, FRAC_BITS, EXP_BITS, EXP_BIAS, DEC_DIG) \
    enum { FERRET_F##BITS##_WORDS = WORDS }; \
    typedef struct { \
        uint64_t words[FERRET_F##BITS##_WORDS]; \
    } ferret_f##BITS;

FERRET_FLOAT_SPECS(FERRET_DECLARE_FLOAT_TYPES)
#undef FERRET_DECLARE_FLOAT_TYPES

// Floating point operations
#define FERRET_DECL_FLOAT_OPS(BITS, WORDS, FRAC_BITS, EXP_BITS, EXP_BIAS, DEC_DIG) \
    ferret_f##BITS ferret_f##BITS##_add(ferret_f##BITS a, ferret_f##BITS b); \
    ferret_f##BITS ferret_f##BITS##_sub(ferret_f##BITS a, ferret_f##BITS b); \
    ferret_f##BITS ferret_f##BITS##_mul(ferret_f##BITS a, ferret_f##BITS b); \
    ferret_f##BITS ferret_f##BITS##_div(ferret_f##BITS a, ferret_f##BITS b); \
    bool ferret_f##BITS##_eq(ferret_f##BITS a, ferret_f##BITS b); \
    bool ferret_f##BITS##_lt(ferret_f##BITS a, ferret_f##BITS b); \
    bool ferret_f##BITS##_gt(ferret_f##BITS a, ferret_f##BITS b);

// Conversion functions for floating point
#define FERRET_DECL_FLOAT_CONV(BITS, WORDS, FRAC_BITS, EXP_BITS, EXP_BIAS, DEC_DIG) \
    ferret_f##BITS ferret_f##BITS##_from_f64(double val); \
    double ferret_f##BITS##_to_f64(ferret_f##BITS val); \
    char* ferret_f##BITS##_to_string(ferret_f##BITS val); \
    ferret_f##BITS ferret_f##BITS##_from_string(const char* str);

FERRET_FLOAT_SPECS(FERRET_DECL_FLOAT_OPS)
FERRET_FLOAT_SPECS(FERRET_DECL_FLOAT_CONV)

#undef FERRET_DECL_FLOAT_OPS
#undef FERRET_DECL_FLOAT_CONV
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
#define FERRET_DECL_FLOAT_PTR_OPS(BITS, WORDS, FRAC_BITS, EXP_BITS, EXP_BIAS, DEC_DIG) \
    void ferret_f##BITS##_add_ptr(const ferret_f##BITS* a, const ferret_f##BITS* b, ferret_f##BITS* out); \
    void ferret_f##BITS##_sub_ptr(const ferret_f##BITS* a, const ferret_f##BITS* b, ferret_f##BITS* out); \
    void ferret_f##BITS##_mul_ptr(const ferret_f##BITS* a, const ferret_f##BITS* b, ferret_f##BITS* out); \
    void ferret_f##BITS##_div_ptr(const ferret_f##BITS* a, const ferret_f##BITS* b, ferret_f##BITS* out); \
    bool ferret_f##BITS##_eq_ptr(const ferret_f##BITS* a, const ferret_f##BITS* b); \
    bool ferret_f##BITS##_lt_ptr(const ferret_f##BITS* a, const ferret_f##BITS* b); \
    bool ferret_f##BITS##_gt_ptr(const ferret_f##BITS* a, const ferret_f##BITS* b); \
    void ferret_f##BITS##_pow_ptr(const ferret_f##BITS* base, const ferret_f##BITS* exp, ferret_f##BITS* out);

FERRET_FLOAT_SPECS(FERRET_DECL_FLOAT_PTR_OPS)
#undef FERRET_DECL_FLOAT_PTR_OPS
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
#define FERRET_DECL_FLOAT_PTR_CONV(BITS, WORDS, FRAC_BITS, EXP_BITS, EXP_BIAS, DEC_DIG) \
    void ferret_f##BITS##_from_f64_ptr(double val, ferret_f##BITS* out); \
    double ferret_f##BITS##_to_f64_ptr(const ferret_f##BITS* val); \
    char* ferret_f##BITS##_to_string_ptr(const ferret_f##BITS* val); \
    void ferret_f##BITS##_from_string_ptr(const char* str, ferret_f##BITS* out);

FERRET_FLOAT_SPECS(FERRET_DECL_FLOAT_PTR_CONV)
#undef FERRET_DECL_FLOAT_PTR_CONV
#endif // FERRET_BIGINT_H
