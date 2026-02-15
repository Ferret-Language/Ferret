// Ferret runtime: Math functions
#include <math.h>
#include <stddef.h>

#include "bigint.h"

typedef struct {
    float re;
    float im;
} ferret_complex64_t;

typedef struct {
    double re;
    double im;
} ferret_complex_t;

typedef struct {
    ferret_f128 re;
    ferret_f128 im;
} ferret_complex256_t;

typedef struct {
    ferret_f256 re;
    ferret_f256 im;
} ferret_complex512_t;

// Implemented in runtime/libs/panic.c.
void ferret_global_panic(const char* msg);

// Power function - wrapper around C's pow()
// Takes two f64 values and returns f64
double ferret_pow(double base, double exp) {
    return pow(base, exp);
}

void ferret_complex64_add(const ferret_complex64_t* a, const ferret_complex64_t* b, ferret_complex64_t* out) {
    if (a == NULL || b == NULL || out == NULL) {
        return;
    }
    out->re = a->re + b->re;
    out->im = a->im + b->im;
}

void ferret_complex64_sub(const ferret_complex64_t* a, const ferret_complex64_t* b, ferret_complex64_t* out) {
    if (a == NULL || b == NULL || out == NULL) {
        return;
    }
    out->re = a->re - b->re;
    out->im = a->im - b->im;
}

void ferret_complex64_mul(const ferret_complex64_t* a, const ferret_complex64_t* b, ferret_complex64_t* out) {
    if (a == NULL || b == NULL || out == NULL) {
        return;
    }
    out->re = (a->re * b->re) - (a->im * b->im);
    out->im = (a->re * b->im) + (a->im * b->re);
}

void ferret_complex64_div(const ferret_complex64_t* a, const ferret_complex64_t* b, ferret_complex64_t* out) {
    if (a == NULL || b == NULL || out == NULL) {
        return;
    }
    float denom = (b->re * b->re) + (b->im * b->im);
    if (denom == 0.0f) {
        ferret_global_panic("division by zero");
        return;
    }
    out->re = ((a->re * b->re) + (a->im * b->im)) / denom;
    out->im = ((a->im * b->re) - (a->re * b->im)) / denom;
}

void ferret_complex_add(const ferret_complex_t* a, const ferret_complex_t* b, ferret_complex_t* out) {
    if (a == NULL || b == NULL || out == NULL) {
        return;
    }
    out->re = a->re + b->re;
    out->im = a->im + b->im;
}

void ferret_complex_sub(const ferret_complex_t* a, const ferret_complex_t* b, ferret_complex_t* out) {
    if (a == NULL || b == NULL || out == NULL) {
        return;
    }
    out->re = a->re - b->re;
    out->im = a->im - b->im;
}

void ferret_complex_mul(const ferret_complex_t* a, const ferret_complex_t* b, ferret_complex_t* out) {
    if (a == NULL || b == NULL || out == NULL) {
        return;
    }
    out->re = (a->re * b->re) - (a->im * b->im);
    out->im = (a->re * b->im) + (a->im * b->re);
}

void ferret_complex_div(const ferret_complex_t* a, const ferret_complex_t* b, ferret_complex_t* out) {
    if (a == NULL || b == NULL || out == NULL) {
        return;
    }
    double denom = (b->re * b->re) + (b->im * b->im);
    if (denom == 0.0) {
        ferret_global_panic("division by zero");
        return;
    }
    out->re = ((a->re * b->re) + (a->im * b->im)) / denom;
    out->im = ((a->im * b->re) - (a->re * b->im)) / denom;
}

void ferret_complex256_add(const ferret_complex256_t* a, const ferret_complex256_t* b, ferret_complex256_t* out) {
    if (a == NULL || b == NULL || out == NULL) {
        return;
    }
    out->re = ferret_f128_add(a->re, b->re);
    out->im = ferret_f128_add(a->im, b->im);
}

void ferret_complex256_sub(const ferret_complex256_t* a, const ferret_complex256_t* b, ferret_complex256_t* out) {
    if (a == NULL || b == NULL || out == NULL) {
        return;
    }
    out->re = ferret_f128_sub(a->re, b->re);
    out->im = ferret_f128_sub(a->im, b->im);
}

