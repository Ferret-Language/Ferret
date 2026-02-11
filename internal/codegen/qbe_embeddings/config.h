#ifndef QBE_CONFIG_H
#define QBE_CONFIG_H

#if defined(__APPLE__)
#if defined(__aarch64__) || defined(__arm64__)
#define Deftgt T_arm64_apple
#else
#define Deftgt T_amd64_apple
#endif
#else
#if defined(__aarch64__) || defined(__arm64__)
#define Deftgt T_arm64
#elif defined(__riscv) && (__riscv_xlen == 64)
#define Deftgt T_rv64
#else
#define Deftgt T_amd64_sysv
#endif
#endif

#endif
