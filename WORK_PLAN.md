# Work Plan

This file is the pause/resume tracker for active compiler work.

## Workflow

1. Keep changes scoped to one approved step at a time.
2. After each implementation step, stop for review.
3. Commit only after explicit approval.
4. Update this file whenever the active step or status changes.

## Current Focus

Topic: Comptime redesign and related compiler behavior

Status: Step 17 const-eval review fixes are complete; waiting for review.

## Steps

- [done] Finalize the comptime language rules and implementation order.
- [done] Implement step 1: route `const` through CTFE and remove the syntax-only const gate.
- [done] Add repo workflow files (`AGENTS.md`, `WORK_PLAN.md`) for pause/resume and approval-first work.
- [done] Add `test_comptime/` repro files and verify them with a built compiler.
- [done] Review step 1 diff and repro results.
- [done] Commit step 1 only after approval.
- [done] Implement step 2: remove legacy `comptime` parameter syntax and plumbing.
- [done] Verify step 2 with focused tests and real Ferret repro files.
- [done] Wait for review before committing step 2.
- [done] Implement step 3: introduce deferred array-length representation.
- [done] Verify step 3 with focused tests and foundation tests.
- [done] Wait for review before committing step 3.
- [done] Implement step 4: resolve deferred array lengths after typechecking.
- [done] Rework step 4 to use a shared type rewrite utility instead of another local recursive type switch.
- [done] Refactor step 4 to use one strict compile-time integer check for array lengths instead of a pipeline-side deferred evaluator.
- [done] Verify step 4 with focused tests and real Ferret repro files.
- [done] Wait for review before committing step 4.
- [done] Implement step 5: reuse early const-eval results for `const` initializers where possible.
- [done] Wait for review before committing step 5.
- [done] Implement step 6: preserve `comptime {}` blocks on AST instead of rewriting them into hard prefix expressions during parsing.
- [done] Wait for review before committing step 6.
- [done] Implement step 7: lower `comptime {}` through a soft CTFE path that executes when values are concrete and skips silently otherwise.
- [done] Wait for review before committing step 7.
- [done] Commit step 7 after approval.
- [done] Run broader touched-package validation for the comptime redesign.
- [done] Wait for review before committing step 8.
- [done] Implement PR review feedback fixes for comptime migration.
- [done] Verify PR review feedback fixes with focused tests.
- [done] Wait for review before committing step 9.
- [done] Implement step 10: on-demand CTFE for const initializers during typechecking.
- [done] Verify step 10 with focused tests and a tuple repro.
- [done] Commit step 10 after approval.
- [done] Add a single repro file covering the currently supported comptime surface.
- [done] Wait for review before committing step 11.
- [done] Remove leftover deferred array-length scaffolding that is no longer used by strict array typing.
- [done] Wait for review before committing step 12.
- [done] Extend early const evaluation with simple loop-carried mutation for CTFE-safe helper calls.
- [done] Implement step 14: reject non-canonical generic self-use/owner syntax in the front-end and stop before lowering on semantic errors.
- [done] Implement step 16: show generic params in declaration hovers.
- [done] Implement step 17: fix early const-eval short-circuiting, explicit generic type args, and imported builtin len resolution.
- [in_review] Wait for review before committing step 17.

## Active Task List

- [done] Verify that `const` initializers use CTFE on real Ferret source files.
- [done] Keep `WORK_PLAN.md` updated with concrete implementation tasks, not only workflow states.
- [done] After approval, commit only step 1 and its verification artifacts.
- [done] Remove `comptime` parameter support from parser, AST, semantic flags, HIR, MIR, and LSP.
- [done] Update tests and Ferret sample files to use plain parameters plus explicit `comptime` calls/blocks.
- [done] Rebuild compiler and run `test_comptime/` repros after the syntax removal.
- [done] Add deferred array-length state to `ArrayType` and preserve it through type copies.
- [done] Keep current eager behavior intact while making later deferred filling possible.
- [done] Require `[N]T` array lengths to be compile-time known during typechecking.
- [done] Centralize recursive type rewriting in `typeinfo` and reuse it in existing transforms.
- [done] Reuse one early const-eval service for array lengths and compile-time index bounds.
- [done] Cover local and imported const array lengths with focused tests and a real repro.
- [done] Cache early const-eval results for `const` declarations and statements.
- [done] Reuse cached early const values in HIR instead of wrapping obvious literals in `comptime`.
- [done] Preserve `comptime {}` as block syntax on AST and reapply the current hard lowering at HIR generation.
- [done] Verify the preserved `comptime {}` path with focused tests and a real Ferret repro.
- [done] Distinguish soft `comptime {}` lowering from hard `comptime expr` in MIR CTFE.
- [done] Verify soft-block skip behavior and hard-inside-soft errors with focused tests and real Ferret repros.
- [done] Re-run the broader touched packages after the soft-block changes.
- [done] Update the persistent plan with the current comptime status and validation coverage.
- [done] Fix PR review feedback around IDE const diagnostics, stale const-index diagnostics, interface-method parser recovery, and documentation cleanup.
- [done] Evaluate CTFE-safe const calls on demand during typing so cached const values can flow into later array-length checks.
- [done] Keep one runnable Ferret repro that documents the currently supported comptime behavior end to end.
- [done] Remove dead `ArrayLenDeferred` / `SizeExpr` bookkeeping now that array lengths are resolved during typing.
- [done] Support `while`-based local helper functions in on-demand const evaluation for `const` initializers and strict array lengths.
- [done] Reject `Node<Node<T>>`-style self-use and `Point<i32>::Method`-style owner syntax by matching generic uses against the declared parameter shape.
- [done] Stop the full pipeline after semantic front-end errors so invalid generic shapes cannot reach lowering/specialization.
- [done] Preserve short-circuit semantics in early CTFE boolean ops.
- [done] Allow explicit generic type args in const-evaluable calls.
- [done] Resolve builtin `len(...)` during early CTFE using the evaluated module context.

