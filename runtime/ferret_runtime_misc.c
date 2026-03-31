#include <stdlib.h>
#include <time.h>

int f_random(void) {
    srand((unsigned)time(NULL));
    return rand();
}
