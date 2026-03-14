# Local Shadowing Notes

## Problem

The compiler previously represented locals in HIR/MIR primarily by `string` name.
This became incorrect once the language allowed shadowing in nested scopes:

- `table.Scope` allows shadowing (inner scope can `Declare("x")` even if an outer
  scope already has `x`).
- HIR used `Name string` for binders (`LetStmt.Name`, `ForStmt.IndexName`, etc.)
  and `Ident.Path` for identifier uses.
- MIR lowering collected locals using `map[string]int` and ignored duplicates.

That combination could miscompile shadowed locals because the later binder would
either overwrite the earlier entry or be dropped, while identifier uses could
still refer to either binding depending on scope.

## Fix (2026-03-14)

`internal/middleend/hir/generate.go` now assigns a unique name to a local only
when there is a collision within the same function:

- First binding keeps the original name (e.g. `x`).
- Shadowed binding gets a stable suffix based on source location
  (e.g. `x#12:9`).
- Identifier uses (`ast.Ident`) are rewritten to the assigned local name based
  on `Bindings.Nodes` resolution, so uses in each scope still reference the
  correct binding.

This keeps existing output stable for non-shadowing code (tests and backend
output remain readable), while making shadowing correct end-to-end.

## Test

`internal/middleend/mir/lower_test.go` includes
`TestPipelineHandlesLocalShadowingInMIR` to assert that:

- both `x` and `x#...` locals exist in MIR
- the `return x` after the inner scope returns the outer `x`

