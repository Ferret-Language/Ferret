// Ferret runtime: Union helpers

#ifndef FERRET_UNION_H
#define FERRET_UNION_H

#include <stdint.h>

#define FERRET_UNION_TAG_SIZE 4

int32_t ferret_union_tag(const void* u);
void* ferret_union_load_ptr(const void* u);
void* ferret_union_load_ptr_deref(const void* u);

#endif // FERRET_UNION_H
