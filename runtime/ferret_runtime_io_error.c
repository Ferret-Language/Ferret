#include "ferret_runtime.h"

#if __STDC_VERSION__ >= 201112L
static _Thread_local ferret_i32 ferret__io_error_code = FERRET_IO_ERR_NONE;
#else
static ferret_i32 ferret__io_error_code = FERRET_IO_ERR_NONE;
#endif

void ferret__io_error_set(ferret_i32 code) {
    ferret__io_error_code = code;
}

void ferret__io_error_clear(void) {
    ferret__io_error_code = FERRET_IO_ERR_NONE;
}

ferret_i32 ferret_std_io_last_error(void) {
    return ferret__io_error_code;
}
