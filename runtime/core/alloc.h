#ifndef FERRET_ALLOC_H
#define FERRET_ALLOC_H

#include <stdint.h>

void *ferret_alloc(uint64_t size);
uint64_t ferret_addr_heap(void *ptr);

#endif
