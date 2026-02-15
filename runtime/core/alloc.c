#include "alloc.h"

#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>

// Main allocation function used by generated code and runtime
void *ferret_alloc(uint64_t size) {
    void* ptr = malloc((size_t)size);
    if (!ptr) {
        return NULL;
    }
    return ptr;
}

// Reallocation function used by runtime
void *ferret_realloc(void *ptr, uint64_t new_size) {
    return realloc(ptr, (size_t)new_size);
}

// Free function used by runtime
void ferret_free(void *ptr) {
    free(ptr);
}

// Calloc function used by runtime
void *ferret_calloc(uint64_t count, uint64_t size) {
    return calloc((size_t)count, (size_t)size);
}