void ferret_complex256_mul(const ferret_complex256_t* a, const ferret_complex256_t* b, ferret_complex256_t* out) {
    if (a == NULL || b == NULL || out == NULL) {
        return;
    }
    ferret_f128 re_re = ferret_f128_mul(a->re, b->re);
    ferret_f128 im_im = ferret_f128_mul(a->im, b->im);
    ferret_f128 re_im = ferret_f128_mul(a->re, b->im);
    ferret_f128 im_re = ferret_f128_mul(a->im, b->re);
    out->re = ferret_f128_sub(re_re, im_im);
    out->im = ferret_f128_add(re_im, im_re);
}

void ferret_complex256_div(const ferret_complex256_t* a, const ferret_complex256_t* b, ferret_complex256_t* out) {
    if (a == NULL || b == NULL || out == NULL) {
        return;
    }
    ferret_f128 b_re_sq = ferret_f128_mul(b->re, b->re);
    ferret_f128 b_im_sq = ferret_f128_mul(b->im, b->im);
    ferret_f128 denom = ferret_f128_add(b_re_sq, b_im_sq);
    if (ferret_f128_eq(denom, ferret_f128_from_f64(0.0))) {
        ferret_global_panic("division by zero");
        return;
    }
    ferret_f128 a_re_b_re = ferret_f128_mul(a->re, b->re);
    ferret_f128 a_im_b_im = ferret_f128_mul(a->im, b->im);
    ferret_f128 a_im_b_re = ferret_f128_mul(a->im, b->re);
    ferret_f128 a_re_b_im = ferret_f128_mul(a->re, b->im);

    ferret_f128 re_num = ferret_f128_add(a_re_b_re, a_im_b_im);
    ferret_f128 im_num = ferret_f128_sub(a_im_b_re, a_re_b_im);
    out->re = ferret_f128_div(re_num, denom);
    out->im = ferret_f128_div(im_num, denom);
}

void ferret_complex512_add(const ferret_complex512_t* a, const ferret_complex512_t* b, ferret_complex512_t* out) {
    if (a == NULL || b == NULL || out == NULL) {
        return;
    }
    out->re = ferret_f256_add(a->re, b->re);
    out->im = ferret_f256_add(a->im, b->im);
}

void ferret_complex512_sub(const ferret_complex512_t* a, const ferret_complex512_t* b, ferret_complex512_t* out) {
    if (a == NULL || b == NULL || out == NULL) {
        return;
    }
    out->re = ferret_f256_sub(a->re, b->re);
    out->im = ferret_f256_sub(a->im, b->im);
}

void ferret_complex512_mul(const ferret_complex512_t* a, const ferret_complex512_t* b, ferret_complex512_t* out) {
    if (a == NULL || b == NULL || out == NULL) {
        return;
    }
    ferret_f256 re_re = ferret_f256_mul(a->re, b->re);
    ferret_f256 im_im = ferret_f256_mul(a->im, b->im);
    ferret_f256 re_im = ferret_f256_mul(a->re, b->im);
    ferret_f256 im_re = ferret_f256_mul(a->im, b->re);
    out->re = ferret_f256_sub(re_re, im_im);
    out->im = ferret_f256_add(re_im, im_re);
}

void ferret_complex512_div(const ferret_complex512_t* a, const ferret_complex512_t* b, ferret_complex512_t* out) {
    if (a == NULL || b == NULL || out == NULL) {
        return;
    }
    ferret_f256 b_re_sq = ferret_f256_mul(b->re, b->re);
    ferret_f256 b_im_sq = ferret_f256_mul(b->im, b->im);
    ferret_f256 denom = ferret_f256_add(b_re_sq, b_im_sq);
    if (ferret_f256_eq(denom, ferret_f256_from_f64(0.0))) {
        ferret_global_panic("division by zero");
        return;
    }
    ferret_f256 a_re_b_re = ferret_f256_mul(a->re, b->re);
    ferret_f256 a_im_b_im = ferret_f256_mul(a->im, b->im);
    ferret_f256 a_im_b_re = ferret_f256_mul(a->im, b->re);
    ferret_f256 a_re_b_im = ferret_f256_mul(a->re, b->im);

    ferret_f256 re_num = ferret_f256_add(a_re_b_re, a_im_b_im);
    ferret_f256 im_num = ferret_f256_sub(a_im_b_re, a_re_b_im);
    out->re = ferret_f256_div(re_num, denom);
    out->im = ferret_f256_div(im_num, denom);
}
