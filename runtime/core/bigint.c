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

    char digits[80];
    int len = 0;
    ferret_limb_t work[FERRET_U256_LIMBS];
    ferret_limb_t quot[FERRET_U256_LIMBS];

    ferret_zero_limbs(work, FERRET_U256_LIMBS);
    ferret_zero_limbs(quot, FERRET_U256_LIMBS);
    ferret_copy_limbs(work, val, n);

    while (!ferret_is_zero_limbs(work, n)) {
        uint32_t rem = ferret_div_small_limbs(work, quot, n, 10);
        digits[len++] = (char)('0' + rem);
        ferret_copy_limbs(work, quot, n);
    }

    char* out = (char*)malloc((size_t)len + 1);
    if (!out) return NULL;
    for (int i = 0; i < len; i++) {
        out[i] = digits[len - 1 - i];
    }
    out[len] = '\0';
    return out;
}

ferret_i128 ferret_i128_add(ferret_i128 a, ferret_i128 b) {
    ferret_i128 result;
    ferret_add_limbs(a.words, b.words, result.words, FERRET_U128_LIMBS);
    return result;
}

ferret_i128 ferret_i128_sub(ferret_i128 a, ferret_i128 b) {
    ferret_i128 result;
    ferret_sub_limbs(a.words, b.words, result.words, FERRET_U128_LIMBS);
    return result;
}

ferret_i128 ferret_i128_mul(ferret_i128 a, ferret_i128 b) {
    bool neg_a = false;
    bool neg_b = false;
    ferret_u128 a_mag;
    ferret_u128 b_mag;
    ferret_abs_limbs(a.words, a_mag.words, FERRET_U128_LIMBS, &neg_a);
    ferret_abs_limbs(b.words, b_mag.words, FERRET_U128_LIMBS, &neg_b);

    ferret_u128 mag;
    ferret_mul_limbs(a_mag.words, b_mag.words, mag.words, FERRET_U128_LIMBS);

    ferret_i128 result;
    ferret_copy_limbs(result.words, mag.words, FERRET_U128_LIMBS);
    if (neg_a != neg_b) {
        ferret_negate_limbs(result.words, FERRET_U128_LIMBS);
    }
    return result;
}

ferret_i128 ferret_i128_div(ferret_i128 a, ferret_i128 b) {
    bool neg_a = false;
    bool neg_b = false;
    ferret_u128 a_mag;
    ferret_u128 b_mag;
    ferret_abs_limbs(a.words, a_mag.words, FERRET_U128_LIMBS, &neg_a);
    ferret_abs_limbs(b.words, b_mag.words, FERRET_U128_LIMBS, &neg_b);

    ferret_u128 quot;
    ferret_u128 rem;
    if (!ferret_div_mod_u_limbs(a_mag.words, b_mag.words, quot.words, rem.words, FERRET_U128_LIMBS)) {
        ferret_zero_limbs(quot.words, FERRET_U128_LIMBS);
    }

    ferret_i128 result;
    ferret_copy_limbs(result.words, quot.words, FERRET_U128_LIMBS);
    if (neg_a != neg_b) {
        ferret_negate_limbs(result.words, FERRET_U128_LIMBS);
    }
    return result;
}

ferret_i128 ferret_i128_mod(ferret_i128 a, ferret_i128 b) {
    bool neg_a = false;
    bool neg_b = false;
    ferret_u128 a_mag;
    ferret_u128 b_mag;
    ferret_abs_limbs(a.words, a_mag.words, FERRET_U128_LIMBS, &neg_a);
    ferret_abs_limbs(b.words, b_mag.words, FERRET_U128_LIMBS, &neg_b);

    ferret_u128 quot;
    ferret_u128 rem;
    if (!ferret_div_mod_u_limbs(a_mag.words, b_mag.words, quot.words, rem.words, FERRET_U128_LIMBS)) {
        ferret_zero_limbs(rem.words, FERRET_U128_LIMBS);
    }

    ferret_i128 result;
    ferret_copy_limbs(result.words, rem.words, FERRET_U128_LIMBS);
    if (neg_a) {
        ferret_negate_limbs(result.words, FERRET_U128_LIMBS);
    }
    (void)neg_b;
    return result;
}

bool ferret_i128_eq(ferret_i128 a, ferret_i128 b) {
    return memcmp(a.words, b.words, sizeof(a.words)) == 0;
}

bool ferret_i128_lt(ferret_i128 a, ferret_i128 b) {
    return ferret_cmp_s_limbs(a.words, b.words, FERRET_U128_LIMBS) < 0;
}

bool ferret_i128_gt(ferret_i128 a, ferret_i128 b) {
    return ferret_cmp_s_limbs(a.words, b.words, FERRET_U128_LIMBS) > 0;
}

ferret_u128 ferret_u128_add(ferret_u128 a, ferret_u128 b) {
    ferret_u128 result;
    ferret_add_limbs(a.words, b.words, result.words, FERRET_U128_LIMBS);
    return result;
}

ferret_u128 ferret_u128_sub(ferret_u128 a, ferret_u128 b) {
    ferret_u128 result;
    ferret_sub_limbs(a.words, b.words, result.words, FERRET_U128_LIMBS);
    return result;
}

ferret_u128 ferret_u128_mul(ferret_u128 a, ferret_u128 b) {
    ferret_u128 result;
    ferret_mul_limbs(a.words, b.words, result.words, FERRET_U128_LIMBS);
    return result;
}

ferret_u128 ferret_u128_div(ferret_u128 a, ferret_u128 b) {
    ferret_u128 result;
    ferret_u128 rem;
    if (!ferret_div_mod_u_limbs(a.words, b.words, result.words, rem.words, FERRET_U128_LIMBS)) {
        ferret_zero_limbs(result.words, FERRET_U128_LIMBS);
    }
    return result;
}

ferret_u128 ferret_u128_mod(ferret_u128 a, ferret_u128 b) {
    ferret_u128 quot;
    ferret_u128 rem;
    if (!ferret_div_mod_u_limbs(a.words, b.words, quot.words, rem.words, FERRET_U128_LIMBS)) {
        ferret_zero_limbs(rem.words, FERRET_U128_LIMBS);
    }
    return rem;
}

bool ferret_u128_eq(ferret_u128 a, ferret_u128 b) {
    return memcmp(a.words, b.words, sizeof(a.words)) == 0;
}

bool ferret_u128_lt(ferret_u128 a, ferret_u128 b) {
    return ferret_cmp_u_limbs(a.words, b.words, FERRET_U128_LIMBS) < 0;
}

bool ferret_u128_gt(ferret_u128 a, ferret_u128 b) {
    return ferret_cmp_u_limbs(a.words, b.words, FERRET_U128_LIMBS) > 0;
}

ferret_i256 ferret_i256_add(ferret_i256 a, ferret_i256 b) {
    ferret_i256 result;
    ferret_add_limbs(a.words, b.words, result.words, FERRET_U256_LIMBS);
    return result;
}

ferret_i256 ferret_i256_sub(ferret_i256 a, ferret_i256 b) {
    ferret_i256 result;
    ferret_sub_limbs(a.words, b.words, result.words, FERRET_U256_LIMBS);
    return result;
}

ferret_i256 ferret_i256_mul(ferret_i256 a, ferret_i256 b) {
    bool neg_a = false;
    bool neg_b = false;
    ferret_u256 a_mag;
    ferret_u256 b_mag;
    ferret_abs_limbs(a.words, a_mag.words, FERRET_U256_LIMBS, &neg_a);
    ferret_abs_limbs(b.words, b_mag.words, FERRET_U256_LIMBS, &neg_b);

    ferret_u256 mag;
    ferret_mul_limbs(a_mag.words, b_mag.words, mag.words, FERRET_U256_LIMBS);

    ferret_i256 result;
    ferret_copy_limbs(result.words, mag.words, FERRET_U256_LIMBS);
    if (neg_a != neg_b) {
        ferret_negate_limbs(result.words, FERRET_U256_LIMBS);
    }
    return result;
}

ferret_i256 ferret_i256_div(ferret_i256 a, ferret_i256 b) {
    bool neg_a = false;
    bool neg_b = false;
    ferret_u256 a_mag;
    ferret_u256 b_mag;
    ferret_abs_limbs(a.words, a_mag.words, FERRET_U256_LIMBS, &neg_a);
    ferret_abs_limbs(b.words, b_mag.words, FERRET_U256_LIMBS, &neg_b);

    ferret_u256 quot;
    ferret_u256 rem;
    if (!ferret_div_mod_u_limbs(a_mag.words, b_mag.words, quot.words, rem.words, FERRET_U256_LIMBS)) {
        ferret_zero_limbs(quot.words, FERRET_U256_LIMBS);
    }

    ferret_i256 result;
    ferret_copy_limbs(result.words, quot.words, FERRET_U256_LIMBS);
    if (neg_a != neg_b) {
        ferret_negate_limbs(result.words, FERRET_U256_LIMBS);
    }
    return result;
}

ferret_i256 ferret_i256_mod(ferret_i256 a, ferret_i256 b) {
    bool neg_a = false;
    bool neg_b = false;
    ferret_u256 a_mag;
    ferret_u256 b_mag;
    ferret_abs_limbs(a.words, a_mag.words, FERRET_U256_LIMBS, &neg_a);
    ferret_abs_limbs(b.words, b_mag.words, FERRET_U256_LIMBS, &neg_b);

    ferret_u256 quot;
    ferret_u256 rem;
    if (!ferret_div_mod_u_limbs(a_mag.words, b_mag.words, quot.words, rem.words, FERRET_U256_LIMBS)) {
        ferret_zero_limbs(rem.words, FERRET_U256_LIMBS);
    }

    ferret_i256 result;
    ferret_copy_limbs(result.words, rem.words, FERRET_U256_LIMBS);
    if (neg_a) {
        ferret_negate_limbs(result.words, FERRET_U256_LIMBS);
    }
    (void)neg_b;
    return result;
}

bool ferret_i256_eq(ferret_i256 a, ferret_i256 b) {
    return memcmp(a.words, b.words, sizeof(a.words)) == 0;
}

bool ferret_i256_lt(ferret_i256 a, ferret_i256 b) {
    return ferret_cmp_s_limbs(a.words, b.words, FERRET_U256_LIMBS) < 0;
}

bool ferret_i256_gt(ferret_i256 a, ferret_i256 b) {
    return ferret_cmp_s_limbs(a.words, b.words, FERRET_U256_LIMBS) > 0;
}

