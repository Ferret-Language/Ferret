// Ferret runtime: File I/O functions
// Native implementations for std/fs module
//
// IMPORTANT: For functions returning result types (str ! str, bool ! str, etc.),
// the out parameter comes FIRST because the compiler prepends it.
// Call convention: call $func(l %out, ...other_args)
//
// Result type layout: [union data][1-byte tag]
// Tag values: 0 = Err, 1 = Ok (IMPORTANT: matches QBE codegen)

#define _GNU_SOURCE  // For getline on POSIX
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include "../core/alloc.h"
#include "../core/array.h"
#include "../core/type_system.h"
#include "../core/result.h"
#include <string.h>
#include <sys/stat.h>
#include <errno.h>

#ifdef _WIN32
#include <io.h>
#include <direct.h>
#define access _access
#define mkdir(path, mode) _mkdir(path)
#define rmdir _rmdir
#define getcwd _getcwd
#define F_OK 0
#define PATH_SEP '\\'
typedef long long ssize_t;
#else
#include <unistd.h>
#define PATH_SEP '/'
#endif

#include "../core/runtime_naming.h"

// Define the module prefix for this file (implements ferret_libs/std/fs.fer)
#define MODULE_PREFIX ferret_std_fs
#define FERRET_FILE FERRET_TYPE(File)

typedef struct {
    int64_t handle;
    const char* path;
    const char* mode;
} FERRET_FILE;

// Result layout: [8-byte union (value or error str)][1-byte tag] (+ padding)
// Tag: 1 = Ok, 0 = Err

// FileInfo struct layout: { str path, i64 size, bool isDir, bool isFile, bool exists }
// File struct layout: { i64 handle, str path, str mode }

// Helper to duplicate string
static char* str_dup(const char* s) {
    if (!s) return NULL;
    size_t len = strlen(s);
    char* copy = (char*)ferret_alloc(len + 1);
    if (copy) {
        memcpy(copy, s, len + 1);
    }
    return copy;
}

#define FERRET_FS_DEFINE_OPEN(name, mode_str, open_error) \
    void FERRET_FUNC(name)(void* out, const char* path) { \
        if (!out) return; \
        int64_t* handle_ptr = (int64_t*)out; \
        char** path_out = (char**)((char*)out + 8); \
        char** mode_out = (char**)((char*)out + 16); \
        int8_t* tag_ptr = (int8_t*)((char*)out + 24); \
        if (!path) { \
            *(char**)out = "path is null"; \
            *tag_ptr = 0; \
            return; \
        } \
        FILE* f = fopen(path, mode_str); \
        if (!f) { \
            *(char**)out = open_error; \
            *tag_ptr = 0; \
            return; \
        } \
        *handle_ptr = (int64_t)(intptr_t)f; \
        *path_out = str_dup(path); \
        *mode_out = mode_str; \
        *tag_ptr = 1; \
    }

#define FERRET_FS_DEFINE_WRITE_HANDLE(name, add_newline) \
    void FERRET_FUNC(name)(void* out, const FERRET_FILE* file, const char* content) { \
        if (!out) return; \
        bool* val_ptr = (bool*)out; \
        int8_t* tag_ptr = (int8_t*)((char*)out + 8); \
        if (!file || file->handle == 0) { \
            *(char**)out = "invalid file handle"; \
            *tag_ptr = 0; \
            return; \
        } \
        FILE* f = (FILE*)(intptr_t)file->handle; \
        if (content) { \
            size_t len = strlen(content); \
            size_t written = fwrite(content, 1, len, f); \
            if (written != len) { \
                *(char**)out = "failed to write all data"; \
                *tag_ptr = 0; \
                return; \
            } \
        } \
        if (add_newline) { \
            if (fputc('\n', f) == EOF) { \
                *(char**)out = "failed to write newline"; \
                *tag_ptr = 0; \
                return; \
            } \
        } \
        *val_ptr = true; \
        *tag_ptr = 1; \
    }

// ============================================
// Quick one-liner functions
// ============================================

// ============================================
// File info functions
// ============================================

// Check if file exists
bool FERRET_FUNC(Exists)(const char* path) {
    if (!path) return false;
    return access(path, F_OK) == 0;
}

