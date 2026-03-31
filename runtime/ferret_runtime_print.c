#include "ferret_runtime.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static void ferret__copy_integer_big_endian(const void *src, ferret_usize size, ferret_u8 *dst) {
    const ferret_u8 *bytes = (const ferret_u8 *)src;
    ferret_usize i;

    if (src == NULL || dst == NULL || size == 0) {
        return;
    }

#if defined(__BYTE_ORDER__) && (__BYTE_ORDER__ == __ORDER_BIG_ENDIAN__)
    memcpy(dst, bytes, (size_t)size);
#else
    for (i = 0; i < size; i++) {
        dst[i] = bytes[size - 1 - i];
    }
#endif
}

static void ferret__twos_complement_abs_big_endian(ferret_u8 *bytes, ferret_usize size) {
    ferret_usize i;
    ferret_u16 carry = 1;

    for (i = size; i > 0; i--) {
        ferret_u16 value = (ferret_u16)((ferret_u8)~bytes[i - 1]) + carry;
        bytes[i - 1] = (ferret_u8)value;
        carry = (ferret_u16)(value >> 8);
    }
}

static void ferret__print_big_integer(const void *data, const FerretTypeInfo *info) {
    ferret_usize size;
    ferret_u8 *work;
    char *digits;
    size_t digit_count = 0;
    ferret_bool negative = 0;
    ferret_usize i;

    if (data == NULL || info == NULL || info->size == 0) {
        fputs("0\n", stdout);
        fflush(stdout);
        return;
    }

    size = info->size;
    work = (ferret_u8 *)malloc((size_t)size);
    if (work == NULL) {
        fprintf(stdout, "<%s %p>\n", info->name ? (const char *)info->name : "int", data);
        fflush(stdout);
        return;
    }
    ferret__copy_integer_big_endian(data, size, work);

    if ((info->flags & FERRET_TYPE_FLAG_SIGNED) != 0u && (work[0] & 0x80u) != 0u) {
        negative = 1;
        ferret__twos_complement_abs_big_endian(work, size);
    }

    digits = (char *)malloc((size_t)(size * 3u + 2u));
    if (digits == NULL) {
        free(work);
        fprintf(stdout, "<%s %p>\n", info->name ? (const char *)info->name : "int", data);
        fflush(stdout);
        return;
    }

    while (1) {
        ferret_u32 carry = 0;
        ferret_bool non_zero = 0;

        for (i = 0; i < size; i++) {
            ferret_u32 value = (carry << 8) | work[i];
            work[i] = (ferret_u8)(value / 10u);
            carry = value % 10u;
            if (work[i] != 0) {
                non_zero = 1;
            }
        }

        digits[digit_count++] = (char)('0' + carry);
        if (!non_zero) {
            break;
        }
    }

    if (negative) {
        fputc('-', stdout);
    }
    while (digit_count > 0) {
        fputc(digits[--digit_count], stdout);
    }
    fputc('\n', stdout);
    fflush(stdout);

    free(digits);
    free(work);
}

void ferret_global_print(const FerretAny *value) {
    if (value == NULL || (value->data == NULL && value->vtable == NULL)) {
        fputs("<any:nil>\n", stdout);
        fflush(stdout);
        return;
    }
    if (value->vtable != NULL) {
        const void *const *vtable = (const void *const *)value->vtable;
        const FerretTypeInfo *info = (const FerretTypeInfo *)vtable[0];
        if (info != NULL) {
            const char *type_name = (const char *)info->name;
            switch (info->id) {
            case FERRET_TYPE_BOOL:
                fputs((*(const ferret_bool *)value->data) ? "true\n" : "false\n", stdout);
                fflush(stdout);
                return;
            case FERRET_TYPE_I8:
                fprintf(stdout, "%d\n", (int)*(const ferret_i8 *)value->data);
                fflush(stdout);
                return;
            case FERRET_TYPE_I16:
                fprintf(stdout, "%d\n", (int)*(const ferret_i16 *)value->data);
                fflush(stdout);
                return;
            case FERRET_TYPE_I32:
                fprintf(stdout, "%d\n", (int)*(const ferret_i32 *)value->data);
                fflush(stdout);
                return;
            case FERRET_TYPE_I64:
                fprintf(stdout, "%lld\n", (long long)*(const ferret_i64 *)value->data);
                fflush(stdout);
                return;
            case FERRET_TYPE_ISIZE:
                fprintf(stdout, "%ld\n", (long)*(const ferret_isize *)value->data);
                fflush(stdout);
                return;
            case FERRET_TYPE_U8:
                fprintf(stdout, "%u\n", (unsigned)*(const ferret_u8 *)value->data);
                fflush(stdout);
                return;
            case FERRET_TYPE_U16:
                fprintf(stdout, "%u\n", (unsigned)*(const ferret_u16 *)value->data);
                fflush(stdout);
                return;
            case FERRET_TYPE_U32:
                fprintf(stdout, "%u\n", (unsigned)*(const ferret_u32 *)value->data);
                fflush(stdout);
                return;
            case FERRET_TYPE_U64:
                fprintf(stdout, "%llu\n", (unsigned long long)*(const ferret_u64 *)value->data);
                fflush(stdout);
                return;
            case FERRET_TYPE_USIZE:
                fprintf(stdout, "%lu\n", (unsigned long)*(const ferret_usize *)value->data);
                fflush(stdout);
                return;
            case FERRET_TYPE_F32:
                fprintf(stdout, "%.9g\n", (double)*(const ferret_f32 *)value->data);
                fflush(stdout);
                return;
            case FERRET_TYPE_F64:
                fprintf(stdout, "%.17g\n", (double)*(const ferret_f64 *)value->data);
                fflush(stdout);
                return;
            case FERRET_TYPE_CHAR:
                fprintf(stdout, "%u\n", (unsigned)*(const ferret_char *)value->data);
                fflush(stdout);
                return;
            case FERRET_TYPE_STR: {
                const FerretStr *s = (const FerretStr *)value->data;
                if (s != NULL && s->ptr != NULL && s->len != 0) {
                    fwrite(s->ptr, 1u, (size_t)s->len, stdout);
                }
                fputc('\n', stdout);
                fflush(stdout);
                return;
            }
            default:
                break;
            }

            if ((info->flags & FERRET_TYPE_FLAG_INTEGER) != 0u) {
                ferret__print_big_integer(value->data, info);
                return;
            }
            if ((info->flags & FERRET_TYPE_FLAG_POINTER) != 0u) {
                fprintf(stdout, "%p\n", value->data ? *(void *const *)value->data : NULL);
                fflush(stdout);
                return;
            }
            if (type_name != NULL) {
                fprintf(stdout, "<%s %p>\n", type_name, value->data);
                fflush(stdout);
                return;
            }
            fprintf(stdout, "<type#%u %p>\n", (unsigned)info->id, value->data);
            fflush(stdout);
            return;
        }
    }
    fprintf(stdout, "<any %p>\n", value->data);
    fflush(stdout);
}
