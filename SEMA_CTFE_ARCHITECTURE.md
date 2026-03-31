# Semantic CTFE Architecture

Status: implemented on branch `sema`

## Why this exists

Ferret originally had two compile-time evaluators:

- semantic AST evaluator:
  - `internal/analysis/semantics/typechecker/const_eval.go`
- MIR evaluator:
  - `internal/ir/mir/ctfe.go`

They both contained the same hard logic:

- recursive expression evaluation
- call-frame/environment handling
- loop execution
- recursion guarding
- operator dispatch
- function-body interpretation

That duplication was the architecture problem this migration solves.

## Design goal

Use one canonical compile-time evaluation service in semantic analysis, then let later phases consume cached results instead of re-interpreting source programs again.

In short:

- resolver makes names callable/addressable
- semantic CTFE evaluates compile-time expressions on demand
- semantic metadata stores CTFE results
- HIR/MIR read cached results
- MIR does propagation/cleanup, not full user-function interpretation

## Previous system

Before this migration the compiler effectively worked like this:

1. parser / collector / resolver
2. typechecker
   - array lengths and const initializers already use semantic AST CTFE
3. HIR generation
   - consumes some cached const values
   - still emits unresolved `comptime` in some cases
4. MIR lowering
5. MIR CTFE
   - re-evaluates many `comptime` expressions
6. ownership / simplify / backend

That meant compile-time execution was split across two phases with overlapping logic.

## Current system

The compiler now works like this:

1. parser / collector / resolver
2. semantic analysis
   - typechecking
   - on-demand CTFE through one semantic evaluator
   - result caching in semantic metadata
3. HIR generation
   - emit literals / aggregates from semantic CTFE cache when available
4. MIR lowering
   - lower already-folded HIR
5. MIR propagation / cleanup
   - no second full interpreter
6. ownership / simplify / backend

## Canonical evaluator

The canonical evaluator is the existing semantic evaluator in:

- `internal/analysis/semantics/typechecker/const_eval.go`

Why this one:

- it already runs after resolver, which is the earliest safe point
- it is already used by array lengths and const initializers
- type formation needs CTFE before HIR or MIR exist
- keeping MIR as the canonical interpreter would still require an early special path

So the architecture choice is:

- keep semantic CTFE
- expand its consumers
- shrink MIR CTFE until removable

## Runtime model of semantic CTFE

Semantic CTFE evaluates resolved AST with a semantic environment:

- current module
- resolved symbols
- local bindings / call frame values
- recursion guard
- loop step limits
- already-computed semantic const values

The evaluator returns `typeinfo.ConstValue`.

That value is already suitable for:

- array lengths
- const initializers
- explicit `comptime` expressions
- future generic/value arguments

## Metadata flow

The key storage location is:

- `internal/analysis/semantics/typeinfo/module.go`

Specifically:

- `ModuleInfo.ConstValues`

The intended flow is:

1. typechecker asks semantic CTFE to evaluate an expression
2. if evaluation succeeds, bind the result in `ModuleInfo.ConstValues`
3. later phases query `LookupConstValue`
4. if found, they lower from the cached result instead of emitting a deferred `comptime`

This is how later phases avoid re-running the interpreter.

## What changed

Step 1 moved explicit `comptime` onto the semantic-cache path for successful cases.

Implemented behavior:

- typechecking now caches successful explicit `comptime` results
- array-length evaluation also records the semantic result
- HIR generation now lowers cached `comptime` results directly to literals/aggregates instead of emitting a `comptime` wrapper when the result is already known

After step 1:

- `const` initializers: semantic CTFE
- array lengths: semantic CTFE
- successful explicit `comptime`: semantic CTFE first, HIR consumes cache
- MIR CTFE: still present as fallback/legacy path for remaining unresolved cases

Step 2 made semantic CTFE caching service-shaped instead of ad hoc.

Implemented behavior:

- `checker.constExpr` now serves as the common semantic CTFE entrypoint for typechecker callers
- successful semantic CTFE requests now cache the expression node automatically
- remaining typechecker CTFE call sites such as tuple indices and array-bound checks now go through the same cache-producing path
- declaration nodes still get separate explicit cache entries only where later consumers need declaration identity

Step 3 completed the semantic/IR handoff.

Implemented behavior:

- semantic CTFE now skips eager execution when a hard `comptime` form still depends on deferred runtime inputs such as wrapper parameters
- successful explicit `comptime` blocks are elided before HIR reaches MIR
- HIR lowering reads semantic const metadata for explicit `comptime` and const initializers instead of preserving interpreter work for MIR

Step 4 removed the duplicate MIR interpreter.

Implemented behavior:

- `internal/ir/mir/ctfe.go` is deleted
- MIR keeps only ordinary lowering plus simplify/propagation responsibilities
- enum/error ordinal lookup now lives in `typeinfo`, so semantic CTFE and MIR lowering share one canonical implementation

## Desired steady-state phase responsibilities

### Resolver

- resolve names, modules, and callable targets
- no evaluation

### Typechecker / semantic CTFE

- type expressions and statements
- evaluate compile-time expressions on demand
- validate compile-time-only requirements
- cache successful CTFE results

### HIR generation

- lower typed syntax
- convert cached CTFE results into concrete HIR literals/aggregates
- avoid preserving `comptime` when the value is already known

### MIR

- lower normalized HIR
- perform ordinary constant propagation / cleanup
- avoid owning primary CTFE semantics

## System flow example

For a successful compile-time expression:

1. resolver binds names in `sumTo(4)`
2. typechecker sees `comptime sumTo(4)`
3. semantic CTFE executes the resolved AST body of `sumTo`
4. semantic CTFE returns `ConstValue{Int: 6}`
5. typechecker stores that result in `ModuleInfo.ConstValues`
6. HIR generation asks for cached const value of the `comptime` node
7. HIR emits `6`, not `comptime sumTo(4)`
8. MIR lowers the literal directly
9. later passes only optimize, they do not need to interpret `sumTo` again

For a failed compile-time expression:

1. semantic CTFE tries evaluation
2. if language rules require compile-time success, typechecker reports the diagnostic
3. if the construct is a soft comptime path, later lowering may preserve the unresolved form until the correct consumer decides what to do

## Migration summary

### Step 1

- cache successful explicit `comptime`
- teach HIR to consume cached results

Status: complete

### Step 2

- expand semantic CTFE caching to remaining type-level and future value-level CTFE call sites
- make semantic metadata the normal source of truth for later phases

Status: complete

### Step 3

- reduce MIR CTFE to propagation / cleanup only
- remove full duplicate interpreter behavior from MIR where semantic CTFE already proved the value

Status: complete

### Step 4

- delete the remaining duplicated MIR interpreter logic once no frontend semantic behavior depends on it

Status: complete

## Practical rule for future work

When adding a new compile-time-capable language feature, the default question should be:

"Can this be expressed as a call to semantic CTFE and stored in semantic metadata?"

If the answer is yes, do that first.

Only add MIR work if it is truly MIR-local optimization, not language-semantic evaluation.