// Get file info - returns FileInfo struct
// FileInfo layout: { str path (8), i64 size (8), bool isDir (1), bool isFile (1), bool exists (1) }
// With alignment: offset 0=path, 8=size, 16=isDir, 17=isFile, 18=exists
// OUT PARAM FIRST
void FERRET_FUNC(Stat)(void* out, const char* path) {
    if (!out) return;
    
    // Result layout: [FileInfo struct][4-byte tag]
    // FileInfo needs: path(8) + size(8) + isDir(1) + isFile(1) + exists(1) + padding = 24 bytes aligned
    char** path_ptr = (char**)out;
    int64_t* size_ptr = (int64_t*)((char*)out + 8);
    bool* isDir_ptr = (bool*)((char*)out + 16);
    bool* isFile_ptr = (bool*)((char*)out + 17);
    bool* exists_ptr = (bool*)((char*)out + 18);
    int8_t* tag_ptr = (int8_t*)((char*)out + 24);  // After struct with alignment
    
    if (!path) {
        *(char**)out = "path is null";
        *tag_ptr = 0;
        return;
    }
    
    struct stat st;
    if (stat(path, &st) != 0) {
        // File doesn't exist - return info with exists=false
        *path_ptr = str_dup(path);
        *size_ptr = 0;
        *isDir_ptr = false;
        *isFile_ptr = false;
        *exists_ptr = false;
        *tag_ptr = 1;
        return;
    }
    
    *path_ptr = str_dup(path);
    *size_ptr = (int64_t)st.st_size;
    *isDir_ptr = S_ISDIR(st.st_mode);
    *isFile_ptr = S_ISREG(st.st_mode);
    *exists_ptr = true;
    *tag_ptr = 1;
}

// Get file size
// OUT PARAM FIRST
void FERRET_FUNC(Size)(void* out, const char* path) {
    if (!out) return;
    
    int64_t* size_ptr = (int64_t*)out;
    int8_t* tag_ptr = (int8_t*)((char*)out + 8);
    
    if (!path) {
        *(char**)out = "path is null";
        *tag_ptr = 0;
        return;
    }
    
    struct stat st;
    if (stat(path, &st) != 0) {
        *(char**)out = "file not found";
        *tag_ptr = 0;
        return;
    }
    
    *size_ptr = (int64_t)st.st_size;
    *tag_ptr = 1;
}

// ============================================
// File handle operations
// ============================================

// Open file for reading - returns File struct
// File layout: { i64 handle (8), str path (8), str mode (8) } = 24 bytes
// OUT PARAM FIRST
FERRET_FS_DEFINE_OPEN(Open, "r", "failed to open file")

// Create/truncate file for writing
// OUT PARAM FIRST
FERRET_FS_DEFINE_OPEN(Create, "w", "failed to create file")

// Open file for appending
// OUT PARAM FIRST
FERRET_FS_DEFINE_OPEN(OpenAppend, "a", "failed to open file for append")

// Close file handle
// File struct passed by value: { i64 handle, str path, str mode }
void FERRET_FUNC(Close)(const FERRET_FILE* file) {
    if (!file || file->handle == 0) return;
    FILE* f = (FILE*)(intptr_t)file->handle;
    fclose(f);
}

// Read line from file handle
// OUT PARAM FIRST
void FERRET_FUNC(ReadLine)(void* out, const FERRET_FILE* file) {
    if (!out) return;
    
    char** str_ptr = (char**)out;
    int8_t* tag_ptr = (int8_t*)((char*)out + 8);
    
    if (!file || file->handle == 0) {
        *str_ptr = "invalid file handle";
        *tag_ptr = 0;
        return;
    }
    
    FILE* f = (FILE*)(intptr_t)file->handle;
    
    char* line = NULL;
    size_t len = 0;
    
#ifdef _WIN32
    // Windows fallback: manual line reading
    size_t capacity = 128;
    line = (char*)ferret_alloc(capacity);
    if (!line) {
        *str_ptr = "out of memory";
        *tag_ptr = 0;
        return;
    }
    
    len = 0;
    int c;
    while ((c = fgetc(f)) != EOF && c != '\n') {
        if (len + 1 >= capacity) {
            capacity *= 2;
            char* newline = (char*)ferret_realloc(line, capacity);
            if (!newline) {
                ferret_free(line);
                *str_ptr = "out of memory";
                *tag_ptr = 0;
                return;
            }
            line = newline;
        }
        line[len++] = (char)c;
    }
    
    if (len == 0 && c == EOF) {
        ferret_free(line);
        *str_ptr = "end of file";
        *tag_ptr = 0;
        return;
    }
    
    line[len] = '\0';
#else
    ssize_t read = getline(&line, &len, f);
    if (read == -1) {
        if (line) ferret_free(line);
        *str_ptr = "end of file";
        *tag_ptr = 0;
        return;
    }
    
    // Remove trailing newline
    if (read > 0 && line[read - 1] == '\n') {
        line[read - 1] = '\0';
    }
#endif
    
    *str_ptr = line;
    *tag_ptr = 1;
}

