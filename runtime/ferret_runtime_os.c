#include "ferret_runtime_internal.h"

#if defined(_WIN32)
#  include <windows.h>
#else
#  include <unistd.h>
#  if defined(__unix__) || defined(__APPLE__) || defined(__linux__) || defined(__FreeBSD__) || defined(__NetBSD__) || defined(__OpenBSD__)
#    include <sys/utsname.h>
#  endif
#endif

ferret_usize ferret_os_cpu_count(void) {
#if defined(_WIN32)
    SYSTEM_INFO info;
    GetSystemInfo(&info);
    if (info.dwNumberOfProcessors == 0) {
        return 1;
    }
    return (ferret_usize)info.dwNumberOfProcessors;
#elif defined(_SC_NPROCESSORS_ONLN)
    long value = sysconf(_SC_NPROCESSORS_ONLN);
    if (value < 1) {
        return 1;
    }
    return (ferret_usize)value;
#else
    return 1;
#endif
}

FerretStr ferret_os_platform(void) {
#if defined(_WIN32)
    return ferret__static_str("windows");
#elif defined(__APPLE__) && defined(__MACH__)
    return ferret__static_str("darwin");
#elif defined(__linux__)
    return ferret__static_str("linux");
#elif defined(__FreeBSD__)
    return ferret__static_str("freebsd");
#elif defined(__NetBSD__)
    return ferret__static_str("netbsd");
#elif defined(__OpenBSD__)
    return ferret__static_str("openbsd");
#else
    return ferret__static_str("unknown");
#endif
}

FerretStr ferret_os_arch(void) {
#if defined(__x86_64__) || defined(_M_X64)
    return ferret__static_str("amd64");
#elif defined(__aarch64__) || defined(_M_ARM64)
    return ferret__static_str("arm64");
#elif defined(__arm__) || defined(_M_ARM)
    return ferret__static_str("arm");
#elif defined(__i386__) || defined(_M_IX86)
    return ferret__static_str("386");
#elif defined(__riscv) && (__riscv_xlen == 64)
    return ferret__static_str("riscv64");
#else
    return ferret__static_str("unknown");
#endif
}

FerretStr ferret_os_name(void) {
#if defined(_WIN32)
    return ferret__static_str("Windows");
#elif defined(__unix__) || defined(__APPLE__) || defined(__linux__) || defined(__FreeBSD__) || defined(__NetBSD__) || defined(__OpenBSD__)
    struct utsname info;
    if (uname(&info) == 0 && info.sysname[0] != '\0') {
        return ferret__static_str(info.sysname);
    }
#endif
    return ferret_os_platform();
}

ferret_bool ferret_os_debug(void) {
#if defined(FERRET_DEBUG)
    return 1;
#else
    return 0;
#endif
}
