// Ferret runtime: Central type system implementation

#include "type_system.h"
#include "bigint.h"

const ferret_primitive_info_t ferret_primitive_info[FERRET_TYPE__PRIMITIVE_END] = {
#define FERRET_PRIMITIVE_INFO(name, lower, ctype, category) \
    [FERRET_TYPE_##name] = { sizeof(ctype), FERRET_PRIMITIVE_##category },
    FERRET_PRIMITIVE_TYPES(FERRET_PRIMITIVE_INFO)
#undef FERRET_PRIMITIVE_INFO
};

#define FERRET_DEFINE_PRIMITIVE_DESC(name, lower, ctype, category) \
    const ferret_type_info_t ferret_type_##lower = { \
        .kind = FERRET_TYPE_##name, \
        .size = sizeof(ctype), \
    };

FERRET_PRIMITIVE_TYPES(FERRET_DEFINE_PRIMITIVE_DESC)
#undef FERRET_DEFINE_PRIMITIVE_DESC