## Verification

- `./build.sh`
- `./build/core/bin/ferret run:llvm test_comptime/const_local_ctfe.fer`
- `./build/core/bin/ferret run:qbe test_comptime/const_local_ctfe.fer`
- `./build/core/bin/ferret run:llvm test_comptime/const_call_ctfe.fer`
- `./build/core/bin/ferret run:qbe test_comptime/const_call_ctfe.fer`
- `./build/core/bin/ferret check test_comptime/const_runtime_call_should_fail.fer`
- `./build/core/bin/ferret check test_comptime/comptime_block_skip_runtime.fer`
- `./build/core/bin/ferret check test_comptime/comptime_block_hard_inside_soft_fail.fer`
- `./build/core/bin/ferret run:llvm test_comptime/comptime_works.fer`
- `./build/core/bin/ferret run:qbe test_comptime/comptime_works.fer`
- `./build/core/bin/ferret run:llvm test_comptime/const_while_loop.fer`
- `./build/core/bin/ferret run:qbe test_comptime/const_while_loop.fer`
- `./build/core/bin/ferret check tuple.fer`
- `go test ./internal/analysis/semantics/typechecker ./internal/ir/mir ./internal/ir/hir -count=1`
- `go test ./internal/analysis/semantics/typeinfo ./internal/analysis/semantics/typechecker -run 'TestTypecheckerResolvesArrayLengthFromConstExpr|TestTypecheckerResolvesArrayLengthFromImportedConst|TestTypecheckerRejectsRuntimeArrayLength|TestTypecheckerAllowsCTFEConstInitializerFromLocalTupleCall|TestInstantiateTypeHandlesRecursiveStruct|TestRefAndRawTypeString|TestEqualRefAndRawTypes' -count=1`
- `go test ./internal/analysis/semantics/typechecker -run 'TestTypecheckerAllowsCTFEConstInitializerFromLocalValue|TestTypecheckerAllowsCTFEConstInitializerFromLocalTupleCall|TestTypecheckerAllowsCTFEConstInitializerFromLocalWhileLoopCall|TestTypecheckerRejectsNonCTFEConstInitializer' -count=1`
- `go test ./internal/analysis/semantics/typechecker -run 'TestTypecheckerAllowsShortCircuitConstInitializer|TestTypecheckerAllowsExplicitTypeArgsInConstCall|TestTypecheckerAllowsImportedLenCallInConstInitializer' -count=1`
- `go test ./internal/analysis/semantics/typechecker -run 'TestTypecheckerRejectsNonCanonicalRecursiveGenericSelfUse|TestTypecheckerRejectsNonCanonicalGenericMethodOwner|TestTypecheckerInfersOwnerTypeArgsForStaticGenericMethodCall' -count=1`
- `go test ./internal/driver -run 'TestParsePathRejectsNonCanonicalRecursiveGenericSelfUseBeforeLowering' -count=1`
- `GOCACHE=$(pwd)/.gocache timeout 5s go run ./cmd/ferret check /tmp/ferret_generic_stress/main.fer`
- `go test ./internal/driver -run 'TestParsePathForIDERejectsRuntimeConstInitializer|TestParsePathForIDEAllowsPotentialCTFEConstCall' -count=1`

Observed results:

- `const_local_ctfe.fer` passed on both LLVM and QBE.
- `const_call_ctfe.fer` passed on both LLVM and QBE.
- `const_runtime_call_should_fail.fer` failed as expected through the CTFE path.
- `comptime_block_skip_runtime.fer` passed with no diagnostics.
- `comptime_block_hard_inside_soft_fail.fer` failed with the expected hard comptime diagnostic.
- `comptime_works.fer` passed on both LLVM and QBE.
- `const_while_loop.fer` passed on both LLVM and QBE.
- `tuple.fer` still typechecked successfully after removing the dead deferred-array metadata.
- The broader touched packages (`typechecker`, `mir`, `hir`) passed under the memory cap.
- Recursive generic self-use now fails fast in the front-end with the canonical generic-shape diagnostic and does not proceed to lowered HIR.

## Notes

- Follow `compiler/RULES.md` for every compiler change.
- Reuse existing logic before adding helpers.
- Add focused regression tests for each approved behavior change.
