#include "alloc.h"

#include <stdlib.h>
#include <stdio.h>
#include <stdint.h>

typedef struct ferret_heap_node {
    void* ptr;
    uint64_t size;
    struct ferret_heap_node* next;
} ferret_heap_node;

static ferret_heap_node* ferret_heap_head = NULL;

void *ferret_alloc(uint64_t size) {
    void* ptr = malloc((size_t)size);
    if (!ptr) {
        return NULL;
    }
    ferret_heap_node* node = (ferret_heap_node*)malloc(sizeof(ferret_heap_node));
    if (!node) {
        return ptr;
    }
    node->ptr = ptr;
    node->size = size;
    node->next = ferret_heap_head;
    ferret_heap_head = node;
    return ptr;
}

uint64_t ferret_addr_heap(void *ptr) {
    if (!ptr) {
        return 0;
    }
    uintptr_t addr = (uintptr_t)ptr;
    for (ferret_heap_node* node = ferret_heap_head; node != NULL; node = node->next) {
        if (!node->ptr || node->size == 0) {
            continue;
        }
        uintptr_t start = (uintptr_t)node->ptr;
        uintptr_t end = start + (uintptr_t)node->size;
        if (addr >= start && addr < end) {
            return (uint64_t)addr;
        }
    }
    return 0;
}

// Union printing (placeholder)
void ferret_io_Println_union(void* u) {
    printf("<union>\n");
}
