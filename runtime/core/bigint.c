#define _POSIX_C_SOURCE 200809L
#include "bigint.h"
#include <string.h>
#include <stdlib.h>
#include <stdio.h>
#include <ctype.h>

#if FERRET_LIMB_BITS == 64
#if defined(FERRET_HAS_WIDE128)
typedef __uint128_t ferret_wide_t;
#else
#error "64-bit limbs require 128-bit wide type"
#endif
#elif FERRET_LIMB_BITS == 32
typedef uint64_t ferret_wide_t;
#else
#error "Unsupported limb size"
#endif

static void ferret_zero_limbs(ferret_limb_t* v, int n) {
    memset(v, 0, (size_t)n * sizeof(*v));
}

static void ferret_copy_limbs(ferret_limb_t* dst, const ferret_limb_t* src, int n) {
    memcpy(dst, src, (size_t)n * sizeof(*dst));
}

static bool ferret_is_zero_limbs(const ferret_limb_t* v, int n) {
    for (int i = 0; i < n; i++) {
        if (v[i] != 0) {
            return false;
        }
    }
    return true;
}

static bool ferret_is_negative_limbs(const ferret_limb_t* v, int n) {
    return ((v[n - 1] >> (FERRET_LIMB_BITS - 1)) & 1) != 0;
}

static int ferret_cmp_u_limbs(const ferret_limb_t* a, const ferret_limb_t* b, int n) {
    for (int i = n - 1; i >= 0; i--) {
        if (a[i] < b[i]) return -1;
        if (a[i] > b[i]) return 1;
    }
    return 0;
}

static int ferret_cmp_s_limbs(const ferret_limb_t* a, const ferret_limb_t* b, int n) {
    bool neg_a = ferret_is_negative_limbs(a, n);
    bool neg_b = ferret_is_negative_limbs(b, n);
    if (neg_a != neg_b) {
        return neg_a ? -1 : 1;
    }
    return ferret_cmp_u_limbs(a, b, n);
}

static void ferret_add_limbs(const ferret_limb_t* a, const ferret_limb_t* b, ferret_limb_t* out, int n) {
    ferret_wide_t carry = 0;
    for (int i = 0; i < n; i++) {
        ferret_wide_t sum = (ferret_wide_t)a[i] + (ferret_wide_t)b[i] + carry;
        out[i] = (ferret_limb_t)sum;
        carry = sum >> FERRET_LIMB_BITS;
    }
}

static void ferret_sub_limbs(const ferret_limb_t* a, const ferret_limb_t* b, ferret_limb_t* out, int n) {
    ferret_limb_t borrow = 0;
    for (int i = 0; i < n; i++) {
        ferret_limb_t bi = b[i] + borrow;
        borrow = (a[i] < bi) ? 1 : 0;
        out[i] = a[i] - bi;
    }
}

static void ferret_negate_limbs(ferret_limb_t* v, int n) {
    ferret_limb_t carry = 1;
    for (int i = 0; i < n; i++) {
        ferret_limb_t inv = (ferret_limb_t)~v[i];
        ferret_limb_t sum = (ferret_limb_t)(inv + carry);
        v[i] = sum;
        carry = (sum < inv) ? 1 : 0;
    }
}

static void ferret_abs_limbs(const ferret_limb_t* in, ferret_limb_t* out, int n, bool* neg) {
    ferret_copy_limbs(out, in, n);
    bool is_neg = ferret_is_negative_limbs(in, n);
    if (neg != NULL) {
        *neg = is_neg;
    }
    if (is_neg) {
        ferret_negate_limbs(out, n);
    }
}

static void ferret_mul_limbs(const ferret_limb_t* a, const ferret_limb_t* b, ferret_limb_t* out, int n) {
    ferret_zero_limbs(out, n);
    for (int i = 0; i < n; i++) {
        ferret_wide_t carry = 0;
        for (int j = 0; j < n - i; j++) {
            ferret_wide_t prod = (ferret_wide_t)a[i] * (ferret_wide_t)b[j];
            ferret_wide_t sum = prod + (ferret_wide_t)out[i + j] + carry;
            out[i + j] = (ferret_limb_t)sum;
            carry = sum >> FERRET_LIMB_BITS;
        }
    }
}

static void ferret_shift_left_limbs(const ferret_limb_t* a, int n, int shift, ferret_limb_t* out) {
    if (shift <= 0) {
        ferret_copy_limbs(out, a, n);
        return;
    }
    int total_bits = n * FERRET_LIMB_BITS;
    if (shift >= total_bits) {
        ferret_zero_limbs(out, n);
        return;
    }

    int word_shift = shift / FERRET_LIMB_BITS;
    int bit_shift = shift % FERRET_LIMB_BITS;

    for (int i = n - 1; i >= 0; i--) {
        int src = i - word_shift;
        if (src < 0) {
            out[i] = 0;
            continue;
        }
        ferret_limb_t val = (ferret_limb_t)(a[src] << bit_shift);
        if (bit_shift != 0 && src > 0) {
            val |= (ferret_limb_t)(a[src - 1] >> (FERRET_LIMB_BITS - bit_shift));
        }
        out[i] = val;
    }
}

static void ferret_shift_right_limbs(const ferret_limb_t* a, int n, int shift, ferret_limb_t* out) {
    if (shift <= 0) {
        ferret_copy_limbs(out, a, n);
        return;
    }
    int total_bits = n * FERRET_LIMB_BITS;
    if (shift >= total_bits) {
        ferret_zero_limbs(out, n);
        return;
    }

    int word_shift = shift / FERRET_LIMB_BITS;
    int bit_shift = shift % FERRET_LIMB_BITS;

    for (int i = 0; i < n; i++) {
        int src = i + word_shift;
        if (src >= n) {
            out[i] = 0;
            continue;
        }
        ferret_limb_t val = (ferret_limb_t)(a[src] >> bit_shift);
        if (bit_shift != 0 && src + 1 < n) {
            val |= (ferret_limb_t)(a[src + 1] << (FERRET_LIMB_BITS - bit_shift));
        }
        out[i] = val;
    }
}

static void ferret_shift_right_signed_limbs(const ferret_limb_t* a, int n, int shift, ferret_limb_t* out) {
    ferret_shift_right_limbs(a, n, shift, out);
    if (!ferret_is_negative_limbs(a, n)) {
        return;
    }
    int total_bits = n * FERRET_LIMB_BITS;
    if (shift >= total_bits) {
        for (int i = 0; i < n; i++) {
            out[i] = FERRET_LIMB_MAX;
        }
        return;
    }

    int word_shift = shift / FERRET_LIMB_BITS;
    int bit_shift = shift % FERRET_LIMB_BITS;

    for (int i = n - 1; i >= n - word_shift; i--) {
        out[i] = FERRET_LIMB_MAX;
    }
    if (bit_shift != 0) {
        int idx = n - 1 - word_shift;
        out[idx] |= (ferret_limb_t)(FERRET_LIMB_MAX << (FERRET_LIMB_BITS - bit_shift));
    }
}

static int ferret_get_bit_limbs(const ferret_limb_t* v, int bit) {
    int word_idx = bit / FERRET_LIMB_BITS;
    int bit_idx = bit % FERRET_LIMB_BITS;
    return (int)((v[word_idx] >> bit_idx) & 1);
}

static void ferret_set_bit_limbs(ferret_limb_t* v, int bit) {
    int word_idx = bit / FERRET_LIMB_BITS;
    int bit_idx = bit % FERRET_LIMB_BITS;
    v[word_idx] |= (ferret_limb_t)1 << bit_idx;
}

static bool ferret_div_mod_u_limbs(const ferret_limb_t* numer, const ferret_limb_t* denom, ferret_limb_t* quot, ferret_limb_t* rem, int n) {
    if (ferret_is_zero_limbs(denom, n)) {
        ferret_zero_limbs(quot, n);
        ferret_zero_limbs(rem, n);
        return false;
    }

    ferret_zero_limbs(quot, n);
    ferret_zero_limbs(rem, n);

    int total_bits = n * FERRET_LIMB_BITS;
    for (int bit = total_bits - 1; bit >= 0; bit--) {
        ferret_limb_t carry = 0;
        for (int i = 0; i < n; i++) {
            ferret_limb_t new_carry = (ferret_limb_t)(rem[i] >> (FERRET_LIMB_BITS - 1));
            rem[i] = (ferret_limb_t)((rem[i] << 1) | carry);
            carry = new_carry;
        }
        if (ferret_get_bit_limbs(numer, bit)) {
            rem[0] |= 1;
        }
        if (ferret_cmp_u_limbs(rem, denom, n) >= 0) {
            ferret_sub_limbs(rem, denom, rem, n);
            ferret_set_bit_limbs(quot, bit);
        }
    }

    return true;
}

static int ferret_digit_value(int c) {
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return 10 + (c - 'a');
    if (c >= 'A' && c <= 'F') return 10 + (c - 'A');
    return -1;
}

static const char* ferret_skip_space(const char* s) {
    while (s && *s && isspace((unsigned char)*s)) {
        s++;
    }
    return s;
}

static int ferret_parse_base(const char** s) {
    if (!s || !*s) return 10;
    if ((*s)[0] == '0' && (*s)[1]) {
        char next = (*s)[1];
        if (next == 'x' || next == 'X') {
            *s += 2;
            return 16;
        }
        if (next == 'o' || next == 'O') {
            *s += 2;
            return 8;
        }
        if (next == 'b' || next == 'B') {
            *s += 2;
            return 2;
        }
    }
    return 10;
}

static void ferret_mul_add_small(ferret_limb_t* v, int n, uint32_t base, uint32_t digit) {
    ferret_wide_t carry = digit;
    for (int i = 0; i < n; i++) {
        ferret_wide_t prod = (ferret_wide_t)v[i] * (ferret_wide_t)base + carry;
        v[i] = (ferret_limb_t)prod;
        carry = prod >> FERRET_LIMB_BITS;
    }
}

static bool ferret_parse_uint(const char* str, bool allow_sign, ferret_limb_t* out, int n, bool* neg_out) {
    if (!out) return false;
    ferret_zero_limbs(out, n);

    if (!str) return false;
    const char* s = ferret_skip_space(str);
    bool neg = false;
    if (*s == '+' || *s == '-') {
        neg = (*s == '-');
        s++;
    }
    if (neg && !allow_sign) {
        if (neg_out) *neg_out = false;
        return false;
    }

    int base = ferret_parse_base(&s);
    bool any = false;
    for (; *s; s++) {
        if (*s == '_') {
            continue;
        }
        int digit = ferret_digit_value(*s);
        if (digit < 0 || digit >= base) {
            break;
        }
        ferret_mul_add_small(out, n, (uint32_t)base, (uint32_t)digit);
        any = true;
    }

    if (neg_out) *neg_out = neg;
    return any;
}

static void ferret_limbs_from_u64(ferret_limb_t* out, int n, uint64_t val) {
    ferret_zero_limbs(out, n);
#if FERRET_LIMB_BITS == 64
    if (n > 0) {
        out[0] = (ferret_limb_t)val;
    }
#else
    uint64_t tmp = val;
    for (int i = 0; i < n; i++) {
        out[i] = (ferret_limb_t)(tmp & (uint64_t)FERRET_LIMB_MAX);
        tmp >>= FERRET_LIMB_BITS;
    }
#endif
}

static void ferret_limbs_from_i64(ferret_limb_t* out, int n, int64_t val) {
    ferret_limbs_from_u64(out, n, (uint64_t)val);
    if (val < 0) {
        int filled = (64 + FERRET_LIMB_BITS - 1) / FERRET_LIMB_BITS;
        for (int i = filled; i < n; i++) {
            out[i] = FERRET_LIMB_MAX;
        }
    }
}

static uint64_t ferret_limbs_to_u64(const ferret_limb_t* in, int n) {
    uint64_t val = 0;
    int limit = (64 + FERRET_LIMB_BITS - 1) / FERRET_LIMB_BITS;
    if (limit > n) limit = n;
    for (int i = 0; i < limit; i++) {
        val |= (uint64_t)in[i] << (i * FERRET_LIMB_BITS);
    }
    return val;
}

static uint32_t ferret_div_small_limbs(const ferret_limb_t* val, ferret_limb_t* out, int n, uint32_t divisor) {
    ferret_wide_t rem = 0;
    for (int i = n - 1; i >= 0; i--) {
        ferret_wide_t acc = (rem << FERRET_LIMB_BITS) | val[i];
        out[i] = (ferret_limb_t)(acc / divisor);
        rem = acc % divisor;
    }
    return (uint32_t)rem;
}

