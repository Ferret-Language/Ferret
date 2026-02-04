#ifndef FERRET_ALLOC_H
#define FERRET_ALLOC_H

#include <stdint.h>

// Main allocation function used by generated code and runtime
void *ferret_alloc(uint64_t size);
// Reallocation function used by runtime
void *ferret_realloc(void *ptr, uint64_t new_size);
// Free function used by runtime
void ferret_free(void *ptr);
// Calloc function used by runtime
void *ferret_calloc(uint64_t count, uint64_t size);

#endif
