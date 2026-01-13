// Ferret runtime: Panic helper

#include <stdio.h>
#include <stdlib.h>

// Exit the program with a message.
void ferret_global_panic(const char* msg) {
    if (msg != NULL && msg[0] != '\0') {
        fprintf(stderr, "panic: %s\n", msg);
    } else {
        fputs("panic\n", stderr);
    }
    fflush(stderr);
    exit(1);
}
