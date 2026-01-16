#ifndef FERRET_STRING_RUNTIME_H
#define FERRET_STRING_RUNTIME_H

#include <stdint.h>

int32_t ferret_string_len(const char* str);
int32_t ferret_strcmp(const char* s1, const char* s2);
void ferret_string_assign(char** dst, const char* src);

#endif
