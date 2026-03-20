# Audit Execution TODO

Source: [CODEBASE_AUDIT_REPORT.md](/home/fuad/Dev/Ferret-compiler-v2/compiler/CODEBASE_AUDIT_REPORT.md)
Status key: `[ ]` pending, `[*]` done

## High Priority

- [*] replace string-sanitized generic specialization tags with structural type mangling
  - done in `internal/ir/hir/specialize.go` + collision regression in `internal/ir/hir/generate_test.go`
- [*] remove raw backend panic path in QBE (`SourceFS`) and propagate errors
  - done in `internal/backend/qbe/qbe.go`, `internal/backend/qbe/toolchain.go`, `internal/backend/qbe/qbe_test.go`
- [*] unify recursive type substitution between `typeinfo.InstantiateType` and `hir.specializer.substituteTypeInternal`
- [*] extract backend-neutral lowering helpers shared by LLVM/QBE (start with panic/aggregate/type-to-ABI helpers)
  - [*] panic payload classification/address fallback extracted to `internal/backend/panic.go` and wired in LLVM/QBE
  - [*] aggregate source resolution extracted to `internal/backend/aggregate.go` and wired in LLVM/QBE
  - [*] shared type-kind helpers (`UnwrapNamed`, `IsVoidType`, union/interface checks) extracted to `internal/backend/type_helpers.go`
  - [*] shared ABI shape classification extracted to `internal/backend/abi.go` and consumed by `llvmABITypeName`/`qbeABIType`
- [*] add cross-backend generic specialization tests (LLVM + QBE parity for constrained owner methods)
  - added `internal/backend/parity_test.go` covering constrained owner static method specialization on both targets
- [*] add mangling collision test suite beyond the current `t_i32` vs `(i32)` case
  - added coverage for owner static-method specializations across multiple owner type args
  - added coverage for cross-module same-name type arguments in generic function specialization
  - added coverage for cross-module same-name type arguments in generic type specialization

## Medium Priority

- [*] convert diagnostics/emitter panic-based invariants into non-crashing internal compiler error flow
  - `internal/core/diagnostics/emitter.go`
  - `internal/core/diagnostics/diagnostic.go`
  - now emits ICE-style internal notes instead of panicking for missing primary-secondary order and multi-primary render cases
- [ ] reduce LSP/typeinfo duplication by moving named-type + method-list rendering into `typeinfo`
  - keep LSP as composition/index layer
- [ ] centralize LLVM runtime declaration emission in one helper
- [ ] harden generic binding lookups to prefer owner-aware identity and minimize name-only fallback
- [ ] improve hover truncation messaging when recursion/depth guard short-circuits output

## Low Priority / Cleanup

- [ ] inline/remove wrapper `renderNamedTypeMarkdown` if it remains a pure delegate
- [ ] inline/remove `substituteTypeWithoutTypeSpecialization` if it stays single-use
- [ ] inline/remove `symbolFuncDecl` if call sites remain local and small

## Critical Coverage Gaps

- [ ] add cross-module generic specialization tests (type/method specialization across imports)
- [ ] add cross-module hover tests for imported generic/constrained symbols
- [ ] add recursive generic/interface hover tests beyond current struct recursion case
- [ ] add backend error-path tests asserting unsupported inputs return errors (no process panic)

## Suggested Execution Order

1. Unify type substitution (`InstantiateType` vs `substituteTypeInternal`)
2. Backend-neutral shared lowering helpers (LLVM/QBE)
3. Cross-backend specialization parity tests
4. Diagnostics panic-to-error conversion
5. Cross-module generic + hover coverage
6. Remaining medium/low cleanup tasks
