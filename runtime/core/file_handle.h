#ifndef FERRET_FILE_HANDLE_H
#define FERRET_FILE_HANDLE_H

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "alloc.h"

typedef struct ferret_file_handle {
    FILE* fp;
    char* path;
    char* mode;
    uint32_t refs;
} ferret_file_handle_t;

static inline ferret_file_handle_t* ferret_file_handle_from_raw(int64_t raw) {
    if (raw == 0) {
        return NULL;
    }
    return (ferret_file_handle_t*)(intptr_t)raw;
}

static inline int64_t ferret_file_handle_to_raw(ferret_file_handle_t* handle) {
    return (int64_t)(intptr_t)handle;
}

static inline FILE* ferret_file_handle_file(const ferret_file_handle_t* handle) {
    if (handle == NULL || handle->fp == NULL || handle->refs == 0) {
        return NULL;
    }
    return handle->fp;
}

static inline const char* ferret_file_handle_path(const ferret_file_handle_t* handle) {
    if (handle == NULL || handle->refs == 0) {
        return NULL;
    }
    return handle->path;
}

static inline const char* ferret_file_handle_mode(const ferret_file_handle_t* handle) {
    if (handle == NULL || handle->refs == 0) {
        return NULL;
    }
    return handle->mode;
}

static inline char* ferret_file_handle_strdup(const char* s) {
    if (s == NULL) {
        return NULL;
    }
    size_t len = strlen(s);
    char* out = (char*)ferret_alloc(len + 1);
    if (out == NULL) {
        return NULL;
    }
    memcpy(out, s, len + 1);
    return out;
}

static inline ferret_file_handle_t* ferret_file_handle_new_with_meta(FILE* fp, const char* path, const char* mode) {
    if (fp == NULL) {
        return NULL;
    }
    ferret_file_handle_t* handle = (ferret_file_handle_t*)ferret_alloc(sizeof(ferret_file_handle_t));
    if (handle == NULL) {
        return NULL;
    }
    handle->fp = fp;
    handle->path = ferret_file_handle_strdup(path);
    handle->mode = ferret_file_handle_strdup(mode);
    handle->refs = 1;
    return handle;
}

static inline ferret_file_handle_t* ferret_file_handle_new(FILE* fp) {
    return ferret_file_handle_new_with_meta(fp, NULL, NULL);
}

static inline ferret_file_handle_t* ferret_file_handle_retain(ferret_file_handle_t* handle) {
    if (handle == NULL || handle->fp == NULL || handle->refs == 0) {
        return NULL;
    }
    handle->refs += 1;
    return handle;
}

static inline void ferret_file_handle_release(ferret_file_handle_t* handle) {
    if (handle == NULL || handle->refs == 0) {
        return;
    }
    handle->refs -= 1;
    if (handle->refs > 0) {
        return;
    }
    FILE* fp = handle->fp;
    handle->fp = NULL;
    if (fp != NULL) {
        fclose(fp);
    }
    if (handle->path != NULL) {
        ferret_free(handle->path);
        handle->path = NULL;
    }
    if (handle->mode != NULL) {
        ferret_free(handle->mode);
        handle->mode = NULL;
    }
    ferret_free(handle);
}

#endif // FERRET_FILE_HANDLE_H
