# Coding Rules (Humans + Agents)

This file defines mandatory engineering rules for the `compiler` repository.

## 1) Core Principle

### Rule: no pass-through wrappers, no duplicated logic

Use existing functions directly. If behavior is identical, do not add a new function that only forwards arguments/returns results.

Why:
- Increases maintenance cost
- Creates multiple sources of truth
- Causes inconsistent behavior across modules/backends

Good:
```go
result := calculateTotal(items)
```

```go
func formatPrice(p float64) string {
	return fmt.Sprintf("$%.2f", p)
}
display1 := formatPrice(100)
display2 := formatPrice(200)
```

Bad:
```go
func getTotal(items []Item) int {
	return calculateTotal(items)
}
```

```go
display1 := fmt.Sprintf("$%.2f", price1)
display2 := fmt.Sprintf("$%.2f", price2)
```

## 2) When a new helper is allowed

A new helper is allowed only if at least one is true:
- It removes repeated logic used in 2+ places.
- It centralizes domain logic that must stay consistent (mangling, symbol lookup, receiver shaping, type formatting, ABI decisions).
- It is needed to cross a real boundary (public API, interface contract, backend abstraction).

A new helper is not allowed when:
- It only renames an existing function.
- It only forwards params/return unchanged.
- It is used once and does not clarify complex logic.

## 3) Reuse and centralization rules

- Prefer existing shared modules before adding new code.
- If LLVM and QBE (or multiple phases) share identical logic, move it to a common utility.
- Keep one canonical implementation for:
  - type text formatting
  - symbol/mangle decisions
  - receiver/parameter shape conversion
  - repeated diagnostics text

## 4) Change scope discipline

- Keep diffs minimal and task-focused.
- Do not refactor unrelated areas in the same change.
- Do not add temporary workaround code in multiple places; fix at the source layer when possible.
- Remove dead code immediately after migration.

## 5) Naming and structure

- Name functions by behavior, not by location or temporary intent.
- Avoid vague names (`handle`, `processData`, `helper2`).
- Keep functions short and single-purpose.
- Prefer data-driven logic over repeated `if/switch` blocks copied across files.

## 6) Error handling and diagnostics

- Preserve root-cause context in error messages.
- Do not hide failures with generic wrappers.
- Reuse shared diagnostic phrasing/constants where available.

## 7) Testing requirements

For behavior changes:
- Add or update focused tests near the changed subsystem.
- Add regression tests for bugs that previously failed.
- Validate both relevant backends when backend behavior is affected.

Minimum validation before commit:
- `gofmt` on touched Go files
- `go test` for touched packages
- targeted Ferret smoke/repro if language/runtime behavior changed

## 8) Commit hygiene

- Commit only relevant source/test/docs.
- Do not commit generated binaries or temporary repro executables.
- Keep commit message specific to real behavior change.

## 9) Agent-specific requirements

Agents must:
- Search for existing implementations before writing new logic.
- Reuse existing function directly when possible.
- Justify any new helper in code review notes/commit rationale.
- Avoid creating compatibility wrappers unless explicitly requested.

## 10) Human review checklist

Before merge, verify:
- No pass-through wrappers were introduced.
- No duplicated logic remains in touched areas.
- Shared logic was centralized when repeated.
- Tests cover the changed behavior and previous failure mode.
- No unrelated files/artifacts were included.
