// Ferret runtime: Dynamic Array Library
// Optimized growable array implementation (similar to Go slices or C++ vectors)

#ifndef FERRET_ARRAY_H
#define FERRET_ARRAY_H

#include <stdint.h>
#include <stdbool.h>
#include <stdlib.h>
#include "type_system.h"
// Note: string.h is included in array.c, not here, to avoid include order issues

// Dynamic array structure
// Similar to Go slices: data pointer, length, and capacity
typedef struct {
    void* data;        // Pointer to array data
    int32_t length;    // Current number of elements
    int32_t capacity;  // Allocated capacity
    size_t elem_size;  // Size of each element in bytes
    ferret_type_info_t* elem_type_info; // Type descriptor for element type
} ferret_array_t;

// Create a new dynamic array with initial capacity
// Returns NULL on allocation failure
ferret_array_t* ferret_array_new(size_t elem_size, int32_t initial_capacity, ferret_type_info_t* elem_type_info);

// Create array from existing data (takes ownership)
ferret_array_t* ferret_array_from_data(void* data, int32_t length, int32_t capacity, size_t elem_size, ferret_type_info_t* elem_type_info);

// Clone array (deep copy of elements)
ferret_array_t* ferret_array_clone(const ferret_array_t* arr);

// Assign array contents into a destination slot, reusing capacity when possible.
void ferret_array_assign(ferret_array_t** dst, const ferret_array_t* src);

// Append an element to the array (amortized O(1))
// Returns false on allocation failure
bool ferret_array_append(ferret_array_t* arr, const void* elem);

// Get element at index (returns pointer to element, NULL if out of bounds)
void* ferret_array_get(ferret_array_t* arr, int32_t index);

// Set element at index (returns false if out of bounds)
bool ferret_array_set(ferret_array_t* arr, int32_t index, const void* elem);

// Get array length
int32_t ferret_array_len(const ferret_array_t* arr);

// Get array capacity
int32_t ferret_array_cap(const ferret_array_t* arr);

#endif // FERRET_ARRAY_H
