// Ferret runtime: OS utilities
// Native implementations for os module

#ifdef __APPLE__
#define _DARWIN_C_SOURCE
#else
#define _POSIX_C_SOURCE 200809L
#endif

#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include "../core/alloc.h"
#include <errno.h>
#include <signal.h>
#include <stdio.h>
#include <limits.h>

#ifdef _WIN32
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <shellapi.h>
#else
#include <unistd.h>
#include <time.h>
#include <sys/types.h>
#include <sys/wait.h>
#endif

#ifdef __APPLE__
#include <crt_externs.h>
#include <sys/sysctl.h>
#endif

#include "../core/alloc.h"
#include "../core/array.h"
#include "../core/type_system.h"
#include "../core/runtime_naming.h"

// Define the module prefix for this file (implements ferret_libs/os.fer)
#define MODULE_PREFIX ferret_os

static char* ferret_os_strdup(const char* s) {
    if (!s) {
        return NULL;
    }
    size_t len = strlen(s);
    char* out = (char*)ferret_alloc(len + 1);
    if (!out) {
        return NULL;
    }
    memcpy(out, s, len + 1);
    return out;
}

static void ferret_os_set_err(void* out, const char* msg) {
    if (!out) {
        return;
    }
    char** str_ptr = (char**)out;
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 8);
    *str_ptr = (char*)(msg ? msg : "unknown error");
    *tag_ptr = 0;
}

static void ferret_os_set_ok_bool(void* out, bool value) {
    if (!out) {
        return;
    }
    bool* val_ptr = (bool*)out;
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 8);
    *val_ptr = value;
    *tag_ptr = 1;
}

static void ferret_os_set_ok_i32(void* out, int32_t value) {
    if (!out) {
        return;
    }
    int32_t* val_ptr = (int32_t*)out;
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 8);
    *val_ptr = value;
    *tag_ptr = 1;
}

static void ferret_os_set_ok_str(void* out, char* value) {
    if (!out) {
        return;
    }
    char** str_ptr = (char**)out;
    uint8_t* tag_ptr = (uint8_t*)((char*)out + 8);
    *str_ptr = value;
    *tag_ptr = 1;
}

static ferret_array_t* ferret_os_args_from_argv(int32_t argc, char** argv) {
    ferret_array_t* arr = ferret_array_new(sizeof(char*), argc, (ferret_type_info_t*)&ferret_type_str);
    if (!arr) {
        return NULL;
    }
    for (int32_t i = 0; i < argc; i++) {
        const char* src = argv ? argv[i] : NULL;
        char* arg = ferret_os_strdup(src ? src : "");
        if (!arg) {
            arg = (char*)"";
        }
        ferret_array_append(arr, &arg);
    }
    return arr;
}

#ifndef _WIN32
static ferret_array_t* ferret_os_args_from_proc(void) {
    FILE* f = fopen("/proc/self/cmdline", "rb");
    if (!f) {
        return ferret_array_new(sizeof(char*), 0, (ferret_type_info_t*)&ferret_type_str);
    }

    size_t cap = 256;
    size_t len = 0;
    char* buf = (char*)ferret_alloc(cap);
    if (!buf) {
        fclose(f);
        return ferret_array_new(sizeof(char*), 0, (ferret_type_info_t*)&ferret_type_str);
    }

    for (;;) {
        if (len + 128 > cap) {
            size_t new_cap = cap * 2;
            char* next = (char*)ferret_realloc(buf, new_cap);
            if (!next) {
                break;
            }
            buf = next;
            cap = new_cap;
        }
        size_t n = fread(buf + len, 1, cap - len, f);
        len += n;
        if (n == 0) {
            break;
        }
    }
    fclose(f);

    ferret_array_t* arr = ferret_array_new(sizeof(char*), 0, (ferret_type_info_t*)&ferret_type_str);
    if (!arr) {
        ferret_free(buf);
        return NULL;
    }

    size_t start = 0;
    for (size_t i = 0; i < len; i++) {
        if (buf[i] == '\0') {
            size_t seg_len = i - start;
            char* arg = (char*)ferret_alloc(seg_len + 1);
            if (arg) {
                if (seg_len > 0) {
                    memcpy(arg, buf + start, seg_len);
                }
                arg[seg_len] = '\0';
            } else {
                arg = (char*)"";
            }
            ferret_array_append(arr, &arg);
            start = i + 1;
        }
    }

    if (start < len) {
        size_t seg_len = len - start;
        char* arg = (char*)ferret_alloc(seg_len + 1);
        if (arg) {
            memcpy(arg, buf + start, seg_len);
            arg[seg_len] = '\0';
        } else {
            arg = (char*)"";
        }
        ferret_array_append(arr, &arg);
    }

    ferret_free(buf);
    return arr;
}
#endif