// Read bytes from file handle
// OUT PARAM FIRST
void FERRET_FUNC(ReadBytes)(void* out, const FERRET_FILE* file, int32_t maxBytes) {
    if (!out) return;

    if (!file || file->handle == 0) {
        FERRET_RESULT_ERR(out, 8, "invalid file handle");
        return;
    }
    if (maxBytes <= 0) {
        FERRET_RESULT_ERR(out, 8, "maxBytes must be > 0");
        return;
    }

    FILE* f = (FILE*)(intptr_t)file->handle;
    uint8_t* buf = (uint8_t*)ferret_alloc((size_t)maxBytes);
    if (!buf) {
        FERRET_RESULT_ERR(out, 8, "out of memory");
        return;
    }

    size_t read = fread(buf, 1, (size_t)maxBytes, f);
    if (read == 0) {
        if (ferror(f)) {
            ferret_free(buf);
            FERRET_RESULT_ERR(out, 8, "failed to read file");
            return;
        }
        ferret_free(buf);
        ferret_array_t* empty = ferret_array_new(sizeof(uint8_t), 0, (ferret_type_info_t*)&ferret_type_byte);
        if (!empty) {
            FERRET_RESULT_ERR(out, 8, "out of memory");
            return;
        }
        FERRET_RESULT_OK(out, 8, ferret_array_t*, empty);
        return;
    }

    ferret_array_t* arr = ferret_array_from_data(buf, (int32_t)read, (int32_t)read, sizeof(uint8_t), (ferret_type_info_t*)&ferret_type_byte);
    if (!arr) {
        ferret_free(buf);
        FERRET_RESULT_ERR(out, 8, "out of memory");
        return;
    }
    FERRET_RESULT_OK(out, 8, ferret_array_t*, arr);
}

// Write string to file handle
// OUT PARAM FIRST
FERRET_FS_DEFINE_WRITE_HANDLE(WriteStr, 0)

// Write bytes to file handle (method receiver)
// OUT PARAM FIRST
void FERRET_FUNC(File_Write)(void* out, const FERRET_FILE* file, ferret_array_t* data) {
    if (!out) return;
    if (!file || file->handle == 0) {
        FERRET_RESULT_ERR(out, 8, "invalid file handle");
        return;
    }
    if (!data || data->length == 0) {
        FERRET_RESULT_OK(out, 8, int32_t, 0);
        return;
    }

    FILE* f = (FILE*)(intptr_t)file->handle;
    size_t len = (size_t)data->length;
    size_t written = fwrite(data->data, 1, len, f);
    if (written != len) {
        FERRET_RESULT_ERR(out, 8, "failed to write all data");
        return;
    }
    FERRET_RESULT_OK(out, 8, int32_t, (int32_t)written);
}

// Write line to file handle (with newline)
// OUT PARAM FIRST
FERRET_FS_DEFINE_WRITE_HANDLE(WriteLine, 1)

// ============================================
// Directory operations
// ============================================

// Delete file
// OUT PARAM FIRST
void FERRET_FUNC(Remove)(void* out, const char* path) {
    if (!out) return;
    
    bool* val_ptr = (bool*)out;
    int8_t* tag_ptr = (int8_t*)((char*)out + 8);
    
    if (!path) {
        *(char**)out = "path is null";
        *tag_ptr = 0;
        return;
    }
    
    if (remove(path) != 0) {
        *(char**)out = "failed to delete file";
        *tag_ptr = 0;
        return;
    }
    
    *val_ptr = true;
    *tag_ptr = 1;
}