static char* ferret_limbs_to_decimal(const ferret_limb_t* val, int n) {
    if (ferret_is_zero_limbs(val, n)) {
        char* out = (char*)malloc(2);
        if (!out) return NULL;
        out[0] = '0';
        out[1] = '\0';
        return out;
    }

    int bits = n * FERRET_LIMB_BITS;
    int max_digits = (int)ceil(bits * 0.30102999566) + 1;
    char* digits = (char*)malloc((size_t)max_digits);
    ferret_limb_t* work = (ferret_limb_t*)malloc((size_t)n * sizeof(*work));
    ferret_limb_t* quot = (ferret_limb_t*)malloc((size_t)n * sizeof(*quot));
    if (!digits || !work || !quot) {
        free(digits);
        free(work);
        free(quot);
        return NULL;
    }

    int len = 0;
    ferret_zero_limbs(work, n);
    ferret_zero_limbs(quot, n);
    ferret_copy_limbs(work, val, n);

    while (!ferret_is_zero_limbs(work, n)) {
        uint32_t rem = ferret_div_small_limbs(work, quot, n, 10);
        digits[len++] = (char)('0' + rem);
        ferret_copy_limbs(work, quot, n);
    }

    char* out = (char*)malloc((size_t)len + 1);
    if (!out) {
        free(digits);
        free(work);
        free(quot);
        return NULL;
    }
    for (int i = 0; i < len; i++) {
        out[i] = digits[len - 1 - i];
    }
    out[len] = '\0';
    free(digits);
    free(work);
    free(quot);
    return out;
}

// Helper: check if lowest bit is set
static bool ferret_is_odd_limbs(const ferret_limb_t* v) {
    return (v[0] & 1) != 0;
}

// Helper: shift right by 1 bit
static void ferret_shr1_limbs(ferret_limb_t* v, int n) {
    for (int i = 0; i < n - 1; i++) {
        v[i] = (v[i] >> 1) | (v[i + 1] << (FERRET_LIMB_BITS - 1));
    }
    v[n - 1] >>= 1;
}

