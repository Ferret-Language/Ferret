#ifndef FERRET_STRING_RUNTIME_H
#define FERRET_STRING_RUNTIME_H

#include <stdint.h>
#include "array.h"

int32_t ferret_string_len(const char* str);
int32_t ferret_strcmp(const char* s1, const char* s2);
void ferret_string_assign(char** dst, const char* src);

// String to array conversions
ferret_array_t* ferret_string_to_char_array(const char* str);
ferret_array_t* ferret_string_to_byte_array(const char* str);

// Array to string conversions
char* ferret_char_array_to_string(const ferret_array_t* arr);
char* ferret_byte_array_to_string(const ferret_array_t* arr);

#endif