ferret_u256 ferret_u256_add(ferret_u256 a, ferret_u256 b) {
    ferret_u256 result;
    ferret_add_limbs(a.words, b.words, result.words, FERRET_U256_LIMBS);
    return result;
}

ferret_u256 ferret_u256_sub(ferret_u256 a, ferret_u256 b) {
    ferret_u256 result;
    ferret_sub_limbs(a.words, b.words, result.words, FERRET_U256_LIMBS);
    return result;
}

ferret_u256 ferret_u256_mul(ferret_u256 a, ferret_u256 b) {
    ferret_u256 result;
    ferret_mul_limbs(a.words, b.words, result.words, FERRET_U256_LIMBS);
    return result;
}

ferret_u256 ferret_u256_div(ferret_u256 a, ferret_u256 b) {
    ferret_u256 result;
    ferret_u256 rem;
    if (!ferret_div_mod_u_limbs(a.words, b.words, result.words, rem.words, FERRET_U256_LIMBS)) {
        ferret_zero_limbs(result.words, FERRET_U256_LIMBS);
    }
    return result;
}

ferret_u256 ferret_u256_mod(ferret_u256 a, ferret_u256 b) {
    ferret_u256 quot;
    ferret_u256 rem;
    if (!ferret_div_mod_u_limbs(a.words, b.words, quot.words, rem.words, FERRET_U256_LIMBS)) {
        ferret_zero_limbs(rem.words, FERRET_U256_LIMBS);
    }
    return rem;
}

bool ferret_u256_eq(ferret_u256 a, ferret_u256 b) {
    return memcmp(a.words, b.words, sizeof(a.words)) == 0;
}

bool ferret_u256_lt(ferret_u256 a, ferret_u256 b) {
    return ferret_cmp_u_limbs(a.words, b.words, FERRET_U256_LIMBS) < 0;
}

bool ferret_u256_gt(ferret_u256 a, ferret_u256 b) {
    return ferret_cmp_u_limbs(a.words, b.words, FERRET_U256_LIMBS) > 0;
}

// Power functions using binary exponentiation (exponentiation by squaring)
// For integer bases: base^exp where exp is treated as unsigned

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

ferret_i128 ferret_i128_pow(ferret_i128 base, ferret_i128 exp) {
    // Handle negative exponent: return 0 (integer division truncates toward zero)
    ferret_i128 zero;
    ferret_zero_limbs(zero.words, FERRET_U128_LIMBS);
    if (ferret_cmp_s_limbs(exp.words, zero.words, FERRET_U128_LIMBS) < 0) {
        return zero;
    }
    
    // result = 1
    ferret_i128 result;
    ferret_limbs_from_i64(result.words, FERRET_U128_LIMBS, 1);
    
    // exp_copy for iteration
    ferret_i128 exp_copy = exp;
    
    // Binary exponentiation
    while (!ferret_is_zero_limbs(exp_copy.words, FERRET_U128_LIMBS)) {
        if (ferret_is_odd_limbs(exp_copy.words)) {
            result = ferret_i128_mul(result, base);
        }
        base = ferret_i128_mul(base, base);
        ferret_shr1_limbs(exp_copy.words, FERRET_U128_LIMBS);
    }
    
    return result;
}

ferret_u128 ferret_u128_pow(ferret_u128 base, ferret_u128 exp) {
    // result = 1
    ferret_u128 result;
    ferret_limbs_from_u64(result.words, FERRET_U128_LIMBS, 1);
    
    // exp_copy for iteration
    ferret_u128 exp_copy = exp;
    
    // Binary exponentiation
    while (!ferret_is_zero_limbs(exp_copy.words, FERRET_U128_LIMBS)) {
        if (ferret_is_odd_limbs(exp_copy.words)) {
            result = ferret_u128_mul(result, base);
        }
        base = ferret_u128_mul(base, base);
        ferret_shr1_limbs(exp_copy.words, FERRET_U128_LIMBS);
    }
    
    return result;
}

ferret_i256 ferret_i256_pow(ferret_i256 base, ferret_i256 exp) {
    // Handle negative exponent: return 0
    ferret_i256 zero;
    ferret_zero_limbs(zero.words, FERRET_U256_LIMBS);
    if (ferret_cmp_s_limbs(exp.words, zero.words, FERRET_U256_LIMBS) < 0) {
        return zero;
    }
    
    // result = 1
    ferret_i256 result;
    ferret_limbs_from_i64(result.words, FERRET_U256_LIMBS, 1);
    
    // exp_copy for iteration
    ferret_i256 exp_copy = exp;
    
    // Binary exponentiation
    while (!ferret_is_zero_limbs(exp_copy.words, FERRET_U256_LIMBS)) {
        if (ferret_is_odd_limbs(exp_copy.words)) {
            result = ferret_i256_mul(result, base);
        }
        base = ferret_i256_mul(base, base);
        ferret_shr1_limbs(exp_copy.words, FERRET_U256_LIMBS);
    }
    
    return result;
}

ferret_u256 ferret_u256_pow(ferret_u256 base, ferret_u256 exp) {
    // result = 1
    ferret_u256 result;
    ferret_limbs_from_u64(result.words, FERRET_U256_LIMBS, 1);
    
    // exp_copy for iteration
    ferret_u256 exp_copy = exp;
    
    // Binary exponentiation
    while (!ferret_is_zero_limbs(exp_copy.words, FERRET_U256_LIMBS)) {
        if (ferret_is_odd_limbs(exp_copy.words)) {
            result = ferret_u256_mul(result, base);
        }
        base = ferret_u256_mul(base, base);
        ferret_shr1_limbs(exp_copy.words, FERRET_U256_LIMBS);
    }
    
    return result;
}

ferret_i128 ferret_i128_and(ferret_i128 a, ferret_i128 b) {
    ferret_i128 result;
    for (int i = 0; i < FERRET_U128_LIMBS; i++) {
        result.words[i] = (ferret_limb_t)(a.words[i] & b.words[i]);
    }
    return result;
}

ferret_i128 ferret_i128_or(ferret_i128 a, ferret_i128 b) {
    ferret_i128 result;
    for (int i = 0; i < FERRET_U128_LIMBS; i++) {
        result.words[i] = (ferret_limb_t)(a.words[i] | b.words[i]);
    }
    return result;
}

ferret_i128 ferret_i128_xor(ferret_i128 a, ferret_i128 b) {
    ferret_i128 result;
    for (int i = 0; i < FERRET_U128_LIMBS; i++) {
        result.words[i] = (ferret_limb_t)(a.words[i] ^ b.words[i]);
    }
    return result;
}

ferret_i128 ferret_i128_not(ferret_i128 a) {
    ferret_i128 result;
    for (int i = 0; i < FERRET_U128_LIMBS; i++) {
        result.words[i] = (ferret_limb_t)~a.words[i];
    }
    return result;
}

ferret_i128 ferret_i128_shl(ferret_i128 a, int n) {
    ferret_i128 result;
    ferret_shift_left_limbs(a.words, FERRET_U128_LIMBS, n, result.words);
    return result;
}

ferret_i128 ferret_i128_shr(ferret_i128 a, int n) {
    ferret_i128 result;
    ferret_shift_right_signed_limbs(a.words, FERRET_U128_LIMBS, n, result.words);
    return result;
}

ferret_u128 ferret_u128_and(ferret_u128 a, ferret_u128 b) {
    ferret_u128 result;
    for (int i = 0; i < FERRET_U128_LIMBS; i++) {
        result.words[i] = (ferret_limb_t)(a.words[i] & b.words[i]);
    }
    return result;
}

ferret_u128 ferret_u128_or(ferret_u128 a, ferret_u128 b) {
    ferret_u128 result;
    for (int i = 0; i < FERRET_U128_LIMBS; i++) {
        result.words[i] = (ferret_limb_t)(a.words[i] | b.words[i]);
    }
    return result;
}

ferret_u128 ferret_u128_xor(ferret_u128 a, ferret_u128 b) {
    ferret_u128 result;
    for (int i = 0; i < FERRET_U128_LIMBS; i++) {
        result.words[i] = (ferret_limb_t)(a.words[i] ^ b.words[i]);
    }
    return result;
}

ferret_u128 ferret_u128_not(ferret_u128 a) {
    ferret_u128 result;
    for (int i = 0; i < FERRET_U128_LIMBS; i++) {
        result.words[i] = (ferret_limb_t)~a.words[i];
    }
    return result;
}

ferret_u128 ferret_u128_shl(ferret_u128 a, int n) {
    ferret_u128 result;
    ferret_shift_left_limbs(a.words, FERRET_U128_LIMBS, n, result.words);
    return result;
}

ferret_u128 ferret_u128_shr(ferret_u128 a, int n) {
    ferret_u128 result;
    ferret_shift_right_limbs(a.words, FERRET_U128_LIMBS, n, result.words);
    return result;
}

ferret_i256 ferret_i256_and(ferret_i256 a, ferret_i256 b) {
    ferret_i256 result;
    for (int i = 0; i < FERRET_U256_LIMBS; i++) {
        result.words[i] = (ferret_limb_t)(a.words[i] & b.words[i]);
    }
    return result;
}

ferret_i256 ferret_i256_or(ferret_i256 a, ferret_i256 b) {
    ferret_i256 result;
    for (int i = 0; i < FERRET_U256_LIMBS; i++) {
        result.words[i] = (ferret_limb_t)(a.words[i] | b.words[i]);
    }
    return result;
}

ferret_i256 ferret_i256_xor(ferret_i256 a, ferret_i256 b) {
    ferret_i256 result;
    for (int i = 0; i < FERRET_U256_LIMBS; i++) {
        result.words[i] = (ferret_limb_t)(a.words[i] ^ b.words[i]);
    }
    return result;
}

ferret_i256 ferret_i256_not(ferret_i256 a) {
    ferret_i256 result;
    for (int i = 0; i < FERRET_U256_LIMBS; i++) {
        result.words[i] = (ferret_limb_t)~a.words[i];
    }
    return result;
}

ferret_i256 ferret_i256_shl(ferret_i256 a, int n) {
    ferret_i256 result;
    ferret_shift_left_limbs(a.words, FERRET_U256_LIMBS, n, result.words);
    return result;
}

ferret_i256 ferret_i256_shr(ferret_i256 a, int n) {
    ferret_i256 result;
    ferret_shift_right_signed_limbs(a.words, FERRET_U256_LIMBS, n, result.words);
    return result;
}