// Create directory
// OUT PARAM FIRST
void FERRET_FUNC(Mkdir)(void* out, const char* path) {
    if (!out) return;
    
    bool* val_ptr = (bool*)out;
    int8_t* tag_ptr = (int8_t*)((char*)out + 8);
    
    if (!path) {
        *(char**)out = "path is null";
        *tag_ptr = 0;
        return;
    }
    
    if (mkdir(path, 0755) != 0) {
        if (errno == EEXIST) {
            // Directory already exists - not an error
            *val_ptr = true;
            *tag_ptr = 1;
            return;
        }
        *(char**)out = "failed to create directory";
        *tag_ptr = 0;
        return;
    }
    
    *val_ptr = true;
    *tag_ptr = 1;
}

// Remove directory
// OUT PARAM FIRST
void FERRET_FUNC(Rmdir)(void* out, const char* path) {
    if (!out) return;
    
    bool* val_ptr = (bool*)out;
    int8_t* tag_ptr = (int8_t*)((char*)out + 8);
    
    if (!path) {
        *(char**)out = "path is null";
        *tag_ptr = 0;
        return;
    }
    
    if (rmdir(path) != 0) {
        *(char**)out = "failed to remove directory";
        *tag_ptr = 0;
        return;
    }
    
    *val_ptr = true;
    *tag_ptr = 1;
}

// ============================================
// Path utilities
// ============================================

// Get current working directory
void FERRET_FUNC(Cwd)(void* out) {
    if (!out) return;
    
    char** str_ptr = (char**)out;
    int8_t* tag_ptr = (int8_t*)((char*)out + 8);
    
    char* buf = (char*)ferret_alloc(4096);
    if (!buf) {
        *str_ptr = "out of memory";
        *tag_ptr = 0;
        return;
    }
    
    if (!getcwd(buf, 4096)) {
        ferret_free(buf);
        *str_ptr = "failed to get current directory";
        *tag_ptr = 0;
        return;
    }
    
    *str_ptr = buf;
    *tag_ptr = 1;
}

// Join two path components
char* FERRET_FUNC(Join)(const char* base, const char* path) {
    if (!base && !path) return str_dup("");
    if (!base) return str_dup(path);
    if (!path) return str_dup(base);
    
    size_t base_len = strlen(base);
    size_t path_len = strlen(path);
    
    // Check if base already ends with separator
    bool has_sep = (base_len > 0 && (base[base_len-1] == '/' || base[base_len-1] == '\\'));
    
    size_t total = base_len + path_len + (has_sep ? 1 : 2);
    char* result = (char*)ferret_alloc(total);
    if (!result) return NULL;
    
    memcpy(result, base, base_len);
    if (!has_sep) {
        result[base_len] = PATH_SEP;
        memcpy(result + base_len + 1, path, path_len + 1);
    } else {
        memcpy(result + base_len, path, path_len + 1);
    }
    
    return result;
}

// Get file extension
char* FERRET_FUNC(Ext)(const char* path) {
    if (!path) return str_dup("");
    
    size_t len = strlen(path);
    // Find last dot
    for (size_t i = len; i > 0; i--) {
        if (path[i-1] == '.') {
            return str_dup(path + i - 1);
        }
        if (path[i-1] == '/' || path[i-1] == '\\') {
            break;  // No extension
        }
    }
    return str_dup("");
}

// Get base name (filename without directory)
char* FERRET_FUNC(Base)(const char* path) {
    if (!path) return str_dup("");
    
    size_t len = strlen(path);
    // Find last separator
    for (size_t i = len; i > 0; i--) {
        if (path[i-1] == '/' || path[i-1] == '\\') {
            return str_dup(path + i);
        }
    }
    return str_dup(path);
}

// Get directory part
char* FERRET_FUNC(Dir)(const char* path) {
    if (!path) return str_dup("");
    
    size_t len = strlen(path);
    // Find last separator
    for (size_t i = len; i > 0; i--) {
        if (path[i-1] == '/' || path[i-1] == '\\') {
            char* result = (char*)ferret_alloc(i);
            if (!result) return NULL;
            memcpy(result, path, i - 1);
            result[i - 1] = '\0';
            return result;
        }
    }
    return str_dup(".");
}