#define FERRET_DEFINE_SIGNED_INT_ARITH(BITS) \
    ferret_i##BITS ferret_i##BITS##_add(ferret_i##BITS a, ferret_i##BITS b) { \
        ferret_i##BITS result; \
        ferret_add_limbs(a.words, b.words, result.words, FERRET_U##BITS##_LIMBS); \
        return result; \
    } \
    ferret_i##BITS ferret_i##BITS##_sub(ferret_i##BITS a, ferret_i##BITS b) { \
        ferret_i##BITS result; \
        ferret_sub_limbs(a.words, b.words, result.words, FERRET_U##BITS##_LIMBS); \
        return result; \
    } \
    ferret_i##BITS ferret_i##BITS##_mul(ferret_i##BITS a, ferret_i##BITS b) { \
        bool neg_a = false; \
        bool neg_b = false; \
        ferret_u##BITS a_mag; \
        ferret_u##BITS b_mag; \
        ferret_abs_limbs(a.words, a_mag.words, FERRET_U##BITS##_LIMBS, &neg_a); \
        ferret_abs_limbs(b.words, b_mag.words, FERRET_U##BITS##_LIMBS, &neg_b); \
        ferret_u##BITS mag; \
        ferret_mul_limbs(a_mag.words, b_mag.words, mag.words, FERRET_U##BITS##_LIMBS); \
        ferret_i##BITS result; \
        ferret_copy_limbs(result.words, mag.words, FERRET_U##BITS##_LIMBS); \
        if (neg_a != neg_b) { \
            ferret_negate_limbs(result.words, FERRET_U##BITS##_LIMBS); \
        } \
        return result; \
    } \
    ferret_i##BITS ferret_i##BITS##_div(ferret_i##BITS a, ferret_i##BITS b) { \
        bool neg_a = false; \
        bool neg_b = false; \
        ferret_u##BITS a_mag; \
        ferret_u##BITS b_mag; \
        ferret_abs_limbs(a.words, a_mag.words, FERRET_U##BITS##_LIMBS, &neg_a); \
        ferret_abs_limbs(b.words, b_mag.words, FERRET_U##BITS##_LIMBS, &neg_b); \
        ferret_u##BITS quot; \
        ferret_u##BITS rem; \
        if (!ferret_div_mod_u_limbs(a_mag.words, b_mag.words, quot.words, rem.words, FERRET_U##BITS##_LIMBS)) { \
            ferret_zero_limbs(quot.words, FERRET_U##BITS##_LIMBS); \
        } \
        ferret_i##BITS result; \
        ferret_copy_limbs(result.words, quot.words, FERRET_U##BITS##_LIMBS); \
        if (neg_a != neg_b) { \
            ferret_negate_limbs(result.words, FERRET_U##BITS##_LIMBS); \
        } \
        return result; \
    } \
    ferret_i##BITS ferret_i##BITS##_mod(ferret_i##BITS a, ferret_i##BITS b) { \
        bool neg_a = false; \
        bool neg_b = false; \
        ferret_u##BITS a_mag; \
        ferret_u##BITS b_mag; \
        ferret_abs_limbs(a.words, a_mag.words, FERRET_U##BITS##_LIMBS, &neg_a); \
        ferret_abs_limbs(b.words, b_mag.words, FERRET_U##BITS##_LIMBS, &neg_b); \
        ferret_u##BITS quot; \
        ferret_u##BITS rem; \
        if (!ferret_div_mod_u_limbs(a_mag.words, b_mag.words, quot.words, rem.words, FERRET_U##BITS##_LIMBS)) { \
            ferret_zero_limbs(rem.words, FERRET_U##BITS##_LIMBS); \
        } \
        ferret_i##BITS result; \
        ferret_copy_limbs(result.words, rem.words, FERRET_U##BITS##_LIMBS); \
        if (neg_a) { \
            ferret_negate_limbs(result.words, FERRET_U##BITS##_LIMBS); \
        } \
        (void)neg_b; \
        return result; \
    } \
    bool ferret_i##BITS##_eq(ferret_i##BITS a, ferret_i##BITS b) { \
        return memcmp(a.words, b.words, sizeof(a.words)) == 0; \
    } \
    bool ferret_i##BITS##_lt(ferret_i##BITS a, ferret_i##BITS b) { \
        return ferret_cmp_s_limbs(a.words, b.words, FERRET_U##BITS##_LIMBS) < 0; \
    } \
    bool ferret_i##BITS##_gt(ferret_i##BITS a, ferret_i##BITS b) { \
        return ferret_cmp_s_limbs(a.words, b.words, FERRET_U##BITS##_LIMBS) > 0; \
    }

#define FERRET_DEFINE_UNSIGNED_INT_ARITH(BITS) \
    ferret_u##BITS ferret_u##BITS##_add(ferret_u##BITS a, ferret_u##BITS b) { \
        ferret_u##BITS result; \
        ferret_add_limbs(a.words, b.words, result.words, FERRET_U##BITS##_LIMBS); \
        return result; \
    } \
    ferret_u##BITS ferret_u##BITS##_sub(ferret_u##BITS a, ferret_u##BITS b) { \
        ferret_u##BITS result; \
        ferret_sub_limbs(a.words, b.words, result.words, FERRET_U##BITS##_LIMBS); \
        return result; \
    } \
    ferret_u##BITS ferret_u##BITS##_mul(ferret_u##BITS a, ferret_u##BITS b) { \
        ferret_u##BITS result; \
        ferret_mul_limbs(a.words, b.words, result.words, FERRET_U##BITS##_LIMBS); \
        return result; \
    } \
    ferret_u##BITS ferret_u##BITS##_div(ferret_u##BITS a, ferret_u##BITS b) { \
        ferret_u##BITS result; \
        ferret_u##BITS rem; \
        if (!ferret_div_mod_u_limbs(a.words, b.words, result.words, rem.words, FERRET_U##BITS##_LIMBS)) { \
            ferret_zero_limbs(result.words, FERRET_U##BITS##_LIMBS); \
        } \
        return result; \
    } \
    ferret_u##BITS ferret_u##BITS##_mod(ferret_u##BITS a, ferret_u##BITS b) { \
        ferret_u##BITS quot; \
        ferret_u##BITS rem; \
        if (!ferret_div_mod_u_limbs(a.words, b.words, quot.words, rem.words, FERRET_U##BITS##_LIMBS)) { \
            ferret_zero_limbs(rem.words, FERRET_U##BITS##_LIMBS); \
        } \
        return rem; \
    } \
    bool ferret_u##BITS##_eq(ferret_u##BITS a, ferret_u##BITS b) { \
        return memcmp(a.words, b.words, sizeof(a.words)) == 0; \
    } \
    bool ferret_u##BITS##_lt(ferret_u##BITS a, ferret_u##BITS b) { \
        return ferret_cmp_u_limbs(a.words, b.words, FERRET_U##BITS##_LIMBS) < 0; \
    } \
    bool ferret_u##BITS##_gt(ferret_u##BITS a, ferret_u##BITS b) { \
        return ferret_cmp_u_limbs(a.words, b.words, FERRET_U##BITS##_LIMBS) > 0; \
    }

#define FERRET_DEFINE_SIGNED_INT_POW(BITS) \
    ferret_i##BITS ferret_i##BITS##_pow(ferret_i##BITS base, ferret_i##BITS exp) { \
        ferret_i##BITS zero; \
        ferret_zero_limbs(zero.words, FERRET_U##BITS##_LIMBS); \
        if (ferret_cmp_s_limbs(exp.words, zero.words, FERRET_U##BITS##_LIMBS) < 0) { \
            return zero; \
        } \
        ferret_i##BITS result; \
        ferret_limbs_from_i64(result.words, FERRET_U##BITS##_LIMBS, 1); \
        ferret_i##BITS exp_copy = exp; \
        while (!ferret_is_zero_limbs(exp_copy.words, FERRET_U##BITS##_LIMBS)) { \
            if (ferret_is_odd_limbs(exp_copy.words)) { \
                result = ferret_i##BITS##_mul(result, base); \
            } \
            base = ferret_i##BITS##_mul(base, base); \
            ferret_shr1_limbs(exp_copy.words, FERRET_U##BITS##_LIMBS); \
        } \
        return result; \
    }

#define FERRET_DEFINE_UNSIGNED_INT_POW(BITS) \
    ferret_u##BITS ferret_u##BITS##_pow(ferret_u##BITS base, ferret_u##BITS exp) { \
        ferret_u##BITS result; \
        ferret_limbs_from_u64(result.words, FERRET_U##BITS##_LIMBS, 1); \
        ferret_u##BITS exp_copy = exp; \
        while (!ferret_is_zero_limbs(exp_copy.words, FERRET_U##BITS##_LIMBS)) { \
            if (ferret_is_odd_limbs(exp_copy.words)) { \
                result = ferret_u##BITS##_mul(result, base); \
            } \
            base = ferret_u##BITS##_mul(base, base); \
            ferret_shr1_limbs(exp_copy.words, FERRET_U##BITS##_LIMBS); \
        } \
        return result; \
    }

#define FERRET_DEFINE_SIGNED_INT_BITS(BITS) \
    ferret_i##BITS ferret_i##BITS##_and(ferret_i##BITS a, ferret_i##BITS b) { \
        ferret_i##BITS result; \
        for (int i = 0; i < FERRET_U##BITS##_LIMBS; i++) { \
            result.words[i] = (ferret_limb_t)(a.words[i] & b.words[i]); \
        } \
        return result; \
    } \
    ferret_i##BITS ferret_i##BITS##_or(ferret_i##BITS a, ferret_i##BITS b) { \
        ferret_i##BITS result; \
        for (int i = 0; i < FERRET_U##BITS##_LIMBS; i++) { \
            result.words[i] = (ferret_limb_t)(a.words[i] | b.words[i]); \
        } \
        return result; \
    } \
    ferret_i##BITS ferret_i##BITS##_xor(ferret_i##BITS a, ferret_i##BITS b) { \
        ferret_i##BITS result; \
        for (int i = 0; i < FERRET_U##BITS##_LIMBS; i++) { \
            result.words[i] = (ferret_limb_t)(a.words[i] ^ b.words[i]); \
        } \
        return result; \
    } \
    ferret_i##BITS ferret_i##BITS##_not(ferret_i##BITS a) { \
        ferret_i##BITS result; \
        for (int i = 0; i < FERRET_U##BITS##_LIMBS; i++) { \
            result.words[i] = (ferret_limb_t)~a.words[i]; \
        } \
        return result; \
    } \
    ferret_i##BITS ferret_i##BITS##_shl(ferret_i##BITS a, int n) { \
        ferret_i##BITS result; \
        ferret_shift_left_limbs(a.words, FERRET_U##BITS##_LIMBS, n, result.words); \
        return result; \
    } \
    ferret_i##BITS ferret_i##BITS##_shr(ferret_i##BITS a, int n) { \
        ferret_i##BITS result; \
        ferret_shift_right_signed_limbs(a.words, FERRET_U##BITS##_LIMBS, n, result.words); \
        return result; \
    }

#define FERRET_DEFINE_UNSIGNED_INT_BITS(BITS) \
    ferret_u##BITS ferret_u##BITS##_and(ferret_u##BITS a, ferret_u##BITS b) { \
        ferret_u##BITS result; \
        for (int i = 0; i < FERRET_U##BITS##_LIMBS; i++) { \
            result.words[i] = (ferret_limb_t)(a.words[i] & b.words[i]); \
        } \
        return result; \
    } \
    ferret_u##BITS ferret_u##BITS##_or(ferret_u##BITS a, ferret_u##BITS b) { \
        ferret_u##BITS result; \
        for (int i = 0; i < FERRET_U##BITS##_LIMBS; i++) { \
            result.words[i] = (ferret_limb_t)(a.words[i] | b.words[i]); \
        } \
        return result; \
    } \
    ferret_u##BITS ferret_u##BITS##_xor(ferret_u##BITS a, ferret_u##BITS b) { \
        ferret_u##BITS result; \
        for (int i = 0; i < FERRET_U##BITS##_LIMBS; i++) { \
            result.words[i] = (ferret_limb_t)(a.words[i] ^ b.words[i]); \
        } \
        return result; \
    } \
    ferret_u##BITS ferret_u##BITS##_not(ferret_u##BITS a) { \
        ferret_u##BITS result; \
        for (int i = 0; i < FERRET_U##BITS##_LIMBS; i++) { \
            result.words[i] = (ferret_limb_t)~a.words[i]; \
        } \
        return result; \
    } \
    ferret_u##BITS ferret_u##BITS##_shl(ferret_u##BITS a, int n) { \
        ferret_u##BITS result; \
        ferret_shift_left_limbs(a.words, FERRET_U##BITS##_LIMBS, n, result.words); \
        return result; \
    } \
    ferret_u##BITS ferret_u##BITS##_shr(ferret_u##BITS a, int n) { \
        ferret_u##BITS result; \
        ferret_shift_right_limbs(a.words, FERRET_U##BITS##_LIMBS, n, result.words); \
        return result; \
    }

#define FERRET_DEFINE_INT_CONVERSIONS(BITS) \
    ferret_i##BITS ferret_i##BITS##_from_i64(int64_t val) { \
        ferret_i##BITS result; \
        ferret_limbs_from_i64(result.words, FERRET_U##BITS##_LIMBS, val); \
        return result; \
    } \
    ferret_u##BITS ferret_u##BITS##_from_u64(uint64_t val) { \
        ferret_u##BITS result; \
        ferret_limbs_from_u64(result.words, FERRET_U##BITS##_LIMBS, val); \
        return result; \
    } \
    int64_t ferret_i##BITS##_to_i64(ferret_i##BITS val) { \
        return (int64_t)ferret_limbs_to_u64(val.words, FERRET_U##BITS##_LIMBS); \
    } \
    uint64_t ferret_u##BITS##_to_u64(ferret_u##BITS val) { \
        return ferret_limbs_to_u64(val.words, FERRET_U##BITS##_LIMBS); \
    }

#define FERRET_DEFINE_INT_STRINGS(BITS) \
    char* ferret_u##BITS##_to_string(ferret_u##BITS val) { \
        return ferret_limbs_to_decimal(val.words, FERRET_U##BITS##_LIMBS); \
    } \
    char* ferret_i##BITS##_to_string(ferret_i##BITS val) { \
        bool neg = ferret_is_negative_limbs(val.words, FERRET_U##BITS##_LIMBS); \
        if (!neg) { \
            return ferret_limbs_to_decimal(val.words, FERRET_U##BITS##_LIMBS); \
        } \
        ferret_u##BITS mag; \
        ferret_abs_limbs(val.words, mag.words, FERRET_U##BITS##_LIMBS, NULL); \
        char* digits = ferret_limbs_to_decimal(mag.words, FERRET_U##BITS##_LIMBS); \
        if (!digits) return NULL; \
        size_t len = strlen(digits); \
        char* out = (char*)malloc(len + 2); \
        if (!out) { \
            free(digits); \
            return NULL; \
        } \
        out[0] = '-'; \
        memcpy(out + 1, digits, len + 1); \
        free(digits); \
        return out; \
    } \
    ferret_i##BITS ferret_i##BITS##_from_string(const char* str) { \
        ferret_i##BITS out; \
        bool neg = false; \
        if (!ferret_parse_uint(str, true, out.words, FERRET_U##BITS##_LIMBS, &neg)) { \
            ferret_zero_limbs(out.words, FERRET_U##BITS##_LIMBS); \
            return out; \
        } \
        if (neg) { \
            ferret_negate_limbs(out.words, FERRET_U##BITS##_LIMBS); \
        } \
        return out; \
    } \
    ferret_u##BITS ferret_u##BITS##_from_string(const char* str) { \
        ferret_u##BITS out; \
        bool neg = false; \
        if (!ferret_parse_uint(str, false, out.words, FERRET_U##BITS##_LIMBS, &neg)) { \
            ferret_zero_limbs(out.words, FERRET_U##BITS##_LIMBS); \
            return out; \
        } \
        return out; \
    }

FERRET_INT_WIDTHS(FERRET_DEFINE_SIGNED_INT_ARITH)
FERRET_INT_WIDTHS(FERRET_DEFINE_UNSIGNED_INT_ARITH)
FERRET_INT_WIDTHS(FERRET_DEFINE_SIGNED_INT_POW)
FERRET_INT_WIDTHS(FERRET_DEFINE_UNSIGNED_INT_POW)
FERRET_INT_WIDTHS(FERRET_DEFINE_SIGNED_INT_BITS)
FERRET_INT_WIDTHS(FERRET_DEFINE_UNSIGNED_INT_BITS)
FERRET_INT_WIDTHS(FERRET_DEFINE_INT_CONVERSIONS)
FERRET_INT_WIDTHS(FERRET_DEFINE_INT_STRINGS)

#undef FERRET_DEFINE_SIGNED_INT_ARITH
#undef FERRET_DEFINE_UNSIGNED_INT_ARITH
#undef FERRET_DEFINE_SIGNED_INT_POW
#undef FERRET_DEFINE_UNSIGNED_INT_POW
#undef FERRET_DEFINE_SIGNED_INT_BITS
#undef FERRET_DEFINE_UNSIGNED_INT_BITS
#undef FERRET_DEFINE_INT_CONVERSIONS
#undef FERRET_DEFINE_INT_STRINGS

// Soft float helpers.
typedef enum {
    SOFT_CLASS_ZERO = 0,
    SOFT_CLASS_NORMAL = 1,
    SOFT_CLASS_INF = 2,
    SOFT_CLASS_NAN = 3
} soft_class_t;

#define SOFT_WORD_BITS 64
#define SOFT_EXTRA_BITS FERRET_SOFT_EXTRA_BITS

static int soft_clz64(uint64_t v) {
#if defined(__GNUC__)
    if (v == 0) {
        return 64;
    }
    return __builtin_clzll(v);
#else
    int count = 0;
    uint64_t bit = (uint64_t)1 << 63;
    while (bit && (v & bit) == 0) {
        count++;
        bit >>= 1;
    }
    return count;
#endif
}

static void soft_u64_zero(uint64_t* v, int n) {
    memset(v, 0, (size_t)n * sizeof(*v));
}

static void soft_u64_copy(uint64_t* dst, const uint64_t* src, int n) {
    memcpy(dst, src, (size_t)n * sizeof(*dst));
}

static bool soft_u64_is_zero(const uint64_t* v, int n) {
    for (int i = 0; i < n; i++) {
        if (v[i] != 0) {
            return false;
        }
    }
    return true;
}

static int soft_u64_cmp(const uint64_t* a, const uint64_t* b, int n) {
    for (int i = n - 1; i >= 0; i--) {
        if (a[i] < b[i]) return -1;
        if (a[i] > b[i]) return 1;
    }
    return 0;
}

static uint64_t soft_u64_add(const uint64_t* a, const uint64_t* b, uint64_t* out, int n) {
    uint64_t carry = 0;
    for (int i = 0; i < n; i++) {
        __uint128_t sum = (__uint128_t)a[i] + (__uint128_t)b[i] + carry;
        out[i] = (uint64_t)sum;
        carry = (uint64_t)(sum >> 64);
    }
    return carry;
}

static uint64_t soft_u64_add_small(uint64_t* v, int n, uint64_t add) {
    __uint128_t sum = (__uint128_t)v[0] + add;
    v[0] = (uint64_t)sum;
    uint64_t carry = (uint64_t)(sum >> 64);
    for (int i = 1; i < n && carry; i++) {
        sum = (__uint128_t)v[i] + carry;
        v[i] = (uint64_t)sum;
        carry = (uint64_t)(sum >> 64);
    }
    return carry;
}

static uint64_t soft_u64_sub(const uint64_t* a, const uint64_t* b, uint64_t* out, int n) {
    uint64_t borrow = 0;
    for (int i = 0; i < n; i++) {
        __int128_t diff = (__int128_t)a[i] - (__int128_t)b[i] - (int64_t)borrow;
        out[i] = (uint64_t)diff;
        borrow = (diff < 0) ? 1 : 0;
    }
    return borrow;
}

static void soft_u64_shift_left(const uint64_t* a, int n, int shift, uint64_t* out) {
    if (shift <= 0) {
        soft_u64_copy(out, a, n);
        return;
    }
    int total_bits = n * SOFT_WORD_BITS;
    if (shift >= total_bits) {
        soft_u64_zero(out, n);
        return;
    }
    int word_shift = shift / SOFT_WORD_BITS;
    int bit_shift = shift % SOFT_WORD_BITS;
    for (int i = n - 1; i >= 0; i--) {
        int src = i - word_shift;
        uint64_t val = 0;
        if (src >= 0) {
            val = a[src] << bit_shift;
            if (bit_shift && src > 0) {
                val |= a[src - 1] >> (SOFT_WORD_BITS - bit_shift);
            }
        }
        out[i] = val;
    }
}

static void soft_u64_shift_left_wide(const uint64_t* a, int a_words, int shift, uint64_t* out, int out_words) {
    soft_u64_zero(out, out_words);
    if (shift <= 0) {
        int count = a_words < out_words ? a_words : out_words;
        soft_u64_copy(out, a, count);
        return;
    }
    int total_bits = out_words * SOFT_WORD_BITS;
    if (shift >= total_bits) {
        return;
    }
    int word_shift = shift / SOFT_WORD_BITS;
    int bit_shift = shift % SOFT_WORD_BITS;
    for (int i = 0; i < a_words; i++) {
        int dst = i + word_shift;
        if (dst >= out_words) {
            continue;
        }
        out[dst] |= a[i] << bit_shift;
        if (bit_shift && dst + 1 < out_words) {
            out[dst + 1] |= a[i] >> (SOFT_WORD_BITS - bit_shift);
        }
    }
}

static void soft_u64_shift_right(const uint64_t* a, int n, int shift, uint64_t* out) {
    if (shift <= 0) {
        soft_u64_copy(out, a, n);
        return;
    }
    int total_bits = n * SOFT_WORD_BITS;
    if (shift >= total_bits) {
        soft_u64_zero(out, n);
        return;
    }
    int word_shift = shift / SOFT_WORD_BITS;
    int bit_shift = shift % SOFT_WORD_BITS;
    for (int i = 0; i < n; i++) {
        int src = i + word_shift;
        uint64_t val = 0;
        if (src < n) {
            val = a[src] >> bit_shift;
            if (bit_shift && src + 1 < n) {
                val |= a[src + 1] << (SOFT_WORD_BITS - bit_shift);
            }
        }
        out[i] = val;
    }
}

static void soft_u64_shift_right_sticky(const uint64_t* a, int n, int shift, uint64_t* out) {
    if (shift <= 0) {
        soft_u64_copy(out, a, n);
        return;
    }
    int total_bits = n * SOFT_WORD_BITS;
    if (shift >= total_bits) {
        uint64_t sticky = soft_u64_is_zero(a, n) ? 0 : 1;
        soft_u64_zero(out, n);
        out[0] = sticky;
        return;
    }
    int word_shift = shift / SOFT_WORD_BITS;
    int bit_shift = shift % SOFT_WORD_BITS;
    uint64_t sticky = 0;
    for (int i = 0; i < word_shift; i++) {
        if (a[i] != 0) {
            sticky = 1;
            break;
        }
    }
    if (!sticky && bit_shift != 0) {
        uint64_t mask = (uint64_t)1 << bit_shift;
        mask -= 1;
        if ((a[word_shift] & mask) != 0) {
            sticky = 1;
        }
    }
    soft_u64_shift_right(a, n, shift, out);
    if (sticky) {
        out[0] |= 1;
    }
}

static int soft_u64_msb_index(const uint64_t* v, int n) {
    for (int i = n - 1; i >= 0; i--) {
        if (v[i] != 0) {
            return i * SOFT_WORD_BITS + (SOFT_WORD_BITS - 1 - soft_clz64(v[i]));
        }
    }
    return -1;
}

static void soft_u64_set_bit(uint64_t* v, int bit) {
    int word_idx = bit / SOFT_WORD_BITS;
    int bit_idx = bit % SOFT_WORD_BITS;
    v[word_idx] |= (uint64_t)1 << bit_idx;
}

static void soft_u64_mul(const uint64_t* a, const uint64_t* b, uint64_t* out, int n) {
    soft_u64_zero(out, n * 2);
    for (int i = 0; i < n; i++) {
        __uint128_t carry = 0;
        for (int j = 0; j < n; j++) {
            int idx = i + j;
            __uint128_t sum = (__uint128_t)a[i] * b[j] + out[idx] + carry;
            out[idx] = (uint64_t)sum;
            carry = sum >> 64;
        }
        int idx = i + n;
        while (carry && idx < n * 2) {
            __uint128_t sum = (__uint128_t)out[idx] + carry;
            out[idx] = (uint64_t)sum;
            carry = sum >> 64;
            idx++;
        }
    }
}

static void soft_u64_div_mod(const uint64_t* numer, const uint64_t* denom, uint64_t* quot, uint64_t* rem, int n) {
    soft_u64_zero(quot, n);
    soft_u64_zero(rem, n);
    if (soft_u64_is_zero(denom, n)) {
        return;
    }
    int total_bits = n * SOFT_WORD_BITS;
    for (int bit = total_bits - 1; bit >= 0; bit--) {
        uint64_t carry = 0;
        for (int i = 0; i < n; i++) {
            uint64_t new_carry = rem[i] >> 63;
            rem[i] = (rem[i] << 1) | carry;
            carry = new_carry;
        }
        if ((numer[bit / SOFT_WORD_BITS] >> (bit % SOFT_WORD_BITS)) & 1) {
            rem[0] |= 1;
        }
        if (soft_u64_cmp(rem, denom, n) >= 0) {
            soft_u64_sub(rem, denom, rem, n);
            soft_u64_set_bit(quot, bit);
        }
    }
}

static void soft_round_sig(uint64_t* sig, int words, int* exp) {
    uint64_t guard = (sig[0] >> 2) & 1;
    uint64_t round = (sig[0] >> 1) & 1;
    uint64_t sticky = sig[0] & 1;
    soft_u64_shift_right(sig, words, SOFT_EXTRA_BITS, sig);
    if (guard && (round || sticky || (sig[0] & 1))) {
        uint64_t carry = soft_u64_add_small(sig, words, 1);
        if (carry) {
            soft_u64_shift_right(sig, words, 1, sig);
            (*exp)++;
        }
    }
}

typedef struct {
    int words;
    int frac_bits;
    int sig_bits;
    int exp_bits;
    int exp_bias;
    int exp_shift;
    int min_exp;
    int max_exp;
    int decimal_dig;
} soft_float_spec;

static uint64_t soft_float_exp_max(int exp_bits) {
    if (exp_bits >= 64) {
        return ~0ULL;
    }
    return ((uint64_t)1 << exp_bits) - 1ULL;
}

static uint64_t soft_float_frac_mask(int exp_shift) {
    if (exp_shift <= 0) {
        return 0;
    }
    if (exp_shift >= 64) {
        return ~0ULL;
    }
    return ((uint64_t)1 << exp_shift) - 1ULL;
}

#define SOFT_FLOAT_DEFINE_SPEC(BITS, WORDS, FRAC_BITS, EXP_BITS, EXP_BIAS, DEC_DIG) \
    static const soft_float_spec soft_f##BITS##_spec = { \
        WORDS, FRAC_BITS, (FRAC_BITS + 1), EXP_BITS, EXP_BIAS, \
        (64 - 1 - (EXP_BITS)), (1 - (EXP_BIAS)), (EXP_BIAS), DEC_DIG \
    };

FERRET_FLOAT_SPECS(SOFT_FLOAT_DEFINE_SPEC)
#undef SOFT_FLOAT_DEFINE_SPEC

static void soft_float_make_special(const soft_float_spec* spec, int sign, soft_class_t cls, uint64_t* out_words) {
    int top = spec->words - 1;
    uint64_t exp_max = soft_float_exp_max(spec->exp_bits);
    soft_u64_zero(out_words, spec->words);
    if (cls == SOFT_CLASS_ZERO) {
        out_words[top] = (uint64_t)sign << 63;
        return;
    }
    if (cls == SOFT_CLASS_NAN) {
        out_words[0] = 1;
    }
    if (cls == SOFT_CLASS_INF || cls == SOFT_CLASS_NAN) {
        out_words[top] = ((uint64_t)sign << 63) | (exp_max << spec->exp_shift);
    }
}

static soft_class_t soft_float_unpack(const soft_float_spec* spec, const uint64_t* words, int* sign, int* exp, uint64_t* sig) {
    int top = spec->words - 1;
    uint64_t exp_max = soft_float_exp_max(spec->exp_bits);
    uint64_t frac_mask = soft_float_frac_mask(spec->exp_shift);
    uint64_t top_word = words[top];
    *sign = (int)(top_word >> 63);
    uint64_t exp_raw = (top_word >> spec->exp_shift) & exp_max;
    uint64_t frac_hi = top_word & frac_mask;

    for (int i = 0; i < top; i++) {
        sig[i] = words[i];
    }
    sig[top] = frac_hi;

    if (exp_raw == exp_max) {
        if (soft_u64_is_zero(sig, spec->words)) {
            return SOFT_CLASS_INF;
        }
        return SOFT_CLASS_NAN;
    }
    if (exp_raw == 0) {
        if (soft_u64_is_zero(sig, spec->words)) {
            return SOFT_CLASS_ZERO;
        }
        *exp = spec->min_exp;
    } else {
        *exp = (int)exp_raw - spec->exp_bias;
        sig[top] |= (uint64_t)1 << spec->exp_shift;
    }

    soft_u64_shift_left(sig, spec->words, SOFT_EXTRA_BITS, sig);
    return SOFT_CLASS_NORMAL;
}

static void soft_float_pack(const soft_float_spec* spec, int sign, int exp, uint64_t* sig, soft_class_t cls, uint64_t* out_words) {
    if (cls == SOFT_CLASS_ZERO || cls == SOFT_CLASS_INF || cls == SOFT_CLASS_NAN) {
        soft_float_make_special(spec, sign, cls, out_words);
        return;
    }
    if (soft_u64_is_zero(sig, spec->words)) {
        soft_float_make_special(spec, sign, SOFT_CLASS_ZERO, out_words);
        return;
    }

    int target = spec->sig_bits - 1 + SOFT_EXTRA_BITS;
    int lead = soft_u64_msb_index(sig, spec->words);
    if (lead < 0) {
        soft_float_make_special(spec, sign, SOFT_CLASS_ZERO, out_words);
        return;
    }
    if (lead > target) {
        int shift = lead - target;
        soft_u64_shift_right_sticky(sig, spec->words, shift, sig);
        exp += shift;
    } else if (lead < target) {
        int shift = target - lead;
        soft_u64_shift_left(sig, spec->words, shift, sig);
        exp -= shift;
    }

    if (exp < spec->min_exp) {
        int shift = spec->min_exp - exp;
        soft_u64_shift_right_sticky(sig, spec->words, shift, sig);
        exp = spec->min_exp;
    }

    soft_round_sig(sig, spec->words, &exp);
    if (soft_u64_is_zero(sig, spec->words)) {
        soft_float_make_special(spec, sign, SOFT_CLASS_ZERO, out_words);
        return;
    }
    if (exp > spec->max_exp) {
        soft_float_make_special(spec, sign, SOFT_CLASS_INF, out_words);
        return;
    }

    int hidden_word = (spec->sig_bits - 1) / SOFT_WORD_BITS;
    int hidden_bit = (spec->sig_bits - 1) % SOFT_WORD_BITS;
    bool normal = exp > spec->min_exp;
    if (exp == spec->min_exp) {
        normal = ((sig[hidden_word] >> hidden_bit) & 1) != 0;
    }

    int top = spec->words - 1;
    uint64_t frac_mask = soft_float_frac_mask(spec->exp_shift);
    for (int i = 0; i < top; i++) {
        out_words[i] = sig[i];
    }
    uint64_t frac_hi = sig[top] & frac_mask;
    if (normal) {
        uint64_t exp_raw = (uint64_t)(exp + spec->exp_bias);
        out_words[top] = ((uint64_t)sign << 63) | (exp_raw << spec->exp_shift) | frac_hi;
    } else {
        out_words[top] = ((uint64_t)sign << 63) | frac_hi;
    }
}

static void soft_float_add(const soft_float_spec* spec, const uint64_t* a_words, const uint64_t* b_words, uint64_t* out_words, bool sub) {
    size_t words = (size_t)spec->words;
    uint64_t* buf = (uint64_t*)malloc(words * 3 * sizeof(uint64_t));
    if (!buf) {
        soft_float_make_special(spec, 0, SOFT_CLASS_ZERO, out_words);
        return;
    }
    uint64_t* sig_a = buf;
    uint64_t* sig_b = buf + words;
    uint64_t* sig_out = buf + words * 2;
    int sign_a = 0;
    int sign_b = 0;
    int exp_a = 0;
    int exp_b = 0;
    soft_class_t cls_a = soft_float_unpack(spec, a_words, &sign_a, &exp_a, sig_a);
    soft_class_t cls_b = soft_float_unpack(spec, b_words, &sign_b, &exp_b, sig_b);
    if (sub) {
        sign_b ^= 1;
    }

    if (cls_a == SOFT_CLASS_NAN || cls_b == SOFT_CLASS_NAN) {
        soft_float_make_special(spec, 0, SOFT_CLASS_NAN, out_words);
        goto cleanup;
    }
    if (cls_a == SOFT_CLASS_INF || cls_b == SOFT_CLASS_INF) {
        if (cls_a == SOFT_CLASS_INF && cls_b == SOFT_CLASS_INF && sign_a != sign_b) {
            soft_float_make_special(spec, 0, SOFT_CLASS_NAN, out_words);
            goto cleanup;
        }
        if (cls_a == SOFT_CLASS_INF) {
            soft_float_make_special(spec, sign_a, SOFT_CLASS_INF, out_words);
            goto cleanup;
        }
        soft_float_make_special(spec, sign_b, SOFT_CLASS_INF, out_words);
        goto cleanup;
    }
    if (cls_a == SOFT_CLASS_ZERO) {
        soft_float_pack(spec, sign_b, exp_b, sig_b, cls_b, out_words);
        goto cleanup;
    }
    if (cls_b == SOFT_CLASS_ZERO) {
        soft_float_pack(spec, sign_a, exp_a, sig_a, cls_a, out_words);
        goto cleanup;
    }

    if (exp_a < exp_b) {
        int tmp_sign = sign_a;
        sign_a = sign_b;
        sign_b = tmp_sign;
        int tmp_exp = exp_a;
        exp_a = exp_b;
        exp_b = tmp_exp;
        uint64_t* tmp_sig = sig_a;
        sig_a = sig_b;
        sig_b = tmp_sig;
    }

    int diff = exp_a - exp_b;
    if (diff > 0) {
        soft_u64_shift_right_sticky(sig_b, spec->words, diff, sig_b);
    }

    int sign_out = sign_a;
    int exp_out = exp_a;

    if (sign_a == sign_b) {
        uint64_t carry = soft_u64_add(sig_a, sig_b, sig_out, spec->words);
        if (carry) {
            soft_u64_shift_right_sticky(sig_out, spec->words, 1, sig_out);
            exp_out += 1;
        }
        soft_float_pack(spec, sign_out, exp_out, sig_out, SOFT_CLASS_NORMAL, out_words);
        goto cleanup;
    }

    int cmp = soft_u64_cmp(sig_a, sig_b, spec->words);
    if (cmp == 0) {
        soft_float_make_special(spec, 0, SOFT_CLASS_ZERO, out_words);
        goto cleanup;
    }
    if (cmp < 0) {
        soft_u64_sub(sig_b, sig_a, sig_out, spec->words);
        sign_out = sign_b;
    } else {
        soft_u64_sub(sig_a, sig_b, sig_out, spec->words);
        sign_out = sign_a;
    }
    soft_float_pack(spec, sign_out, exp_out, sig_out, SOFT_CLASS_NORMAL, out_words);

cleanup:
    free(buf);
}

static void soft_float_mul(const soft_float_spec* spec, const uint64_t* a_words, const uint64_t* b_words, uint64_t* out_words) {
    size_t words = (size_t)spec->words;
    uint64_t* buf = (uint64_t*)malloc(words * 9 * sizeof(uint64_t));
    if (!buf) {
        soft_float_make_special(spec, 0, SOFT_CLASS_ZERO, out_words);
        return;
    }
    uint64_t* sig_a = buf;
    uint64_t* sig_b = buf + words;
    uint64_t* sig_a_raw = buf + words * 2;
    uint64_t* sig_b_raw = buf + words * 3;
    uint64_t* sig_out = buf + words * 4;
    uint64_t* prod = buf + words * 5;
    uint64_t* tmp = prod + words * 2;

    int sign_a = 0;
    int sign_b = 0;
    int exp_a = 0;
    int exp_b = 0;
    soft_class_t cls_a = soft_float_unpack(spec, a_words, &sign_a, &exp_a, sig_a);
    soft_class_t cls_b = soft_float_unpack(spec, b_words, &sign_b, &exp_b, sig_b);

    if (cls_a == SOFT_CLASS_NAN || cls_b == SOFT_CLASS_NAN) {
        soft_float_make_special(spec, 0, SOFT_CLASS_NAN, out_words);
        goto cleanup;
    }
    if ((cls_a == SOFT_CLASS_INF && cls_b == SOFT_CLASS_ZERO) ||
        (cls_b == SOFT_CLASS_INF && cls_a == SOFT_CLASS_ZERO)) {
        soft_float_make_special(spec, 0, SOFT_CLASS_NAN, out_words);
        goto cleanup;
    }
    if (cls_a == SOFT_CLASS_INF || cls_b == SOFT_CLASS_INF) {
        soft_float_make_special(spec, sign_a ^ sign_b, SOFT_CLASS_INF, out_words);
        goto cleanup;
    }
    if (cls_a == SOFT_CLASS_ZERO || cls_b == SOFT_CLASS_ZERO) {
        soft_float_make_special(spec, sign_a ^ sign_b, SOFT_CLASS_ZERO, out_words);
        goto cleanup;
    }

    soft_u64_shift_right(sig_a, spec->words, SOFT_EXTRA_BITS, sig_a_raw);
    soft_u64_shift_right(sig_b, spec->words, SOFT_EXTRA_BITS, sig_b_raw);

    soft_u64_mul(sig_a_raw, sig_b_raw, prod, spec->words);
    int lead = soft_u64_msb_index(prod, spec->words * 2);
    if (lead < 0) {
        soft_float_make_special(spec, sign_a ^ sign_b, SOFT_CLASS_ZERO, out_words);
        goto cleanup;
    }
    int shift_raw = lead - (spec->sig_bits - 1);
    int shift = shift_raw - SOFT_EXTRA_BITS;
    if (shift >= 0) {
        soft_u64_shift_right_sticky(prod, spec->words * 2, shift, tmp);
        soft_u64_copy(sig_out, tmp, spec->words);
    } else {
        soft_u64_zero(sig_out, spec->words);
        soft_u64_shift_left(prod, spec->words * 2, -shift, prod);
        soft_u64_copy(sig_out, prod, spec->words);
    }
    int exp_out = exp_a + exp_b - (spec->sig_bits - 1) + shift_raw;
    soft_float_pack(spec, sign_a ^ sign_b, exp_out, sig_out, SOFT_CLASS_NORMAL, out_words);

cleanup:
    free(buf);
}

static void soft_float_div(const soft_float_spec* spec, const uint64_t* a_words, const uint64_t* b_words, uint64_t* out_words) {
    size_t words = (size_t)spec->words;
    uint64_t* buf = (uint64_t*)malloc(words * 13 * sizeof(uint64_t));
    if (!buf) {
        soft_float_make_special(spec, 0, SOFT_CLASS_ZERO, out_words);
        return;
    }
    uint64_t* sig_a = buf;
    uint64_t* sig_b = buf + words;
    uint64_t* sig_a_raw = buf + words * 2;
    uint64_t* sig_b_raw = buf + words * 3;
    uint64_t* sig_out = buf + words * 4;
    uint64_t* numer = buf + words * 5;
    uint64_t* denom = numer + words * 2;
    uint64_t* quot = denom + words * 2;
    uint64_t* rem = quot + words * 2;

    int sign_a = 0;
    int sign_b = 0;
    int exp_a = 0;
    int exp_b = 0;
    soft_class_t cls_a = soft_float_unpack(spec, a_words, &sign_a, &exp_a, sig_a);
    soft_class_t cls_b = soft_float_unpack(spec, b_words, &sign_b, &exp_b, sig_b);

    if (cls_a == SOFT_CLASS_NAN || cls_b == SOFT_CLASS_NAN) {
        soft_float_make_special(spec, 0, SOFT_CLASS_NAN, out_words);
        goto cleanup;
    }
    if (cls_a == SOFT_CLASS_INF && cls_b == SOFT_CLASS_INF) {
        soft_float_make_special(spec, 0, SOFT_CLASS_NAN, out_words);
        goto cleanup;
    }
    if (cls_a == SOFT_CLASS_INF) {
        soft_float_make_special(spec, sign_a ^ sign_b, SOFT_CLASS_INF, out_words);
        goto cleanup;
    }
    if (cls_b == SOFT_CLASS_INF) {
        soft_float_make_special(spec, sign_a ^ sign_b, SOFT_CLASS_ZERO, out_words);
        goto cleanup;
    }
    if (cls_b == SOFT_CLASS_ZERO) {
        if (cls_a == SOFT_CLASS_ZERO) {
            soft_float_make_special(spec, 0, SOFT_CLASS_NAN, out_words);
            goto cleanup;
        }
        soft_float_make_special(spec, sign_a ^ sign_b, SOFT_CLASS_INF, out_words);
        goto cleanup;
    }
    if (cls_a == SOFT_CLASS_ZERO) {
        soft_float_make_special(spec, sign_a ^ sign_b, SOFT_CLASS_ZERO, out_words);
        goto cleanup;
    }

    soft_u64_shift_right(sig_a, spec->words, SOFT_EXTRA_BITS, sig_a_raw);
    soft_u64_shift_right(sig_b, spec->words, SOFT_EXTRA_BITS, sig_b_raw);

    int shift = (spec->sig_bits - 1) + SOFT_EXTRA_BITS;
    soft_u64_shift_left_wide(sig_a_raw, spec->words, shift, numer, spec->words * 2);

    soft_u64_zero(denom, spec->words * 2);
    soft_u64_copy(denom, sig_b_raw, spec->words);

    soft_u64_div_mod(numer, denom, quot, rem, spec->words * 2);
    soft_u64_copy(sig_out, quot, spec->words);
    if (!soft_u64_is_zero(rem, spec->words * 2)) {
        sig_out[0] |= 1;
    }
    int exp_out = exp_a - exp_b;
    soft_float_pack(spec, sign_a ^ sign_b, exp_out, sig_out, SOFT_CLASS_NORMAL, out_words);

cleanup:
    free(buf);
}

static int soft_float_compare(const soft_float_spec* spec, const uint64_t* a_words, const uint64_t* b_words, bool* unordered) {
    size_t words = (size_t)spec->words;
    uint64_t* buf = (uint64_t*)malloc(words * 2 * sizeof(uint64_t));
    if (!buf) {
        if (unordered) {
            *unordered = true;
        }
        return 0;
    }
    uint64_t* sig_a = buf;
    uint64_t* sig_b = buf + words;
    int sign_a = 0;
    int sign_b = 0;
    int exp_a = 0;
    int exp_b = 0;
    soft_class_t cls_a = soft_float_unpack(spec, a_words, &sign_a, &exp_a, sig_a);
    soft_class_t cls_b = soft_float_unpack(spec, b_words, &sign_b, &exp_b, sig_b);

    if (cls_a == SOFT_CLASS_NAN || cls_b == SOFT_CLASS_NAN) {
        if (unordered) {
            *unordered = true;
        }
        free(buf);
        return 0;
    }
    if (cls_a == SOFT_CLASS_ZERO && cls_b == SOFT_CLASS_ZERO) {
        if (unordered) {
            *unordered = false;
        }
        free(buf);
        return 0;
    }
    if (cls_a == SOFT_CLASS_INF || cls_b == SOFT_CLASS_INF) {
        if (unordered) {
            *unordered = false;
        }
        if (cls_a == cls_b) {
            if (sign_a == sign_b) {
                free(buf);
                return 0;
            }
            free(buf);
            return sign_a ? -1 : 1;
        }
        if (cls_a == SOFT_CLASS_INF) {
            free(buf);
            return sign_a ? -1 : 1;
        }
        free(buf);
        return sign_b ? 1 : -1;
    }
    if (sign_a != sign_b) {
        if (unordered) {
            *unordered = false;
        }
        free(buf);
        return sign_a ? -1 : 1;
    }

    soft_u64_shift_right(sig_a, spec->words, SOFT_EXTRA_BITS, sig_a);
    soft_u64_shift_right(sig_b, spec->words, SOFT_EXTRA_BITS, sig_b);

    int cmp = 0;
    if (exp_a < exp_b) {
        cmp = -1;
    } else if (exp_a > exp_b) {
        cmp = 1;
    } else {
        cmp = soft_u64_cmp(sig_a, sig_b, spec->words);
    }
    if (unordered) {
        *unordered = false;
    }
    if (sign_a) {
        cmp = -cmp;
    }
    free(buf);
    return cmp;
}

static void soft_float_from_f64(const soft_float_spec* spec, double val, uint64_t* out_words) {
    union {
        double d;
        uint64_t u;
    } bits;
    bits.d = val;

    uint64_t sign = bits.u >> 63;
    uint64_t exp_raw = (bits.u >> 52) & 0x7FFu;
    uint64_t frac = bits.u & 0xFFFFFFFFFFFFFULL;

    uint64_t* sig = (uint64_t*)malloc((size_t)spec->words * sizeof(uint64_t));
    if (!sig) {
        soft_float_make_special(spec, 0, SOFT_CLASS_ZERO, out_words);
        return;
    }
    soft_u64_zero(sig, spec->words);
    int exp = 0;
    soft_class_t cls = SOFT_CLASS_NORMAL;

    if (exp_raw == 0x7FFu) {
        cls = (frac == 0) ? SOFT_CLASS_INF : SOFT_CLASS_NAN;
        soft_float_pack(spec, (int)sign, 0, sig, cls, out_words);
        free(sig);
        return;
    }
    if (exp_raw == 0) {
        if (frac == 0) {
            soft_float_pack(spec, (int)sign, 0, sig, SOFT_CLASS_ZERO, out_words);
            free(sig);
            return;
        }
        int lead = SOFT_WORD_BITS - 1 - soft_clz64(frac);
        int shift = 52 - lead;
        uint64_t sig64 = frac << shift;
        exp = lead - 1074;
        sig[0] = sig64;
        soft_u64_shift_left(sig, spec->words, spec->frac_bits - 52, sig);
    } else {
        exp = (int)exp_raw - 1023;
        uint64_t sig64 = (uint64_t)1 << 52;
        sig64 |= frac;
        sig[0] = sig64;
        soft_u64_shift_left(sig, spec->words, spec->frac_bits - 52, sig);
    }
    soft_u64_shift_left(sig, spec->words, SOFT_EXTRA_BITS, sig);
    soft_float_pack(spec, (int)sign, exp, sig, cls, out_words);
    free(sig);
}

static double soft_to_double(int sign, int exp, const uint64_t* sig, int sig_words, int sig_bits, uint64_t* sig_raw) {
    if (soft_u64_is_zero(sig, sig_words)) {
        return sign ? -0.0 : 0.0;
    }
    soft_u64_shift_right(sig, sig_words, SOFT_EXTRA_BITS, sig_raw);

    long double value = 0.0L;
    for (int i = sig_words - 1; i >= 0; i--) {
        value = ldexp(value, SOFT_WORD_BITS);
        value += (long double)sig_raw[i];
    }
    int shift = exp - (sig_bits - 1);
    value = ldexp(value, shift);
    if (sign) {
        value = -value;
    }
    return (double)value;
}

static double soft_float_to_f64(const soft_float_spec* spec, const uint64_t* words) {
    size_t wcount = (size_t)spec->words;
    uint64_t* buf = (uint64_t*)malloc(wcount * 2 * sizeof(uint64_t));
    if (!buf) {
        return 0.0;
    }
    uint64_t* sig = buf;
    uint64_t* sig_raw = buf + wcount;
    int sign = 0;
    int exp = 0;
    soft_class_t cls = soft_float_unpack(spec, words, &sign, &exp, sig);
    if (cls == SOFT_CLASS_NAN) {
        free(buf);
        return NAN;
    }
    if (cls == SOFT_CLASS_INF) {
        free(buf);
        return sign ? -INFINITY : INFINITY;
    }
    double out = soft_to_double(sign, exp, sig, spec->words, spec->sig_bits, sig_raw);
    free(buf);
    return out;
}
static void ferret_ensure_float_decimal(char* buf, size_t size) {
    if (!buf || size == 0) {
        return;
    }

    bool has_dot = false;
    bool has_exp = false;
    for (size_t i = 0; buf[i] != '\0'; i++) {
        char c = buf[i];
        if (c == '.') {
            has_dot = true;
        } else if (c == 'e' || c == 'E') {
            has_exp = true;
        } else if ((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
            return;
        }
    }

    if (has_dot || has_exp) {
        return;
    }

    size_t len = strlen(buf);
    if (len + 2 >= size) {
        return;
    }
    buf[len] = '.';
    buf[len + 1] = '0';
    buf[len + 2] = '\0';
}

typedef struct {
    uint32_t* limbs;
    size_t len;
    size_t cap;
} ferret_big_uint;

static int big_bit_length(const ferret_big_uint* b);

static int ferret_clz32(uint32_t v) {
#if defined(__GNUC__)
    if (v == 0) {
        return 32;
    }
    return __builtin_clz(v);
#else
    int count = 0;
    uint32_t bit = 0x80000000u;
    while (bit && (v & bit) == 0) {
        count++;
        bit >>= 1;
    }
    return count;
#endif
}

static void big_init(ferret_big_uint* b) {
    b->limbs = NULL;
    b->len = 0;
    b->cap = 0;
}

static void big_free(ferret_big_uint* b) {
    if (!b) {
        return;
    }
    free(b->limbs);
    b->limbs = NULL;
    b->len = 0;
    b->cap = 0;
}

static void big_reserve(ferret_big_uint* b, size_t cap) {
    if (cap <= b->cap) {
        return;
    }
    size_t new_cap = b->cap ? b->cap : 4;
    while (new_cap < cap) {
        new_cap *= 2;
    }
    uint32_t* next = (uint32_t*)realloc(b->limbs, new_cap * sizeof(uint32_t));
    if (!next) {
        return;
    }
    b->limbs = next;
    b->cap = new_cap;
}

static void big_normalize(ferret_big_uint* b) {
    while (b->len > 0 && b->limbs[b->len - 1] == 0) {
        b->len--;
    }
}

static void big_set_u64(ferret_big_uint* b, uint64_t val) {
    big_reserve(b, 2);
    b->limbs[0] = (uint32_t)(val & 0xffffffffu);
    b->limbs[1] = (uint32_t)(val >> 32);
    b->len = (b->limbs[1] != 0) ? 2 : (b->limbs[0] != 0 ? 1 : 0);
}

static void big_from_words64(ferret_big_uint* b, const uint64_t* words, int count) {
    big_reserve(b, (size_t)count * 2);
    size_t idx = 0;
    for (int i = 0; i < count; i++) {
        b->limbs[idx++] = (uint32_t)(words[i] & 0xffffffffu);
        b->limbs[idx++] = (uint32_t)(words[i] >> 32);
    }
    b->len = idx;
    big_normalize(b);
}

static void big_copy(ferret_big_uint* dst, const ferret_big_uint* src) {
    big_reserve(dst, src->len);
    if (src->len > 0) {
        memcpy(dst->limbs, src->limbs, src->len * sizeof(uint32_t));
    }
    dst->len = src->len;
}

static bool big_is_zero(const ferret_big_uint* b) {
    return b->len == 0;
}

static int big_cmp(const ferret_big_uint* a, const ferret_big_uint* b) {
    if (a->len < b->len) return -1;
    if (a->len > b->len) return 1;
    for (size_t i = a->len; i-- > 0;) {
        uint32_t av = a->limbs[i];
        uint32_t bv = b->limbs[i];
        if (av < bv) return -1;
        if (av > bv) return 1;
    }
    return 0;
}

static void big_shift_left_bits(ferret_big_uint* b, int bits) {
    if (b->len == 0 || bits <= 0) {
        return;
    }
    size_t orig_len = b->len;
    int word_shift = bits / 32;
    int bit_shift = bits % 32;
    size_t new_len = orig_len + (size_t)word_shift + (bit_shift ? 1 : 0);
    big_reserve(b, new_len);
    for (size_t i = orig_len; i-- > 0;) {
        b->limbs[i + word_shift] = b->limbs[i];
    }
    for (int i = 0; i < word_shift; i++) {
        b->limbs[i] = 0;
    }
    if (bit_shift) {
        uint32_t carry = 0;
        for (size_t i = (size_t)word_shift; i < orig_len + (size_t)word_shift; i++) {
            uint64_t val = ((uint64_t)b->limbs[i] << bit_shift) | carry;
            b->limbs[i] = (uint32_t)val;
            carry = (uint32_t)(val >> 32);
        }
        if (carry) {
            b->limbs[orig_len + (size_t)word_shift] = carry;
            b->len = orig_len + (size_t)word_shift + 1;
        } else {
            b->limbs[orig_len + (size_t)word_shift] = 0;
            b->len = orig_len + (size_t)word_shift;
        }
        big_normalize(b);
        return;
    }
    b->len = orig_len + (size_t)word_shift;
    big_normalize(b);
}

static void big_shift_right_bits(ferret_big_uint* b, int bits) {
    if (b->len == 0 || bits <= 0) {
        return;
    }
    int word_shift = bits / 32;
    int bit_shift = bits % 32;
    if ((size_t)word_shift >= b->len) {
        b->len = 0;
        return;
    }
    for (size_t i = 0; i < b->len - (size_t)word_shift; i++) {
        b->limbs[i] = b->limbs[i + word_shift];
    }
    b->len -= (size_t)word_shift;
    if (bit_shift) {
        uint32_t carry = 0;
        for (size_t i = b->len; i-- > 0;) {
            uint32_t next = b->limbs[i];
            b->limbs[i] = (next >> bit_shift) | (carry << (32 - bit_shift));
            carry = next & ((1u << bit_shift) - 1u);
        }
    }
    big_normalize(b);
}

static void big_add_small(ferret_big_uint* b, uint32_t add) {
    if (b->len == 0) {
        big_set_u64(b, add);
        return;
    }
    uint64_t sum = (uint64_t)b->limbs[0] + add;
    b->limbs[0] = (uint32_t)sum;
    uint64_t carry = sum >> 32;
    size_t idx = 1;
    while (carry && idx < b->len) {
        sum = (uint64_t)b->limbs[idx] + carry;
        b->limbs[idx] = (uint32_t)sum;
        carry = sum >> 32;
        idx++;
    }
    if (carry) {
        big_reserve(b, b->len + 1);
        b->limbs[b->len++] = (uint32_t)carry;
    }
}

static void big_mul_small(ferret_big_uint* b, uint32_t mul) {
    if (b->len == 0 || mul == 0) {
        b->len = 0;
        return;
    }
    uint64_t carry = 0;
    for (size_t i = 0; i < b->len; i++) {
        uint64_t prod = (uint64_t)b->limbs[i] * mul + carry;
        b->limbs[i] = (uint32_t)prod;
        carry = prod >> 32;
    }
    if (carry) {
        big_reserve(b, b->len + 1);
        b->limbs[b->len++] = (uint32_t)carry;
    }
}

static void big_mul(const ferret_big_uint* a, const ferret_big_uint* b, ferret_big_uint* out) {
    if (a->len == 0 || b->len == 0) {
        out->len = 0;
        return;
    }
    size_t out_len = a->len + b->len;
    big_reserve(out, out_len);
    memset(out->limbs, 0, out_len * sizeof(uint32_t));
    for (size_t i = 0; i < a->len; i++) {
        uint64_t carry = 0;
        for (size_t j = 0; j < b->len; j++) {
            size_t idx = i + j;
            uint64_t cur = out->limbs[idx];
            uint64_t prod = (uint64_t)a->limbs[i] * b->limbs[j] + cur + carry;
            out->limbs[idx] = (uint32_t)prod;
            carry = prod >> 32;
        }
        size_t idx = i + b->len;
        while (carry && idx < out_len) {
            uint64_t cur = (uint64_t)out->limbs[idx] + carry;
            out->limbs[idx] = (uint32_t)cur;
            carry = cur >> 32;
            idx++;
        }
    }
    out->len = out_len;
    big_normalize(out);
}

static void big_sub(ferret_big_uint* a, const ferret_big_uint* b) {
    uint64_t borrow = 0;
    for (size_t i = 0; i < a->len; i++) {
        uint64_t ai = a->limbs[i];
        uint64_t bi = (i < b->len) ? b->limbs[i] : 0;
        uint64_t sub = ai - bi - borrow;
        a->limbs[i] = (uint32_t)sub;
        borrow = (sub >> 63) & 1u;
    }
    big_normalize(a);
}

static uint32_t big_div_small(ferret_big_uint* a, uint32_t divisor) {
    uint64_t rem = 0;
    for (size_t i = a->len; i-- > 0;) {
        uint64_t cur = (rem << 32) | a->limbs[i];
        a->limbs[i] = (uint32_t)(cur / divisor);
        rem = cur % divisor;
    }
    big_normalize(a);
    return (uint32_t)rem;
}

static void big_set_bit(ferret_big_uint* b, int bit) {
    if (bit < 0) {
        return;
    }
    size_t idx = (size_t)bit / 32;
    int shift = bit % 32;
    big_reserve(b, idx + 1);
    if (b->len <= idx) {
        for (size_t i = b->len; i <= idx; i++) {
            b->limbs[i] = 0;
        }
        b->len = idx + 1;
    }
    b->limbs[idx] |= (uint32_t)(1u << shift);
}

static void big_div_mod(const ferret_big_uint* u, const ferret_big_uint* v, ferret_big_uint* q, ferret_big_uint* r) {
    if (v->len == 0) {
        q->len = 0;
        r->len = 0;
        return;
    }
    if (u->len == 0) {
        q->len = 0;
        r->len = 0;
        return;
    }
    if (big_cmp(u, v) < 0) {
        q->len = 0;
        big_copy(r, u);
        return;
    }
    if (v->len == 1) {
        big_copy(q, u);
        uint32_t rem = big_div_small(q, v->limbs[0]);
        big_set_u64(r, rem);
        return;
    }

    int u_bits = big_bit_length(u);
    int v_bits = big_bit_length(v);
    int shift = u_bits - v_bits;

    ferret_big_uint denom;
    big_init(&denom);
    big_copy(&denom, v);
    if (shift > 0) {
        big_shift_left_bits(&denom, shift);
    }

    big_reserve(q, (size_t)(shift / 32 + 2));
    q->len = 0;
    big_reserve(r, u->len);
    big_copy(r, u);

    for (int i = shift; i >= 0; i--) {
        if (big_cmp(r, &denom) >= 0) {
            big_sub(r, &denom);
            big_set_bit(q, i);
        }
        if (i > 0) {
            big_shift_right_bits(&denom, 1);
        }
    }

    big_normalize(q);
    big_normalize(r);
    big_free(&denom);
}

static int big_bit_length(const ferret_big_uint* b) {
    if (b->len == 0) {
        return 0;
    }
    uint32_t hi = b->limbs[b->len - 1];
    int lead = ferret_clz32(hi);
    return (int)((b->len - 1) * 32 + (32 - lead));
}

static long double big_log2(const ferret_big_uint* b) {
    if (b->len == 0) {
        return -INFINITY;
    }
    uint32_t hi = b->limbs[b->len - 1];
    int lead = ferret_clz32(hi);
    int msb = (int)((b->len - 1) * 32 + (31 - lead));
    uint32_t hi2 = (b->len > 1) ? b->limbs[b->len - 2] : 0;
    uint64_t top = ((uint64_t)hi << 32) | hi2;
    top <<= lead;
    long double frac = (long double)top / (long double)(1ULL << 63);
    return (long double)msb + log2l(frac);
}

static void big_pow5(uint32_t exp, ferret_big_uint* out) {
    ferret_big_uint result;
    ferret_big_uint base;
    big_init(&result);
    big_init(&base);
    big_set_u64(&result, 1);
    big_set_u64(&base, 5);
    uint32_t e = exp;
    while (e > 0) {
        if (e & 1u) {
            ferret_big_uint tmp;
            big_init(&tmp);
            big_mul(&result, &base, &tmp);
            big_free(&result);
            result = tmp;
        }
        e >>= 1;
        if (e) {
            ferret_big_uint tmp;
            big_init(&tmp);
            big_mul(&base, &base, &tmp);
            big_free(&base);
            base = tmp;
        }
    }
    big_free(&base);
    *out = result;
}

static char* big_to_decimal(const ferret_big_uint* b) {
    if (b->len == 0) {
        char* out = (char*)malloc(2);
        if (!out) return NULL;
        out[0] = '0';
        out[1] = '\0';
        return out;
    }
    ferret_big_uint tmp;
    big_init(&tmp);
    big_copy(&tmp, b);
    const uint32_t base = 1000000000u;
    uint32_t* parts = NULL;
    size_t parts_len = 0;
    size_t parts_cap = 0;
    while (!big_is_zero(&tmp)) {
        uint32_t rem = big_div_small(&tmp, base);
        if (parts_len == parts_cap) {
            size_t next = parts_cap ? parts_cap * 2 : 4;
            uint32_t* next_parts = (uint32_t*)realloc(parts, next * sizeof(uint32_t));
            if (!next_parts) {
                free(parts);
                big_free(&tmp);
                return NULL;
            }
            parts = next_parts;
            parts_cap = next;
        }
        parts[parts_len++] = rem;
    }
    size_t buf_size = parts_len * 9 + 1;
    char* out = (char*)malloc(buf_size);
    if (!out) {
        free(parts);
        big_free(&tmp);
        return NULL;
    }
    char* ptr = out;
    if (parts_len > 0) {
        ptr += snprintf(ptr, buf_size, "%u", parts[parts_len - 1]);
        for (size_t i = parts_len - 1; i-- > 0;) {
            ptr += snprintf(ptr, buf_size - (size_t)(ptr - out), "%09u", parts[i]);
        }
    }
    *ptr = '\0';
    free(parts);
    big_free(&tmp);
    return out;
}

static long double soft_log2_from_sig(const uint64_t* sig, int words) {
    int msb = soft_u64_msb_index(sig, words);
    if (msb < 0) {
        return -INFINITY;
    }
    int word_idx = msb / 64;
    int bit_idx = msb % 64;
    uint64_t hi = sig[word_idx];
    uint64_t lo = (word_idx > 0) ? sig[word_idx - 1] : 0;
    int lead = 63 - bit_idx;
    uint64_t top = hi << lead;
    if (lead && word_idx > 0) {
        top |= lo >> (64 - lead);
    }
    long double frac = (long double)top / (long double)(1ULL << 63);
    return (long double)msb + log2l(frac);
}

static char* soft_format_decimal(int sign, int exp, const uint64_t* sig_raw, int sig_words, int sig_bits, soft_class_t cls, int precision) {
    if (cls == SOFT_CLASS_NAN) {
        char* out = (char*)malloc(4);
        if (!out) return NULL;
        strcpy(out, "nan");
        return out;
    }
    if (cls == SOFT_CLASS_INF) {
        char* out = (char*)malloc(sign ? 5 : 4);
        if (!out) return NULL;
        if (sign) {
            strcpy(out, "-inf");
        } else {
            strcpy(out, "inf");
        }
        return out;
    }
    if (cls == SOFT_CLASS_ZERO || soft_u64_is_zero(sig_raw, sig_words)) {
        char* out = (char*)malloc(sign ? 5 : 4);
        if (!out) return NULL;
        if (sign) {
            strcpy(out, "-0.0");
        } else {
            strcpy(out, "0.0");
        }
        return out;
    }

    int exp2 = exp - (sig_bits - 1);
    long double log2_sig = soft_log2_from_sig(sig_raw, sig_words);
    long double log10_val = (log2_sig + (long double)exp2) * log10l(2.0L);
    int exp10 = (int)floorl(log10_val);
    int k = precision - 1 - exp10;

    ferret_big_uint num;
    ferret_big_uint den;
    big_init(&num);
    big_init(&den);
    big_from_words64(&num, sig_raw, sig_words);
    big_set_u64(&den, 1);

    if (k >= 0) {
        ferret_big_uint pow5;
        big_init(&pow5);
        big_pow5((uint32_t)k, &pow5);
        ferret_big_uint tmp;
        big_init(&tmp);
        big_mul(&num, &pow5, &tmp);
        big_free(&num);
        num = tmp;
        big_free(&pow5);
    } else {
        ferret_big_uint pow5;
        big_init(&pow5);
        big_pow5((uint32_t)(-k), &pow5);
        big_free(&den);
        den = pow5;
    }

    int shift2 = exp2 + k;
    if (shift2 >= 0) {
        big_shift_left_bits(&num, shift2);
    } else {
        big_shift_left_bits(&den, -shift2);
    }

    ferret_big_uint q;
    ferret_big_uint r;
    big_init(&q);
    big_init(&r);
    big_div_mod(&num, &den, &q, &r);

    if (!big_is_zero(&r)) {
        ferret_big_uint r2;
        big_init(&r2);
        big_copy(&r2, &r);
        big_mul_small(&r2, 2);
        int cmp = big_cmp(&r2, &den);
        if (cmp > 0 || (cmp == 0 && (q.len > 0 && (q.limbs[0] & 1u)))) {
            big_add_small(&q, 1);
        }
        big_free(&r2);
    }

    char* digits = big_to_decimal(&q);
    if (!digits) {
        big_free(&num);
        big_free(&den);
        big_free(&q);
        big_free(&r);
        return NULL;
    }
    size_t len = strlen(digits);
    if (len > (size_t)precision) {
        big_div_small(&q, 10);
        exp10 += 1;
        free(digits);
        digits = big_to_decimal(&q);
        if (!digits) {
            big_free(&num);
            big_free(&den);
            big_free(&q);
            big_free(&r);
            return NULL;
        }
        len = strlen(digits);
    }
    if (len < (size_t)precision) {
        size_t diff = (size_t)precision - len;
        char* padded = (char*)malloc((size_t)precision + 1);
        if (!padded) {
            free(digits);
            big_free(&num);
            big_free(&den);
            big_free(&q);
            big_free(&r);
            return NULL;
        }
        memset(padded, '0', diff);
        memcpy(padded + diff, digits, len + 1);
        free(digits);
        digits = padded;
        exp10 -= (int)diff;
        len = (size_t)precision;
    }

    int use_fixed = (exp10 >= -4 && exp10 < precision);
    size_t out_cap = (size_t)precision + 32;
    char* out = (char*)malloc(out_cap);
    if (!out) {
        free(digits);
        big_free(&num);
        big_free(&den);
        big_free(&q);
        big_free(&r);
        return NULL;
    }
    char* ptr = out;
    if (sign) {
        *ptr++ = '-';
    }
    if (use_fixed) {
        int point = exp10 + 1;
        if (point <= 0) {
            *ptr++ = '0';
            *ptr++ = '.';
            for (int i = 0; i < -point; i++) {
                *ptr++ = '0';
            }
            memcpy(ptr, digits, len);
            ptr += len;
        } else if (point >= (int)len) {
            memcpy(ptr, digits, len);
            ptr += len;
            for (int i = 0; i < point - (int)len; i++) {
                *ptr++ = '0';
            }
            *ptr++ = '.';
            *ptr++ = '0';
        } else {
            memcpy(ptr, digits, (size_t)point);
            ptr += point;
            *ptr++ = '.';
            memcpy(ptr, digits + point, len - (size_t)point);
            ptr += len - (size_t)point;
        }
    } else {
        *ptr++ = digits[0];
        *ptr++ = '.';
        if (len > 1) {
            memcpy(ptr, digits + 1, len - 1);
            ptr += len - 1;
        } else {
            *ptr++ = '0';
        }
        *ptr++ = 'e';
        if (exp10 >= 0) {
            *ptr++ = '+';
        } else {
            *ptr++ = '-';
            exp10 = -exp10;
        }
        ptr += snprintf(ptr, out_cap - (size_t)(ptr - out), "%d", exp10);
    }
    *ptr = '\0';

    free(digits);
    big_free(&num);
    big_free(&den);
    big_free(&q);
    big_free(&r);
    ferret_ensure_float_decimal(out, out_cap);
    return out;
}

static bool ferret_parse_special_float(const char* str, int* sign, soft_class_t* cls) {
    if (!str || !sign || !cls) {
        return false;
    }
    const char* s = str;
    while (*s && isspace((unsigned char)*s)) {
        s++;
    }
    int sign_val = 0;
    if (*s == '+' || *s == '-') {
        sign_val = (*s == '-');
        s++;
    }
    if (*s == '\0') {
        return false;
    }
    char c0 = (char)tolower((unsigned char)s[0]);
    char c1 = (char)tolower((unsigned char)s[1]);
    char c2 = (char)tolower((unsigned char)s[2]);
    if (c0 == 'i' && c1 == 'n' && c2 == 'f') {
        *sign = sign_val;
        *cls = SOFT_CLASS_INF;
        return true;
    }
    if (c0 == 'n' && c1 == 'a' && c2 == 'n') {
        *sign = sign_val;
        *cls = SOFT_CLASS_NAN;
        return true;
    }
    return false;
}

static bool ferret_parse_float_string(const char* str, int* sign, char** digits_out, int* exp10_out) {
    if (!str || !sign || !digits_out || !exp10_out) {
        return false;
    }
    const char* s = ferret_skip_space(str);
    int sign_val = 0;
    if (*s == '+' || *s == '-') {
        sign_val = (*s == '-') ? 1 : 0;
        s++;
    }
    size_t cap = strlen(s) + 1;
    char* digits = (char*)malloc(cap);
    if (!digits) {
        return false;
    }
    size_t len = 0;
    int digits_before = 0;
    bool saw_dot = false;
    while (*s) {
        char c = *s;
        if (c == '_') {
            s++;
            continue;
        }
        if (c == '.') {
            if (saw_dot) break;
            saw_dot = true;
            s++;
            continue;
        }
        if (c >= '0' && c <= '9') {
            digits[len++] = c;
            if (!saw_dot) {
                digits_before++;
            }
            s++;
            continue;
        }
        break;
    }
    digits[len] = '\0';
    if (len == 0) {
        free(digits);
        return false;
    }
    int exp_part = 0;
    if (*s == 'e' || *s == 'E') {
        s++;
        int exp_sign = 1;
        if (*s == '+' || *s == '-') {
            exp_sign = (*s == '-') ? -1 : 1;
            s++;
        }
        while (*s >= '0' && *s <= '9') {
            exp_part = exp_part * 10 + (*s - '0');
            s++;
        }
        exp_part *= exp_sign;
    }
    int digits_after = (int)len - digits_before;
    int exp10 = exp_part - digits_after;
    size_t trim = 0;
    while (trim < len && digits[trim] == '0') {
        trim++;
    }
    if (trim == len) {
        free(digits);
        *sign = sign_val;
        *digits_out = NULL;
        *exp10_out = 0;
        return true;
    }
    if (trim > 0) {
        memmove(digits, digits + trim, len - trim + 1);
    }
    *sign = sign_val;
    *digits_out = digits;
    *exp10_out = exp10;
    return true;
}

static void big_from_decimal(ferret_big_uint* out, const char* digits) {
    big_set_u64(out, 0);
    for (const char* p = digits; *p; p++) {
        if (*p < '0' || *p > '9') {
            continue;
        }
        big_mul_small(out, 10);
        big_add_small(out, (uint32_t)(*p - '0'));
    }
}

static void big_to_words64(const ferret_big_uint* b, uint64_t* words, int word_count) {
    for (int i = 0; i < word_count; i++) {
        words[i] = 0;
    }
    size_t max_limbs = (size_t)word_count * 2;
    size_t limit = b->len < max_limbs ? b->len : max_limbs;
    for (size_t i = 0; i < limit; i++) {
        int word = (int)(i / 2);
        if (i % 2 == 0) {
            words[word] |= (uint64_t)b->limbs[i];
        } else {
            words[word] |= (uint64_t)b->limbs[i] << 32;
        }
    }
}

static void soft_float_from_decimal(const soft_float_spec* spec, const char* str, uint64_t* out_words) {
    int sign = 0;
    int exp10 = 0;
    char* digits = NULL;
    if (!ferret_parse_float_string(str, &sign, &digits, &exp10)) {
        soft_float_make_special(spec, 0, SOFT_CLASS_ZERO, out_words);
        return;
    }
    if (!digits) {
        soft_float_make_special(spec, sign, SOFT_CLASS_ZERO, out_words);
        return;
    }

    ferret_big_uint dec;
    ferret_big_uint num;
    ferret_big_uint den;
    ferret_big_uint q;
    ferret_big_uint r;
    big_init(&dec);
    big_init(&num);
    big_init(&den);
    big_init(&q);
    big_init(&r);

    big_from_decimal(&dec, digits);
    free(digits);
    if (big_is_zero(&dec)) {
        soft_float_make_special(spec, sign, SOFT_CLASS_ZERO, out_words);
        goto cleanup;
    }

    const long double log2_10 = log2l(10.0L);
    long double log2_dec = big_log2(&dec);
    long double log2_val = log2_dec + (long double)exp10 * log2_10;
    int exp2 = (int)floorl(log2_val);
    int min_sub = spec->min_exp - (spec->sig_bits - 1);
    if (exp2 > spec->max_exp) {
        soft_float_make_special(spec, sign, SOFT_CLASS_INF, out_words);
        goto cleanup;
    }
    if (exp2 < min_sub) {
        soft_float_make_special(spec, sign, SOFT_CLASS_ZERO, out_words);
        goto cleanup;
    }

    int target_exp = exp2;
    if (target_exp < spec->min_exp) {
        target_exp = spec->min_exp;
    }
    int shift = (spec->sig_bits - 1) - target_exp;
    int shift2 = exp10 + shift;

    big_copy(&num, &dec);
    big_set_u64(&den, 1);
    if (exp10 >= 0) {
        ferret_big_uint pow5;
        big_init(&pow5);
        big_pow5((uint32_t)exp10, &pow5);
        ferret_big_uint tmp;
        big_init(&tmp);
        big_mul(&num, &pow5, &tmp);
        big_free(&num);
        num = tmp;
        big_free(&pow5);
    } else {
        ferret_big_uint pow5;
        big_init(&pow5);
        big_pow5((uint32_t)(-exp10), &pow5);
        big_free(&den);
        den = pow5;
    }
    if (shift2 >= 0) {
        big_shift_left_bits(&num, shift2);
    } else {
        big_shift_left_bits(&den, -shift2);
    }

    big_div_mod(&num, &den, &q, &r);
    if (!big_is_zero(&r)) {
        ferret_big_uint r2;
        big_init(&r2);
        big_copy(&r2, &r);
        big_mul_small(&r2, 2);
        int cmp = big_cmp(&r2, &den);
        if (cmp > 0 || (cmp == 0 && (q.len > 0 && (q.limbs[0] & 1u)))) {
            big_add_small(&q, 1);
        }
        big_free(&r2);
    }

    int q_bits = big_bit_length(&q);
    if (q_bits > spec->sig_bits) {
        big_shift_right_bits(&q, 1);
        target_exp += 1;
    }
    if (target_exp > spec->max_exp) {
        soft_float_make_special(spec, sign, SOFT_CLASS_INF, out_words);
        goto cleanup;
    }

    uint64_t* sig = (uint64_t*)malloc((size_t)spec->words * sizeof(uint64_t));
    if (!sig) {
        soft_float_make_special(spec, sign, SOFT_CLASS_ZERO, out_words);
        goto cleanup;
    }
    big_to_words64(&q, sig, spec->words);
    soft_u64_shift_left(sig, spec->words, SOFT_EXTRA_BITS, sig);
    soft_float_pack(spec, sign, target_exp, sig, SOFT_CLASS_NORMAL, out_words);
    free(sig);

cleanup:
    big_free(&dec);
    big_free(&num);
    big_free(&den);
    big_free(&q);
    big_free(&r);
}

static char* soft_float_to_string(const soft_float_spec* spec, const uint64_t* words) {
    size_t wcount = (size_t)spec->words;
    uint64_t* buf = (uint64_t*)malloc(wcount * 2 * sizeof(uint64_t));
    if (!buf) {
        return NULL;
    }
    uint64_t* sig = buf;
    uint64_t* sig_raw = buf + wcount;
    int sign = 0;
    int exp = 0;
    soft_class_t cls = soft_float_unpack(spec, words, &sign, &exp, sig);
    soft_u64_shift_right(sig, spec->words, SOFT_EXTRA_BITS, sig_raw);
    char* out = soft_format_decimal(sign, exp, sig_raw, spec->words, spec->sig_bits, cls, spec->decimal_dig);
    free(buf);
    return out;
}

static void soft_float_from_string(const soft_float_spec* spec, const char* str, uint64_t* out_words) {
    if (!str) {
        soft_float_make_special(spec, 0, SOFT_CLASS_ZERO, out_words);
        return;
    }
    int sign = 0;
    soft_class_t cls = SOFT_CLASS_ZERO;
    if (ferret_parse_special_float(str, &sign, &cls)) {
        soft_float_make_special(spec, sign, cls, out_words);
        return;
    }
    soft_float_from_decimal(spec, str, out_words);
}

static long double soft_float_pow_long_double(long double base, long double exp) {
#if defined(_MSC_VER)
    return (long double)pow((double)base, (double)exp);
#else
    return powl(base, exp);
#endif
}

#define FERRET_DEFINE_FLOAT_API(BITS, WORDS, FRAC_BITS, EXP_BITS, EXP_BIAS, DEC_DIG) \
    ferret_f##BITS ferret_f##BITS##_add(ferret_f##BITS a, ferret_f##BITS b) { \
        ferret_f##BITS out; \
        soft_float_add(&soft_f##BITS##_spec, a.words, b.words, out.words, false); \
        return out; \
    } \
    ferret_f##BITS ferret_f##BITS##_sub(ferret_f##BITS a, ferret_f##BITS b) { \
        ferret_f##BITS out; \
        soft_float_add(&soft_f##BITS##_spec, a.words, b.words, out.words, true); \
        return out; \
    } \
    ferret_f##BITS ferret_f##BITS##_mul(ferret_f##BITS a, ferret_f##BITS b) { \
        ferret_f##BITS out; \
        soft_float_mul(&soft_f##BITS##_spec, a.words, b.words, out.words); \
        return out; \
    } \
    ferret_f##BITS ferret_f##BITS##_div(ferret_f##BITS a, ferret_f##BITS b) { \
        ferret_f##BITS out; \
        soft_float_div(&soft_f##BITS##_spec, a.words, b.words, out.words); \
        return out; \
    } \
    bool ferret_f##BITS##_eq(ferret_f##BITS a, ferret_f##BITS b) { \
        bool unordered = false; \
        int cmp = soft_float_compare(&soft_f##BITS##_spec, a.words, b.words, &unordered); \
        return !unordered && cmp == 0; \
    } \
    bool ferret_f##BITS##_lt(ferret_f##BITS a, ferret_f##BITS b) { \
        bool unordered = false; \
        int cmp = soft_float_compare(&soft_f##BITS##_spec, a.words, b.words, &unordered); \
        return !unordered && cmp < 0; \
    } \
    bool ferret_f##BITS##_gt(ferret_f##BITS a, ferret_f##BITS b) { \
        bool unordered = false; \
        int cmp = soft_float_compare(&soft_f##BITS##_spec, a.words, b.words, &unordered); \
        return !unordered && cmp > 0; \
    } \
    ferret_f##BITS ferret_f##BITS##_from_f64(double val) { \
        ferret_f##BITS out; \
        soft_float_from_f64(&soft_f##BITS##_spec, val, out.words); \
        return out; \
    } \
    double ferret_f##BITS##_to_f64(ferret_f##BITS val) { \
        return soft_float_to_f64(&soft_f##BITS##_spec, val.words); \
    } \
    char* ferret_f##BITS##_to_string(ferret_f##BITS val) { \
        return soft_float_to_string(&soft_f##BITS##_spec, val.words); \
    } \
    ferret_f##BITS ferret_f##BITS##_from_string(const char* str) { \
        ferret_f##BITS out; \
        soft_float_from_string(&soft_f##BITS##_spec, str, out.words); \
        return out; \
    } \
    ferret_f##BITS ferret_f##BITS##_pow(ferret_f##BITS base, ferret_f##BITS exp) { \
        double base_val = ferret_f##BITS##_to_f64(base); \
        double exp_val = ferret_f##BITS##_to_f64(exp); \
        double result = pow(base_val, exp_val); \
        if (isfinite(base_val) && isfinite(exp_val) && isfinite(result)) { \
            return ferret_f##BITS##_from_f64(result); \
        } \
        char* base_str = ferret_f##BITS##_to_string(base); \
        char* exp_str = ferret_f##BITS##_to_string(exp); \
        if (!base_str || !exp_str) { \
            free(base_str); \
            free(exp_str); \
            return ferret_f##BITS##_from_f64(result); \
        } \
        long double base_ld = strtold(base_str, NULL); \
        long double exp_ld = strtold(exp_str, NULL); \
        free(base_str); \
        free(exp_str); \
        long double result_ld = soft_float_pow_long_double(base_ld, exp_ld); \
        char buf[DEC_DIG + 64]; \
        snprintf(buf, sizeof(buf), "%.*Lg", DEC_DIG, result_ld); \
        return ferret_f##BITS##_from_string(buf); \
    }

FERRET_FLOAT_SPECS(FERRET_DEFINE_FLOAT_API)
#undef FERRET_DEFINE_FLOAT_API
void ferret_memcpy(void* dst, const void* src, uint64_t size) {
    if (!dst || !src || size == 0) {
        return;
    }
    memcpy(dst, src, (size_t)size);
}

#define FERRET_PTR_BIN_OP(type, func) \
    void func##_ptr(const type* a, const type* b, type* out) { \
        if (!out || !a || !b) return; \
        *out = func(*a, *b); \
    }

#define FERRET_PTR_CMP_OP(type, func) \
    bool func##_ptr(const type* a, const type* b) { \
        if (!a || !b) return false; \
        return func(*a, *b); \
    }

#define FERRET_PTR_UNARY_OP(type, func) \
    void func##_ptr(const type* a, type* out) { \
        if (!out || !a) return; \
        *out = func(*a); \
    }

#define FERRET_DEFINE_INT_PTR_OPS(BITS) \
    FERRET_PTR_BIN_OP(ferret_i##BITS, ferret_i##BITS##_add) \
    FERRET_PTR_BIN_OP(ferret_i##BITS, ferret_i##BITS##_sub) \
    FERRET_PTR_BIN_OP(ferret_i##BITS, ferret_i##BITS##_mul) \
    FERRET_PTR_BIN_OP(ferret_i##BITS, ferret_i##BITS##_div) \
    FERRET_PTR_BIN_OP(ferret_i##BITS, ferret_i##BITS##_mod) \
    FERRET_PTR_CMP_OP(ferret_i##BITS, ferret_i##BITS##_eq) \
    FERRET_PTR_CMP_OP(ferret_i##BITS, ferret_i##BITS##_lt) \
    FERRET_PTR_CMP_OP(ferret_i##BITS, ferret_i##BITS##_gt) \
    FERRET_PTR_BIN_OP(ferret_i##BITS, ferret_i##BITS##_and) \
    FERRET_PTR_BIN_OP(ferret_i##BITS, ferret_i##BITS##_or) \
    FERRET_PTR_BIN_OP(ferret_i##BITS, ferret_i##BITS##_xor) \
    FERRET_PTR_UNARY_OP(ferret_i##BITS, ferret_i##BITS##_not) \
    FERRET_PTR_BIN_OP(ferret_i##BITS, ferret_i##BITS##_pow) \
    FERRET_PTR_BIN_OP(ferret_u##BITS, ferret_u##BITS##_add) \
    FERRET_PTR_BIN_OP(ferret_u##BITS, ferret_u##BITS##_sub) \
    FERRET_PTR_BIN_OP(ferret_u##BITS, ferret_u##BITS##_mul) \
    FERRET_PTR_BIN_OP(ferret_u##BITS, ferret_u##BITS##_div) \
    FERRET_PTR_BIN_OP(ferret_u##BITS, ferret_u##BITS##_mod) \
    FERRET_PTR_CMP_OP(ferret_u##BITS, ferret_u##BITS##_eq) \
    FERRET_PTR_CMP_OP(ferret_u##BITS, ferret_u##BITS##_lt) \
    FERRET_PTR_CMP_OP(ferret_u##BITS, ferret_u##BITS##_gt) \
    FERRET_PTR_BIN_OP(ferret_u##BITS, ferret_u##BITS##_and) \
    FERRET_PTR_BIN_OP(ferret_u##BITS, ferret_u##BITS##_or) \
    FERRET_PTR_BIN_OP(ferret_u##BITS, ferret_u##BITS##_xor) \
    FERRET_PTR_UNARY_OP(ferret_u##BITS, ferret_u##BITS##_not) \
    FERRET_PTR_BIN_OP(ferret_u##BITS, ferret_u##BITS##_pow)
    
FERRET_INT_WIDTHS(FERRET_DEFINE_INT_PTR_OPS)
#undef FERRET_DEFINE_INT_PTR_OPS

#define FERRET_DEFINE_FLOAT_PTR_OPS(BITS, WORDS, FRAC_BITS, EXP_BITS, EXP_BIAS, DEC_DIG) \
    FERRET_PTR_BIN_OP(ferret_f##BITS, ferret_f##BITS##_add) \
    FERRET_PTR_BIN_OP(ferret_f##BITS, ferret_f##BITS##_sub) \
    FERRET_PTR_BIN_OP(ferret_f##BITS, ferret_f##BITS##_mul) \
    FERRET_PTR_BIN_OP(ferret_f##BITS, ferret_f##BITS##_div) \
    FERRET_PTR_CMP_OP(ferret_f##BITS, ferret_f##BITS##_eq) \
    FERRET_PTR_CMP_OP(ferret_f##BITS, ferret_f##BITS##_lt) \
    FERRET_PTR_CMP_OP(ferret_f##BITS, ferret_f##BITS##_gt) \
    FERRET_PTR_BIN_OP(ferret_f##BITS, ferret_f##BITS##_pow)

FERRET_FLOAT_SPECS(FERRET_DEFINE_FLOAT_PTR_OPS)
#undef FERRET_DEFINE_FLOAT_PTR_OPS

#define FERRET_DEFINE_INT_PTR_CONV(BITS) \
    void ferret_i##BITS##_from_i64_ptr(int64_t val, ferret_i##BITS* out) { \
        if (!out) return; \
        *out = ferret_i##BITS##_from_i64(val); \
    } \
    void ferret_u##BITS##_from_u64_ptr(uint64_t val, ferret_u##BITS* out) { \
        if (!out) return; \
        *out = ferret_u##BITS##_from_u64(val); \
    } \
    int64_t ferret_i##BITS##_to_i64_ptr(const ferret_i##BITS* val) { \
        if (!val) return 0; \
        return ferret_i##BITS##_to_i64(*val); \
    } \
    uint64_t ferret_u##BITS##_to_u64_ptr(const ferret_u##BITS* val) { \
        if (!val) return 0; \
        return ferret_u##BITS##_to_u64(*val); \
    } \
    char* ferret_i##BITS##_to_string_ptr(const ferret_i##BITS* val) { \
        if (!val) return NULL; \
        return ferret_i##BITS##_to_string(*val); \
    } \
    char* ferret_u##BITS##_to_string_ptr(const ferret_u##BITS* val) { \
        if (!val) return NULL; \
        return ferret_u##BITS##_to_string(*val); \
    } \
    void ferret_i##BITS##_from_string_ptr(const char* str, ferret_i##BITS* out) { \
        if (!out) return; \
        *out = ferret_i##BITS##_from_string(str); \
    } \
    void ferret_u##BITS##_from_string_ptr(const char* str, ferret_u##BITS* out) { \
        if (!out) return; \
        *out = ferret_u##BITS##_from_string(str); \
    }

FERRET_INT_WIDTHS(FERRET_DEFINE_INT_PTR_CONV)
#undef FERRET_DEFINE_INT_PTR_CONV

#define FERRET_DEFINE_FLOAT_PTR_CONV(BITS, WORDS, FRAC_BITS, EXP_BITS, EXP_BIAS, DEC_DIG) \
    void ferret_f##BITS##_from_f64_ptr(double val, ferret_f##BITS* out) { \
        if (!out) return; \
        *out = ferret_f##BITS##_from_f64(val); \
    } \
    double ferret_f##BITS##_to_f64_ptr(const ferret_f##BITS* val) { \
        if (!val) return 0.0; \
        return ferret_f##BITS##_to_f64(*val); \
    } \
    char* ferret_f##BITS##_to_string_ptr(const ferret_f##BITS* val) { \
        if (!val) return NULL; \
        return ferret_f##BITS##_to_string(*val); \
    } \
    void ferret_f##BITS##_from_string_ptr(const char* str, ferret_f##BITS* out) { \
        if (!out) return; \
        *out = ferret_f##BITS##_from_string(str); \
    }

FERRET_FLOAT_SPECS(FERRET_DEFINE_FLOAT_PTR_CONV)
#undef FERRET_DEFINE_FLOAT_PTR_CONV

#undef FERRET_PTR_BIN_OP
#undef FERRET_PTR_CMP_OP
#undef FERRET_PTR_UNARY_OP