// Command-line arguments
ferret_array_t* FERRET_FUNC(Args)(void) {
#ifdef _WIN32
    int argc = 0;
    LPWSTR* argv_w = CommandLineToArgvW(GetCommandLineW(), &argc);
    if (!argv_w || argc <= 0) {
        if (argv_w) {
            LocalFree(argv_w);
        }
        return ferret_array_new(sizeof(char*), 0, (ferret_type_info_t*)&ferret_type_str);
    }
    ferret_array_t* arr = ferret_array_new(sizeof(char*), argc, (ferret_type_info_t*)&ferret_type_str);
    if (!arr) {
        LocalFree(argv_w);
        return NULL;
    }
    for (int i = 0; i < argc; i++) {
        int needed = WideCharToMultiByte(CP_UTF8, 0, argv_w[i], -1, NULL, 0, NULL, NULL);
        char* arg = NULL;
        if (needed > 0) {
            arg = (char*)ferret_alloc((size_t)needed);
            if (arg) {
                WideCharToMultiByte(CP_UTF8, 0, argv_w[i], -1, arg, needed, NULL, NULL);
            }
        }
        if (!arg) {
            arg = (char*)"";
        }
        ferret_array_append(arr, &arg);
    }
    LocalFree(argv_w);
    return arr;
#elif defined(__APPLE__)
    int argc = 0;
    char** argv = NULL;
    int* argc_ptr = _NSGetArgc();
    char*** argv_ptr = _NSGetArgv();
    if (argc_ptr) {
        argc = *argc_ptr;
    }
    if (argv_ptr) {
        argv = *argv_ptr;
    }
    return ferret_os_args_from_argv(argc, argv);
#else
    ferret_array_t* arr = ferret_os_args_from_proc();
    if (arr) {
        return arr;
    }
    return ferret_array_new(sizeof(char*), 0, (ferret_type_info_t*)&ferret_type_str);
#endif
}

// Exit the process
void FERRET_FUNC(Exit)(int32_t code) {
    exit((int)code);
}

// Get current process ID
int32_t FERRET_FUNC(GetPid)(void) {
#ifdef _WIN32
    return (int32_t)GetCurrentProcessId();
#else
    return (int32_t)getpid();
#endif
}

// Sleep for N milliseconds
void FERRET_FUNC(Sleep)(int32_t ms) {
    if (ms <= 0) {
        return;
    }
#ifdef _WIN32
    Sleep((DWORD)ms);
#else
    struct timespec req;
    req.tv_sec = ms / 1000;
    req.tv_nsec = (long)(ms % 1000) * 1000000L;
    nanosleep(&req, NULL);
#endif
}

// Execute a shell command (str!i32)
void FERRET_FUNC(Exec)(void* out, const char* cmd) {
    if (!out) {
        return;
    }
    if (!cmd) {
        ferret_os_set_err(out, "command is null");
        return;
    }
    int status = system(cmd);
    if (status == -1) {
        ferret_os_set_err(out, "failed to execute command");
        return;
    }
#ifndef _WIN32
    if (WIFEXITED(status)) {
        ferret_os_set_ok_i32(out, (int32_t)WEXITSTATUS(status));
        return;
    }
    if (WIFSIGNALED(status)) {
        ferret_os_set_ok_i32(out, (int32_t)(128 + WTERMSIG(status)));
        return;
    }
#endif
    ferret_os_set_ok_i32(out, (int32_t)status);
}

// Send a signal to a process (str!bool)
void FERRET_FUNC(Kill)(void* out, int32_t pid, int32_t signal) {
    if (!out) {
        return;
    }
#ifdef _WIN32
    if (signal == 0) {
        HANDLE proc = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, (DWORD)pid);
        if (!proc) {
            ferret_os_set_err(out, "process not found");
            return;
        }
        CloseHandle(proc);
        ferret_os_set_ok_bool(out, true);
        return;
    }
    HANDLE proc = OpenProcess(PROCESS_TERMINATE, FALSE, (DWORD)pid);
    if (!proc) {
        ferret_os_set_err(out, "failed to open process");
        return;
    }
    BOOL ok = TerminateProcess(proc, (UINT)signal);
    CloseHandle(proc);
    if (!ok) {
        ferret_os_set_err(out, "failed to terminate process");
        return;
    }
    ferret_os_set_ok_bool(out, true);
#else
    if (kill((pid_t)pid, signal) != 0) {
        ferret_os_set_err(out, strerror(errno));
        return;
    }
    ferret_os_set_ok_bool(out, true);