ferret_u256 ferret_u256_and(ferret_u256 a, ferret_u256 b) {
    ferret_u256 result;
    for (int i = 0; i < FERRET_U256_LIMBS; i++) {
        result.words[i] = (ferret_limb_t)(a.words[i] & b.words[i]);
    }
    return result;
}

ferret_u256 ferret_u256_or(ferret_u256 a, ferret_u256 b) {
    ferret_u256 result;
    for (int i = 0; i < FERRET_U256_LIMBS; i++) {
        result.words[i] = (ferret_limb_t)(a.words[i] | b.words[i]);
    }
    return result;
}

ferret_u256 ferret_u256_xor(ferret_u256 a, ferret_u256 b) {
    ferret_u256 result;
    for (int i = 0; i < FERRET_U256_LIMBS; i++) {
        result.words[i] = (ferret_limb_t)(a.words[i] ^ b.words[i]);
    }
    return result;
}

ferret_u256 ferret_u256_not(ferret_u256 a) {
    ferret_u256 result;
    for (int i = 0; i < FERRET_U256_LIMBS; i++) {
        result.words[i] = (ferret_limb_t)~a.words[i];
    }
    return result;
}

ferret_u256 ferret_u256_shl(ferret_u256 a, int n) {
    ferret_u256 result;
    ferret_shift_left_limbs(a.words, FERRET_U256_LIMBS, n, result.words);
    return result;
}

ferret_u256 ferret_u256_shr(ferret_u256 a, int n) {
    ferret_u256 result;
    ferret_shift_right_limbs(a.words, FERRET_U256_LIMBS, n, result.words);
    return result;
}

// Conversion functions
ferret_i128 ferret_i128_from_i64(int64_t val) {
    ferret_i128 result;
    ferret_limbs_from_i64(result.words, FERRET_U128_LIMBS, val);
    return result;
}

ferret_u128 ferret_u128_from_u64(uint64_t val) {
    ferret_u128 result;
    ferret_limbs_from_u64(result.words, FERRET_U128_LIMBS, val);
    return result;
}

ferret_i256 ferret_i256_from_i64(int64_t val) {
    ferret_i256 result;
    ferret_limbs_from_i64(result.words, FERRET_U256_LIMBS, val);
    return result;
}

ferret_u256 ferret_u256_from_u64(uint64_t val) {
    ferret_u256 result;
    ferret_limbs_from_u64(result.words, FERRET_U256_LIMBS, val);
    return result;
}

int64_t ferret_i128_to_i64(ferret_i128 val) {
    return (int64_t)ferret_limbs_to_u64(val.words, FERRET_U128_LIMBS);
}

uint64_t ferret_u128_to_u64(ferret_u128 val) {
    return ferret_limbs_to_u64(val.words, FERRET_U128_LIMBS);
}

int64_t ferret_i256_to_i64(ferret_i256 val) {
    return (int64_t)ferret_limbs_to_u64(val.words, FERRET_U256_LIMBS);
}

uint64_t ferret_u256_to_u64(ferret_u256 val) {
    return ferret_limbs_to_u64(val.words, FERRET_U256_LIMBS);
}

// String conversion functions
char* ferret_u128_to_string(ferret_u128 val) {
    return ferret_limbs_to_decimal(val.words, FERRET_U128_LIMBS);
}

char* ferret_u256_to_string(ferret_u256 val) {
    return ferret_limbs_to_decimal(val.words, FERRET_U256_LIMBS);
}

char* ferret_i128_to_string(ferret_i128 val) {
    bool neg = ferret_is_negative_limbs(val.words, FERRET_U128_LIMBS);
    if (!neg) {
        return ferret_limbs_to_decimal(val.words, FERRET_U128_LIMBS);
    }
    ferret_u128 mag;
    ferret_abs_limbs(val.words, mag.words, FERRET_U128_LIMBS, NULL);
    char* digits = ferret_limbs_to_decimal(mag.words, FERRET_U128_LIMBS);
    if (!digits) return NULL;
    size_t len = strlen(digits);
    char* out = (char*)malloc(len + 2);
    if (!out) {
        free(digits);
        return NULL;
    }
    out[0] = '-';
    memcpy(out + 1, digits, len + 1);
    free(digits);
    return out;
}

char* ferret_i256_to_string(ferret_i256 val) {
    bool neg = ferret_is_negative_limbs(val.words, FERRET_U256_LIMBS);
    if (!neg) {
        return ferret_limbs_to_decimal(val.words, FERRET_U256_LIMBS);
    }
    ferret_u256 mag;
    ferret_abs_limbs(val.words, mag.words, FERRET_U256_LIMBS, NULL);
    char* digits = ferret_limbs_to_decimal(mag.words, FERRET_U256_LIMBS);
    if (!digits) return NULL;
    size_t len = strlen(digits);
    char* out = (char*)malloc(len + 2);
    if (!out) {
        free(digits);
        return NULL;
    }
    out[0] = '-';
    memcpy(out + 1, digits, len + 1);
    free(digits);
    return out;
}

// Soft float helpers for f128/f256 fallback implementations.
typedef enum {
    SOFT_CLASS_ZERO = 0,
    SOFT_CLASS_NORMAL = 1,
    SOFT_CLASS_INF = 2,
    SOFT_CLASS_NAN = 3
} soft_class_t;

#define SOFT_WORD_BITS 64
#define SOFT_EXTRA_BITS 3
#define SOFT_F128_WORDS 2
#define SOFT_F256_WORDS 4

#define SOFT_F128_FRAC_BITS 112
#define SOFT_F128_SIG_BITS 113
#define SOFT_F128_EXP_BITS 15
#define SOFT_F128_EXP_BIAS 16383
#define SOFT_F128_EXP_MAX 0x7FFF
#define SOFT_F128_MAX_EXP 16383
#define SOFT_F128_MIN_EXP (-16382)

#define SOFT_F256_FRAC_BITS 236
#define SOFT_F256_SIG_BITS 237
#define SOFT_F256_EXP_BITS 19
#define SOFT_F256_EXP_BIAS 262143
#define SOFT_F256_EXP_MAX 0x7FFFF
#define SOFT_F256_MAX_EXP 262143
#define SOFT_F256_MIN_EXP (-262142)

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

#ifndef FERRET_HAS_FLOAT128
static soft_class_t soft_f128_unpack(const ferret_f128* v, int* sign, int* exp, uint64_t sig[SOFT_F128_WORDS]) {
    uint64_t w0 = v->mantissa_lo;
    uint64_t w1 = v->mantissa_hi;
    *sign = (int)(w1 >> 63);
    uint64_t exp_raw = (w1 >> 48) & 0x7FFFu;
    uint64_t frac_hi = w1 & 0x0000FFFFFFFFFFFFULL;
    sig[0] = w0;
    sig[1] = frac_hi;

    if (exp_raw == SOFT_F128_EXP_MAX) {
        if (soft_u64_is_zero(sig, SOFT_F128_WORDS)) {
            return SOFT_CLASS_INF;
        }
        return SOFT_CLASS_NAN;
    }
    if (exp_raw == 0) {
        if (soft_u64_is_zero(sig, SOFT_F128_WORDS)) {
            return SOFT_CLASS_ZERO;
        }
        *exp = SOFT_F128_MIN_EXP;
    } else {
        *exp = (int)exp_raw - SOFT_F128_EXP_BIAS;
        sig[1] |= (uint64_t)1 << 48;
    }
    soft_u64_shift_left(sig, SOFT_F128_WORDS, SOFT_EXTRA_BITS, sig);
    return SOFT_CLASS_NORMAL;
}

static ferret_f128 soft_f128_pack(int sign, int exp, uint64_t sig[SOFT_F128_WORDS], soft_class_t cls) {
    ferret_f128 out;
    if (cls == SOFT_CLASS_NAN) {
        out.mantissa_lo = 1;
        out.mantissa_hi = ((uint64_t)sign << 63) | ((uint64_t)SOFT_F128_EXP_MAX << 48);
        return out;
    }
    if (cls == SOFT_CLASS_INF) {
        out.mantissa_lo = 0;
        out.mantissa_hi = ((uint64_t)sign << 63) | ((uint64_t)SOFT_F128_EXP_MAX << 48);
        return out;
    }
    if (cls == SOFT_CLASS_ZERO || soft_u64_is_zero(sig, SOFT_F128_WORDS)) {
        out.mantissa_lo = 0;
        out.mantissa_hi = (uint64_t)sign << 63;
        return out;
    }

    int target = SOFT_F128_SIG_BITS - 1 + SOFT_EXTRA_BITS;
    int lead = soft_u64_msb_index(sig, SOFT_F128_WORDS);
    if (lead < 0) {
        out.mantissa_lo = 0;
        out.mantissa_hi = (uint64_t)sign << 63;
        return out;
    }
    if (lead > target) {
        int shift = lead - target;
        soft_u64_shift_right_sticky(sig, SOFT_F128_WORDS, shift, sig);
        exp += shift;
    } else if (lead < target) {
        int shift = target - lead;
        soft_u64_shift_left(sig, SOFT_F128_WORDS, shift, sig);
        exp -= shift;
    }

    if (exp < SOFT_F128_MIN_EXP) {
        int shift = SOFT_F128_MIN_EXP - exp;
        soft_u64_shift_right_sticky(sig, SOFT_F128_WORDS, shift, sig);
        exp = SOFT_F128_MIN_EXP;
    }

    soft_round_sig(sig, SOFT_F128_WORDS, &exp);
    if (soft_u64_is_zero(sig, SOFT_F128_WORDS)) {
        out.mantissa_lo = 0;
        out.mantissa_hi = (uint64_t)sign << 63;
        return out;
    }
    if (exp > SOFT_F128_MAX_EXP) {
        out.mantissa_lo = 0;
        out.mantissa_hi = ((uint64_t)sign << 63) | ((uint64_t)SOFT_F128_EXP_MAX << 48);
        return out;
    }

    int hidden_word = (SOFT_F128_SIG_BITS - 1) / SOFT_WORD_BITS;
    int hidden_bit = (SOFT_F128_SIG_BITS - 1) % SOFT_WORD_BITS;
    bool normal = exp > SOFT_F128_MIN_EXP;
    if (exp == SOFT_F128_MIN_EXP) {
        normal = ((sig[hidden_word] >> hidden_bit) & 1) != 0;
    }

    uint64_t frac_lo = sig[0];
    uint64_t frac_hi = sig[1] & 0x0000FFFFFFFFFFFFULL;
    if (normal) {
        uint64_t exp_raw = (uint64_t)(exp + SOFT_F128_EXP_BIAS);
        frac_hi &= 0x0000FFFFFFFFFFFFULL;
        out.mantissa_lo = frac_lo;
        out.mantissa_hi = ((uint64_t)sign << 63) | (exp_raw << 48) | frac_hi;
        return out;
    }
    out.mantissa_lo = frac_lo;
    out.mantissa_hi = ((uint64_t)sign << 63) | frac_hi;
    return out;
}

