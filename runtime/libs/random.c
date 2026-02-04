// Ferret runtime: Random number generation

#include <stdlib.h>
#include <time.h>
#include "../core/runtime_naming.h"

// Define the module prefix for this file (implements ferret_libs/random.fer)
#define MODULE_PREFIX ferret_random

// Generate a random float between 0.0 and 1.0
float FERRET_FUNC(Random)(void) {
    static int seeded = 0;
    if (!seeded) {
        seeded = 1;
        srand((unsigned int)time(NULL));
    }
    return (float)rand() / (float)RAND_MAX;
}
