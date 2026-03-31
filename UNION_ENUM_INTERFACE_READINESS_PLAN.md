# Union / Enum / Interface Readiness Plan

## Goal

Close the remaining implementation gaps found during readiness review:

1. LLVM `SwitchTerm` lowering for general `match` codegen.
2. Runtime interface-to-concrete type tests/downcasts.
3. Focused regressions and smoke coverage for the completed behavior.

## Mandatory Pre-Change Check

1. What existing function/module already implements some or all of this behavior?
   - QBE already lowers MIR `SwitchTerm` in `internal/backend/qbe/qbe.go`.
   - LLVM already lowers MIR `TypeTestValue` for union/optional runtime type tests in `internal/backend/llvm/llvm.go`.
   - The typechecker already classifies type tests in `internal/analysis/semantics/typechecker/typechecker.go`.
   - The runtime already exports `ferret__interface_panic` in `runtime/ferret_runtime.h` and `runtime/ferret_runtime_panic.c`.
2. Can I reuse an existing function directly instead of adding a wrapper?
   - Yes. The LLVM switch work should reuse existing `lowerValue`, `llvmCompareOp`, `freshTemp`, and block-label helpers directly.
   - The interface type-test work should reuse the existing runtime type-info/vtable data already emitted for interface values instead of adding a second metadata path.
3. Am I duplicating logic across files, phases, or backends?
   - I need to avoid duplicating backend-specific compare/type-tag logic. LLVM should mirror the existing QBE switch behavior without inventing a separate semantic model.
   - Interface runtime type tests must reuse the canonical interface type-info metadata already shared by dispatch and `Any` printing.
4. If I add a helper, which rule in `compiler/RULES.md` allows it?
   - A helper is only allowed if it removes repeated logic used in 2+ places or centralizes domain logic that must stay consistent across backends/phases.

## Steps

- [x] Step 1: Implement LLVM `SwitchTerm` lowering and add LLVM regression coverage.
- [x] Step 2: Implement runtime interface-to-concrete type tests/downcasts end to end.
- [x] Step 3: Add focused Ferret smoke coverage for the finished enum/union/interface behavior.
