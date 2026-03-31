# Interface Downcast Plan

## Goal

Implement explicit interface-to-concrete downcasts using the runtime type-info
path added for interface `is` checks.

## Mandatory Pre-Change Check

1. What existing function/module already implements some or all of this behavior?
   - `typeOfCast` in `internal/analysis/semantics/typechecker/typechecker.go`
     already owns explicit-cast legality.
   - `lowerCast` in both LLVM and QBE backends already owns runtime cast
     codegen.
   - Runtime type-info lookup for interfaces already exists in backend
     lowering after the readiness work.
   - `ferret__interface_panic` already exists in `runtime/ferret_runtime.h`
     and `runtime/ferret_runtime_panic.c`.
2. Can I reuse an existing function directly instead of adding a wrapper?
   - Yes. The new cast path should reuse the existing interface type-info
     extraction and panic symbol directly.
3. Am I duplicating logic across files, phases, or backends?
   - I need to avoid a second independent interface type-info check path.
     Explicit downcast should reuse the same runtime type-info comparison used
     by interface `is`.
4. If I add a helper, which rule in `compiler/RULES.md` allows it?
   - A helper is allowed only if it centralizes repeated runtime type-info /
     panic preparation logic used in both backends or multiple code paths.

## Steps

- [x] Step 1: Allow explicit interface-to-concrete casts in the typechecker and add semantic regressions.
- [x] Step 2: Lower explicit interface downcasts on LLVM and QBE with panic-on-mismatch behavior.
- [x] Step 3: Add end-to-end Ferret smoke coverage for successful and failing interface downcasts.
