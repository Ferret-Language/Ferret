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
#define SOFT_F128_DECIMAL_DIG 36

#define SOFT_F256_FRAC_BITS 236
#define SOFT_F256_SIG_BITS 237
#define SOFT_F256_EXP_BITS 19
#define SOFT_F256_EXP_BIAS 262143
#define SOFT_F256_EXP_MAX 0x7FFFF
#define SOFT_F256_MAX_EXP 262143
#define SOFT_F256_MIN_EXP (-262142)
#define SOFT_F256_DECIMAL_DIG 73

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

typedef struct {
    uint32_t* limbs;
    size_t len;
    size_t cap;
} ferret_big_uint;

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
    int word_shift = bits / 32;
    int bit_shift = bits % 32;
    size_t new_len = b->len + (size_t)word_shift + (bit_shift ? 1 : 0);
    big_reserve(b, new_len);
    for (size_t i = b->len; i-- > 0;) {
        b->limbs[i + word_shift] = b->limbs[i];
    }
    for (int i = 0; i < word_shift; i++) {
        b->limbs[i] = 0;
    }
    if (bit_shift) {
        uint32_t carry = 0;
        for (size_t i = (size_t)word_shift; i < b->len + (size_t)word_shift; i++) {
            uint64_t val = ((uint64_t)b->limbs[i] << bit_shift) | carry;
            b->limbs[i] = (uint32_t)val;
            carry = (uint32_t)(val >> 32);
        }
        if (carry) {
            b->limbs[b->len + (size_t)word_shift] = carry;
            b->len = b->len + (size_t)word_shift + 1;
            return;
        }
    }
    b->len = b->len + (size_t)word_shift + (bit_shift ? 1 : 0);
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

    const uint32_t base = 0x100000000u;
    int shift = ferret_clz32(v->limbs[v->len - 1]);

    ferret_big_uint vn;
    ferret_big_uint un;
    big_init(&vn);
    big_init(&un);
    big_copy(&vn, v);
    big_copy(&un, u);
    if (shift) {
        big_shift_left_bits(&vn, shift);
        big_shift_left_bits(&un, shift);
    }
    if (un.len == u->len) {
        big_reserve(&un, un.len + 1);
        un.limbs[un.len++] = 0;
    }

    size_t n = vn.len;
    size_t m = un.len - n;
    big_reserve(q, m + 1);
    memset(q->limbs, 0, (m + 1) * sizeof(uint32_t));
    q->len = m + 1;

    for (size_t j = m + 1; j-- > 0;) {
        uint64_t ujn = (j + n < un.len) ? un.limbs[j + n] : 0;
        uint64_t ujn1 = un.limbs[j + n - 1];
        uint64_t ujn2 = un.limbs[j + n - 2];
        uint64_t vnn1 = vn.limbs[n - 1];
        uint64_t vnn2 = vn.limbs[n - 2];

        uint64_t numerator = (ujn << 32) | ujn1;
        uint64_t qhat = numerator / vnn1;
        uint64_t rhat = numerator % vnn1;
        if (qhat >= base) {
            qhat = base - 1;
        }
        while (qhat * vnn2 > (rhat << 32) + ujn2) {
            qhat--;
            rhat += vnn1;
            if (rhat >= base) {
                break;
            }
        }

        uint64_t carry = 0;
        for (size_t i = 0; i < n; i++) {
            uint64_t p = qhat * vn.limbs[i];
            uint64_t p_low = p & 0xffffffffu;
            uint64_t p_high = p >> 32;
            uint64_t uval = un.limbs[i + j];
            uint64_t sub = uval - p_low - carry;
            un.limbs[i + j] = (uint32_t)sub;
            carry = p_high + ((sub >> 63) & 1u);
        }
        uint64_t uval = un.limbs[j + n];
        uint64_t sub = uval - carry;
        un.limbs[j + n] = (uint32_t)sub;
        if (sub >> 63) {
            qhat--;
            uint64_t carry2 = 0;
            for (size_t i = 0; i < n; i++) {
                uint64_t sum = (uint64_t)un.limbs[i + j] + vn.limbs[i] + carry2;
                un.limbs[i + j] = (uint32_t)sum;
                carry2 = sum >> 32;
            }
            un.limbs[j + n] = (uint32_t)((uint64_t)un.limbs[j + n] + carry2);
        }
        q->limbs[j] = (uint32_t)qhat;
    }
    big_normalize(q);

    big_reserve(r, n);
    r->len = n;
    for (size_t i = 0; i < n; i++) {
        r->limbs[i] = un.limbs[i];
    }
    if (shift) {
        big_shift_right_bits(r, shift);
    }
    big_normalize(r);
    big_free(&vn);
    big_free(&un);
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

