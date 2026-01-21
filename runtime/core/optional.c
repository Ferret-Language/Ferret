// Ferret runtime: Optional helpers
#include <stdbool.h>
#include <stdint.h>
#include <string.h>
#include "alloc.h"
#include "optional.h"

// Unwrap optional into out buffer using default when none.
// Layout: value bytes followed by 1-byte is_some flag at offset val_size.
void ferret_optional_unwrap_or(const void* opt, const void* default_val, void* out, uint64_t val_size) {
    if (out == NULL) {
        return;
    }

    uint8_t* out_bytes = (uint8_t*)out;
    if (val_size == 0) {
        return;
    }

    if (opt == NULL) {
        if (default_val != NULL) {
            memcpy(out_bytes, default_val, (size_t)val_size);
        } else {
            memset(out_bytes, 0, (size_t)val_size);
        }
        return;
    }

    const uint8_t* opt_bytes = (const uint8_t*)opt;
    const uint8_t* flag_ptr = opt_bytes + val_size;
    if (*flag_ptr) {
        memcpy(out_bytes, opt_bytes, (size_t)val_size);
    } else if (default_val != NULL) {
        memcpy(out_bytes, default_val, (size_t)val_size);
    } else {
        memset(out_bytes, 0, (size_t)val_size);
    }
}

size_t ferret_optional_payload_size(size_t value_size, size_t value_align) {
    if (value_align == 0) {
        value_align = 1;
    }
    size_t size = value_size + 1;
    if (value_align > 1) {
        size = (size + value_align - 1) & ~(value_align - 1);
    }
    return size;
}

void* ferret_optional_alloc_none(size_t value_size, size_t value_align) {
    size_t payload = ferret_optional_payload_size(value_size, value_align);
    uint8_t* out = (uint8_t*)ferret_alloc(payload);
    if (out == NULL) {
        return NULL;
    }
    memset(out, 0, payload);
    return out;
}
