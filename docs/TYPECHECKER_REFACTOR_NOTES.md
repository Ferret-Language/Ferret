# Typechecker Refactor Notes

This file is a running log of maintainability-only refactors in the typechecker
package. The goal is to keep context about why changes were made so future work
does not regress into "everything in one file" or duplicate state across phases.

## 2026-03-14

### What Changed

- Split `internal/semantics/typechecker/typechecker.go` by responsibility.
- Kept behavior the same; all compiler tests still pass.

New files:

- `internal/semantics/typechecker/core.go`
  - `refineScope` (narrowing overlay keyed by `*symbols.Symbol`)
  - `checker` struct and helpers (`bindDeclSymbol`, `symbolMutable`, etc.)
  - `CheckModule` entrypoint
- `internal/semantics/typechecker/stmts.go`
  - Statement checking (`checkStmt`, `checkReturn`)
  - Assignment target checks
- `internal/semantics/typechecker/symbol_types.go`
  - `typeOfSymbol` and symbol-type synthesis helpers (`funcType`, etc.)

### Why

- The package had grown into a large single file, making navigation and review
  difficult even when the semantic architecture is sound.
- Separating "plumbing" from "rules" reduces accidental coupling and makes it
  easier to keep the semantic flow single-source.

### Next Work

- Further split `internal/semantics/typechecker/typechecker.go` into cohesive
  units (expressions, narrowing, method lookup, casts, etc.) while keeping tests
  green and avoiding behavior changes.

