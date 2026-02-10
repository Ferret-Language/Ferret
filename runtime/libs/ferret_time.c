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

// Define the module prefix for this file (implements ferret_libs/std/time.fer)
#define MODULE_PREFIX ferret_std_time

// Get current local time as HH:MM:SS
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
    if (localtime_s(&tm_val, &raw) == 0) {
        tm_ptr = &tm_val;
    }
#else
    tm_ptr = localtime(&raw);
#endif

    // Format: "HH:MM:SS"
    char* buffer = (char*)ferret_alloc(9);
    if (buffer == NULL) {
        return NULL;
    }
    buffer[0] = '\0';
    if (tm_ptr == NULL) {
        return buffer;
    }

    size_t written = strftime(buffer, 9, "%H:%M:%S", tm_ptr);
    if (written == 0) {
        buffer[0] = '\0';
    }
    return buffer;
}

// Get current local date + time as "YYYY-MM-DD HH:MM:SS"
char* FERRET_FUNC(Local)(void) {
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
    if (localtime_s(&tm_val, &raw) == 0) {
        tm_ptr = &tm_val;
    }
#else
    tm_ptr = localtime(&raw);
#endif

    char* buffer = (char*)ferret_alloc(20);
    if (buffer == NULL) {
        return NULL;
    }
    buffer[0] = '\0';
    if (tm_ptr == NULL) {
        return buffer;
    }

    size_t written = strftime(buffer, 20, "%Y-%m-%d %H:%M:%S", tm_ptr);
    if (written == 0) {
        buffer[0] = '\0';
    }
    return buffer;
}

// Get current time as ISO 8601 UTC string
char* FERRET_FUNC(NowUTC)(void) {
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

// Get current local date by format
char* FERRET_FUNC(Date)(int32_t format) {
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
    if (localtime_s(&tm_val, &raw) == 0) {
        tm_ptr = &tm_val;
    }
#else
    tm_ptr = localtime(&raw);
#endif

    const char* fmt = "%Y-%m-%d";
    switch (format) {
        case 0: // MM_DD_YY
            fmt = "%m/%d/%y";
            break;
        case 1: // MM_DD_YYYY
            fmt = "%m/%d/%Y";
            break;
        case 2: // DD_MM_YY
            fmt = "%d/%m/%y";
            break;
        case 3: // DD_MM_YYYY
            fmt = "%d/%m/%Y";
            break;
        case 4: // YYYY_MM_DD
        default:
            fmt = "%Y-%m-%d";
            break;
    }

    char* buffer = (char*)ferret_alloc(11);
    if (buffer == NULL) {
        return NULL;
    }
    buffer[0] = '\0';
    if (tm_ptr == NULL) {
        return buffer;
    }

    size_t written = strftime(buffer, 11, fmt, tm_ptr);
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

int64_t FERRET_FUNC(NowUnixUs)(void) {
#if defined(_WIN32)
    FILETIME ft;
    ULARGE_INTEGER ticks;
    GetSystemTimeAsFileTime(&ft);
    ticks.LowPart = ft.dwLowDateTime;
    ticks.HighPart = ft.dwHighDateTime;
    if (ticks.QuadPart < 116444736000000000ULL) {
        return 0;
    }
    return (int64_t)((ticks.QuadPart - 116444736000000000ULL) / 10ULL);
#else
    struct timeval tv;
    if (gettimeofday(&tv, NULL) != 0) {
        return FERRET_FUNC(NowUnix)() * 1000000;
    }
    return ((int64_t)tv.tv_sec * 1000000) + (int64_t)tv.tv_usec;
#endif
}
