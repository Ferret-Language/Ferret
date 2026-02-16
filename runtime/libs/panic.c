// Ferret runtime: Panic helper

#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "../core/optional.h"
#include "../core/runtime_naming.h"

// Define the module prefix for this file (implements built-in panic functionality)
#define MODULE_PREFIX ferret_global

static const char* ferret_panic_msg = NULL;
static bool ferret_panic_active = false;
static bool ferret_panic_recovered = false;
static bool ferret_panic_in_defer = false;

void FERRET_FUNC(panic_begin)(const char* msg) {
    ferret_panic_active = true;
    ferret_panic_recovered = false;
    ferret_panic_in_defer = false;
    ferret_panic_msg = msg;
}

void FERRET_FUNC(panic_enter_defer)(void) {
    if (ferret_panic_active) {
        ferret_panic_in_defer = true;
    }
}

void FERRET_FUNC(panic_leave_defer)(void) {
    ferret_panic_in_defer = false;
}

bool FERRET_FUNC(panic_is_recovered)(void) {
    return ferret_panic_active && ferret_panic_recovered;
}

void FERRET_FUNC(panic_clear)(void) {
    ferret_panic_active = false;
    ferret_panic_recovered = false;
    ferret_panic_in_defer = false;
    ferret_panic_msg = NULL;
}

void FERRET_FUNC(panic_abort)(void) {
    const char* msg = ferret_panic_msg;
    ferret_panic_active = false;
    ferret_panic_recovered = false;
    ferret_panic_in_defer = false;
    ferret_panic_msg = NULL;

    if (msg != NULL && msg[0] != '\0') {
        fprintf(stderr, "panic: %s\n", msg);
    } else {
        fputs("panic\n", stderr);
    }
    fflush(stderr);
    exit(1);
}

void FERRET_FUNC(panic_recover)(void* out) {
    if (out == NULL) {
        return;
    }
    void** out_ptr = (void**)out;
    *out_ptr = NULL;

    void* opt = ferret_optional_alloc_none(sizeof(char*), sizeof(void*));
    if (opt == NULL) {
        return;
    }

    if (ferret_panic_active && ferret_panic_in_defer && !ferret_panic_recovered) {
        const char* msg = ferret_panic_msg;
        if (msg == NULL) {
            msg = "";
        }
        memcpy(opt, &msg, sizeof(char*));
        uint8_t* flag = (uint8_t*)opt + sizeof(char*);
        *flag = 1;
        ferret_panic_recovered = true;
    }

    *out_ptr = opt;
}

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
