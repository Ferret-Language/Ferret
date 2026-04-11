#include "ferret_runtime_internal.h"

#include <stdlib.h>
#include <string.h>
#include <stdio.h>

typedef struct {
    ferret_u8    *data;
    ferret_usize  len;
    ferret_usize  cap;
    ferret_usize  read_pos;
} FerretStdIoBuffer;

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

    if (file == NULL) {
        ferret__io_error_set(FERRET_IO_ERR_UNKNOWN);
        return 0;
    }
    if (text == NULL || text->ptr == NULL || text->len == 0) {
        ferret__io_error_clear();
        return 0;
    }

    written = fwrite((const void *)text->ptr, 1, (size_t)text->len, file);
    fflush(file);
    if (written == 0 && ferror(file)) {
        ferret__io_error_set(FERRET_IO_ERR_UNKNOWN);
        clearerr(file);
        return 0;
    }
    ferret__io_error_clear();
    return (ferret_usize)written;
}

static ferret_bool ferret__io_buffer_reserve(FerretStdIoBuffer *buffer, ferret_usize additional) {
    ferret_usize needed;
    ferret_usize next;
    void *next_data;

    if (buffer == NULL) {
        return 0;
    }

    needed = buffer->len + additional;
    if (needed <= buffer->cap) {
        return 1;
    }

    next = buffer->cap;
    if (next == 0) {
        next = 8;
    }
    while (next < needed) {
        next = next + next;
    }

    next_data = realloc(buffer->data, (size_t)next);
    if (next_data == NULL) {
        return 0;
    }

    buffer->data = (ferret_u8 *)next_data;
    buffer->cap = next;
    return 1;
}

ferret_raw ferret_std_io_buffer_new(void) {
    FerretStdIoBuffer *buffer = (FerretStdIoBuffer *)calloc(1, sizeof(FerretStdIoBuffer));
    return (ferret_raw)buffer;
}

ferret_usize ferret_std_io_buffer_write(ferret_raw handle, const FerretStr *text) {
    FerretStdIoBuffer *buffer = (FerretStdIoBuffer *)handle;
    ferret_usize count;

    if (buffer == NULL) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return 0;
    }
    if (text == NULL || text->ptr == NULL || text->len == 0) {
        ferret__io_error_clear();
        return 0;
    }

    count = text->len;
    if (!ferret__io_buffer_reserve(buffer, count)) {
        ferret__io_error_set(FERRET_IO_ERR_UNKNOWN);
        return 0;
    }

    memcpy((void *)(buffer->data + buffer->len), (const void *)text->ptr, (size_t)count);
    buffer->len += count;
    ferret__io_error_clear();
    return count;
}

FerretSliceU8 ferret_std_io_buffer_read(ferret_raw handle, ferret_usize size) {
    FerretStdIoBuffer *buffer = (FerretStdIoBuffer *)handle;
    FerretSliceU8 out = {0};
    ferret_usize available;
    ferret_usize count;

    if (buffer == NULL) {
        ferret__io_error_set(FERRET_IO_ERR_CLOSED);
        return out;
    }
    if (size == 0 || buffer->read_pos >= buffer->len) {
        ferret__io_error_clear();
        return out;
    }

    available = buffer->len - buffer->read_pos;
    count = size;
    if (count > available) {
        count = available;
    }

    out.ptr = buffer->data + buffer->read_pos;
    out.len = count;
    buffer->read_pos += count;
    ferret__io_error_clear();
    return out;
}

FerretStr ferret_std_io_buffer_view(ferret_raw handle) {
    FerretStdIoBuffer *buffer = (FerretStdIoBuffer *)handle;
    FerretStr out = {0};

    if (buffer == NULL || buffer->data == NULL || buffer->len == 0) {
        return out;
    }

    out.ptr = buffer->data;
    out.len = buffer->len;
    return out;
}

void ferret_std_io_buffer_reset(ferret_raw handle) {
    FerretStdIoBuffer *buffer = (FerretStdIoBuffer *)handle;
    if (buffer == NULL) {
        return;
    }
    buffer->len = 0;
    buffer->read_pos = 0;
}

void ferret_std_io_buffer_close(ferret_raw handle) {
    FerretStdIoBuffer *buffer = (FerretStdIoBuffer *)handle;
    if (buffer == NULL) {
        return;
    }
    free(buffer->data);
    free(buffer);
}
