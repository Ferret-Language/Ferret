// Ferret runtime: Central type system implementation

#include "type_system.h"
#include <string.h>

const ferret_primitive_info_t ferret_primitive_info[FERRET_TYPE__PRIMITIVE_END] = {
#define FERRET_PRIMITIVE_INFO(name, lower, ctype, category, bits) \
    [FERRET_TYPE_##name] = { sizeof(ctype), FERRET_PRIMITIVE_##category },
    FERRET_PRIMITIVE_TYPES(FERRET_PRIMITIVE_INFO)
#undef FERRET_PRIMITIVE_INFO
};

#define FERRET_DEFINE_PRIMITIVE_DESC(name, lower, ctype, category, bits) \
    const ferret_type_info_t ferret_type_##lower = { \
        .kind = FERRET_TYPE_##name, \
        .size = sizeof(ctype), \
    };

FERRET_PRIMITIVE_TYPES(FERRET_DEFINE_PRIMITIVE_DESC)
#undef FERRET_DEFINE_PRIMITIVE_DESC

#define FERRET_PRIMITIVE_EQ_INT(data1, data2, ctype, lower) \
    (memcmp((data1), (data2), sizeof(ctype)) == 0)

#define FERRET_PRIMITIVE_EQ_UINT(data1, data2, ctype, lower) \
    (memcmp((data1), (data2), sizeof(ctype)) == 0)

#define FERRET_PRIMITIVE_EQ_BOOL(data1, data2, ctype, lower) \
    (memcmp((data1), (data2), sizeof(ctype)) == 0)

#define FERRET_PRIMITIVE_EQ_BYTE(data1, data2, ctype, lower) \
    (memcmp((data1), (data2), sizeof(ctype)) == 0)

#define FERRET_PRIMITIVE_EQ_CHAR(data1, data2, ctype, lower) \
    (memcmp((data1), (data2), sizeof(ctype)) == 0)

#define FERRET_PRIMITIVE_EQ_FLOAT(data1, data2, ctype, lower) \
    (ferret_##lower##_eq(*(const ctype*)(data1), *(const ctype*)(data2)))

#define FERRET_PRIMITIVE_EQ_STRING(data1, data2, ctype, lower) \
    (ferret_string_equals((data1), (data2)))

static bool ferret_string_equals(const void* data1, const void* data2) {
    const char* str1 = *(const char**)data1;
    const char* str2 = *(const char**)data2;
    if (str1 == str2) {
        return true;
    }
    if (str1 == NULL || str2 == NULL) {
        return false;
    }
    return strcmp(str1, str2) == 0;
}

#define FERRET_DEFINE_PRIMITIVE_EQ(name, lower, ctype, category, bits) \
    static bool ferret_primitive_eq_##lower(const void* data1, const void* data2) { \
        return FERRET_PRIMITIVE_EQ_##category(data1, data2, ctype, lower); \
    }

FERRET_PRIMITIVE_TYPES(FERRET_DEFINE_PRIMITIVE_EQ)
#undef FERRET_DEFINE_PRIMITIVE_EQ

#undef FERRET_PRIMITIVE_EQ_INT
#undef FERRET_PRIMITIVE_EQ_UINT
#undef FERRET_PRIMITIVE_EQ_BOOL
#undef FERRET_PRIMITIVE_EQ_BYTE
#undef FERRET_PRIMITIVE_EQ_CHAR
#undef FERRET_PRIMITIVE_EQ_FLOAT
#undef FERRET_PRIMITIVE_EQ_STRING

bool ferret_primitive_equals(ferret_type_kind_t kind, const void* data1, const void* data2) {
    if (!ferret_type_kind_is_primitive(kind)) {
        return false;
    }
    switch (kind) {
#define FERRET_PRIMITIVE_EQ_CASE(name, lower, ctype, category, bits) \
        case FERRET_TYPE_##name: \
            return ferret_primitive_eq_##lower(data1, data2);
        FERRET_PRIMITIVE_TYPES(FERRET_PRIMITIVE_EQ_CASE)
#undef FERRET_PRIMITIVE_EQ_CASE
        default:
            return false;
    }
}