static ferret_f128 soft_f128_add(ferret_f128 a, ferret_f128 b, bool sub) {
    int sign_a = 0;
    int sign_b = 0;
    int exp_a = 0;
    int exp_b = 0;
    uint64_t sig_a[SOFT_F128_WORDS];
    uint64_t sig_b[SOFT_F128_WORDS];
    soft_class_t cls_a = soft_f128_unpack(&a, &sign_a, &exp_a, sig_a);
    soft_class_t cls_b = soft_f128_unpack(&b, &sign_b, &exp_b, sig_b);
    if (sub) {
        sign_b ^= 1;
    }

    if (cls_a == SOFT_CLASS_NAN || cls_b == SOFT_CLASS_NAN) {
        return soft_f128_pack(0, 0, sig_a, SOFT_CLASS_NAN);
    }
    if (cls_a == SOFT_CLASS_INF || cls_b == SOFT_CLASS_INF) {
        if (cls_a == SOFT_CLASS_INF && cls_b == SOFT_CLASS_INF && sign_a != sign_b) {
            return soft_f128_pack(0, 0, sig_a, SOFT_CLASS_NAN);
        }
        if (cls_a == SOFT_CLASS_INF) {
            return soft_f128_pack(sign_a, 0, sig_a, SOFT_CLASS_INF);
        }
        return soft_f128_pack(sign_b, 0, sig_b, SOFT_CLASS_INF);
    }
    if (cls_a == SOFT_CLASS_ZERO) {
        return soft_f128_pack(sign_b, exp_b, sig_b, cls_b);
    }
    if (cls_b == SOFT_CLASS_ZERO) {
        return soft_f128_pack(sign_a, exp_a, sig_a, cls_a);
    }

    if (exp_a < exp_b) {
        int tmp_sign = sign_a;
        sign_a = sign_b;
        sign_b = tmp_sign;
        int tmp_exp = exp_a;
        exp_a = exp_b;
        exp_b = tmp_exp;
        uint64_t tmp_sig[SOFT_F128_WORDS];
        soft_u64_copy(tmp_sig, sig_a, SOFT_F128_WORDS);
        soft_u64_copy(sig_a, sig_b, SOFT_F128_WORDS);
        soft_u64_copy(sig_b, tmp_sig, SOFT_F128_WORDS);
    }

    int diff = exp_a - exp_b;
    if (diff > 0) {
        soft_u64_shift_right_sticky(sig_b, SOFT_F128_WORDS, diff, sig_b);
    }

    uint64_t sig_out[SOFT_F128_WORDS];
    int sign_out = sign_a;
    int exp_out = exp_a;

    if (sign_a == sign_b) {
        uint64_t carry = soft_u64_add(sig_a, sig_b, sig_out, SOFT_F128_WORDS);
        if (carry) {
            soft_u64_shift_right_sticky(sig_out, SOFT_F128_WORDS, 1, sig_out);
            exp_out += 1;
        }
        return soft_f128_pack(sign_out, exp_out, sig_out, SOFT_CLASS_NORMAL);
    }

    int cmp = soft_u64_cmp(sig_a, sig_b, SOFT_F128_WORDS);
    if (cmp == 0) {
        return soft_f128_pack(0, 0, sig_out, SOFT_CLASS_ZERO);
    }
    if (cmp < 0) {
        soft_u64_sub(sig_b, sig_a, sig_out, SOFT_F128_WORDS);
        sign_out = sign_b;
    } else {
        soft_u64_sub(sig_a, sig_b, sig_out, SOFT_F128_WORDS);
        sign_out = sign_a;
    }
    return soft_f128_pack(sign_out, exp_out, sig_out, SOFT_CLASS_NORMAL);
}

static ferret_f128 soft_f128_mul(ferret_f128 a, ferret_f128 b) {
    int sign_a = 0;
    int sign_b = 0;
    int exp_a = 0;
    int exp_b = 0;
    uint64_t sig_a[SOFT_F128_WORDS];
    uint64_t sig_b[SOFT_F128_WORDS];
    soft_class_t cls_a = soft_f128_unpack(&a, &sign_a, &exp_a, sig_a);
    soft_class_t cls_b = soft_f128_unpack(&b, &sign_b, &exp_b, sig_b);

    if (cls_a == SOFT_CLASS_NAN || cls_b == SOFT_CLASS_NAN) {
        return soft_f128_pack(0, 0, sig_a, SOFT_CLASS_NAN);
    }
    if ((cls_a == SOFT_CLASS_INF && cls_b == SOFT_CLASS_ZERO) ||
        (cls_b == SOFT_CLASS_INF && cls_a == SOFT_CLASS_ZERO)) {
        return soft_f128_pack(0, 0, sig_a, SOFT_CLASS_NAN);
    }
    if (cls_a == SOFT_CLASS_INF || cls_b == SOFT_CLASS_INF) {
        return soft_f128_pack(sign_a ^ sign_b, 0, sig_a, SOFT_CLASS_INF);
    }
    if (cls_a == SOFT_CLASS_ZERO || cls_b == SOFT_CLASS_ZERO) {
        return soft_f128_pack(sign_a ^ sign_b, 0, sig_a, SOFT_CLASS_ZERO);
    }

    uint64_t sig_a_raw[SOFT_F128_WORDS];
    uint64_t sig_b_raw[SOFT_F128_WORDS];
    soft_u64_shift_right(sig_a, SOFT_F128_WORDS, SOFT_EXTRA_BITS, sig_a_raw);
    soft_u64_shift_right(sig_b, SOFT_F128_WORDS, SOFT_EXTRA_BITS, sig_b_raw);

    uint64_t prod[SOFT_F128_WORDS * 2];
    soft_u64_mul(sig_a_raw, sig_b_raw, prod, SOFT_F128_WORDS);
    int lead = soft_u64_msb_index(prod, SOFT_F128_WORDS * 2);
    if (lead < 0) {
        return soft_f128_pack(sign_a ^ sign_b, 0, sig_a, SOFT_CLASS_ZERO);
    }
    int shift_raw = lead - (SOFT_F128_SIG_BITS - 1);
    int shift = shift_raw - SOFT_EXTRA_BITS;
    uint64_t sig_out[SOFT_F128_WORDS];
    if (shift >= 0) {
        uint64_t tmp[SOFT_F128_WORDS * 2];
        soft_u64_shift_right_sticky(prod, SOFT_F128_WORDS * 2, shift, tmp);
        sig_out[0] = tmp[0];
        sig_out[1] = tmp[1];
    } else {
        soft_u64_zero(sig_out, SOFT_F128_WORDS);
        soft_u64_shift_left(prod, SOFT_F128_WORDS * 2, -shift, prod);
        sig_out[0] = prod[0];
        sig_out[1] = prod[1];
    }
    int exp_out = exp_a + exp_b - (SOFT_F128_SIG_BITS - 1) + shift_raw;
    return soft_f128_pack(sign_a ^ sign_b, exp_out, sig_out, SOFT_CLASS_NORMAL);
}

static ferret_f128 soft_f128_div(ferret_f128 a, ferret_f128 b) {
    int sign_a = 0;
    int sign_b = 0;
    int exp_a = 0;
    int exp_b = 0;
    uint64_t sig_a[SOFT_F128_WORDS];
    uint64_t sig_b[SOFT_F128_WORDS];
    soft_class_t cls_a = soft_f128_unpack(&a, &sign_a, &exp_a, sig_a);
    soft_class_t cls_b = soft_f128_unpack(&b, &sign_b, &exp_b, sig_b);

    if (cls_a == SOFT_CLASS_NAN || cls_b == SOFT_CLASS_NAN) {
        return soft_f128_pack(0, 0, sig_a, SOFT_CLASS_NAN);
    }
    if (cls_a == SOFT_CLASS_INF && cls_b == SOFT_CLASS_INF) {
        return soft_f128_pack(0, 0, sig_a, SOFT_CLASS_NAN);
    }
    if (cls_a == SOFT_CLASS_INF) {
        return soft_f128_pack(sign_a ^ sign_b, 0, sig_a, SOFT_CLASS_INF);
    }
    if (cls_b == SOFT_CLASS_INF) {
        return soft_f128_pack(sign_a ^ sign_b, 0, sig_a, SOFT_CLASS_ZERO);
    }
    if (cls_b == SOFT_CLASS_ZERO) {
        if (cls_a == SOFT_CLASS_ZERO) {
            return soft_f128_pack(0, 0, sig_a, SOFT_CLASS_NAN);
        }
        return soft_f128_pack(sign_a ^ sign_b, 0, sig_a, SOFT_CLASS_INF);
    }
    if (cls_a == SOFT_CLASS_ZERO) {
        return soft_f128_pack(sign_a ^ sign_b, 0, sig_a, SOFT_CLASS_ZERO);
    }

    uint64_t sig_a_raw[SOFT_F128_WORDS];
    uint64_t sig_b_raw[SOFT_F128_WORDS];
    soft_u64_shift_right(sig_a, SOFT_F128_WORDS, SOFT_EXTRA_BITS, sig_a_raw);
    soft_u64_shift_right(sig_b, SOFT_F128_WORDS, SOFT_EXTRA_BITS, sig_b_raw);

    uint64_t numer[SOFT_F128_WORDS * 2];
    int shift = (SOFT_F128_SIG_BITS - 1) + SOFT_EXTRA_BITS;
    soft_u64_shift_left_wide(sig_a_raw, SOFT_F128_WORDS, shift, numer, SOFT_F128_WORDS * 2);

    uint64_t denom[SOFT_F128_WORDS * 2];
    soft_u64_zero(denom, SOFT_F128_WORDS * 2);
    soft_u64_copy(denom, sig_b_raw, SOFT_F128_WORDS);

    uint64_t quot[SOFT_F128_WORDS * 2];
    uint64_t rem[SOFT_F128_WORDS * 2];
    soft_u64_div_mod(numer, denom, quot, rem, SOFT_F128_WORDS * 2);

    uint64_t sig_out[SOFT_F128_WORDS];
    sig_out[0] = quot[0];
    sig_out[1] = quot[1];
    if (!soft_u64_is_zero(rem, SOFT_F128_WORDS * 2)) {
        sig_out[0] |= 1;
    }
    int exp_out = exp_a - exp_b;
    return soft_f128_pack(sign_a ^ sign_b, exp_out, sig_out, SOFT_CLASS_NORMAL);
}

