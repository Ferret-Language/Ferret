# Code Scan TODO (Follow-Up)

This file captures validated items from a pasted static-scan report. The report contains noise; items below are filtered to things that actually exist in this repo and are worth follow-up.

## Confirmed Noise / False Positives

- "Circular dependencies" between files like `bundler/build_tools.go` and `bundler/deps_fs.go` are not Go import cycles. They are the same `package main` and can reference each other normally.
- Many "duplicate function name" findings (e.g. `AnalyzeModule`) are across different Go packages and represent different analysis passes. Same exported name is not a correctness issue, but improving naming can still increase readability.
- Some flags reference symbols that do not exist verbatim (e.g. `func fail` was not found via `rg "^func fail\\b"`).

## CodeFlow Report Notes

The attached `codeflow-report.md` contains several misclassifications:
- "HIGH: Hardcoded Secret" flagged test strings and diagnostic codes (e.g. `"P0001"`) as credentials. Treat as false positive unless an actual credential pattern is present.
- "MEDIUM: Function Constructor" is not actionable in Go in the way it is in dynamic languages; treat as informational only unless it points to real unsafe behavior.

TODO:
- If you want real secret scanning, add a dedicated secret scanner (gitleaks/trufflehog) and ignore CodeFlow's "hardcoded secret" heuristics for this repo.

## Naming Improvements (Readability)

### 0) Make analysis entrypoints self-describing

Goal:
- Make analysis entrypoints readable without relying on package/file context.

Confirmed cases:
- `cfganalysis.AnalyzeModule` in `internal/analysis/cfg/analysis/analyze.go`
- `usage.AnalyzeModule` in `internal/analysis/semantics/usage/usage.go`
- `ownership.AnalyzeModule` in `internal/analysis/semantics/ownership/ownership.go`

TODO (priority: high):
- Rename entrypoints to phase-specific names:
  - `cfganalysis.AnalyzeCFGModule`
  - `usage.AnalyzeUsageModule` / `usage.AnalyzeUsageModules`
  - `ownership.AnalyzeOwnershipModule`
- Update call sites in `internal/pipeline/pipeline.go`.
- Consider renaming generic internal structs like `type analyzer struct` to package-scoped intent names (`usageAnalyzer`, `ownershipAnalyzer`) where it improves local readability.

Status:
- Implemented: `AnalyzeCFGModule`, `AnalyzeUsageModule`/`AnalyzeUsageModules`, `AnalyzeOwnershipModule` and pipeline call-site updates.
- Implemented: `analyzer` -> `usageAnalyzer` and `ownershipAnalyzer` in the corresponding packages.

## High-Value Refactors (Real Duplication)

### 1) `derefForSelector` duplicated 3x with identical logic

Locations:
- `internal/ir/mir/helpers.go` (free function)
- `internal/analysis/semantics/typechecker/typechecker.go` (`(*checker).derefForSelector`)
- `internal/analysis/semantics/ownership/ownership.go` (`(*analyzer).derefForSelector`)

Why it matters:
- It's the same domain rule (selector deref behavior). Duplication risks divergence and subtle inconsistencies across phases.

TODO:
- Introduce a single canonical helper (likely in `internal/analysis/semantics/typeinfo`, or another shared low-level package already imported by all three call-sites).
- Replace all three call-sites with the shared helper.
- Add a small unit test for the helper covering `PointerType`, `RefType`, and non-deref types.

Status:
- Implemented `typeinfo.DerefForSelector` in `internal/analysis/semantics/typeinfo/selectors.go`.
- Removed local copies in `internal/ir/mir/helpers.go`, `internal/analysis/semantics/typechecker/typechecker.go`, `internal/analysis/semantics/ownership/ownership.go`.
- Added test in `internal/analysis/semantics/typeinfo/selectors_test.go`.

### 2) `findModuleForSymbol` appears in resolver/typechecker/ownership

Locations:
- `internal/analysis/semantics/resolver/resolver.go` (`(*resolver).findModuleForSymbol`)
- `internal/analysis/semantics/typechecker/typechecker.go` (`(*checker).findModuleForSymbol`)
- `internal/analysis/semantics/ownership/ownership.go` (`(*analyzer).findModuleForSymbol`)

Status:
- Real duplication, but likely not identical (typechecker has prelude behavior elsewhere; the policies may differ).

TODO:
- Compare implementations and decide whether there is a common "symbol -> owner module" primitive that can be safely shared.
- If policies differ by phase, rename functions to encode intent (example: `findOwnerModuleForSymbol`, `findDefiningModuleForSymbol`, etc.) and document differences.

Status:
- Renamed to clarify intent without changing policy:
  - Resolver: `findOwnerModuleForSymbol` (includes Prelude + type members)
  - Typechecker: `findOwnerModuleForSymbol` (includes Prelude + type members)
  - Ownership: `findCandidateModuleForSymbol` (modules/method sets only)

### 3) `syntaxType` appears in HIR generation and semantic phases

Locations:
- `internal/ir/hir/generate.go` (free function)
- `internal/analysis/semantics/typechecker/*` (method on checker)
- `internal/analysis/semantics/ownership/ownership.go` (free function)

Status:
- Likely not identical (HIR generation uses `typeinfo.ModuleInfo` and syntax-level mapping).

TODO:
- Ensure names reflect phase and behavior (example: `hirSyntaxType`, `checkerSyntaxType`), or centralize common logic where truly shared.

Status:
- Renamed for clarity (no behavior change):
  - `internal/ir/hir/generate.go`: `syntaxType` -> `typeFromTypeExpr`
  - `internal/analysis/semantics/typechecker/core.go`: `(*checker).syntaxType` -> `(*checker).typeFromTypeExpr`
  - `internal/analysis/semantics/ownership/ownership.go`: `syntaxType` -> `typeFromTypeExpr`

## Test Code Duplication

### `mustWrite(t, path, content)` duplicated across multiple `*_test.go`

Locations:
- `internal/driver/compiler_test.go`
- `internal/core/project/project_test.go`
- `internal/pipeline/pipeline_test.go`
- `internal/analysis/semantics/collector/collector_test.go`
- `internal/backend/llvm/lower_test.go`
- `internal/backend/qbe/lower_test.go`

TODO:
- Consider moving a shared helper into `internal/testutils` (or similar) to reduce copy/paste.
- Keep it minimal: avoid creating generic wrappers that obscure what tests are doing.

## Very Large Files / "God Object" Flags

The scan lists many large/high-complexity files (e.g. typechecker/llvm/lsp). These are real, but refactoring them is a major project.

TODO:
- Pick one high-churn hotspot and split it by responsibility, but only when there is an active feature/bug that already requires touching that area.
- Prefer extraction of cohesive subsystems (not "move random 200 lines").
