# Ferret Semantics: Ownership, Stack/Heap, Move, and Borrow

This document defines the intended semantics for Ferret ownership and references.

## 1. Value Kinds

Ferret has two ownership-bearing value kinds:

- `T`: stack-owned value
- `#T`: heap-owned boxed value

`#T` is an owning box type (similar to Rust `Box<T>`). It is a first-class type, not metadata.

## 2. Allocation

- `#expr` allocates a new heap box and produces `#T` where `T` is the value type of `expr`.
- Heap allocation is only valid for values that are not already heap-backed runtime containers.
  - Invalid: `#[]T`, `#map[K]V`, `#str`

## 3. Copy vs Move

Ferret uses a mixed ownership rule:

- **Implicit copy** for implicitly-copyable types (primitives, complex scalars, fixed arrays/aggregates of copyable fields, shared refs `&T`).
- **Implicit move** for non-copyable owned values (`#T`, dynamic arrays/maps, resources, `&mut T`, and aggregates containing them).
- After move, a binding is unusable until reinitialized.

Rules:

- Moves are allowed only from movable bindings (locals/params/receiver), not constants/module globals.
- Move from references is invalid when the reference itself is the source of ownership transfer.
- `return x` moves or copies according to the source type category above.

## 4. Borrow

Borrow operators:

- `&x` shared borrow (`&T`)
- `&mut x` mutable borrow (`&mut T`)

Rules:

- Multiple shared borrows are allowed.
- Exactly one mutable borrow is allowed at a time.
- Shared and mutable borrows cannot overlap for the same place.
- `&mut` cannot be taken through an immutable borrow chain.
- Borrows follow non-lexical lifetimes (NLL): borrow can end at last use before scope end.

## 5. Assignment and Ownership

### Stack values (`T`)

- Copyable `T` values are copied on assignment/call/return.
- Non-copyable `T` values are moved on assignment/call/return.

### Heap values (`#T`)

- `x: #T = #v` creates/rebinds heap ownership.
- `x = y` (both `#T`) transfers heap ownership from `y` to `x` (move by default).
- No implicit deep clone of heap ownership is performed.

## 6. References to Heap Values

- Borrowing a `#T` value borrows its payload (`T`), not ownership.

## 7. Function Boundaries

- `T` return: returns stack/value semantics.
- `#T` return: returns heap ownership.
- `return v` copies copyable types and moves non-copyable types.

## 8. Safety Invariants

Ferret must enforce:

- No use-after-move
- No aliasing violation (`&mut` vs `&`)
- No escaping references to dead stack locals
- Ownership state driven by type (`#T`), not by ad-hoc symbol flags

## 9. Migration Notes (v0.0.6 -> current)

- Non-copyable values move implicitly at assignment/call/return boundaries.
- To avoid consumption, pass references (`&T` / `&mut T`) in APIs that only need borrowing.
