#include "ferret_runtime.h"

ferret_raw ferret_std_mem_Expose(ferret_raw owner) {
    return owner;
}

ferret_raw ferret_std_mem_ExposeRef(const ferret_raw *owner) {
    if (owner == NULL) {
        return NULL;
    }
    return *owner;
}

ferret_raw ferret_std_mem_Adopt(ferret_raw raw) {
    return raw;
}
