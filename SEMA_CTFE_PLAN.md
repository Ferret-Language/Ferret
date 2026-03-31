# Sema CTFE Migration Plan

Status: completed
Branch: `sema`

## Goal

Unify compile-time evaluation around one semantic CTFE service that is callable after resolver and reusable by typechecking, HIR generation, and later optimization passes.

## Pre-change check

1. What existing function/module already implements some or all of this behavior?
`internal/analysis/semantics/typechecker/const_eval.go` already evaluates resolved AST expressions and function bodies for const initializers and array lengths. `internal/analysis/semantics/typeinfo/module.go` already stores semantic const metadata. `internal/ir/hir/generate.go` already consumes cached semantic const values for const declarations/statements. `internal/ir/mir/ctfe.go` duplicates interpreter logic later on MIR.

2. Can I reuse an existing function directly instead of adding a wrapper?
Yes. The existing `checker.constExpr` / `checker.constExprIn` path is the canonical early evaluator to extend. Do not add a pass-through wrapper around MIR CTFE.

3. Am I duplicating logic across files, phases, or backends?
Yes today. AST/sema CTFE and MIR CTFE both implement recursive evaluation, call frames, loop execution, recursion guards, and operator dispatch. The migration removes that duplication by moving more consumers onto semantic CTFE metadata.

4. If I add a helper, which rule in `RULES.md` allows it?
Only helpers that centralize repeated logic used in 2+ places are allowed. In this migration, helpers are justified only when they centralize:
- semantic const-to-HIR lowering shared by multiple HIR code paths
- deferred-input gating shared by multiple semantic CTFE call sites
- enum/error ordinal lookup shared by semantic CTFE and MIR lowering

## Approved migration direction

1. Make semantic CTFE the canonical evaluator after resolver.
2. Cache semantic CTFE results in `typeinfo.ModuleInfo.ConstValues`.
3. Teach HIR lowering to read cached semantic CTFE results for explicit `comptime` expressions instead of forcing MIR re-evaluation on the success path.
4. Shrink MIR CTFE to cleanup/propagation after HIR lowering stops emitting unresolved `comptime` for successful cases.
5. Remove MIR interpretation once all hard CTFE consumers are satisfied by semantic CTFE metadata.

## Step plan

- [x] Inspect current CTFE architecture and metadata flow.
- [x] Create persistent migration plan.
- [x] Step 1: cache successful explicit `comptime` expression results in semantic metadata and consume them in HIR lowering.
- [x] Step 2: cache remaining type-level semantic CTFE results that later phases can reuse directly.
- [x] Step 3: reduce MIR CTFE to propagation/cleanup for already-folded values.
- [x] Step 4: remove duplicated MIR interpreter logic after all semantic CTFE consumers are migrated.

## Notes

- Ferret does not currently expose generic value arguments as a distinct syntax surface; this migration still treats "generic/value arguments" as future semantic CTFE call sites rather than a separate parser migration.
- Step 1 validation:
  - `go test ./internal/ir/hir ./internal/analysis/semantics/typechecker`
- Step 2 notes:
  - `checker.constExpr` is now the semantic CTFE service entrypoint for typechecker call sites.
  - successful semantic CTFE requests now cache expression-node results automatically in `ModuleInfo.ConstValues`.
  - declaration-node caching remains explicit only where later consumers need declaration identity.
- Step 2 validation:
  - `go test ./internal/analysis/semantics/typechecker ./internal/ir/hir`
- Step 3 notes:
  - eager semantic CTFE now skips definition-time execution for hard `comptime` forms that still depend on deferred runtime inputs, allowing wrapper patterns to be checked once concrete compile-time arguments exist.
  - HIR no longer preserves successful `comptime` expressions or blocks for MIR to interpret later.
- Step 4 notes:
  - `internal/ir/mir/ctfe.go` was deleted.
  - MIR now relies on semantic CTFE metadata plus its existing simplify/propagation passes.
  - enum/error member ordinals are centralized in `typeinfo` so semantic CTFE and MIR lowering share one canonical implementation.
- Final validation:
  - `go test ./...`
