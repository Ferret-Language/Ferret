// Ferret runtime: Time functions
#define _POSIX_C_SOURCE 200809L

#include <stdint.h>
#include <time.h>
#if defined(_WIN32)
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#else
#include <sys/time.h>
#endif

#include "../core/alloc.h"
#include "../core/runtime_naming.h"

// Define the module prefix for this file (implements ferret_libs/time.fer)
#define MODULE_PREFIX ferret_time

// Get current time as ISO 8601 string
char* FERRET_FUNC(Now)(void) {
    time_t raw = time(NULL);
    if (raw == (time_t)-1) {
        char* empty = (char*)ferret_alloc(1);
        if (empty != NULL) {
            empty[0] = '\0';
        }
        return empty;
    }

    struct tm tm_val;
    struct tm* tm_ptr = NULL;
#if defined(_WIN32)
    if (gmtime_s(&tm_val, &raw) == 0) {
        tm_ptr = &tm_val;
    }
#else
    tm_ptr = gmtime(&raw);
#endif

    char* buffer = (char*)ferret_alloc(21);
    if (buffer == NULL) {
        return NULL;
    }
    buffer[0] = '\0';
    if (tm_ptr == NULL) {
        return buffer;
    }

    size_t written = strftime(buffer, 21, "%Y-%m-%dT%H:%M:%SZ", tm_ptr);
    if (written == 0) {
        buffer[0] = '\0';
    }
    return buffer;
}

int64_t FERRET_FUNC(NowUnix)(void) {
    time_t raw = time(NULL);
    if (raw == (time_t)-1) {
        return 0;
    }
    return (int64_t)raw;
}

int64_t FERRET_FUNC(NowUnixMs)(void) {
#if defined(_WIN32)
    FILETIME ft;
    ULARGE_INTEGER ticks;
    GetSystemTimeAsFileTime(&ft);
    ticks.LowPart = ft.dwLowDateTime;
    ticks.HighPart = ft.dwHighDateTime;
    if (ticks.QuadPart < 116444736000000000ULL) {
        return 0;
    }
    return (int64_t)((ticks.QuadPart - 116444736000000000ULL) / 10000ULL);
#else
    struct timeval tv;
    if (gettimeofday(&tv, NULL) != 0) {
        return FERRET_FUNC(NowUnix)() * 1000;
    }
    return ((int64_t)tv.tv_sec * 1000) + (int64_t)(tv.tv_usec / 1000);
#endif
}

void FERRET_FUNC(SleepMs)(int32_t ms) {
    if (ms <= 0) {
        return;
    }
#if defined(_WIN32)
    Sleep((DWORD)ms);
#else
    struct timespec req;
    req.tv_sec = ms / 1000;
    req.tv_nsec = (long)(ms % 1000) * 1000000L;
    nanosleep(&req, NULL);
#endif
}