#endif
}

// Send a signal to the current process (str!bool)
void FERRET_FUNC(Signal)(void* out, int32_t signal) {
    if (!out) {
        return;
    }
    if (raise(signal) != 0) {
        ferret_os_set_err(out, "failed to raise signal");
        return;
    }
    ferret_os_set_ok_bool(out, true);
}

// Get environment variable (str!str)
void FERRET_FUNC(GetEnv)(void* out, const char* key) {
    if (!out) {
        return;
    }
    if (!key) {
        ferret_os_set_err(out, "key is null");
        return;
    }
#ifdef _WIN32
    DWORD len = GetEnvironmentVariableA(key, NULL, 0);
    if (len == 0) {
        ferret_os_set_err(out, "env var not found");
        return;
    }
    char* buf = (char*)ferret_alloc((size_t)len);
    if (!buf) {
        ferret_os_set_err(out, "out of memory");
        return;
    }
    GetEnvironmentVariableA(key, buf, len);
    ferret_os_set_ok_str(out, buf);
#else
    const char* val = getenv(key);
    if (!val) {
        ferret_os_set_err(out, "env var not found");
        return;
    }
    char* copy = ferret_os_strdup(val);
    if (!copy) {
        ferret_os_set_err(out, "out of memory");
        return;
    }
    ferret_os_set_ok_str(out, copy);
#endif
}

// Set environment variable (str!bool)
void FERRET_FUNC(SetEnv)(void* out, const char* key, const char* value) {
    if (!out) {
        return;
    }
    if (!key) {
        ferret_os_set_err(out, "key is null");
        return;
    }
#ifdef _WIN32
    if (!SetEnvironmentVariableA(key, value ? value : "")) {
        ferret_os_set_err(out, "failed to set env var");
        return;
    }
    ferret_os_set_ok_bool(out, true);
#else
    if (setenv(key, value ? value : "", 1) != 0) {
        ferret_os_set_err(out, strerror(errno));
        return;
    }
    ferret_os_set_ok_bool(out, true);
#endif
}

// Unset environment variable (str!bool)
void FERRET_FUNC(UnsetEnv)(void* out, const char* key) {
    if (!out) {
        return;
    }
    if (!key) {
        ferret_os_set_err(out, "key is null");
        return;
    }
#ifdef _WIN32
    if (!SetEnvironmentVariableA(key, NULL)) {
        ferret_os_set_err(out, "failed to unset env var");
        return;
    }
    ferret_os_set_ok_bool(out, true);
#else
    if (unsetenv(key) != 0) {
        ferret_os_set_err(out, strerror(errno));
        return;
    }
    ferret_os_set_ok_bool(out, true);
#endif
}

// Hostname (str!str)
void FERRET_FUNC(Hostname)(void* out) {
    if (!out) {
        return;
    }
    char buf[256];
    buf[0] = '\0';
#ifdef _WIN32
    DWORD size = (DWORD)sizeof(buf);
    if (!GetComputerNameA(buf, &size)) {
        ferret_os_set_err(out, "failed to get hostname");
        return;
    }
    char* copy = ferret_os_strdup(buf);
    if (!copy) {
        ferret_os_set_err(out, "out of memory");
        return;
    }
    ferret_os_set_ok_str(out, copy);
#else
    if (gethostname(buf, sizeof(buf) - 1) != 0) {
        ferret_os_set_err(out, strerror(errno));
        return;
    }
    buf[sizeof(buf) - 1] = '\0';
    char* copy = ferret_os_strdup(buf);
    if (!copy) {
        ferret_os_set_err(out, "out of memory");
        return;
    }
    ferret_os_set_ok_str(out, copy);
#endif
}

// Number of logical CPUs
int32_t FERRET_FUNC(Cpus)(void) {
#ifdef _WIN32
    SYSTEM_INFO info;
    GetSystemInfo(&info);
    if (info.dwNumberOfProcessors == 0) {
        return 0;
    }
    return (int32_t)info.dwNumberOfProcessors;
#elif defined(__APPLE__)
    int count;
    size_t size = sizeof(count);
    if (sysctlbyname("hw.ncpu", &count, &size, NULL, 0) != 0) {
        return 0;
    }
    if (count < 1) {
        return 0;
    }
    if (count > INT32_MAX) {
        return INT32_MAX;
    }
    return (int32_t)count;
#else
    long count = sysconf(_SC_NPROCESSORS_ONLN);
    if (count < 1) {
        return 0;
    }
    if (count > INT32_MAX) {
        return INT32_MAX;
    }
    return (int32_t)count;
#endif
}