static void ferret_f128_get_bits(ferret_f128 val, uint64_t* lo, uint64_t* hi) {
#ifdef FERRET_HAS_FLOAT128
    union {
        ferret_f128 f;
        struct {
            uint64_t lo;
            uint64_t hi;
        } parts;
    } u;
    u.f = val;
    *lo = u.parts.lo;
    *hi = u.parts.hi;
#else
    *lo = val.mantissa_lo;
    *hi = val.mantissa_hi;
#endif
}

static ferret_f128 ferret_f128_from_bits(uint64_t lo, uint64_t hi) {
#ifdef FERRET_HAS_FLOAT128
    union {
        ferret_f128 f;
        struct {
            uint64_t lo;
            uint64_t hi;
        } parts;
    } u;
    u.parts.lo = lo;
    u.parts.hi = hi;
    return u.f;
#else
    ferret_f128 out;
    out.mantissa_lo = lo;
    out.mantissa_hi = hi;
    return out;
#endif
}

static soft_class_t soft_f128_unpack_bits(uint64_t lo, uint64_t hi, int* sign, int* exp, uint64_t sig[SOFT_F128_WORDS]) {
    *sign = (int)(hi >> 63);
    uint64_t exp_raw = (hi >> 48) & 0x7FFFu;
    uint64_t frac_hi = hi & 0x0000FFFFFFFFFFFFULL;
    sig[0] = lo;
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

static void soft_f128_pack_bits(int sign, int exp, uint64_t sig[SOFT_F128_WORDS], soft_class_t cls, uint64_t* lo, uint64_t* hi) {
    if (cls == SOFT_CLASS_NAN) {
        *lo = 1;
        *hi = ((uint64_t)sign << 63) | ((uint64_t)SOFT_F128_EXP_MAX << 48);
        return;
    }
    if (cls == SOFT_CLASS_INF) {
        *lo = 0;
        *hi = ((uint64_t)sign << 63) | ((uint64_t)SOFT_F128_EXP_MAX << 48);
        return;
    }
    if (cls == SOFT_CLASS_ZERO || soft_u64_is_zero(sig, SOFT_F128_WORDS)) {
        *lo = 0;
        *hi = (uint64_t)sign << 63;
        return;
    }

    int target = SOFT_F128_SIG_BITS - 1 + SOFT_EXTRA_BITS;
    int lead = soft_u64_msb_index(sig, SOFT_F128_WORDS);
    if (lead < 0) {
        *lo = 0;
        *hi = (uint64_t)sign << 63;
        return;
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
        *lo = 0;
        *hi = (uint64_t)sign << 63;
        return;
    }
    if (exp > SOFT_F128_MAX_EXP) {
        *lo = 0;
        *hi = ((uint64_t)sign << 63) | ((uint64_t)SOFT_F128_EXP_MAX << 48);
        return;
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
        *lo = frac_lo;
        *hi = ((uint64_t)sign << 63) | (exp_raw << 48) | frac_hi;
        return;
    }
    *lo = frac_lo;
    *hi = ((uint64_t)sign << 63) | frac_hi;
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

static ferret_f128 ferret_f128_from_decimal(const char* str) {
    int sign = 0;
    int exp10 = 0;
    char* digits = NULL;
    if (!ferret_parse_float_string(str, &sign, &digits, &exp10)) {
        return ferret_f128_from_bits(0, 0);
    }
    if (!digits) {
        return ferret_f128_from_bits(0, (uint64_t)sign << 63);
    }

    ferret_big_uint dec;
    big_init(&dec);
    big_from_decimal(&dec, digits);
    free(digits);
    if (big_is_zero(&dec)) {
        big_free(&dec);
        return ferret_f128_from_bits(0, (uint64_t)sign << 63);
    }

    const int sig_bits = SOFT_F128_SIG_BITS;
    const int min_exp = SOFT_F128_MIN_EXP;
    const int max_exp = SOFT_F128_MAX_EXP;
    const long double log2_10 = log2l(10.0L);
    long double log2_dec = big_log2(&dec);
    long double log2_val = log2_dec + (long double)exp10 * log2_10;
    int exp2 = (int)floorl(log2_val);
    int min_sub = min_exp - (sig_bits - 1);
    if (exp2 > max_exp) {
        uint64_t lo = 0;
        uint64_t hi = ((uint64_t)sign << 63) | ((uint64_t)SOFT_F128_EXP_MAX << 48);
        big_free(&dec);
        return ferret_f128_from_bits(lo, hi);
    }
    if (exp2 < min_sub) {
        big_free(&dec);
        return ferret_f128_from_bits(0, (uint64_t)sign << 63);
    }

    int target_exp = exp2;
    if (target_exp < min_exp) {
        target_exp = min_exp;
    }
    int shift = (sig_bits - 1) - target_exp;
    int shift2 = exp10 + shift;

    ferret_big_uint num;
    ferret_big_uint den;
    big_init(&num);
    big_init(&den);
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

    int q_bits = big_bit_length(&q);
    if (q_bits > sig_bits) {
        big_shift_right_bits(&q, 1);
        target_exp += 1;
    }
    if (target_exp > max_exp) {
        uint64_t lo = 0;
        uint64_t hi = ((uint64_t)sign << 63) | ((uint64_t)SOFT_F128_EXP_MAX << 48);
        big_free(&dec);
        big_free(&num);
        big_free(&den);
        big_free(&q);
        big_free(&r);
        return ferret_f128_from_bits(lo, hi);
    }

    uint64_t sig[SOFT_F128_WORDS];
    big_to_words64(&q, sig, SOFT_F128_WORDS);
    soft_u64_shift_left(sig, SOFT_F128_WORDS, SOFT_EXTRA_BITS, sig);
    uint64_t lo = 0;
    uint64_t hi = 0;
    soft_f128_pack_bits(sign, target_exp, sig, SOFT_CLASS_NORMAL, &lo, &hi);

    big_free(&dec);
    big_free(&num);
    big_free(&den);
    big_free(&q);
    big_free(&r);
    return ferret_f128_from_bits(lo, hi);
}

static ferret_f256 ferret_f256_from_decimal(const char* str) {
    int sign = 0;
    int exp10 = 0;
    char* digits = NULL;
    if (!ferret_parse_float_string(str, &sign, &digits, &exp10)) {
        ferret_f256 zero = {{0, 0, 0}, 0};
        return zero;
    }
    ferret_f256 zero = {{0, 0, 0}, 0};
    if (!digits) {
        if (sign) {
            zero.exp_sign = (uint64_t)sign << 63;
        }
        return zero;
    }

    ferret_big_uint dec;
    big_init(&dec);
    big_from_decimal(&dec, digits);
    free(digits);
    if (big_is_zero(&dec)) {
        big_free(&dec);
        if (sign) {
            zero.exp_sign = (uint64_t)sign << 63;
        }
        return zero;
    }

    const int sig_bits = SOFT_F256_SIG_BITS;
    const int min_exp = SOFT_F256_MIN_EXP;
    const int max_exp = SOFT_F256_MAX_EXP;
    const long double log2_10 = log2l(10.0L);
    long double log2_dec = big_log2(&dec);
    long double log2_val = log2_dec + (long double)exp10 * log2_10;
    int exp2 = (int)floorl(log2_val);
    int min_sub = min_exp - (sig_bits - 1);
    if (exp2 > max_exp) {
        ferret_f256 inf = {{0, 0, 0}, ((uint64_t)sign << 63) | ((uint64_t)SOFT_F256_EXP_MAX << 44)};
        big_free(&dec);
        return inf;
    }
    if (exp2 < min_sub) {
        big_free(&dec);
        if (sign) {
            zero.exp_sign = (uint64_t)sign << 63;
        }
        return zero;
    }

    int target_exp = exp2;
    if (target_exp < min_exp) {
        target_exp = min_exp;
    }
    int shift = (sig_bits - 1) - target_exp;
    int shift2 = exp10 + shift;

    ferret_big_uint num;
    ferret_big_uint den;
    big_init(&num);
    big_init(&den);
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

    int q_bits = big_bit_length(&q);
    if (q_bits > sig_bits) {
        big_shift_right_bits(&q, 1);
        target_exp += 1;
    }
    if (target_exp > max_exp) {
        ferret_f256 inf = {{0, 0, 0}, ((uint64_t)sign << 63) | ((uint64_t)SOFT_F256_EXP_MAX << 44)};
        big_free(&dec);
        big_free(&num);
        big_free(&den);
        big_free(&q);
        big_free(&r);
        return inf;
    }

    uint64_t sig[SOFT_F256_WORDS];
    big_to_words64(&q, sig, SOFT_F256_WORDS);
    soft_u64_shift_left(sig, SOFT_F256_WORDS, SOFT_EXTRA_BITS, sig);
    ferret_f256 out = soft_f256_pack(sign, target_exp, sig, SOFT_CLASS_NORMAL);

    big_free(&dec);
    big_free(&num);
    big_free(&den);
    big_free(&q);
    big_free(&r);
    return out;
}

char* ferret_f128_to_string(ferret_f128 val) {
    uint64_t lo = 0;
    uint64_t hi = 0;
    ferret_f128_get_bits(val, &lo, &hi);
    int sign = 0;
    int exp = 0;
    uint64_t sig[SOFT_F128_WORDS];
    soft_class_t cls = soft_f128_unpack_bits(lo, hi, &sign, &exp, sig);
    uint64_t sig_raw[SOFT_F128_WORDS];
    soft_u64_shift_right(sig, SOFT_F128_WORDS, SOFT_EXTRA_BITS, sig_raw);
    return soft_format_decimal(sign, exp, sig_raw, SOFT_F128_WORDS, SOFT_F128_SIG_BITS, cls, SOFT_F128_DECIMAL_DIG);
}

char* ferret_f256_to_string(ferret_f256 val) {
    int sign = 0;
    int exp = 0;
    uint64_t sig[SOFT_F256_WORDS];
    soft_class_t cls = soft_f256_unpack(&val, &sign, &exp, sig);
    uint64_t sig_raw[SOFT_F256_WORDS];
    soft_u64_shift_right(sig, SOFT_F256_WORDS, SOFT_EXTRA_BITS, sig_raw);
    return soft_format_decimal(sign, exp, sig_raw, SOFT_F256_WORDS, SOFT_F256_SIG_BITS, cls, SOFT_F256_DECIMAL_DIG);
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
    int sign = 0;
    soft_class_t cls = SOFT_CLASS_ZERO;
    if (ferret_parse_special_float(str, &sign, &cls)) {
        uint64_t lo = 0;
        uint64_t hi = 0;
        if (cls == SOFT_CLASS_NAN) {
            lo = 1;
            hi = ((uint64_t)sign << 63) | ((uint64_t)SOFT_F128_EXP_MAX << 48);
        } else if (cls == SOFT_CLASS_INF) {
            hi = ((uint64_t)sign << 63) | ((uint64_t)SOFT_F128_EXP_MAX << 48);
        }
        return ferret_f128_from_bits(lo, hi);
    }
    return ferret_f128_from_decimal(str);
}

ferret_f256 ferret_f256_from_string(const char* str) {
    ferret_f256 result = {{0, 0, 0}, 0};
    if (!str) return result;
    int sign = 0;
    soft_class_t cls = SOFT_CLASS_ZERO;
    if (ferret_parse_special_float(str, &sign, &cls)) {
        if (cls == SOFT_CLASS_NAN) {
            result.mantissa[0] = 1;
            result.exp_sign = ((uint64_t)sign << 63) | ((uint64_t)SOFT_F256_EXP_MAX << 44);
            return result;
        }
        if (cls == SOFT_CLASS_INF) {
            result.exp_sign = ((uint64_t)sign << 63) | ((uint64_t)SOFT_F256_EXP_MAX << 44);
            return result;
        }
    }
    return ferret_f256_from_decimal(str);
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