static int soft_f128_compare(ferret_f128 a, ferret_f128 b, bool* unordered) {
    int sign_a = 0;
    int sign_b = 0;
    int exp_a = 0;
    int exp_b = 0;
    uint64_t sig_a[SOFT_F128_WORDS];
    uint64_t sig_b[SOFT_F128_WORDS];
    soft_class_t cls_a = soft_f128_unpack(&a, &sign_a, &exp_a, sig_a);
    soft_class_t cls_b = soft_f128_unpack(&b, &sign_b, &exp_b, sig_b);

    if (cls_a == SOFT_CLASS_NAN || cls_b == SOFT_CLASS_NAN) {
        if (unordered) {
            *unordered = true;
        }
        return 0;
    }
    if (cls_a == SOFT_CLASS_ZERO && cls_b == SOFT_CLASS_ZERO) {
        if (unordered) {
            *unordered = false;
        }
        return 0;
    }
    if (cls_a == SOFT_CLASS_INF || cls_b == SOFT_CLASS_INF) {
        if (unordered) {
            *unordered = false;
        }
        if (cls_a == cls_b) {
            if (sign_a == sign_b) {
                return 0;
            }
            return sign_a ? -1 : 1;
        }
        if (cls_a == SOFT_CLASS_INF) {
            return sign_a ? -1 : 1;
        }
        return sign_b ? 1 : -1;
    }
    if (sign_a != sign_b) {
        if (unordered) {
            *unordered = false;
        }
        return sign_a ? -1 : 1;
    }

    uint64_t sig_a_raw[SOFT_F128_WORDS];
    uint64_t sig_b_raw[SOFT_F128_WORDS];
    soft_u64_shift_right(sig_a, SOFT_F128_WORDS, SOFT_EXTRA_BITS, sig_a_raw);
    soft_u64_shift_right(sig_b, SOFT_F128_WORDS, SOFT_EXTRA_BITS, sig_b_raw);

    int cmp = 0;
    if (exp_a < exp_b) {
        cmp = -1;
    } else if (exp_a > exp_b) {
        cmp = 1;
    } else {
        cmp = soft_u64_cmp(sig_a_raw, sig_b_raw, SOFT_F128_WORDS);
    }
    if (unordered) {
        *unordered = false;
    }
    if (sign_a) {
        return -cmp;
    }
    return cmp;
}
#endif // !FERRET_HAS_FLOAT128

static soft_class_t soft_f256_unpack(const ferret_f256* v, int* sign, int* exp, uint64_t sig[SOFT_F256_WORDS]) {
    uint64_t w0 = v->mantissa[0];
    uint64_t w1 = v->mantissa[1];
    uint64_t w2 = v->mantissa[2];
    uint64_t w3 = v->exp_sign;
    *sign = (int)(w3 >> 63);
    uint64_t exp_raw = (w3 >> 44) & 0x7FFFFu;
    uint64_t frac_hi = w3 & 0x00000FFFFFFFFFFFULL;
    sig[0] = w0;
    sig[1] = w1;
    sig[2] = w2;
    sig[3] = frac_hi;

    if (exp_raw == SOFT_F256_EXP_MAX) {
        if (soft_u64_is_zero(sig, SOFT_F256_WORDS)) {
            return SOFT_CLASS_INF;
        }
        return SOFT_CLASS_NAN;
    }
    if (exp_raw == 0) {
        if (soft_u64_is_zero(sig, SOFT_F256_WORDS)) {
            return SOFT_CLASS_ZERO;
        }
        *exp = SOFT_F256_MIN_EXP;
    } else {
        *exp = (int)exp_raw - SOFT_F256_EXP_BIAS;
        sig[3] |= (uint64_t)1 << 44;
    }
    soft_u64_shift_left(sig, SOFT_F256_WORDS, SOFT_EXTRA_BITS, sig);
    return SOFT_CLASS_NORMAL;
}

static ferret_f256 soft_f256_pack(int sign, int exp, uint64_t sig[SOFT_F256_WORDS], soft_class_t cls) {
    ferret_f256 out = {{0, 0, 0}, 0};
    if (cls == SOFT_CLASS_NAN) {
        out.mantissa[0] = 1;
        out.exp_sign = ((uint64_t)sign << 63) | ((uint64_t)SOFT_F256_EXP_MAX << 44);
        return out;
    }
    if (cls == SOFT_CLASS_INF) {
        out.mantissa[0] = 0;
        out.exp_sign = ((uint64_t)sign << 63) | ((uint64_t)SOFT_F256_EXP_MAX << 44);
        return out;
    }
    if (cls == SOFT_CLASS_ZERO || soft_u64_is_zero(sig, SOFT_F256_WORDS)) {
        out.exp_sign = (uint64_t)sign << 63;
        return out;
    }

    int target = SOFT_F256_SIG_BITS - 1 + SOFT_EXTRA_BITS;
    int lead = soft_u64_msb_index(sig, SOFT_F256_WORDS);
    if (lead < 0) {
        out.exp_sign = (uint64_t)sign << 63;
        return out;
    }
    if (lead > target) {
        int shift = lead - target;
        soft_u64_shift_right_sticky(sig, SOFT_F256_WORDS, shift, sig);
        exp += shift;
    } else if (lead < target) {
        int shift = target - lead;
        soft_u64_shift_left(sig, SOFT_F256_WORDS, shift, sig);
        exp -= shift;
    }

    if (exp < SOFT_F256_MIN_EXP) {
        int shift = SOFT_F256_MIN_EXP - exp;
        soft_u64_shift_right_sticky(sig, SOFT_F256_WORDS, shift, sig);
        exp = SOFT_F256_MIN_EXP;
    }

    soft_round_sig(sig, SOFT_F256_WORDS, &exp);
    if (soft_u64_is_zero(sig, SOFT_F256_WORDS)) {
        out.exp_sign = (uint64_t)sign << 63;
        return out;
    }
    if (exp > SOFT_F256_MAX_EXP) {
        out.exp_sign = ((uint64_t)sign << 63) | ((uint64_t)SOFT_F256_EXP_MAX << 44);
        return out;
    }

    int hidden_word = (SOFT_F256_SIG_BITS - 1) / SOFT_WORD_BITS;
    int hidden_bit = (SOFT_F256_SIG_BITS - 1) % SOFT_WORD_BITS;
    bool normal = exp > SOFT_F256_MIN_EXP;
    if (exp == SOFT_F256_MIN_EXP) {
        normal = ((sig[hidden_word] >> hidden_bit) & 1) != 0;
    }

    out.mantissa[0] = sig[0];
    out.mantissa[1] = sig[1];
    out.mantissa[2] = sig[2];
    uint64_t frac_hi = sig[3] & 0x00000FFFFFFFFFFFULL;
    if (normal) {
        uint64_t exp_raw = (uint64_t)(exp + SOFT_F256_EXP_BIAS);
        out.exp_sign = ((uint64_t)sign << 63) | (exp_raw << 44) | frac_hi;
        return out;
    }
    out.exp_sign = ((uint64_t)sign << 63) | frac_hi;
    return out;
}

static ferret_f256 soft_f256_add(ferret_f256 a, ferret_f256 b, bool sub) {
    int sign_a = 0;
    int sign_b = 0;
    int exp_a = 0;
    int exp_b = 0;
    uint64_t sig_a[SOFT_F256_WORDS];
    uint64_t sig_b[SOFT_F256_WORDS];
    soft_class_t cls_a = soft_f256_unpack(&a, &sign_a, &exp_a, sig_a);
    soft_class_t cls_b = soft_f256_unpack(&b, &sign_b, &exp_b, sig_b);
    if (sub) {
        sign_b ^= 1;
    }

    if (cls_a == SOFT_CLASS_NAN || cls_b == SOFT_CLASS_NAN) {
        return soft_f256_pack(0, 0, sig_a, SOFT_CLASS_NAN);
    }
    if (cls_a == SOFT_CLASS_INF || cls_b == SOFT_CLASS_INF) {
        if (cls_a == SOFT_CLASS_INF && cls_b == SOFT_CLASS_INF && sign_a != sign_b) {
            return soft_f256_pack(0, 0, sig_a, SOFT_CLASS_NAN);
        }
        if (cls_a == SOFT_CLASS_INF) {
            return soft_f256_pack(sign_a, 0, sig_a, SOFT_CLASS_INF);
        }
        return soft_f256_pack(sign_b, 0, sig_b, SOFT_CLASS_INF);
    }
    if (cls_a == SOFT_CLASS_ZERO) {
        return soft_f256_pack(sign_b, exp_b, sig_b, cls_b);
    }
    if (cls_b == SOFT_CLASS_ZERO) {
        return soft_f256_pack(sign_a, exp_a, sig_a, cls_a);
    }

    if (exp_a < exp_b) {
        int tmp_sign = sign_a;
        sign_a = sign_b;
        sign_b = tmp_sign;
        int tmp_exp = exp_a;
        exp_a = exp_b;
        exp_b = tmp_exp;
        uint64_t tmp_sig[SOFT_F256_WORDS];
        soft_u64_copy(tmp_sig, sig_a, SOFT_F256_WORDS);
        soft_u64_copy(sig_a, sig_b, SOFT_F256_WORDS);
        soft_u64_copy(sig_b, tmp_sig, SOFT_F256_WORDS);
    }

    int diff = exp_a - exp_b;
    if (diff > 0) {
        soft_u64_shift_right_sticky(sig_b, SOFT_F256_WORDS, diff, sig_b);
    }

    uint64_t sig_out[SOFT_F256_WORDS];
    int sign_out = sign_a;
    int exp_out = exp_a;

    if (sign_a == sign_b) {
        uint64_t carry = soft_u64_add(sig_a, sig_b, sig_out, SOFT_F256_WORDS);
        if (carry) {
            soft_u64_shift_right_sticky(sig_out, SOFT_F256_WORDS, 1, sig_out);
            exp_out += 1;
        }
        return soft_f256_pack(sign_out, exp_out, sig_out, SOFT_CLASS_NORMAL);
    }

    int cmp = soft_u64_cmp(sig_a, sig_b, SOFT_F256_WORDS);
    if (cmp == 0) {
        return soft_f256_pack(0, 0, sig_out, SOFT_CLASS_ZERO);
    }
    if (cmp < 0) {
        soft_u64_sub(sig_b, sig_a, sig_out, SOFT_F256_WORDS);
        sign_out = sign_b;
    } else {
        soft_u64_sub(sig_a, sig_b, sig_out, SOFT_F256_WORDS);
        sign_out = sign_a;
    }
    return soft_f256_pack(sign_out, exp_out, sig_out, SOFT_CLASS_NORMAL);
}

