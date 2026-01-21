#include <string.h>
#include "union.h"

int32_t ferret_union_tag(const void* u) {
    int32_t tag = -1;
    if (u == NULL) {
        return tag;
    }
    memcpy(&tag, u, sizeof(tag));
    return tag;
}

void* ferret_union_load_ptr(const void* u) {
    void* value = NULL;
    if (u == NULL) {
        return NULL;
    }
    memcpy(&value, (const uint8_t*)u + FERRET_UNION_TAG_SIZE, sizeof(value));
    return value;
}

void* ferret_union_load_ptr_deref(const void* u) {
    void* ptr = ferret_union_load_ptr(u);
    void* value = NULL;
    if (ptr == NULL) {
        return NULL;
    }
    memcpy(&value, ptr, sizeof(value));
    return value;
}
