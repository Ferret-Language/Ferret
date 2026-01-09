# Move Semantics Implementation Plan (Draft)
Ferret already constructs rvalues from literals and composite literals without extra copies.
## Goals
- Keep Ferret copy-by-default.
- Add explicit move via `@` to transfer ownership from an lvalue.
- Enforce compile-time safety: no use-after-move, no move while borrowed.
- Start with identifier-only moves; leave field/index moves for later.

## Proposed Syntax and Semantics
- `@x` converts an lvalue binding into an rvalue and **moves** it.
- `@` is allowed only on identifiers (phase 1).
- `@` is illegal on references (`&T`, `&mut T`).
- `@` is illegal if the binding is currently borrowed.
- After `@x`, any further use of `x` is an error.
- Rvalues (literals, composite literals, call results) are already rvalues and do not need `@`.

Examples:
```ferret
let x := 10;    // rvalue constructs into x
let y := x;     // copy
let z := @x;    // move; x is now invalid

fn f() -> i32 {
    let a := 1;
    return @a;  // move out
}

fn g() -> i32 {
    let a := 1;
    return a;   // copy out
}
```

## Implementation Steps
1. **Parser/AST**
   - Add `@` as a unary operator (if not already present) that only targets lvalues.
   - Represent `@x` as a distinct AST/HIR node or a unary expression with an "is-move" flag.

2. **Typechecker**
   - Require the operand of `@` to be an addressable binding (identifier only in phase 1).
   - Reject `@` on reference types.
   - Reject `@` when the operand is not an lvalue (literals, calls, binary expr, etc.).

3. **Borrow/Move Analysis**
   - Track moved state per binding (initially per local/param).
   - On `@x`, mark `x` as moved.
   - Error on any use of a moved binding.
   - Error on `@x` if `x` is currently borrowed.
   - `return @x` should move, `return x` should copy.

4. **Codegen**
   - No runtime changes required; `@` should be compile-time only.
   - Codegen can treat `@x` as the value of `x` (same as a copy), since safety is enforced statically.

5. **Diagnostics**
   - New errors:
     - "cannot move from reference"
     - "cannot move non-lvalue"
     - "use of moved value"
     - "cannot move while borrowed"
   - Optional: warn on `@` of rvalues if later allowed (not in phase 1).

6. **Tests**
   - Positive: move from identifier, move in return, move into new binding.
   - Negative: move from reference, move while borrowed, use-after-move, `@` on rvalue.

## Future Extensions (Not in Phase 1)
- Allow `@s.field` with partial-move tracking.
- Decide on `@arr[i]` semantics (likely forbid for non-copy types or require `take/replace` helpers).
- Add per-field moved tracking if/when destructors or drop logic are added.


---

# Considerations

## Typechecker / Semantic Rules

Operand must be a local variable or parameter binding (identifier only).

References are illegal: `@x` is an error if `x` has type `&T` or `&mut T`.

Non-lvalues are illegal: `@(x + 1)` → error.

Copy-by-default remains the rule; `@` only changes lvalues into moved rvalues.

## Borrow/Move Analysis

Track per-binding state: Available | Borrowed | Moved.

On @x:

Ensure state is Available.

Set state to Moved.

On any use:

Check for Moved → error: "use of moved value".

If currently borrowed → error on @.

Return handling:

return x → copy.

return @x → move (set x as moved).