static ferret_f256 soft_f256_mul(ferret_f256 a, ferret_f256 b) {
    int sign_a = 0;
    int sign_b = 0;
    int exp_a = 0;
    int exp_b = 0;
    uint64_t sig_a[SOFT_F256_WORDS];
    uint64_t sig_b[SOFT_F256_WORDS];
    soft_class_t cls_a = soft_f256_unpack(&a, &sign_a, &exp_a, sig_a);
    soft_class_t cls_b = soft_f256_unpack(&b, &sign_b, &exp_b, sig_b);

    if (cls_a == SOFT_CLASS_NAN || cls_b == SOFT_CLASS_NAN) {
        return soft_f256_pack(0, 0, sig_a, SOFT_CLASS_NAN);
    }
    if ((cls_a == SOFT_CLASS_INF && cls_b == SOFT_CLASS_ZERO) ||
        (cls_b == SOFT_CLASS_INF && cls_a == SOFT_CLASS_ZERO)) {
        return soft_f256_pack(0, 0, sig_a, SOFT_CLASS_NAN);
    }
    if (cls_a == SOFT_CLASS_INF || cls_b == SOFT_CLASS_INF) {
        return soft_f256_pack(sign_a ^ sign_b, 0, sig_a, SOFT_CLASS_INF);
    }
    if (cls_a == SOFT_CLASS_ZERO || cls_b == SOFT_CLASS_ZERO) {
        return soft_f256_pack(sign_a ^ sign_b, 0, sig_a, SOFT_CLASS_ZERO);
    }

    uint64_t sig_a_raw[SOFT_F256_WORDS];
    uint64_t sig_b_raw[SOFT_F256_WORDS];
    soft_u64_shift_right(sig_a, SOFT_F256_WORDS, SOFT_EXTRA_BITS, sig_a_raw);
    soft_u64_shift_right(sig_b, SOFT_F256_WORDS, SOFT_EXTRA_BITS, sig_b_raw);

    uint64_t prod[SOFT_F256_WORDS * 2];
    soft_u64_mul(sig_a_raw, sig_b_raw, prod, SOFT_F256_WORDS);
    int lead = soft_u64_msb_index(prod, SOFT_F256_WORDS * 2);
    if (lead < 0) {
        return soft_f256_pack(sign_a ^ sign_b, 0, sig_a, SOFT_CLASS_ZERO);
    }
    int shift_raw = lead - (SOFT_F256_SIG_BITS - 1);
    int shift = shift_raw - SOFT_EXTRA_BITS;
    uint64_t sig_out[SOFT_F256_WORDS];
    if (shift >= 0) {
        uint64_t tmp[SOFT_F256_WORDS * 2];
        soft_u64_shift_right_sticky(prod, SOFT_F256_WORDS * 2, shift, tmp);
        sig_out[0] = tmp[0];
        sig_out[1] = tmp[1];
        sig_out[2] = tmp[2];
        sig_out[3] = tmp[3];
    } else {
        soft_u64_zero(sig_out, SOFT_F256_WORDS);
        soft_u64_shift_left(prod, SOFT_F256_WORDS * 2, -shift, prod);
        sig_out[0] = prod[0];
        sig_out[1] = prod[1];
        sig_out[2] = prod[2];
        sig_out[3] = prod[3];
    }
    int exp_out = exp_a + exp_b - (SOFT_F256_SIG_BITS - 1) + shift_raw;
    return soft_f256_pack(sign_a ^ sign_b, exp_out, sig_out, SOFT_CLASS_NORMAL);
}

static ferret_f256 soft_f256_div(ferret_f256 a, ferret_f256 b) {
    int sign_a = 0;
    int sign_b = 0;
    int exp_a = 0;
    int exp_b = 0;
    uint64_t sig_a[SOFT_F256_WORDS];
    uint64_t sig_b[SOFT_F256_WORDS];
    soft_class_t cls_a = soft_f256_unpack(&a, &sign_a, &exp_a, sig_a);
    soft_class_t cls_b = soft_f256_unpack(&b, &sign_b, &exp_b, sig_b);

    if (cls_a == SOFT_CLASS_NAN || cls_b == SOFT_CLASS_NAN) {
        return soft_f256_pack(0, 0, sig_a, SOFT_CLASS_NAN);
    }
    if (cls_a == SOFT_CLASS_INF && cls_b == SOFT_CLASS_INF) {
        return soft_f256_pack(0, 0, sig_a, SOFT_CLASS_NAN);
    }
    if (cls_a == SOFT_CLASS_INF) {
        return soft_f256_pack(sign_a ^ sign_b, 0, sig_a, SOFT_CLASS_INF);
    }
    if (cls_b == SOFT_CLASS_INF) {
        return soft_f256_pack(sign_a ^ sign_b, 0, sig_a, SOFT_CLASS_ZERO);
    }
    if (cls_b == SOFT_CLASS_ZERO) {
        if (cls_a == SOFT_CLASS_ZERO) {
            return soft_f256_pack(0, 0, sig_a, SOFT_CLASS_NAN);
        }
        return soft_f256_pack(sign_a ^ sign_b, 0, sig_a, SOFT_CLASS_INF);
    }
    if (cls_a == SOFT_CLASS_ZERO) {
        return soft_f256_pack(sign_a ^ sign_b, 0, sig_a, SOFT_CLASS_ZERO);
    }

    uint64_t sig_a_raw[SOFT_F256_WORDS];
    uint64_t sig_b_raw[SOFT_F256_WORDS];
    soft_u64_shift_right(sig_a, SOFT_F256_WORDS, SOFT_EXTRA_BITS, sig_a_raw);
    soft_u64_shift_right(sig_b, SOFT_F256_WORDS, SOFT_EXTRA_BITS, sig_b_raw);

    uint64_t numer[SOFT_F256_WORDS * 2];
    int shift = (SOFT_F256_SIG_BITS - 1) + SOFT_EXTRA_BITS;
    soft_u64_shift_left_wide(sig_a_raw, SOFT_F256_WORDS, shift, numer, SOFT_F256_WORDS * 2);

    uint64_t denom[SOFT_F256_WORDS * 2];
    soft_u64_zero(denom, SOFT_F256_WORDS * 2);
    soft_u64_copy(denom, sig_b_raw, SOFT_F256_WORDS);

    uint64_t quot[SOFT_F256_WORDS * 2];
    uint64_t rem[SOFT_F256_WORDS * 2];
    soft_u64_div_mod(numer, denom, quot, rem, SOFT_F256_WORDS * 2);

    uint64_t sig_out[SOFT_F256_WORDS];
    sig_out[0] = quot[0];
    sig_out[1] = quot[1];
    sig_out[2] = quot[2];
    sig_out[3] = quot[3];
    if (!soft_u64_is_zero(rem, SOFT_F256_WORDS * 2)) {
        sig_out[0] |= 1;
    }
    int exp_out = exp_a - exp_b;
    return soft_f256_pack(sign_a ^ sign_b, exp_out, sig_out, SOFT_CLASS_NORMAL);
}

static int soft_f256_compare(ferret_f256 a, ferret_f256 b, bool* unordered) {
    int sign_a = 0;
    int sign_b = 0;
    int exp_a = 0;
    int exp_b = 0;
    uint64_t sig_a[SOFT_F256_WORDS];
    uint64_t sig_b[SOFT_F256_WORDS];
    soft_class_t cls_a = soft_f256_unpack(&a, &sign_a, &exp_a, sig_a);
    soft_class_t cls_b = soft_f256_unpack(&b, &sign_b, &exp_b, sig_b);

    if (cls_a == SOFT_CLASS_NAN || cls_b == SOFT_CLASS_NAN) {
        if (unordered) {
            *unordered = true;
        }
        return 0;
    }
    if (cls_a == SOFT_CLASS_ZERO && cls_b == SOFT_CLASS_ZERO) {
        if (unordered) {
            *unordered = false;
        }
        return 0;
    }
    if (cls_a == SOFT_CLASS_INF || cls_b == SOFT_CLASS_INF) {
        if (unordered) {
            *unordered = false;
        }
        if (cls_a == cls_b) {
            if (sign_a == sign_b) {
                return 0;
            }
            return sign_a ? -1 : 1;
        }
        if (cls_a == SOFT_CLASS_INF) {
            return sign_a ? -1 : 1;
        }
        return sign_b ? 1 : -1;
    }
    if (sign_a != sign_b) {
        if (unordered) {
            *unordered = false;
        }
        return sign_a ? -1 : 1;
    }

    uint64_t sig_a_raw[SOFT_F256_WORDS];
    uint64_t sig_b_raw[SOFT_F256_WORDS];
    soft_u64_shift_right(sig_a, SOFT_F256_WORDS, SOFT_EXTRA_BITS, sig_a_raw);
    soft_u64_shift_right(sig_b, SOFT_F256_WORDS, SOFT_EXTRA_BITS, sig_b_raw);

    int cmp = 0;
    if (exp_a < exp_b) {
        cmp = -1;
    } else if (exp_a > exp_b) {
        cmp = 1;
    } else {
        cmp = soft_u64_cmp(sig_a_raw, sig_b_raw, SOFT_F256_WORDS);
    }
    if (unordered) {
        *unordered = false;
    }
    if (sign_a) {
        return -cmp;
    }
    return cmp;
}

// 128-bit floating point operations (fallback implementation)
#ifndef FERRET_HAS_FLOAT128

ferret_f128 ferret_f128_add(ferret_f128 a, ferret_f128 b) {
    return soft_f128_add(a, b, false);
}

