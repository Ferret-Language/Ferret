#include "ferret_runtime.h"

ferret_raw ferret_std_mem_Expose(ferret_raw owner) {
    return owner;
}

ferret_raw ferret_std_mem_Adopt(ferret_raw raw) {
    return raw;
}
