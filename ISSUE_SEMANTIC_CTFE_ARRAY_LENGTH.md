# Issue: Typechecker Deadlock for Array Lengths That Depend on CTFE

Status: fixed on branch `sema`

## Summary

Ferret had a phase-ordering problem when a type needed a compile-time value during typechecking.

The concrete failure mode was array lengths such as:

```ferret
let size = comptime someFunc()
let items: [size]i32
```

or:

```ferret
let items: [someFunc()]i32
```

The typechecker needed the array length immediately in order to build the array type, but compile-time evaluation was still split across semantic analysis and a later MIR CTFE pass.

## Problem

Before the fix, Ferret had two evaluators:

- semantic AST CTFE in `internal/analysis/semantics/typechecker/const_eval.go`
- MIR CTFE in `internal/ir/mir/ctfe.go`

This created an architectural gap:

- the typechecker needed concrete values for type formation
- some compile-time execution logic still effectively lived later in MIR
- successful `comptime` results were not the single source of truth in semantic metadata

That made array-length resolution fragile and duplicated CTFE behavior across phases.

## Reproduction

```ferret
fn count() -> i32 {
    let mut i = 0
    let mut sum = 0
    while i < 5 {
        sum = sum + 1
        i = i + 1
    }
    return sum
}

fn main() -> i32 {
    let size = comptime count()
    let items: [size]i32 = [size]i32{1, 2, 3, 4, 5}
    return items[4]
}
```

Expected:

- `size` is evaluated during semantic analysis
- `[size]i32` resolves to `[5]i32` during typechecking

## Root Cause

The compiler needed compile-time values in the typechecker itself, but compile-time execution was not fully owned by semantic analysis.

The specific architectural issue was:

1. type formation needed constant values immediately
2. explicit `comptime` and other CTFE consumers were not fully normalized into semantic metadata
3. MIR still owned a second interpreter, so there was no single canonical CTFE source of truth

## Fix

Unify CTFE around one semantic evaluator after resolver.

Implemented changes:

- semantic CTFE in `const_eval.go` is now the canonical interpreter
- type syntax resolution calls semantic CTFE directly for array lengths in `syntax_types.go`
- successful CTFE results are cached in `typeinfo.ModuleInfo.ConstValues`
- HIR consumes semantic const metadata instead of preserving successful `comptime` for MIR
- MIR CTFE interpreter was removed

This means the typechecker can now resolve:

- array lengths from const expressions
- array lengths from imported consts
- array lengths from direct CTFE function calls
- array lengths from earlier immutable `let` bindings whose value came from `comptime`

## Regression Coverage

Added tests:

- `TestTypecheckerResolvesArrayLengthFromCTFEFunctionCall`
- `TestTypecheckerResolvesArrayLengthFromEarlierComptimeLet`

Added Ferret smoke test:

- `tests/semma/array_len.fer`

## Acceptance Criteria

- `[someFunc()]T` resolves during typechecking when `someFunc` is CTFE-evaluable
- `let size = comptime someFunc(); let items: [size]T` resolves during typechecking
- non-CTFE-evaluable array lengths still report a typechecker error
- later phases do not need to re-interpret successful CTFE array-length expressions

## Validation

- `go test ./internal/analysis/semantics/typechecker`
- `go test ./...`
- `ferret run tests/semma/array_len.fer`