ferret_f128 ferret_f128_sub(ferret_f128 a, ferret_f128 b) {
    return soft_f128_add(a, b, true);
}

ferret_f128 ferret_f128_mul(ferret_f128 a, ferret_f128 b) {
    return soft_f128_mul(a, b);
}

ferret_f128 ferret_f128_div(ferret_f128 a, ferret_f128 b) {
    return soft_f128_div(a, b);
}

bool ferret_f128_eq(ferret_f128 a, ferret_f128 b) {
    bool unordered = false;
    int cmp = soft_f128_compare(a, b, &unordered);
    return !unordered && cmp == 0;
}

bool ferret_f128_lt(ferret_f128 a, ferret_f128 b) {
    bool unordered = false;
    int cmp = soft_f128_compare(a, b, &unordered);
    return !unordered && cmp < 0;
}

bool ferret_f128_gt(ferret_f128 a, ferret_f128 b) {
    bool unordered = false;
    int cmp = soft_f128_compare(a, b, &unordered);
    return !unordered && cmp > 0;
}

#endif // !FERRET_HAS_FLOAT128

// 256-bit floating point operations (always struct-based)
ferret_f256 ferret_f256_add(ferret_f256 a, ferret_f256 b) {
    return soft_f256_add(a, b, false);
}

ferret_f256 ferret_f256_sub(ferret_f256 a, ferret_f256 b) {
    return soft_f256_add(a, b, true);
}

ferret_f256 ferret_f256_mul(ferret_f256 a, ferret_f256 b) {
    return soft_f256_mul(a, b);
}

ferret_f256 ferret_f256_div(ferret_f256 a, ferret_f256 b) {
    return soft_f256_div(a, b);
}

bool ferret_f256_eq(ferret_f256 a, ferret_f256 b) {
    bool unordered = false;
    int cmp = soft_f256_compare(a, b, &unordered);
    return !unordered && cmp == 0;
}

bool ferret_f256_lt(ferret_f256 a, ferret_f256 b) {
    bool unordered = false;
    int cmp = soft_f256_compare(a, b, &unordered);
    return !unordered && cmp < 0;
}

bool ferret_f256_gt(ferret_f256 a, ferret_f256 b) {
    bool unordered = false;
    int cmp = soft_f256_compare(a, b, &unordered);
    return !unordered && cmp > 0;
}

// Floating point power functions - use C's pow() on f64 for now.
ferret_f128 ferret_f128_pow(ferret_f128 base, ferret_f128 exp) {
    double base_val = ferret_f128_to_f64(base);
    double exp_val = ferret_f128_to_f64(exp);
    return ferret_f128_from_f64(pow(base_val, exp_val));
}

ferret_f256 ferret_f256_pow(ferret_f256 base, ferret_f256 exp) {
    double base_val = ferret_f256_to_f64(base);
    double exp_val = ferret_f256_to_f64(exp);
    return ferret_f256_from_f64(pow(base_val, exp_val));
}

// Conversion functions for floating point
#ifndef FERRET_HAS_FLOAT128
static ferret_f128 soft_f128_from_f64(double val) {
    union {
        double d;
        uint64_t u;
    } bits;
    bits.d = val;

    uint64_t sign = bits.u >> 63;
    uint64_t exp_raw = (bits.u >> 52) & 0x7FFu;
    uint64_t frac = bits.u & 0xFFFFFFFFFFFFFULL;

    uint64_t sig[SOFT_F128_WORDS];
    soft_u64_zero(sig, SOFT_F128_WORDS);
    int exp = 0;
    soft_class_t cls = SOFT_CLASS_NORMAL;

    if (exp_raw == 0x7FFu) {
        cls = (frac == 0) ? SOFT_CLASS_INF : SOFT_CLASS_NAN;
        return soft_f128_pack((int)sign, 0, sig, cls);
    }
    if (exp_raw == 0) {
        if (frac == 0) {
            return soft_f128_pack((int)sign, 0, sig, SOFT_CLASS_ZERO);
        }
        int lead = SOFT_WORD_BITS - 1 - soft_clz64(frac);
        int shift = 52 - lead;
        uint64_t sig64 = frac << shift;
        exp = lead - 1074;
        sig[0] = sig64;
        sig[1] = 0;
        soft_u64_shift_left(sig, SOFT_F128_WORDS, SOFT_F128_FRAC_BITS - 52, sig);
    } else {
        exp = (int)exp_raw - 1023;
        uint64_t sig64 = (uint64_t)1 << 52;
        sig64 |= frac;
        sig[0] = sig64;
        sig[1] = 0;
        soft_u64_shift_left(sig, SOFT_F128_WORDS, SOFT_F128_FRAC_BITS - 52, sig);
    }
    soft_u64_shift_left(sig, SOFT_F128_WORDS, SOFT_EXTRA_BITS, sig);
    return soft_f128_pack((int)sign, exp, sig, cls);
}
#endif // !FERRET_HAS_FLOAT128

static ferret_f256 soft_f256_from_f64(double val) {
    union {
        double d;
        uint64_t u;
    } bits;
    bits.d = val;

    uint64_t sign = bits.u >> 63;
    uint64_t exp_raw = (bits.u >> 52) & 0x7FFu;
    uint64_t frac = bits.u & 0xFFFFFFFFFFFFFULL;

    uint64_t sig[SOFT_F256_WORDS];
    soft_u64_zero(sig, SOFT_F256_WORDS);
    int exp = 0;
    soft_class_t cls = SOFT_CLASS_NORMAL;

    if (exp_raw == 0x7FFu) {
        cls = (frac == 0) ? SOFT_CLASS_INF : SOFT_CLASS_NAN;
        return soft_f256_pack((int)sign, 0, sig, cls);
    }
    if (exp_raw == 0) {
        if (frac == 0) {
            return soft_f256_pack((int)sign, 0, sig, SOFT_CLASS_ZERO);
        }
        int lead = SOFT_WORD_BITS - 1 - soft_clz64(frac);
        int shift = 52 - lead;
        uint64_t sig64 = frac << shift;
        exp = lead - 1074;
        sig[0] = sig64;
        sig[1] = 0;
        sig[2] = 0;
        sig[3] = 0;
        soft_u64_shift_left(sig, SOFT_F256_WORDS, SOFT_F256_FRAC_BITS - 52, sig);
    } else {
        exp = (int)exp_raw - 1023;
        uint64_t sig64 = (uint64_t)1 << 52;
        sig64 |= frac;
        sig[0] = sig64;
        sig[1] = 0;
        sig[2] = 0;
        sig[3] = 0;
        soft_u64_shift_left(sig, SOFT_F256_WORDS, SOFT_F256_FRAC_BITS - 52, sig);
    }
    soft_u64_shift_left(sig, SOFT_F256_WORDS, SOFT_EXTRA_BITS, sig);
    return soft_f256_pack((int)sign, exp, sig, cls);
}

ferret_f128 ferret_f128_from_f64(double val) {
#ifdef FERRET_HAS_FLOAT128
    return (ferret_f128)val;
#else
    return soft_f128_from_f64(val);
#endif
}

ferret_f256 ferret_f256_from_f64(double val) {
    return soft_f256_from_f64(val);
}

