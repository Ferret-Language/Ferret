/*
 * ferret_runtime.h — Ferret runtime ABI contract.
 *
 * This header defines the stable interface between compiler-emitted code and
 * the tiny C runtime support library (ferret_runtime.c).  Backend lowering
 * must conform to this ABI when synthesising calls to runtime symbols.
 *
 * Rule: the runtime NEVER does type dispatch.  All interface/union/enum
 * dispatch is compiler-emitted static IR.  The runtime only handles
 * unrecoverable failure paths.
 */

#ifndef FERRET_RUNTIME_H
#define FERRET_RUNTIME_H

#ifdef __cplusplus
extern "C" {
#endif

/* -------------------------------------------------------------------------
 * Primitive types mirroring the Ferret ABI on the C side.
 * -------------------------------------------------------------------------*/

typedef unsigned int    ferret_type_id;
typedef unsigned long   ferret_usize;
typedef signed   long   ferret_isize;

/* Every named type gets a unique, stable ferret_type_id.  The compiler emits
 * these as compile-time constants; ID 0 is reserved for the unknown type.    */
#define FERRET_TYPE_UNKNOWN 0u

/* Type-info record emitted by the backend once per named type into .rodata.
 * Future use: typeof(), reflection, generic specialisation names.            */
typedef struct {
    ferret_type_id  id;
    const char     *name;   /* null-terminated display name                  */
    ferret_usize    size;   /* sizeof the type in bytes                      */
    ferret_usize    align;  /* alignof the type in bytes                     */
    unsigned int    flags;  /* reserved, must be zero for now                */
} FerretTypeInfo;

/* -------------------------------------------------------------------------
 * Panic / failure surface — called by compiler-emitted code only.
 * -------------------------------------------------------------------------*/

/*
 * ferret__panic — called by `panic <msg>` statements.
 *
 * ptr  — pointer to message bytes (need not be NUL-terminated)
 * len  — byte length of the message
 *
 * This function does not return.
 */
__attribute__((noreturn))
void ferret__panic(const char *ptr, ferret_usize len);

/*
 * ferret__interface_panic — called when a bad interface downcast is attempted.
 *
 * iface / iface_len  — name of the expected interface
 * got   / got_len    — display name of the actual concrete type
 *
 * This function does not return.
 *
 * NOTE: not yet emitted by the compiler; stub is present so the ABI is
 * frozen before interface lowering is implemented.
 */
__attribute__((noreturn))
void ferret__interface_panic(const char *iface, ferret_usize iface_len,
                             const char *got,   ferret_usize got_len);

#ifdef __cplusplus
}
#endif

#endif /* FERRET_RUNTIME_H */
