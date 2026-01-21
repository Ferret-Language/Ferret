// Ferret runtime: Optional helpers

#ifndef FERRET_OPTIONAL_H
#define FERRET_OPTIONAL_H

#include <stddef.h>
#include <stdint.h>

void ferret_optional_unwrap_or(const void* opt, const void* default_val, void* out, uint64_t val_size);
size_t ferret_optional_payload_size(size_t value_size, size_t value_align);
void* ferret_optional_alloc_none(size_t value_size, size_t value_align);

#endif // FERRET_OPTIONAL_H