static double soft_to_double(int sign, int exp, const uint64_t* sig, int sig_words, int sig_bits) {
    if (soft_u64_is_zero(sig, sig_words)) {
        return sign ? -0.0 : 0.0;
    }
    uint64_t sig_raw[SOFT_F256_WORDS];
    soft_u64_zero(sig_raw, sig_words);
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

double ferret_f128_to_f64(ferret_f128 val) {
#ifdef FERRET_HAS_FLOAT128
    return (double)val;
#else
    int sign = 0;
    int exp = 0;
    uint64_t sig[SOFT_F128_WORDS];
    soft_class_t cls = soft_f128_unpack(&val, &sign, &exp, sig);
    if (cls == SOFT_CLASS_NAN) {
        return NAN;
    }
    if (cls == SOFT_CLASS_INF) {
        return sign ? -INFINITY : INFINITY;
    }
    return soft_to_double(sign, exp, sig, SOFT_F128_WORDS, SOFT_F128_SIG_BITS);
#endif
}

double ferret_f256_to_f64(ferret_f256 val) {
    int sign = 0;
    int exp = 0;
    uint64_t sig[SOFT_F256_WORDS];
    soft_class_t cls = soft_f256_unpack(&val, &sign, &exp, sig);
    if (cls == SOFT_CLASS_NAN) {
        return NAN;
    }
    if (cls == SOFT_CLASS_INF) {
        return sign ? -INFINITY : INFINITY;
    }
    return soft_to_double(sign, exp, sig, SOFT_F256_WORDS, SOFT_F256_SIG_BITS);
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

char* ferret_f128_to_string(ferret_f128 val) {
    char* buf = (char*)malloc(128);
    if (!buf) return NULL;
    double d = ferret_f128_to_f64(val);
    snprintf(buf, 128, "%.34g", d);
    ferret_ensure_float_decimal(buf, 128);
    return buf;
}

char* ferret_f256_to_string(ferret_f256 val) {
    char* buf = (char*)malloc(256);
    if (!buf) return NULL;
    double d = ferret_f256_to_f64(val);
    snprintf(buf, 256, "%.71g", d);
    ferret_ensure_float_decimal(buf, 256);
    return buf;
}

// String to number conversion (supports decimal/hex/oct/bin and underscores)
ferret_i128 ferret_i128_from_string(const char* str) {
    ferret_i128 out;
    bool neg = false;
    if (!ferret_parse_uint(str, true, out.words, FERRET_U128_LIMBS, &neg)) {
        ferret_zero_limbs(out.words, FERRET_U128_LIMBS);
        return out;
    }
    if (neg) {
        ferret_negate_limbs(out.words, FERRET_U128_LIMBS);
    }
    return out;
}

ferret_u128 ferret_u128_from_string(const char* str) {
    ferret_u128 out;
    bool neg = false;
    if (!ferret_parse_uint(str, false, out.words, FERRET_U128_LIMBS, &neg)) {
        ferret_zero_limbs(out.words, FERRET_U128_LIMBS);
        return out;
    }
    return out;
}

ferret_i256 ferret_i256_from_string(const char* str) {
    ferret_i256 out;
    bool neg = false;
    if (!ferret_parse_uint(str, true, out.words, FERRET_U256_LIMBS, &neg)) {
        ferret_zero_limbs(out.words, FERRET_U256_LIMBS);
        return out;
    }
    if (neg) {
        ferret_negate_limbs(out.words, FERRET_U256_LIMBS);
    }
    return out;
}

ferret_u256 ferret_u256_from_string(const char* str) {
    ferret_u256 out;
    bool neg = false;
    if (!ferret_parse_uint(str, false, out.words, FERRET_U256_LIMBS, &neg)) {
        ferret_zero_limbs(out.words, FERRET_U256_LIMBS);
        return out;
    }
    return out;
}

ferret_f128 ferret_f128_from_string(const char* str) {
    if (!str) {
#ifdef FERRET_HAS_FLOAT128
        return (ferret_f128)0.0;
#else
        ferret_f128 zero = {0, 0};
        return zero;
#endif
    }
    double val = strtod(str, NULL);
    return ferret_f128_from_f64(val);
}

ferret_f256 ferret_f256_from_string(const char* str) {
    ferret_f256 result = {{0, 0, 0}, 0};
    if (!str) return result;
    double val = strtod(str, NULL);
    return ferret_f256_from_f64(val);
}

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

FERRET_PTR_BIN_OP(ferret_i128, ferret_i128_add)
FERRET_PTR_BIN_OP(ferret_i128, ferret_i128_sub)
FERRET_PTR_BIN_OP(ferret_i128, ferret_i128_mul)
FERRET_PTR_BIN_OP(ferret_i128, ferret_i128_div)
FERRET_PTR_BIN_OP(ferret_i128, ferret_i128_mod)
FERRET_PTR_CMP_OP(ferret_i128, ferret_i128_eq)
FERRET_PTR_CMP_OP(ferret_i128, ferret_i128_lt)
FERRET_PTR_CMP_OP(ferret_i128, ferret_i128_gt)
FERRET_PTR_BIN_OP(ferret_i128, ferret_i128_and)
FERRET_PTR_BIN_OP(ferret_i128, ferret_i128_or)
FERRET_PTR_BIN_OP(ferret_i128, ferret_i128_xor)
FERRET_PTR_BIN_OP(ferret_i128, ferret_i128_pow)

FERRET_PTR_BIN_OP(ferret_u128, ferret_u128_add)
FERRET_PTR_BIN_OP(ferret_u128, ferret_u128_sub)
FERRET_PTR_BIN_OP(ferret_u128, ferret_u128_mul)
FERRET_PTR_BIN_OP(ferret_u128, ferret_u128_div)
FERRET_PTR_BIN_OP(ferret_u128, ferret_u128_mod)
FERRET_PTR_CMP_OP(ferret_u128, ferret_u128_eq)
FERRET_PTR_CMP_OP(ferret_u128, ferret_u128_lt)
FERRET_PTR_CMP_OP(ferret_u128, ferret_u128_gt)
FERRET_PTR_BIN_OP(ferret_u128, ferret_u128_and)
FERRET_PTR_BIN_OP(ferret_u128, ferret_u128_or)
FERRET_PTR_BIN_OP(ferret_u128, ferret_u128_xor)
FERRET_PTR_BIN_OP(ferret_u128, ferret_u128_pow)

FERRET_PTR_BIN_OP(ferret_i256, ferret_i256_add)
FERRET_PTR_BIN_OP(ferret_i256, ferret_i256_sub)
FERRET_PTR_BIN_OP(ferret_i256, ferret_i256_mul)
FERRET_PTR_BIN_OP(ferret_i256, ferret_i256_div)
FERRET_PTR_BIN_OP(ferret_i256, ferret_i256_mod)
FERRET_PTR_CMP_OP(ferret_i256, ferret_i256_eq)
FERRET_PTR_CMP_OP(ferret_i256, ferret_i256_lt)
FERRET_PTR_CMP_OP(ferret_i256, ferret_i256_gt)
FERRET_PTR_BIN_OP(ferret_i256, ferret_i256_and)
FERRET_PTR_BIN_OP(ferret_i256, ferret_i256_or)
FERRET_PTR_BIN_OP(ferret_i256, ferret_i256_xor)
FERRET_PTR_UNARY_OP(ferret_i256, ferret_i256_not)
FERRET_PTR_BIN_OP(ferret_i256, ferret_i256_pow)

FERRET_PTR_BIN_OP(ferret_u256, ferret_u256_add)
FERRET_PTR_BIN_OP(ferret_u256, ferret_u256_sub)
FERRET_PTR_BIN_OP(ferret_u256, ferret_u256_mul)
FERRET_PTR_BIN_OP(ferret_u256, ferret_u256_div)
FERRET_PTR_BIN_OP(ferret_u256, ferret_u256_mod)
FERRET_PTR_CMP_OP(ferret_u256, ferret_u256_eq)
FERRET_PTR_CMP_OP(ferret_u256, ferret_u256_lt)
FERRET_PTR_CMP_OP(ferret_u256, ferret_u256_gt)
FERRET_PTR_BIN_OP(ferret_u256, ferret_u256_and)
FERRET_PTR_BIN_OP(ferret_u256, ferret_u256_or)
FERRET_PTR_BIN_OP(ferret_u256, ferret_u256_xor)
FERRET_PTR_UNARY_OP(ferret_u256, ferret_u256_not)
FERRET_PTR_BIN_OP(ferret_u256, ferret_u256_pow)

FERRET_PTR_BIN_OP(ferret_f128, ferret_f128_add)
FERRET_PTR_BIN_OP(ferret_f128, ferret_f128_sub)
FERRET_PTR_BIN_OP(ferret_f128, ferret_f128_mul)
FERRET_PTR_BIN_OP(ferret_f128, ferret_f128_div)
FERRET_PTR_CMP_OP(ferret_f128, ferret_f128_eq)
FERRET_PTR_CMP_OP(ferret_f128, ferret_f128_lt)
FERRET_PTR_CMP_OP(ferret_f128, ferret_f128_gt)
FERRET_PTR_BIN_OP(ferret_f128, ferret_f128_pow)

FERRET_PTR_BIN_OP(ferret_f256, ferret_f256_add)
FERRET_PTR_BIN_OP(ferret_f256, ferret_f256_sub)
FERRET_PTR_BIN_OP(ferret_f256, ferret_f256_mul)
FERRET_PTR_BIN_OP(ferret_f256, ferret_f256_div)
FERRET_PTR_CMP_OP(ferret_f256, ferret_f256_eq)
FERRET_PTR_CMP_OP(ferret_f256, ferret_f256_lt)
FERRET_PTR_CMP_OP(ferret_f256, ferret_f256_gt)
FERRET_PTR_BIN_OP(ferret_f256, ferret_f256_pow)

void ferret_i128_from_i64_ptr(int64_t val, ferret_i128* out) {
    if (!out) return;
    *out = ferret_i128_from_i64(val);
}

void ferret_u128_from_u64_ptr(uint64_t val, ferret_u128* out) {
    if (!out) return;
    *out = ferret_u128_from_u64(val);
}

void ferret_i256_from_i64_ptr(int64_t val, ferret_i256* out) {
    if (!out) return;
    *out = ferret_i256_from_i64(val);
}

void ferret_u256_from_u64_ptr(uint64_t val, ferret_u256* out) {
    if (!out) return;
    *out = ferret_u256_from_u64(val);
}

void ferret_f128_from_f64_ptr(double val, ferret_f128* out) {
    if (!out) return;
    *out = ferret_f128_from_f64(val);
}

void ferret_f256_from_f64_ptr(double val, ferret_f256* out) {
    if (!out) return;
    *out = ferret_f256_from_f64(val);
}

int64_t ferret_i128_to_i64_ptr(const ferret_i128* val) {
    if (!val) return 0;
    return ferret_i128_to_i64(*val);
}

uint64_t ferret_u128_to_u64_ptr(const ferret_u128* val) {
    if (!val) return 0;
    return ferret_u128_to_u64(*val);
}

int64_t ferret_i256_to_i64_ptr(const ferret_i256* val) {
    if (!val) return 0;
    return ferret_i256_to_i64(*val);
}

uint64_t ferret_u256_to_u64_ptr(const ferret_u256* val) {
    if (!val) return 0;
    return ferret_u256_to_u64(*val);
}

double ferret_f128_to_f64_ptr(const ferret_f128* val) {
    if (!val) return 0.0;
    return ferret_f128_to_f64(*val);
}

double ferret_f256_to_f64_ptr(const ferret_f256* val) {
    if (!val) return 0.0;
    return ferret_f256_to_f64(*val);
}

char* ferret_i128_to_string_ptr(const ferret_i128* val) {
    if (!val) return NULL;
    return ferret_i128_to_string(*val);
}

char* ferret_u128_to_string_ptr(const ferret_u128* val) {
    if (!val) return NULL;
    return ferret_u128_to_string(*val);
}

char* ferret_i256_to_string_ptr(const ferret_i256* val) {
    if (!val) return NULL;
    return ferret_i256_to_string(*val);
}

char* ferret_u256_to_string_ptr(const ferret_u256* val) {
    if (!val) return NULL;
    return ferret_u256_to_string(*val);
}

char* ferret_f128_to_string_ptr(const ferret_f128* val) {
    if (!val) return NULL;
    return ferret_f128_to_string(*val);
}

char* ferret_f256_to_string_ptr(const ferret_f256* val) {
    if (!val) return NULL;
    return ferret_f256_to_string(*val);
}

void ferret_i128_from_string_ptr(const char* str, ferret_i128* out) {
    if (!out) return;
    *out = ferret_i128_from_string(str);
}

void ferret_u128_from_string_ptr(const char* str, ferret_u128* out) {
    if (!out) return;
    *out = ferret_u128_from_string(str);
}

void ferret_i256_from_string_ptr(const char* str, ferret_i256* out) {
    if (!out) return;
    *out = ferret_i256_from_string(str);
}

void ferret_u256_from_string_ptr(const char* str, ferret_u256* out) {
    if (!out) return;
    *out = ferret_u256_from_string(str);
}

void ferret_f128_from_string_ptr(const char* str, ferret_f128* out) {
    if (!out) return;
    *out = ferret_f128_from_string(str);
}

void ferret_f256_from_string_ptr(const char* str, ferret_f256* out) {
    if (!out) return;
    *out = ferret_f256_from_string(str);
}

#undef FERRET_PTR_BIN_OP
#undef FERRET_PTR_CMP_OP
#undef FERRET_PTR_UNARY_OP
