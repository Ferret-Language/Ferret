#include "ferret_runtime.h"

#include <inttypes.h>
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

static void ferret__write_big_integer(const void *data, const FerretTypeInfo *info) {
    ferret_usize size;
    ferret_u8 *work;
    char *digits;
    size_t digit_count = 0;
    ferret_bool negative = 0;
    ferret_usize i;

    if (data == NULL || info == NULL || info->size == 0) {
        fputc('0', stdout);
        return;
    }

    size = info->size;
    work = (ferret_u8 *)malloc((size_t)size);
    if (work == NULL) {
        fprintf(stdout, "<%s %p>", info->name ? (const char *)info->name : "int", data);
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
        fprintf(stdout, "<%s %p>", info->name ? (const char *)info->name : "int", data);
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

    free(digits);
    free(work);
}

static void ferret__write_typed(const void *data, const FerretTypeInfo *info);

static void ferret__write_dynamic(const FerretAny *value) {
    if (value == NULL || (value->data == NULL && value->vtable == NULL)) {
        fputs("<any:nil>", stdout);
        return;
    }
    if (value->vtable != NULL) {
        const void *const *vtable = (const void *const *)value->vtable;
        const FerretTypeInfo *info = (const FerretTypeInfo *)vtable[0];
        if (info != NULL) {
            ferret__write_typed(value->data, info);
            return;
        }
    }
    fprintf(stdout, "<any %p>", value->data);
}

static void ferret__write_array(const void *data, const FerretTypeInfo *info) {
    const FerretArrayTypeInfo *meta = (const FerretArrayTypeInfo *)info->meta;
    const ferret_u8 *base = (const ferret_u8 *)data;
    ferret_usize i;

    fputs((const char *)info->name, stdout);
    fputc('{', stdout);
    if (meta != NULL && meta->elem != NULL && base != NULL) {
        for (i = 0; i < meta->len; i++) {
            if (i != 0) {
                fputs(", ", stdout);
            }
            if ((meta->elem->flags & FERRET_TYPE_FLAG_INTERFACE) != 0u) {
                ferret__write_dynamic((const FerretAny *)(base + (i * meta->stride)));
            } else {
                ferret__write_typed(base + (i * meta->stride), meta->elem);
            }
        }
    }
    fputc('}', stdout);
}

static void ferret__write_slice(const void *data, const FerretTypeInfo *info) {
    const FerretSliceTypeInfo *meta = (const FerretSliceTypeInfo *)info->meta;
    const FerretSlicePtr *slice = (const FerretSlicePtr *)data;
    const ferret_u8 *base;
    ferret_usize i;

    fputs((const char *)info->name, stdout);
    fputc('{', stdout);
    if (meta != NULL && meta->elem != NULL && slice != NULL && slice->ptr != NULL) {
        base = (const ferret_u8 *)slice->ptr;
        for (i = 0; i < slice->len; i++) {
            if (i != 0) {
                fputs(", ", stdout);
            }
            if ((meta->elem->flags & FERRET_TYPE_FLAG_INTERFACE) != 0u) {
                ferret__write_dynamic((const FerretAny *)(base + (i * meta->stride)));
            } else {
                ferret__write_typed(base + (i * meta->stride), meta->elem);
            }
        }
    }
    fputc('}', stdout);
}

static void ferret__write_tuple(const void *data, const FerretTypeInfo *info) {
    const FerretTupleTypeInfo *meta = (const FerretTupleTypeInfo *)info->meta;
    const ferret_u8 *base = (const ferret_u8 *)data;
    ferret_usize i;

    fputc('(', stdout);
    if (meta != NULL && meta->fields != NULL && base != NULL) {
        for (i = 0; i < meta->len; i++) {
            const FerretTupleFieldInfo *field = &meta->fields[i];
            if (i != 0) {
                fputs(", ", stdout);
            }
            if (field->type != NULL && (field->type->flags & FERRET_TYPE_FLAG_INTERFACE) != 0u) {
                ferret__write_dynamic((const FerretAny *)(base + field->offset));
            } else {
                ferret__write_typed(base + field->offset, field->type);
            }
        }
    }
    fputc(')', stdout);
}

static void ferret__write_typed(const void *data, const FerretTypeInfo *info) {
    const char *type_name = info != NULL ? (const char *)info->name : NULL;

    if (info == NULL) {
        fprintf(stdout, "<type:nil %p>", data);
        return;
    }

    if ((info->flags & FERRET_TYPE_FLAG_VARIANTS) != 0u) {
        const FerretVariantTypeInfo *meta = (const FerretVariantTypeInfo *)info->meta;
        ferret_i32 ordinal = data != NULL ? *(const ferret_i32 *)data : 0;
        if (meta != NULL && meta->names != NULL && ordinal >= 0 && (ferret_usize)ordinal < meta->len) {
            const ferret_i8 *name = meta->names[ordinal];
            if (name != NULL) {
                fputs((const char *)name, stdout);
                return;
            }
        }
    }

    switch (info->id) {
    case FERRET_TYPE_BOOL:
        fputs((data != NULL && *(const ferret_bool *)data) ? "true" : "false", stdout);
        return;
    case FERRET_TYPE_I8:
        fprintf(stdout, "%d", data != NULL ? (int)*(const ferret_i8 *)data : 0);
        return;
    case FERRET_TYPE_I16:
        fprintf(stdout, "%d", data != NULL ? (int)*(const ferret_i16 *)data : 0);
        return;
    case FERRET_TYPE_I32:
        fprintf(stdout, "%d", data != NULL ? (int)*(const ferret_i32 *)data : 0);
        return;
    case FERRET_TYPE_I64:
        fprintf(stdout, "%lld", data != NULL ? (long long)*(const ferret_i64 *)data : 0LL);
        return;
    case FERRET_TYPE_ISIZE:
        fprintf(stdout, "%" PRIdPTR, data != NULL ? (intptr_t)*(const ferret_isize *)data : (intptr_t)0);
        return;
    case FERRET_TYPE_U8:
        fprintf(stdout, "%u", data != NULL ? (unsigned)*(const ferret_u8 *)data : 0u);
        return;
    case FERRET_TYPE_U16:
        fprintf(stdout, "%u", data != NULL ? (unsigned)*(const ferret_u16 *)data : 0u);
        return;
    case FERRET_TYPE_U32:
        fprintf(stdout, "%u", data != NULL ? (unsigned)*(const ferret_u32 *)data : 0u);
        return;
    case FERRET_TYPE_U64:
        fprintf(stdout, "%llu", data != NULL ? (unsigned long long)*(const ferret_u64 *)data : 0ull);
        return;
    case FERRET_TYPE_USIZE:
        fprintf(stdout, "%" PRIuPTR, data != NULL ? (uintptr_t)*(const ferret_usize *)data : (uintptr_t)0);
        return;
    case FERRET_TYPE_F32:
        fprintf(stdout, "%.9g", data != NULL ? (double)*(const ferret_f32 *)data : 0.0);
        return;
    case FERRET_TYPE_F64:
        fprintf(stdout, "%.17g", data != NULL ? (double)*(const ferret_f64 *)data : 0.0);
        return;
    case FERRET_TYPE_CHAR:
        fprintf(stdout, "%u", data != NULL ? (unsigned)*(const ferret_char *)data : 0u);
        return;
    case FERRET_TYPE_STR: {
        const FerretStr *s = (const FerretStr *)data;
        if (s != NULL && s->ptr != NULL && s->len != 0) {
            fwrite(s->ptr, 1u, (size_t)s->len, stdout);
        }
        return;
    }
    default:
        break;
    }
    if ((info->flags & FERRET_TYPE_FLAG_INTEGER) != 0u) {
        ferret__write_big_integer(data, info);
        return;
    }
    if ((info->flags & FERRET_TYPE_FLAG_ARRAY) != 0u) {
        ferret__write_array(data, info);
        return;
    }
    if ((info->flags & FERRET_TYPE_FLAG_SLICE) != 0u) {
        ferret__write_slice(data, info);
        return;
    }
    if ((info->flags & FERRET_TYPE_FLAG_TUPLE) != 0u) {
        ferret__write_tuple(data, info);
        return;
    }
    if ((info->flags & FERRET_TYPE_FLAG_POINTER) != 0u) {
        fprintf(stdout, "%p", data ? *(void *const *)data : NULL);
        return;
    }
    if (type_name != NULL) {
        fprintf(stdout, "<%s %p>", type_name, data);
        return;
    }
    fprintf(stdout, "<type#%u %p>", (unsigned)info->id, data);
}

void ferret_global_print(const FerretSliceAny *values) {
    ferret_usize i;

    if (values == NULL || values->ptr == NULL || values->len == 0) {
        return;
    }
    for (i = 0; i < values->len; i++) {
        ferret__write_dynamic(&values->ptr[i]);
        fputc('\n', stdout);
    }
    fflush(stdout);
}
