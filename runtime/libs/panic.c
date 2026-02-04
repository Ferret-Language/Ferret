// Ferret runtime: Panic helper

#include <stdio.h>
#include <stdlib.h>
#include "../core/runtime_naming.h"

// Define the module prefix for this file (implements built-in panic functionality)
#define MODULE_PREFIX ferret_global

// Exit the program with a message.
void FERRET_FUNC(panic)(const char* msg) {
    if (msg != NULL && msg[0] != '\0') {
        fprintf(stderr, "panic: %s\n", msg);
    } else {
        fputs("panic\n", stderr);
    }
    fflush(stderr);
    exit(1);
}
