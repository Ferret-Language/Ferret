#include "alloc.h"

#include <stdlib.h>
#include <stdint.h>

void *ferret_alloc(uint64_t size) {
    void* ptr = malloc((size_t)size);
    if (!ptr) {
        return NULL;
    }
    return ptr;
}
