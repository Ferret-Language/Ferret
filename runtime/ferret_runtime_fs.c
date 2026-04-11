#include "ferret_runtime_internal.h"

#include <errno.h>
#include <stdio.h>

typedef struct {
    FILE *file;
} FerretStdFileHandle;

static ferret_i32 ferret__fs_map_errno(int code) {
    switch (code) {
    case EACCES:
    case EPERM:
        return FERRET_IO_ERR_PERMISSION_DENIED;
    case ENOENT:
        return FERRET_IO_ERR_NOT_FOUND;
    case EPIPE:
    case EBADF:
        return FERRET_IO_ERR_CLOSED;
    default:
        return FERRET_IO_ERR_UNKNOWN;
    }
}

ferret_raw ferret_std_fs_open(const FerretStr *path) {
    char *c_path;
    FILE *file;
    FerretStdFileHandle *handle;

    c_path = (char *)ferret_global_str_cstr(path);
    if (c_path == NULL) {
        ferret__io_error_set(FERRET_IO_ERR_UNKNOWN);
        return NULL;
    }
    file = fopen(c_path, "wb");
    free(c_path);
    if (file == NULL) {
        ferret__io_error_set(ferret__fs_map_errno(errno));
        return NULL;
    }
    handle = (FerretStdFileHandle *)malloc(sizeof(FerretStdFileHandle));
    if (handle == NULL) {
        fclose(file);
        ferret__io_error_set(FERRET_IO_ERR_UNKNOWN);
        return NULL;
    }
    handle->file = file;
    ferret__io_error_clear();
    return (ferret_raw)handle;
}

ferret_usize ferret_std_fs_write(ferret_raw handle, const FerretStr *text) {
    FerretStdFileHandle *owned = (FerretStdFileHandle *)handle;
    FILE *file;
    size_t written;

    if (owned == NULL) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return 0;
    }
    if (text == NULL || text->ptr == NULL || text->len == 0) {
        ferret__io_error_clear();
        return 0;
    }
    file = owned->file;
    if (file == NULL) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return 0;
    }

    written = fwrite((const void *)text->ptr, 1, (size_t)text->len, file);
    fflush(file);
    if (written == 0 && ferror(file)) {
        ferret__io_error_set(ferret__fs_map_errno(errno));
        clearerr(file);
        return 0;
    }
    ferret__io_error_clear();
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
