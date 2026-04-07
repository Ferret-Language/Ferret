#include "ferret_runtime_internal.h"

#include <stdio.h>

static FILE *ferret__io_stream_file(ferret_i32 kind) {
    switch (kind) {
    case 0:
        return stdin;
    case 1:
        return stdout;
    case 2:
        return stderr;
    default:
        return NULL;
    }
}

ferret_usize ferret_std_io_write_stream(ferret_i32 kind, const FerretStr *text) {
    FILE *file = ferret__io_stream_file(kind);
    size_t written;

    if (file == NULL || text == NULL || text->ptr == NULL || text->len == 0) {
        return 0;
    }

    written = fwrite((const void *)text->ptr, 1, (size_t)text->len, file);
    fflush(file);
    return (ferret_usize)written;
}
