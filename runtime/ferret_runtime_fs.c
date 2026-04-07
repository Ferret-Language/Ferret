#include "ferret_runtime_internal.h"

#include <stdio.h>

typedef struct {
    FILE *file;
} FerretStdFileHandle;

ferret_raw ferret_std_fs_open(const FerretStr *path) {
    char *c_path;
    FILE *file;
    FerretStdFileHandle *handle;

    c_path = (char *)ferret_global_str_cstr(path);
    if (c_path == NULL) {
        return NULL;
    }
    file = fopen(c_path, "wb");
    free(c_path);
    if (file == NULL) {
        return NULL;
    }
    handle = (FerretStdFileHandle *)malloc(sizeof(FerretStdFileHandle));
    if (handle == NULL) {
        fclose(file);
        return NULL;
    }
    handle->file = file;
    return (ferret_raw)handle;
}

ferret_usize ferret_std_fs_write(ferret_raw handle, const FerretStr *text) {
    FerretStdFileHandle *owned = (FerretStdFileHandle *)handle;
    FILE *file;
    size_t written;

    if (owned == NULL || text == NULL || text->ptr == NULL || text->len == 0) {
        return 0;
    }
    file = owned->file;
    if (file == NULL) {
        return 0;
    }

    written = fwrite((const void *)text->ptr, 1, (size_t)text->len, file);
    fflush(file);
    return (ferret_usize)written;
}

void ferret_std_fs_close(ferret_raw handle) {
    FerretStdFileHandle *owned = (FerretStdFileHandle *)handle;
    FILE *file;

    if (owned == NULL) {
        return;
    }
    file = owned->file;
    owned->file = NULL;
    free(owned);
    if (file == NULL) {
        return;
    }
    fclose(file);
}
